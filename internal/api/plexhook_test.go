package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/getreely/reely/internal/plex"
)

// Plex telling reely that something landed.
//
// The token in the URL is the whole guard — Plex sends no credentials —
// and the route must not be reachable from the portal, where a stranger
// could ask reely to go and walk its whole library.

// plexHookPost builds the multipart body Plex actually sends: a payload
// field of JSON, alongside a thumbnail nobody reads.
func plexHookPost(t *testing.T, h http.Handler, token, event string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	form := multipart.NewWriter(&body)
	if err := form.WriteField("payload",
		`{"event":"`+event+`","user":false,"owner":true,`+
			`"Metadata":{"type":"episode","ratingKey":"801","title":"Chapter One"}}`); err != nil {
		t.Fatal(err)
	}
	part, err := form.CreateFormFile("thumb", "thumb.jpg")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(bytes.Repeat([]byte{0xff}, 2048)); err != nil {
		t.Fatal(err)
	}
	if err := form.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", "/api/v1/plex/webhook?token="+token, &body)
	req.Header.Set("Content-Type", form.FormDataContentType())
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	return w
}

func TestPlexHookNeedsItsToken(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()

	// before a token exists the endpoint refuses everything, including
	// an empty token — otherwise "no token configured" would mean "open"
	if w := plexHookPost(t, h, "", "library.new"); w.Code != http.StatusNotFound {
		t.Errorf("with no token set = %d, want 404", w.Code)
	}
	if err := srv.Settings.Set(plexHookTokenKey, "s3cret-token"); err != nil {
		t.Fatal(err)
	}
	if w := plexHookPost(t, h, "wrong", "library.new"); w.Code != http.StatusNotFound {
		t.Errorf("wrong token = %d, want 404", w.Code)
	}
	if w := plexHookPost(t, h, "", "library.new"); w.Code != http.StatusNotFound {
		t.Errorf("empty token = %d, want 404", w.Code)
	}
	if w := plexHookPost(t, h, "s3cret-token", "library.new"); w.Code != http.StatusOK {
		t.Errorf("right token = %d: %s", w.Code, w.Body)
	}
	// an event it ignores still answers 200 — an error would be retried,
	// and there is nothing here worth retrying
	if w := plexHookPost(t, h, "s3cret-token", "media.play"); w.Code != http.StatusOK {
		t.Errorf("ignored event = %d, want 200", w.Code)
	}
}

// The media server talks to reely over the LAN. Nothing on the internet
// should be able to ask reely to go and walk its library.
func TestPlexHookIsNotOnThePortal(t *testing.T) {
	srv := testServer(t)
	if err := srv.Settings.Set(plexHookTokenKey, "s3cret-token"); err != nil {
		t.Fatal(err)
	}
	outside := srv.ExternalHandler()
	if w := plexHookPost(t, outside, "s3cret-token", "library.new"); w.Code != http.StatusNotFound {
		t.Errorf("the webhook answered from the portal with %d", w.Code)
	}
	// and the token routes are the owner's, on the LAN only
	if w := as(t, outside, nil, "POST", "/api/v1/plex/webhook/token", nil); w.Code != http.StatusNotFound {
		t.Errorf("minting a token from the portal = %d, want it absent", w.Code)
	}
}

// A season pack lands one webhook per episode. Each pass walks the whole
// library, so a dozen of them arriving together must become one pass —
// not a dozen racing to compute the same answer and hammering Plex.
func TestABurstOfPokesBecomesOnePass(t *testing.T) {
	srv := testServer(t)
	// this server's window only: the package default stays put, so a
	// pass another test is still waiting on reads what it was built with
	srv.recGather = 40 * time.Millisecond

	for i := 0; i < 12; i++ {
		srv.reconcileSoon()
	}
	srv.recMu.Lock()
	queued := srv.recQueued
	srv.recMu.Unlock()
	if !queued {
		t.Fatal("nothing was queued")
	}

	// once the gather window closes the queue is clear again, so a later
	// change gets its own pass rather than being swallowed
	time.Sleep(120 * time.Millisecond)
	srv.recMu.Lock()
	queued = srv.recQueued
	srv.recMu.Unlock()
	if queued {
		t.Error("the queue never cleared, so no later change would run")
	}
}

// Matching a folder to the Plex library that holds it. The prefix has to
// stop at a separator: "/data/films" must not claim "/data/films-4k", or
// reely would ask the wrong library to scan a folder it cannot see and
// the right one would never hear about the file at all.
func TestWhichPlexLibraryHoldsAFolder(t *testing.T) {
	libs := []plex.Library{
		{Key: "1", Title: "Movies", Paths: []string{"/data/media/films"}},
		{Key: "2", Title: "Movies 4K", Paths: []string{"/data/media/films-4k"}},
		{Key: "3", Title: "TV", Paths: []string{"/data/media/tv", "/mnt/old/tv"}},
	}
	for _, tc := range []struct {
		dir, want string
	}{
		{"/data/media/films/Inception (2010)", "1"},
		{"/data/media/films", "1"},
		{"/data/media/films-4k/Dune (2021)", "2"},
		{"/data/media/tv/Breaking Bad/Season 01", "3"},
		{"/mnt/old/tv/Firefly", "3"},          // a library's second folder counts
		{"/data/media/films/../tv/Show", "3"}, // cleaned before matching
		{"/data/media/filmsomething", ""},     // a longer name is not a child
		{"/srv/other/Movie (1999)", ""},       // no library covers it
		{"/data/media", ""},                   // the parent of a library is not in it
	} {
		if got := sectionHolding(libs, tc.dir); got != tc.want {
			t.Errorf("sectionHolding(%q) = %q, want %q", tc.dir, got, tc.want)
		}
	}
}
