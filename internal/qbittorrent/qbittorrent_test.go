package qbittorrent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/getreely/reely/internal/download"
)

// stubQB is a qBittorrent that insists on the things the real one
// insists on: a session cookie and a matching Referer.
type stubQB struct {
	mu       sync.Mutex
	torrents string // JSON body for /torrents/info
	calls    []string
	forms    []url.Values
	loggedIn bool
	logins   int
	badPass  bool
	// expire makes the NEXT authenticated call answer 403, the way a
	// lapsed session does
	expire bool
	// missing answers 404 for these paths, the way a qBittorrent that
	// predates an endpoint — or has dropped a deprecated one — does
	missing map[string]bool
}

func (q *stubQB) handler(t *testing.T) http.Handler {
	t.Helper()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("unparseable body: %v", err)
		}
		q.mu.Lock()
		defer q.mu.Unlock()
		q.calls = append(q.calls, r.URL.Path)
		q.forms = append(q.forms, r.PostForm)

		if r.Header.Get("Referer") == "" {
			// the real qBittorrent refuses this outright
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("no Referer"))
			return
		}
		if r.URL.Path == "/api/v2/auth/login" {
			q.logins++
			if q.badPass {
				_, _ = w.Write([]byte("Fails."))
				return
			}
			q.loggedIn = true
			// Path matters: without it Go scopes the cookie to
			// /api/v2/auth/, so it never reaches /api/v2/torrents/* and
			// every later call looks like a lapsed session. The real
			// qBittorrent sends path=/, HttpOnly and SameSite=Strict.
			//
			// Secure is deliberately absent, and is why the linter is
			// waved off below: qBittorrent sets it only over HTTPS, and
			// this fake serves plain HTTP like a WebUI on the LAN. A
			// Secure cookie would never be sent back here at all.
			//
			//nolint:gosec // G124: a test fake over plain HTTP, matching what qBittorrent sends
			http.SetCookie(w, &http.Cookie{
				Name: "SID", Value: "session-token", Path: "/",
				HttpOnly: true, SameSite: http.SameSiteStrictMode,
			})
			_, _ = w.Write([]byte("Ok."))
			return
		}
		if c, _ := r.Cookie("SID"); c == nil || !q.loggedIn || q.expire {
			q.expire = false // one lapse, then the re-login works
			q.loggedIn = false
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if q.missing[r.URL.Path] {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("Not Found"))
			return
		}
		switch r.URL.Path {
		case "/api/v2/torrents/info":
			_, _ = w.Write([]byte(q.torrents))
		case "/api/v2/app/version":
			_, _ = w.Write([]byte("v5.0.0"))
		default:
			_, _ = w.Write([]byte("Ok."))
		}
	})
}

func testClient(t *testing.T, q *stubQB) *Client {
	t.Helper()
	srv := httptest.NewServer(q.handler(t))
	t.Cleanup(srv.Close)
	return New(func() string { return srv.URL },
		func() string { return "admin" },
		func() string { return "hunter2" })
}

func (q *stubQB) called(path string) int {
	q.mu.Lock()
	defer q.mu.Unlock()
	n := 0
	for _, c := range q.calls {
		if c == path {
			n++
		}
	}
	return n
}

// formFor returns the body of the last call to path.
func (q *stubQB) formFor(path string) url.Values {
	q.mu.Lock()
	defer q.mu.Unlock()
	var last url.Values
	for i, c := range q.calls {
		if c == path {
			last = q.forms[i]
		}
	}
	return last
}

// A lapsed session is ordinary — qBittorrent expires them, and it
// restarts — so a 403 buys one sign-in and one retry rather than
// surfacing as a failure the user has to do something about.
func TestALapsedSessionSignsBackIn(t *testing.T) {
	q := &stubQB{torrents: "[]"}
	c := testClient(t, q)

	if _, err := c.Version(context.Background()); err != nil {
		t.Fatalf("first call: %v", err)
	}
	if q.logins != 1 {
		t.Fatalf("signed in %d times for the first call, want 1", q.logins)
	}
	// the cookie carries the next one — no second sign-in
	if _, err := c.Version(context.Background()); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if q.logins != 1 {
		t.Errorf("signed in again with a good cookie (%d logins)", q.logins)
	}

	q.mu.Lock()
	q.expire = true
	q.mu.Unlock()
	if _, err := c.Version(context.Background()); err != nil {
		t.Fatalf("call after the session lapsed: %v", err)
	}
	if q.logins != 2 {
		t.Errorf("a lapsed session did not sign back in (%d logins)", q.logins)
	}
}

