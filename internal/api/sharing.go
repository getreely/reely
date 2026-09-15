package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/plex"
	"github.com/getreely/reely/internal/sharing"
)

// Splitting the library: who may see which titles in Plex.
//
// All of this is the owner's, and none of it is on the portal. A
// requester asks for titles; deciding who can see what is not a thing
// they do, and exposing the group list to the internet would tell
// anybody who reaches it the name of every household on the install.

type groupBody struct {
	Name string `json:"name"`
	// Everyone marks this as the household group: every account joins it
	// as it is created, and it is never a request default. Owner only,
	// like everything else on this card.
	Everyone bool `json:"everyone"`
}

func (s *Server) handleGroups(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	groups, err := s.Catalog.Groups()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// members are wanted alongside the list — the owner is picking who is
	// in what, and a list of counts alone does not answer that
	// Mine says whether the owner is in this group themselves. An owner
	// is a person too — they are in households like anybody else — and
	// what they add defaults to their own, so the picker has to know
	// which of the install's groups are theirs.
	type row struct {
		catalog.Group
		MemberIDs []int64 `json:"memberIds"`
		Mine      bool    `json:"mine"`
	}
	me := int64(0)
	if a := s.access(r); a.user != nil {
		me = a.user.ID
	}
	out := make([]row, 0, len(groups))
	for _, g := range groups {
		ids, err := s.Catalog.GroupMemberIDs(g.ID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		mine := false
		for _, id := range ids {
			mine = mine || id == me
		}
		out = append(out, row{Group: g, MemberIDs: ids, Mine: mine})
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleCreateGroup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var body groupBody
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	g, err := s.Catalog.CreateGroup(body.Name)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if body.Everyone {
		if g, err = s.markEveryone(g.ID); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	writeJSON(w, http.StatusCreated, g)
}

// handleSetGroupEveryone marks a group as the household, or clears it.
//
// Separate from rename because it is a different kind of decision: a
// name is cosmetic, and this changes who is in the group and who may
// share into it.
func (s *Server) handleSetGroupEveryone(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		Everyone *bool `json:"everyone"`
	}
	if err := decodeJSON(r, &body); err != nil || body.Everyone == nil {
		writeErr(w, http.StatusBadRequest, errors.New(`body needs {"everyone": true|false}`))
		return
	}
	if !*body.Everyone {
		if err := s.Catalog.SetEveryoneGroup(id, false); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if _, err := s.markEveryone(id); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// markEveryone sets the flag and brings the existing accounts in, since
// a group named as the household after the fact should hold the people
// already here rather than only the ones who arrive next.
func (s *Server) markEveryone(id int64) (*catalog.Group, error) {
	if err := s.Catalog.SetEveryoneGroup(id, true); err != nil {
		return nil, err
	}
	if _, err := s.Catalog.SyncEveryoneMembers(id); err != nil {
		return nil, err
	}
	s.reconcileSoon()
	return s.Catalog.Group(id)
}

func (s *Server) handleRenameGroup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body groupBody
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Catalog.RenameGroup(id, body.Name); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	g, err := s.Catalog.Group(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

func (s *Server) handleDeleteGroup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Catalog.DeleteGroup(id); err != nil {
		status := http.StatusBadRequest
		if errors.Is(err, catalog.ErrGroupInUse) {
			// not a malformed request — the group has to be emptied
			// first, and saying so is the whole answer
			status = http.StatusConflict
		}
		writeErr(w, status, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type membersBody struct {
	UserIDs []int64 `json:"userIds"`
}

// handleSetMembers replaces a group's membership in one call, because
// the UI edits it as a set of ticks rather than one person at a time.
func (s *Server) handleSetMembers(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body membersBody
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Catalog.SetGroupMembers(id, body.UserIDs); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	g, err := s.Catalog.Group(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, g)
}

// handleTitleGroups answers the question the owner actually asks about a
// title: not which labels are on it, but who can see it and why.
func (s *Server) handleTitleGroups(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	kind, tmdb, tvdb, err := titleKey(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	viewers, err := s.Catalog.SharedWith(kind, tmdb, tvdb)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	groups, err := s.Catalog.TitleGroupIDs(kind, tmdb, tvdb)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"viewers":  viewers,
		"groupIds": groups,
	})
}

type titleGroupsBody struct {
	GroupIDs []int64 `json:"groupIds"`
	Title    string  `json:"title"`
}

func (s *Server) handleSetTitleGroups(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	kind, tmdb, tvdb, err := titleKey(r)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	var body titleGroupsBody
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	by := int64(0)
	if a := s.access(r); a.user != nil {
		by = a.user.ID
	}
	if err := s.Catalog.SetTitleGroups(kind, tmdb, tvdb, body.Title, body.GroupIDs, by); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.reconcileSoon()
	w.WriteHeader(http.StatusNoContent)
}

type bulkShareBody struct {
	Mode     string  `json:"mode"` // add | remove | replace
	GroupIDs []int64 `json:"groupIds"`
	Titles   []struct {
		Kind   string `json:"kind"`
		TmdbID int    `json:"tmdbId"`
		TvdbID int    `json:"tvdbId"`
		Title  string `json:"title"`
	} `json:"titles"`
}

// handleBulkShare changes who can see many titles at once — a run picked
// out of the grid, shared with a household in one gesture.
//
// It is its own endpoint rather than a loop over the single-title one
// because that schedules a reconcile per call: forty titles would mean
// forty passes over the library, all but the last of them wasted.
func (s *Server) handleBulkShare(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var body bulkShareBody
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if len(body.Titles) == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("nothing selected"))
		return
	}
	titles := make([]catalog.EntitledTitle, 0, len(body.Titles))
	for _, t := range body.Titles {
		titles = append(titles, catalog.EntitledTitle{
			Kind: t.Kind, TmdbID: t.TmdbID, TvdbID: t.TvdbID, Title: t.Title,
		})
	}
	by := int64(0)
	if a := s.access(r); a.user != nil {
		by = a.user.ID
	}
	changed, err := s.Catalog.BulkShare(catalog.ShareMode(body.Mode),
		body.GroupIDs, titles, by)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.reconcileSoon()
	writeJSON(w, http.StatusOK, map[string]int{"changed": changed})
}

type seedBody struct {
	// Name is what the backfill group is called, and so what the tag on
	// every existing title reads in Plex. Optional; there is a sensible
	// default and it can be renamed afterwards like any group.
	Name string `json:"name"`
}

// handleSeed grants a group everything the install already holds.
//
// Most people arrive with a Plex library that is already full and
// already shared with everybody. Splitting that retroactively would take
// away access people already have, so the sensible first move is to
// grant what exists to everyone and let the split apply only to what
// comes next.
func (s *Server) handleSeed(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var body seedBody
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	by := int64(0)
	if a := s.access(r); a.user != nil {
		by = a.user.ID
	}
	// Read Plex first. A server that has been running for years holds
	// titles reely never imported, and seeding without them would leave
	// exactly those unlabelled — so the first person switched to managed
	// would lose the larger part of what they can see today.
	if rec, err := s.reconciler(); err == nil {
		if err := rec.RefreshItems(r.Context()); err != nil {
			writeErr(w, http.StatusBadGateway,
				fmt.Errorf("could not read your Plex library: %w", err))
			return
		}
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		name = "Existing library"
	}
	group, err := s.Catalog.BackfillGroup(name)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// everybody who can already see the library, because that is who
	// would otherwise lose it
	members, err := s.Catalog.SyncBackfillMembers(group.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	added, err := s.Catalog.GrantHeld(group.ID, by)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.reconcileSoon()
	writeJSON(w, http.StatusOK, map[string]any{
		"added": added, "members": members, "group": group,
	})
}

// handleMyGroups is the requester's own groups — theirs and any
// household they are in. Safe on the portal: somebody already knows
// which households they are in, and this is not the install-wide list.
func (s *Server) handleMyGroups(w http.ResponseWriter, r *http.Request) {
	a := s.access(r)
	if a.user == nil {
		writeJSON(w, http.StatusOK, []catalog.GroupRef{})
		return
	}
	groups, err := s.Catalog.GroupRefsOf(a.user.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// The owner is offered the back catalogue whether or not they belong
	// to it — sharing into it is theirs to do by role, and a picker that
	// hides it because a membership row is missing offers no way to find
	// that out. Nobody else is offered it at all; the server refuses it
	// for them however the audience arrives.
	if a.user.IsAdmin() {
		bf, err := s.Catalog.BackfillGroupRef()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if bf != nil {
			listed := false
			for _, g := range groups {
				listed = listed || g.ID == bf.ID
			}
			if !listed {
				groups = append(groups, *bf)
			}
		}
	}
	writeJSON(w, http.StatusOK, groups)
}

type managedBody struct {
	Managed bool `json:"managed"`
	// ShareAccountID is the Plex account this person watches on, when it
	// is not the one they sign in with. The owner is why: they cannot be
	// restricted on their own server, so an owner who wants a curated
	// view watches on a second account.
	ShareAccountID *int64 `json:"shareAccountId,omitempty"`
}

func (s *Server) handleShareStates(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	states, err := s.Catalog.ShareStates()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, states)
}

// handlePlexAccounts lists the Plex accounts this server is shared with,
// so the owner can say which one somebody actually watches on.
//
// It is the sharing list as Plex holds it, not reely's accounts: the
// point of the setting is to name an account reely may have no user row
// for at all. The owner is absent from it, which is the whole reason
// the setting exists — an owner cannot be restricted on their own
// server, so they watch on a second account that IS in this list.
func (s *Server) handlePlexAccounts(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	client, cfg, err := s.plexClient()
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	shares, err := client.SharedServers(r.Context(), cfg.Token, cfg.MachineID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	out := make([]map[string]any, 0, len(shares))
	for _, sh := range shares {
		out = append(out, map[string]any{
			"accountId": sh.AccountID, "username": sh.Username, "email": sh.Email,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"accounts": out})
}

// handleTitleStreams lists the tracks in a title's file, as the media
// server reports them.
//
// Plex probed the file when it scanned it and already knows this, so
// reely asks rather than opening the file a second time. That makes it
// Plex-only and best-effort: a title the server has not scanned has no
// answer, and neither does an install with no Plex. Both return an empty
// list rather than an error, because this is extra information about a
// title and never the reason a page fails to load.
//
// kind is "movie" or "episode" — the two things that are one file. A
// show is not: its tracks differ episode to episode, and answering for
// the show as a whole would mean picking one and calling it the rest.
func (s *Server) handleTitleStreams(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		empty := map[string]any{"streams": []plex.Stream{}}
		tmdb, tvdb, season, episode := 0, 0, 0, 0
		if kind == "movie" {
			if !s.mayRematch(w, r, "movie", id) {
				return
			}
			m, err := s.Catalog.GetMovie(id)
			if err != nil {
				notFoundOr500(w, err)
				return
			}
			tmdb = m.TmdbID
		} else {
			ep, err := s.Catalog.GetEpisode(id)
			if err != nil {
				notFoundOr500(w, err)
				return
			}
			if !s.mayRematch(w, r, "show", ep.ShowID) {
				return
			}
			sh, err := s.Catalog.GetShow(ep.ShowID)
			if err != nil {
				notFoundOr500(w, err)
				return
			}
			tmdb, tvdb = sh.TmdbID, sh.TvdbID
			season, episode = ep.Season, ep.Episode
		}
		item, err := s.Catalog.PlexItemFor(plexKind(kind), tmdb, tvdb)
		if err != nil || item == nil {
			writeJSON(w, http.StatusOK, empty)
			return
		}
		client, cfg, err := s.plexClient()
		if err != nil || cfg.ServerURL == "" {
			writeJSON(w, http.StatusOK, empty)
			return
		}
		var streams []plex.Stream
		if kind == "movie" {
			streams, err = client.ItemStreams(r.Context(), cfg.ServerURL, cfg.Token, item.RatingKey)
		} else {
			streams, err = client.EpisodeStreams(r.Context(), cfg.ServerURL, cfg.Token,
				item.RatingKey, season, episode)
		}
		if err != nil {
			// the server is there and would not answer for this item:
			// worth the log, not worth failing the page over
			log.Printf("reely: plex streams for %s %d: %v", kind, id, err)
			writeJSON(w, http.StatusOK, empty)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"streams": streams})
	}
}

// plexKind maps reely's episode-shaped kind onto the one plex_items uses.
func plexKind(kind string) string {
	if kind == "movie" {
		return "movie"
	}
	return "show"
}

func (s *Server) handleSetManaged(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body managedBody
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if body.ShareAccountID != nil {
		if err := s.Catalog.SetShareAccount(id, *body.ShareAccountID); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	if err := s.Catalog.SetManaged(id, body.Managed); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	st, err := s.Catalog.ShareStateOf(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.reconcileSoon()
	writeJSON(w, http.StatusOK, st)
}

// handleReconcile runs a pass now rather than waiting for the loop, so
// the owner can change something and watch it land.
func (s *Server) handleReconcile(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	rec, err := s.reconciler()
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	res, err := rec.Run(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	res.Log()
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) reconciler() (*sharing.Reconciler, error) {
	client, cfg, err := s.plexClient()
	if err != nil {
		return nil, err
	}
	if cfg.ServerURL == "" {
		return nil, errors.New("link Plex before splitting the library")
	}
	return &sharing.Reconciler{
		Cat:  s.Catalog,
		Plex: client,
		Cfg: sharing.Config{
			Token: cfg.Token, MachineID: cfg.MachineID, ServerURL: cfg.ServerURL,
		},
	}, nil
}

// titleKey reads the title a sharing call is about. Both ids are carried
// because a show sourced from TheTVDB may have no TMDB id at all.
func titleKey(r *http.Request) (kind string, tmdb, tvdb int, err error) {
	kind = r.PathValue("kind")
	if kind != "movie" && kind != "show" {
		return "", 0, 0, errors.New("kind must be movie or show")
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		return "", 0, 0, errors.New("a title needs an id")
	}
	// a show is addressed by whichever id the caller holds; movies are
	// TMDB-only
	if kind == "show" && r.URL.Query().Get("source") == "tvdb" {
		return kind, 0, id, nil
	}
	return kind, id, 0, nil
}

// reconcileSoon projects the change onto Plex without making the caller
// wait for it. The request has already changed the truth; Plex catching
// up is a consequence, and a slow media server must not turn a click
// into a timeout.
//
// A failure here is logged rather than returned for the same reason: the
// entitlement stands regardless, and the next scheduled pass will try
// again.

// reconcileGather is the default gather window: how long a request for a
// pass waits for company.
//
// Callers arrive in bursts — approving three requests, or a season pack
// landing one Plex webhook per episode — and each pass walks the whole
// library. Waiting a moment turns a dozen requests into one pass.
//
// Each server copies this into recGather at build time, so a test can
// shorten its own window without writing a package variable that another
// test's still-waiting pass is reading.
const reconcileGather = 5 * time.Second

// reconcileSoon asks for a sharing pass shortly.
//
// It used to spawn a goroutine per call, which was survivable while
// every caller was a person clicking something. It stopped being
// survivable the moment a burst could arrive on its own: a season pack
// import fires one webhook per episode, and a dozen concurrent passes
// would each walk the library and hammer Plex to compute the same
// answer.
//
// So at most one pass runs, and at most one waits behind it. A request
// arriving while a pass is already queued is already covered by that
// pass; one arriving DURING a pass queues the next, because the pass may
// have read the library before the change landed.
func (s *Server) reconcileSoon() {
	s.recMu.Lock()
	defer s.recMu.Unlock()
	if s.recQueued {
		return // the queued pass will see this change too
	}
	s.recQueued = true
	go s.reconcileAfterGathering()
}

func (s *Server) reconcileAfterGathering() {
	time.Sleep(s.recGather)
	s.recMu.Lock()
	s.recQueued = false
	s.recMu.Unlock()

	// one pass at a time: two walking the library at once would race to
	// write the same labels, and Plex is the thing that suffers
	s.recRunning.Lock()
	defer s.recRunning.Unlock()

	rec, err := s.reconciler()
	if err != nil {
		return // Plex is not linked; nothing to project onto
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	res, err := rec.Run(ctx)
	if err != nil {
		log.Printf("reely: sharing — %v", err)
		return
	}
	res.Log()
}

// SharingInterval is how often Plex is brought back into line without
// anybody asking.
//
// The pass exists mostly for the things nothing else notices: a title
// that finished downloading after the last one, a label somebody changed
// by hand in Plex, a share edited over there. Every edit made here
// already triggers one, so this is a safety net rather than the main
// path — which is why it is minutes rather than seconds.
const SharingInterval = 10 * time.Minute

// RunSharingLoop keeps Plex in line until ctx is done. Safe to start on
// an install with no Plex link: it finds nothing to do and says nothing.
func (s *Server) RunSharingLoop(ctx context.Context) {
	ticker := time.NewTicker(SharingInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			rec, err := s.reconciler()
			if err != nil {
				continue // Plex is not linked; nothing to project onto
			}
			// nobody is opted in yet, so there is nothing this could
			// usefully do and every pass would be a wasted sweep of the
			// whole library
			if users, err := s.Catalog.ManagedUsers(); err != nil || len(users) == 0 {
				continue
			}
			pass, cancel := context.WithTimeout(ctx, SharingInterval)
			res, err := rec.Run(pass)
			cancel()
			if err != nil {
				log.Printf("reely: sharing — %v", err)
				continue
			}
			// a quiet pass says nothing: this runs all day and a log line
			// every ten minutes saying "nothing changed" is noise
			if res.Labelled > 0 || res.Shares > 0 || len(res.Drifted) > 0 || len(res.Errors) > 0 {
				res.Log()
			}
		}
	}
}

// ensureSharing gives a new account the group its own titles go in.
//
// Every account needs one, because "share this with me" has to mean
// something the moment somebody can add or ask for a title — and an
// account with nowhere to put its own is a state no code path here
// handles. The migration backfilled the accounts that existed; this
// covers the ones made since, however they arrived.
func (s *Server) ensureSharing(userID int64, name string) {
	if err := s.Catalog.EnsurePersonalGroup(userID, name); err != nil {
		log.Printf("reely: sharing — personal group for %s: %v", name, err)
	}
	// and into the household, where the owner has named one. This is the
	// point of difference with the backfill group, whose membership is
	// frozen on purpose: a latecomer never had the old library, but they
	// are one of the people the owner shares Plex with.
	if err := s.Catalog.JoinEveryone(userID); err != nil {
		log.Printf("reely: sharing — everyone group for %s: %v", name, err)
	}
}

// Scoping the library listing by who may actually see a title.
//
// Two different jobs from one set of rows. The owner gets each title's
// groups attached, so the grid can filter by household. A requester gets
// the list itself narrowed to what they may see — otherwise reely
// advertises titles they cannot play in Plex, and tells them what other
// households have.
//
// Narrowing only applies to somebody reely actually manages in Plex.
// An account not yet switched over has no entitlements, and scoping it
// would empty their library — failing closed in the one place that would
// be wrong, since nothing about their Plex access has changed.
// watchable reports whether an account may actually watch a title —
// entitlement rather than library access.
//
// The two come apart for exactly the accounts this matters to. A shared
// account reaches a whole library, because that is how the Plex share
// matched it; what they may watch inside it is decided title by title by
// their tags. An owner, and anybody whose share reely does not write, is
// not restricted at all and may watch everything.
func (s *Server) watchable(r *http.Request, kind string, tmdbID, tvdbID int) (bool, error) {
	a := s.access(r)
	if a.admin() || a.user == nil {
		return true, nil
	}
	managed, err := s.managedViewer(a.user.ID)
	if err != nil || !managed {
		return true, err
	}
	visible, err := s.Catalog.VisibleTitles(a.user.ID, kind)
	if err != nil {
		return false, err
	}
	return visible.Has(tmdbID, tvdbID), nil
}

func (s *Server) scopeMovies(r *http.Request, movies []catalog.Movie) ([]catalog.Movie, error) {
	a := s.access(r)
	if a.admin() {
		groups, err := s.Catalog.GroupsByTitle("movie")
		if err != nil {
			return nil, err
		}
		for i := range movies {
			movies[i].GroupIDs = groups.For(movies[i].TmdbID, 0)
		}
		return movies, nil
	}
	if a.user == nil {
		return movies, nil
	}
	managed, err := s.managedViewer(a.user.ID)
	if err != nil || !managed {
		return movies, err
	}
	visible, err := s.Catalog.VisibleTitles(a.user.ID, "movie")
	if err != nil {
		return nil, err
	}
	out := movies[:0]
	for _, m := range movies {
		if visible.Has(m.TmdbID, 0) {
			out = append(out, m)
		}
	}
	return out, nil
}

func (s *Server) scopeShows(r *http.Request, shows []catalog.Show) ([]catalog.Show, error) {
	a := s.access(r)
	// A show carries both ids and a grant may name either, so matching
	// asks about both rather than picking one to be keyed on. Picking
	// one is what hid a show granted by its TMDB id from a portal whose
	// row also had a TVDB id.
	if a.admin() {
		groups, err := s.Catalog.GroupsByTitle("show")
		if err != nil {
			return nil, err
		}
		for i := range shows {
			shows[i].GroupIDs = groups.For(shows[i].TmdbID, shows[i].TvdbID)
		}
		return shows, nil
	}
	if a.user == nil {
		return shows, nil
	}
	managed, err := s.managedViewer(a.user.ID)
	if err != nil || !managed {
		return shows, err
	}
	visible, err := s.Catalog.VisibleTitles(a.user.ID, "show")
	if err != nil {
		return nil, err
	}
	out := shows[:0]
	for _, sh := range shows {
		if visible.Has(sh.TmdbID, sh.TvdbID) {
			out = append(out, sh)
		}
	}
	return out, nil
}

// managedViewer reports whether reely writes this account's Plex share.
// Only then is narrowing their reely view right: until it does, what
// they can see in Plex has not changed and neither should this.
func (s *Server) managedViewer(userID int64) (bool, error) {
	st, err := s.Catalog.ShareStateOf(userID)
	if err != nil {
		return false, err
	}
	return st.Managed, nil
}
