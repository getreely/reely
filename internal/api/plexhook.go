package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	"github.com/getreely/reely/internal/auth"
	"github.com/getreely/reely/internal/plex"
)

// Plex telling reely that something landed.
//
// reely already knows when it imported a file — it did the importing. What
// it cannot know is the moment PLEX has an item for that file, and that is
// the moment sharing can act: labels go on items, so until the scan
// finishes there is nothing to label and the reconciler files the title as
// waiting. Before this, that wait lasted until the next scheduled pass.
//
// A webhook is the only thing that reports the scan. It needs Plex Pass,
// which is a real limitation and the reason the periodic pass stays
// exactly as it was: this makes sharing prompt where it can be, and
// changes nothing where it cannot.

// plexHookTokenKey is the setting holding the token in the webhook URL.
//
// Plex sends no credentials with a webhook — it POSTs to whatever URL it
// was given — so the URL has to be the credential. It is generated rather
// than chosen, because a guessable one is an open door to a route that
// asks reely to go and talk to Plex.
//
// The name below is a settings key, not a credential: the value it
// stores is generated at runtime and lives in the database.
//
//nolint:gosec // a settings key name, not a hardcoded credential
const plexHookTokenKey = "plex_webhook_token"

// plexHookMemory caps what a webhook may hold in memory. Plex posts
// multipart with the payload alongside a thumbnail of the item, and the
// thumbnail is of no interest — parsing spills past this to a temp file,
// which is deleted with the request.
const plexHookMemory = 256 << 10

// PlexHookURL is the address to paste into Plex, or empty where no token
// has been generated yet.
func (s *Server) PlexHookURL(base string) string {
	token := s.Settings.Get(plexHookTokenKey)
	if token == "" || base == "" {
		return ""
	}
	return strings.TrimRight(base, "/") + "/api/v1/plex/webhook?token=" + token
}

// handleGetPlexHook reports whether the webhook is on, and the URL to
// paste into Plex when it is.
//
// The URL is built from server_url, which is the address reely is
// reachable at. Without it there is nothing to paste, and saying so is
// more use than handing over a path and letting somebody guess the host.
func (s *Server) handleGetPlexHook(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	base := strings.TrimSpace(s.Settings.Get("server_url"))
	writeJSON(w, http.StatusOK, map[string]any{
		"set": s.Settings.Get(plexHookTokenKey) != "",
		"url": s.PlexHookURL(base),
	})
}

// handleNewPlexHookToken mints a token, replacing any existing one.
//
// Rotating is the same call, which is what somebody who pasted the URL
// somewhere they regret needs: a new token, and the old URL dead.
func (s *Server) handleNewPlexHookToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	token := auth.NewClientID()
	if err := s.Settings.Set(plexHookTokenKey, token); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"token": token})
}

// handleDeletePlexHookToken turns the webhook off.
func (s *Server) handleDeletePlexHookToken(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if err := s.Settings.Set(plexHookTokenKey, ""); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// plexHookPayload is the sliver of Plex's webhook body reely reads.
//
// Deliberately just the event. reely does not label the item the webhook
// names — it asks for a pass, and the pass computes the whole desired
// state from its own tables. Trusting the payload's ids would mean
// trusting an unauthenticated request to say which title to act on.
type plexHookPayload struct {
	Event string `json:"event"`
}

// handlePlexHook takes Plex's word that the library changed.
//
// Public, because Plex cannot sign in. The token in the URL is the whole
// guard, so it is compared in constant time and the route is rate
// limited: without a token configured the endpoint refuses everything,
// which is also what an install that never set one up wants.
//
// It answers 200 to everything it understands, including events it
// ignores. A webhook that answers an error gets retried, and there is
// nothing here worth retrying.
func (s *Server) handlePlexHook(w http.ResponseWriter, r *http.Request) {
	want := s.Settings.Get(plexHookTokenKey)
	got := r.URL.Query().Get("token")
	if want == "" || subtle.ConstantTimeCompare([]byte(want), []byte(got)) != 1 {
		// no detail: a wrong token learns only that it was wrong
		writeErr(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	if err := r.ParseMultipartForm(plexHookMemory); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("not a Plex webhook body"))
		return
	}
	defer func() {
		if r.MultipartForm != nil {
			_ = r.MultipartForm.RemoveAll()
		}
	}()

	var body plexHookPayload
	if err := json.Unmarshal([]byte(r.FormValue("payload")), &body); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("no readable payload"))
		return
	}

	// library.new is the one that matters: an item Plex did not have a
	// moment ago, which is exactly what the reconciler was waiting for.
	// Every other event — plays, pauses, ratings — says nothing about
	// what exists, and answering 200 to them keeps Plex from retrying.
	if body.Event == "library.new" {
		log.Printf("reely: plex webhook — library.new, reconciling")
		s.reconcileSoon()
	}
	w.WriteHeader(http.StatusOK)
}

// ScanPlexIn asks Plex to look at the folders files just landed in.
//
// The other half of the webhook, and the half reely can do unaided.
// Sharing labels items, so a title reely just placed cannot be labelled
// until Plex has scanned it — and left alone, Plex scans when it feels
// like it. Pointing it at the folder turns "eventually" into "now", and
// the webhook then reports the moment it finished.
//
// Best-effort throughout. A scan reely could not ask for is a title
// labelled on the next scheduled pass instead of this one, which is
// exactly where things stood before, so nothing here is worth failing an
// import over.
//
// The reconcile is deliberate rather than incidental: an install with no
// webhook configured still gets its labels, just after the scan has had
// a moment rather than the instant it was asked for.
func (s *Server) ScanPlexIn(dirs []string) {
	client, cfg, err := s.plexClient()
	if err != nil || cfg.ServerURL == "" {
		return // no Plex to tell
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	libs, err := client.Sections(ctx, cfg.ServerURL, cfg.Token)
	if err != nil {
		log.Printf("reely: plex scan — could not read libraries: %v", err)
		return
	}
	asked := 0
	for _, dir := range dirs {
		key := sectionHolding(libs, dir)
		if key == "" {
			// a folder no Plex library covers: reely holds a library Plex
			// does not, which is a setup question rather than an error here
			continue
		}
		if err := client.Scan(ctx, cfg.ServerURL, cfg.Token, key, dir); err != nil {
			log.Printf("reely: plex scan %q: %v", dir, err)
			continue
		}
		asked++
	}
	if asked > 0 {
		log.Printf("reely: asked Plex to scan %d folder(s)", asked)
		s.reconcileSoon()
	}
}

// sectionHolding is the Plex section whose folders contain dir, or "".
//
// A Plex library may point at several folders, so every one is
// considered. Matching is by prefix on a cleaned path with a separator
// guard, so "/data/media/films" does not claim "/data/media/films-4k".
func sectionHolding(libs []plex.Library, dir string) string {
	clean := filepath.Clean(dir)
	for _, lib := range libs {
		for _, root := range lib.Paths {
			root = filepath.Clean(root)
			if clean == root || strings.HasPrefix(clean, root+string(filepath.Separator)) {
				return lib.Key
			}
		}
	}
	return ""
}