// A wrong password is not a lapsed session, and retrying it forever
// would be how an account gets its address banned.
func TestABadPasswordIsReportedNotRetried(t *testing.T) {
	q := &stubQB{torrents: "[]", badPass: true}
	c := testClient(t, q)
	_, err := c.Version(context.Background())
	if err == nil {
		t.Fatal("a refused sign-in reported success")
	}
	if !strings.Contains(err.Error(), "username or password") {
		t.Errorf("unhelpful error: %v", err)
	}
	if q.logins > 1 {
		t.Errorf("retried a known-bad password %d times", q.logins)
	}
}

// Downloading and seeding are different lists. A torrent that finished
// is still there — it is seeding — so sorting by state is the whole
// difference between "in flight" and "ready to import".
func TestSeedingTorrentsAreHistoryNotQueue(t *testing.T) {
	q := &stubQB{torrents: `[
	  {"hash":"aaa","name":"One.1080p","state":"downloading","category":"movies",
	   "size":2097152,"amount_left":1048576,"progress":0.5,"eta":90,
	   "content_path":"/dl/One.1080p"},
	  {"hash":"bbb","name":"Two.1080p","state":"stalledUP","category":"movies",
	   "size":2097152,"amount_left":0,"progress":1,"eta":8640000,
	   "content_path":"/dl/Two.1080p"},
	  {"hash":"ccc","name":"Three.1080p","state":"checkingDL","category":"movies",
	   "size":2097152,"amount_left":0,"progress":1,"eta":8640000,
	   "content_path":"/dl/Three.1080p"}
	]`}
	c := testClient(t, q)

	queue, total, err := c.Queue(context.Background(), "movies", 0, 50)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(queue) != 2 {
		t.Fatalf("queue = %d items (total %d), want the two unfinished ones", len(queue), total)
	}
	if queue[0].ID != "aaa" || queue[0].Percentage != 50 || queue[0].TimeLeft != "0:01:30" {
		t.Errorf("in-flight row = %+v", queue[0])
	}
	if queue[0].SizeMB != 2 || queue[0].LeftMB != 1 {
		t.Errorf("bytes were not turned into MB: %+v", queue[0])
	}
	// checking is NOT done: the files are still being written to
	if queue[1].ID != "ccc" || queue[1].Status != "Checking" {
		t.Errorf("a checking torrent should still be in flight: %+v", queue[1])
	}

	hist, err := c.History(context.Background(), "movies", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || hist[0].ID != "bbb" {
		t.Fatalf("history = %+v, want just the seeding one", hist)
	}
	if hist[0].Status != download.StatusCompleted {
		t.Errorf("status = %q, want the word the importer switches on", hist[0].Status)
	}
	// content_path, never save_path: the import path scans this and
	// elsewhere deletes it, and save_path is the shared downloads root
	if hist[0].Storage != "/dl/Two.1080p" {
		t.Errorf("storage = %q, want the torrent's own content path", hist[0].Storage)
	}
}

// A torrent qBittorrent cannot finish is reported as failed rather than
// left in the queue forever, which is what lets the blocklist ban the
// release and the search go and find another.
func TestABrokenTorrentIsAFailure(t *testing.T) {
	q := &stubQB{torrents: `[
	  {"hash":"ddd","name":"Bad.1080p","state":"missingFiles","category":"movies",
	   "content_path":"/dl/Bad.1080p"}
	]`}
	c := testClient(t, q)
	hist, err := c.History(context.Background(), "movies", 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(hist) != 1 || hist[0].Status != download.StatusFailed {
		t.Fatalf("history = %+v, want one failure", hist)
	}
	if hist[0].FailMessage == "" {
		t.Error("a failure with no reason tells the activity view nothing")
	}
}

// The name reely pins on the torrent is what a finished download is
// matched back to a title by, so add has to set it — and the hash has
// to come back, since qBittorrent's add answers only "Ok.".
func TestAddPinsTheReleaseNameAndFindsItsHash(t *testing.T) {
	q := &stubQB{torrents: `[
	  {"hash":"eee","name":"Wanted.2024.1080p","state":"downloading","category":"movies"}
	]`}
	c := testClient(t, q)

	id, err := c.AddURL(context.Background(),
		"https://indexer/file.torrent", "Wanted.2024.1080p", "movies")
	if err != nil {
		t.Fatal(err)
	}
	if id != "eee" {
		t.Errorf("hash = %q, want the torrent that was just added", id)
	}
	form := q.formFor("/api/v2/torrents/add")
	if form.Get("rename") != "Wanted.2024.1080p" {
		t.Errorf("rename = %q — the release name is how the import matches back", form.Get("rename"))
	}
	if form.Get("category") != "movies" || form.Get("urls") != "https://indexer/file.torrent" {
		t.Errorf("add form = %v", form)
	}
}

// A magnet carries its own hash, so there is nothing to go looking for.
func TestAMagnetNeedsNoLookup(t *testing.T) {
	q := &stubQB{torrents: `[]`}
	c := testClient(t, q)
	id, err := c.AddURL(context.Background(),
		"magnet:?xt=urn:btih:ABCDEF0123456789&dn=Thing", "Thing", "movies")
	if err != nil {
		t.Fatal(err)
	}
	if id != "abcdef0123456789" {
		t.Errorf("hash = %q, want the one out of the magnet", id)
	}
	if n := q.called("/api/v2/torrents/info"); n != 0 {
		t.Errorf("looked the torrent up %d times when the magnet already said", n)
	}
}

// Zero is a real ratio — stop the instant the download finishes — so it
// must reach qBittorrent as 0 and not be mistaken for "unset".
func TestShareLimitSentinels(t *testing.T) {
	for _, tc := range []struct {
		name  string
		ratio float64
		want  string
	}{
		{"stop at zero", 0, "0"},
		{"a real ratio", 1.5, "1.5"},
		{"unlimited", RatioUnlimited, "-1"},
		{"global", RatioGlobal, "-2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &stubQB{torrents: "[]"}
			c := testClient(t, q)
			if err := c.SetShareLimit(context.Background(), "aaa", tc.ratio, RatioGlobal); err != nil {
				t.Fatal(err)
			}
			form := q.formFor("/api/v2/torrents/setShareLimits")
			if got := form.Get("ratioLimit"); got != tc.want {
				t.Errorf("ratioLimit = %q, want %q", got, tc.want)
			}
			if form.Get("hashes") != "aaa" {
				t.Errorf("hashes = %q", form.Get("hashes"))
			}
		})
	}
}

// qBittorrent has no priority scale, only ends. Normal means "where the
// queue already put it", so it must not shuffle the torrent anywhere.
func TestPriorityMapsToTheEndsOfTheQueue(t *testing.T) {
	for _, tc := range []struct {
		name     string
		priority int
		want     string
	}{
		{"force", 2, "/api/v2/torrents/topPrio"},
		{"high", 1, "/api/v2/torrents/topPrio"},
		{"low", -1, "/api/v2/torrents/bottomPrio"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := &stubQB{torrents: "[]"}
			c := testClient(t, q)
			// sign in first, so the count below is the priority call
			// itself and not the 403-then-retry that opens a session
			if _, err := c.Version(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := c.SetPriority(context.Background(), "aaa", tc.priority); err != nil {
				t.Fatal(err)
			}
			if q.called(tc.want) != 1 {
				t.Errorf("did not call %s: %v", tc.want, q.calls)
			}
		})
	}
	t.Run("normal moves nothing", func(t *testing.T) {
		q := &stubQB{torrents: "[]"}
		c := testClient(t, q)
		if err := c.SetPriority(context.Background(), "aaa", 0); err != nil {
			t.Fatal(err)
		}
		if q.called("/api/v2/torrents/topPrio")+q.called("/api/v2/torrents/bottomPrio") != 0 {
			t.Errorf("normal priority moved the torrent: %v", q.calls)
		}
	})
}

// Without a URL there is nothing to talk to, and every call should say
// so rather than making a request to the empty string.
func TestUnconfiguredRefusesQuietly(t *testing.T) {
	c := New(func() string { return "" }, func() string { return "" }, func() string { return "" })
	if c.Configured() {
		t.Error("a client with no URL called itself configured")
	}
	if _, err := c.Version(context.Background()); err == nil {
		t.Error("unconfigured Version did not error")
	}
	// a password is optional: qBittorrent can have auth switched off for
	// the local subnet, and that is a normal install
	c = New(func() string { return "http://qb:8080" }, func() string { return "" }, func() string { return "" })
	if !c.Configured() {
		t.Error("a URL with no password should still be configured")
	}
}
