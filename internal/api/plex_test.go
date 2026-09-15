package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
)

// Signing in with Plex. The whole authorization decision is checked
// here: plex.tv says who somebody is, the owner's sharing list says
// whether that account reaches this server, and only then is there a
// session.

// fakePlexTV stands in for plex.tv AND for the media server, since a
// sign-in touches both. accountID is whoever claims the PIN.
func fakePlexTV(t *testing.T, accountID string) *httptest.Server {
	t.Helper()
	shares, err := os.ReadFile("../plex/testdata/shared_servers.xml")
	if err != nil {
		t.Fatal(err)
	}
	sections, err := os.ReadFile("../plex/testdata/sections.xml")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/pins":
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"id":77,"code":"abcd","expiresIn":1800}`))
		case r.URL.Path == "/api/v2/pins/77":
			_, _ = w.Write([]byte(`{"id":77,"code":"abcd","authToken":"tok","expiresIn":1200}`))
		case r.URL.Path == "/api/v2/user":
			_, _ = w.Write([]byte(`{"id":` + accountID + `,"username":"whoever","email":"w@example.com"}`))
		case r.URL.Path == "/api/v2/resources":
			_, _ = w.Write([]byte(`[{"name":"home","clientIdentifier":"srv-1111","provides":"server","owned":true}]`))
		case strings.HasSuffix(r.URL.Path, "/shared_servers"):
			_, _ = w.Write(shares)
		case r.URL.Path == "/library/sections":
			_, _ = w.Write(sections)
		case r.URL.Path == "/":
			// the media server's root, which is where it says what it is
			// called — the name its owner gave it
			_, _ = w.Write([]byte(
				`<MediaContainer friendlyName="the basement" size="0"/>`))
		default:
			t.Errorf("unexpected call: %s %s", r.Method, r.URL.Path)
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// plexWorld is an install whose owner has linked Plex, with reely
// libraries whose paths match the fake server's sections.
func plexWorld(t *testing.T, accountID string) (*testWorld, *httptest.Server) {
	t.Helper()
	world := requesterWorld(t)
	fake := fakePlexTV(t, accountID)
	for _, kv := range [][2]string{
		{"plex_client_id", "reely-test-install"},
		{"plex_owner_token", "owner-tok"},
		{"plex_machine_id", "srv-1111"},
		{"plex_server_url", fake.URL},
	} {
		if err := world.srv.Settings.Set(kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	world.srv.plexBase = fake.URL
	// the fixture's sections point here, so a match is possible
	if _, err := world.srv.Catalog.CreateLibrary("Films", "/srv/library/films", "movies"); err != nil {
		t.Fatal(err)
	}
	if _, err := world.srv.Catalog.CreateLibrary("Series", "/srv/library/series", "shows"); err != nil {
		t.Fatal(err)
	}
	return world, fake
}

func plexCheck(t *testing.T, world *testWorld) *httptest.ResponseRecorder {
	t.Helper()
	return as(t, world.h, nil, "POST", "/api/v1/auth/plex/check", map[string]any{"pinId": 77})
}

// An account on the sharing list gets in, as a requester — somebody who
// arrived by being shared with, rather than by an admin deciding
// anything, asks for titles rather than adding them.
func TestSharedPlexAccountSignsInAsARequester(t *testing.T) {
	world, _ := plexWorld(t, "500001") // ana: accepted, allLibraries
	w := plexCheck(t, world)
	if w.Code != http.StatusOK {
		t.Fatalf("sign-in = %d: %s", w.Code, w.Body)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"mayAdd":false`)) {
		t.Errorf("a Plex account arrived able to add: %s", w.Body)
	}
	if len(w.Result().Cookies()) == 0 {
		t.Error("no session cookie was set")
	}
	// and the grant followed the sharing list through the folder match
	u := world.srv.Auth.UserByPlexID(500001)
	if u == nil {
		t.Fatal("no account was created")
	}
	if len(u.Libraries) != 2 {
		t.Errorf("libraries = %v, want both sections matched", u.Libraries)
	}
}

// A partial share reaches only what it was given.
func TestPartialShareGetsOnlyItsSections(t *testing.T) {
	world, _ := plexWorld(t, "500002") // ben: films shared, series not
	if w := plexCheck(t, world); w.Code != http.StatusOK {
		t.Fatalf("sign-in = %d: %s", w.Code, w.Body)
	}
	u := world.srv.Auth.UserByPlexID(500002)
	if u == nil || len(u.Libraries) != 1 {
		t.Fatalf("libraries = %+v, want just the shared one", u)
	}
}

// An invitation nobody accepted reaches nothing in Plex, so it gets
// nothing here — it cannot sign in.
func TestUnacceptedInviteCannotSignIn(t *testing.T) {
	world, _ := plexWorld(t, "500003") // cass: invited, never accepted
	w := plexCheck(t, world)
	if w.Code != http.StatusForbidden {
		t.Fatalf("sign-in = %d, want 403: %s", w.Code, w.Body)
	}
	if world.srv.Auth.UserByPlexID(500003) != nil {
		t.Error("an unaccepted invite created an account")
	}
}

// Somebody the server was never shared with is refused, and told
// nothing about why. "Never shared", "invited only" and "unshared
// yesterday" are one answer: the difference describes the owner's
// sharing arrangements, and no caller can act on it.
func TestUnsharedAccountIsRefusedWithoutDetail(t *testing.T) {
	world, _ := plexWorld(t, "999999")
	w := plexCheck(t, world)
	if w.Code != http.StatusForbidden {
		t.Fatalf("sign-in = %d, want 403: %s", w.Code, w.Body)
	}
	for _, leak := range []string{"invite", "accepted", "ana", "ben", "cass"} {
		if bytes.Contains(bytes.ToLower(w.Body.Bytes()), []byte(leak)) {
			t.Errorf("the refusal leaked %q: %s", leak, w.Body)
		}
	}
}

// Revocation is the flag AND the session. Somebody unshared halfway
// through an afternoon keeps a valid cookie otherwise, and would go on
// browsing and requesting until it expired.
func TestDeactivationDropsTheSessionImmediately(t *testing.T) {
	world, _ := plexWorld(t, "500001")
	w := plexCheck(t, world)
	if w.Code != http.StatusOK {
		t.Fatalf("sign-in = %d: %s", w.Code, w.Body)
	}
	var session *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == sessionCookie {
			session = c
		}
	}
	if session == nil {
		t.Fatal("no session cookie")
	}
	if got := as(t, world.h, session, "GET", "/api/v1/requests", nil); got.Code != http.StatusOK {
		t.Fatalf("the fresh session couldn't browse: %d", got.Code)
	}
	// unshared with everybody
	if _, err := world.srv.Auth.DeactivatePlexUsersExcept(nil); err != nil {
		t.Fatal(err)
	}
	if got := as(t, world.h, session, "POST", "/api/v1/requests", dune()); got.Code == http.StatusCreated {
		t.Error("a deactivated account could still request")
	}
}

// A Plex account has no password, so it must not be reachable by
// guessing one.
func TestPlexAccountCannotSignInWithAPassword(t *testing.T) {
	world, _ := plexWorld(t, "500001")
	if w := plexCheck(t, world); w.Code != http.StatusOK {
		t.Fatalf("sign-in = %d: %s", w.Code, w.Body)
	}
	u := world.srv.Auth.UserByPlexID(500001)
	for _, pw := range []string{"", " ", "password", "whoever"} {
		if _, _, err := world.srv.Auth.Login(u.Username, pw, "10.0.0.1"); err == nil {
			t.Errorf("password %q signed in a Plex account", pw)
		}
	}
}

// An install nobody has linked offers no sign-in: the first stranger to
// find the endpoint would otherwise get a PIN and then a refusal they
// could not act on.
//
// But the admin must still get one, because linking IS a sign-in and it
// is what sets the token that check tests for. Refusing them too is a
// deadlock: the owner cannot link because nothing is linked, which is
// exactly what shipped and had to be fixed.
func TestPlexSignInIsClosedUntilTheOwnerLinks(t *testing.T) {
	world := requesterWorld(t)
	fake := fakePlexTV(t, "500001")
	world.srv.plexBase = fake.URL

	w := as(t, world.h, nil, "POST", "/api/v1/auth/plex/pin", nil)
	if w.Code != http.StatusPreconditionFailed {
		t.Fatalf("PIN for a stranger on an unlinked install = %d, want 412: %s", w.Code, w.Body)
	}
	// a signed-in account that is not an admin has nothing to link either
	jen := login(t, world.h, "jen", "another long one")
	if w := as(t, world.h, jen, "POST", "/api/v1/auth/plex/pin", nil); w.Code != http.StatusPreconditionFailed {
		t.Errorf("PIN for a requester on an unlinked install = %d, want 412: %s", w.Code, w.Body)
	}
	// the owner, though, is the one person who needs it
	if w := as(t, world.h, world.admin, "POST", "/api/v1/auth/plex/pin", nil); w.Code != http.StatusOK {
		t.Fatalf("the owner could not start a link on an unlinked install: %d %s", w.Code, w.Body)
	}
}

// And the whole way through: an admin on a bare install links their
// account and the token lands, with nothing configured beforehand.
func TestOwnerCanLinkFromNothing(t *testing.T) {
	world := requesterWorld(t)
	fake := fakePlexTV(t, "500001")
	world.srv.plexBase = fake.URL

	pin := as(t, world.h, world.admin, "POST", "/api/v1/auth/plex/pin", nil)
	if pin.Code != http.StatusOK {
		t.Fatalf("PIN = %d: %s", pin.Code, pin.Body)
	}
	w := as(t, world.h, world.admin, "POST", "/api/v1/auth/plex/check?link=1",
		map[string]any{"pinId": 77})
	if w.Code != http.StatusOK {
		t.Fatalf("link = %d: %s", w.Code, w.Body)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("linked")) {
		t.Errorf("link did not report success: %s", w.Body)
	}
	if got := world.srv.Settings.Get("plex_owner_token"); got == "" {
		t.Error("the owner token was not stored")
	}
	// one owned server needs no question, so the machine id is settled too
	if got := world.srv.Settings.Get("plex_machine_id"); got != "srv-1111" {
		t.Errorf("machine id = %q, want the one owned server", got)
	}
}

// The internet-facing surface: admin routes are absent, not refused.
// Derived from the levels the routes already declare, so a route added
// as admin is unreachable from outside without anybody remembering to
// add it to a list.
func TestExternalHandlerHasNoAdminSurface(t *testing.T) {
	world := requesterWorld(t)
	external := world.srv.ExternalHandler()

	// an admin with a perfectly good session still finds nothing there.
	// The request queue is the one administrative thing that IS served
	// out here, covered by its own test — deciding is the owner's, and
	// the owner is usually not at home when somebody asks.
	for _, path := range []string{
		"/api/v1/users", "/api/v1/plex/status",
		"/api/v1/settings/tmdb_api_key", "/api/v1/backups",
	} {
		w := as(t, external, world.admin, "GET", path, nil)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s on the external listener = %d, want 404: %s", path, w.Code, w.Body)
		}
	}

	// while the requester's own surface is there and behaves normally
	jen := login(t, world.h, "jen", "another long one")
	if w := as(t, external, jen, "GET", "/api/v1/requests", nil); w.Code != http.StatusOK {
		t.Errorf("requests on the external listener = %d: %s", w.Code, w.Body)
	}
	if w := as(t, external, jen, "POST", "/api/v1/requests", dune()); w.Code != http.StatusCreated {
		t.Errorf("requesting from outside = %d: %s", w.Code, w.Body)
	}
}

// The external surface is exactly the portal: the routes the requesting
// job needs, and nothing else. Pinned as a list, because this is the
// surface published to the internet and it should not change without
// somebody saying so out loud.
//
// Deriving it from "public or any signed-in account" was wrong and
// shipped once: that tier also carries deleting a movie, triggering a
// grab, toggling monitoring and uploading files, none of which belong
// on a portal for finding something and asking for it.
func TestPortalSurfaceIsPinned(t *testing.T) {
	srv := testServer(t)
	ext := newRouter(srv)
	ext.portalOnly = true
	srv.register(ext)

	want := map[string]bool{
		// signing in, and the app's own probes
		"GET /api/v1/system/status":    true,
		"GET /api/v1/auth/me":          true,
		"POST /api/v1/auth/login":      true,
		"POST /api/v1/auth/plex/pin":   true,
		"POST /api/v1/auth/plex/check": true,
		"POST /api/v1/auth/logout":     true,
		// finding something
		"GET /api/v1/search":              true,
		"GET /api/v1/explore":             true,
		"GET /api/v1/preview/{kind}/{id}": true,
		"GET /api/v1/similar/{kind}/{id}": true,
		"GET /api/v1/people/{id}":         true,
		// knowing whether it is already here or already asked for
		"GET /api/v1/movies":    true,
		"GET /api/v1/shows":     true,
		"GET /api/v1/libraries": true,
		"GET /api/v1/requests":  true,
		"GET /api/v1/calendar":  true,
		// asking for it, and saying where it should land
		"POST /api/v1/requests":                true,
		"PUT /api/v1/users/me/default-library": true,
		// and deciding, which only the owner may do. The one
		// administrative corner of this surface: the guard still refuses
		// anybody else, and an ask should not wait for the owner to be
		// home. Nothing else administrative belongs here.
		"GET /api/v1/requests/queue":         true,
		"POST /api/v1/requests/{id}/approve": true,
		"POST /api/v1/requests/{id}/deny":    true,
		// their OWN groups, so a request can be kept to themselves or
		// pointed at one household rather than all of them. Deliberately
		// not the install-wide list, which names every household here.
		"GET /api/v1/sharing/me/groups": true,
		// Setting a list's audience, which is the owner's call wherever it
		// is asked. The groups to choose from ride along on the lists
		// payload rather than opening the install-wide sharing endpoint,
		// which stays off this surface with the rest of its siblings.
		"PUT /api/v1/lists/{id}/groups": true,
		// a watchlist, which for an account that asks files requests
		// rather than adding. The cadence is deliberately absent: it is
		// one install-wide setting, not anybody's own.
		"GET /api/v1/lists":              true,
		"POST /api/v1/lists":             true,
		"PUT /api/v1/lists/{id}/enabled": true,
		"DELETE /api/v1/lists/{id}":      true,
		"POST /api/v1/lists/{id}/sync":   true,
		"GET /api/v1/mdblist/key":        true,
		"PUT /api/v1/mdblist/key":        true,
	}

	got := map[string]bool{}
	for pattern := range srv.routes().levels {
		method, path, found := strings.Cut(pattern, " ")
		if !found {
			continue
		}
		req := httptest.NewRequest(method,
			strings.NewReplacer("{id}", "1", "{key}", "k", "{kind}", "movie",
				"{season}", "1").Replace(path), nil)
		if _, matched := ext.mux.Handler(req); matched != "/" && matched != "" {
			got[pattern] = true
		}
	}
	for pattern := range want {
		if !got[pattern] {
			t.Errorf("%s should be on the portal but is missing", pattern)
		}
	}
	for pattern := range got {
		if !want[pattern] {
			t.Errorf("%s is published to the internet and should not be", pattern)
		}
	}
}

// The things a portal must never carry: changing the library, running
// the downloader, or touching files.
func TestPortalCannotChangeAnything(t *testing.T) {
	world := requesterWorld(t)
	external := world.srv.ExternalHandler()
	for _, r := range []struct{ method, path string }{
		{"DELETE", "/api/v1/movies/1"},
		{"DELETE", "/api/v1/movies/1/file"},
		{"DELETE", "/api/v1/shows/1/files"},
		{"POST", "/api/v1/movies/1/grab"},
		{"POST", "/api/v1/movies/1/search"},
		{"GET", "/api/v1/movies/1/releases"},
		{"PUT", "/api/v1/movies/1/monitor"},
		{"POST", "/api/v1/movies/1/upload"},
		{"POST", "/api/v1/movies"},
		{"POST", "/api/v1/shows"},
		// install-wide rather than anybody's own
		{"GET", "/api/v1/lists/cadence"},
		{"PUT", "/api/v1/lists/cadence"},
	} {
		// an admin's own session, so this is the surface refusing rather
		// than the guard
		w := as(t, external, world.admin, r.method, r.path, nil)
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s on the portal = %d, want 404: %s", r.method, r.path, w.Code, w.Body)
		}
	}
}

// The app is told which side it is on, so an admin signing in from
// outside isn't shown tabs whose every call answers 404.
func TestExternalRequestsAreMarked(t *testing.T) {
	world := requesterWorld(t)
	inside := as(t, world.h, world.admin, "GET", "/api/v1/auth/me", nil)
	if bytes.Contains(inside.Body.Bytes(), []byte(`"external"`)) {
		t.Errorf("the internal listener claimed to be external: %s", inside.Body)
	}
	outside := as(t, world.srv.ExternalHandler(), world.admin, "GET", "/api/v1/auth/me", nil)
	if !bytes.Contains(outside.Body.Bytes(), []byte(`"external":true`)) {
		t.Errorf("the external listener did not mark itself: %s", outside.Body)
	}
}

// The public sign-in routes are bounded. A PIN request costs a round
// trip to plex.tv, and an unbounded one makes this server hammer
// plex.tv on somebody else's behalf.
func TestPlexPinIsRateLimited(t *testing.T) {
	world, _ := plexWorld(t, "500001")
	var limited bool
	for range 40 {
		if w := as(t, world.h, nil, "POST", "/api/v1/auth/plex/pin", nil); w.Code == http.StatusTooManyRequests {
			limited = true
			break
		}
	}
	if !limited {
		t.Error("the PIN endpoint took 40 requests without complaining")
	}
}

// The test button answers the question that actually matters: not
// "connected?" but "which Plex library landed on which reely one?".
// A wrong server address or a folder that doesn't line up leaves
// sign-in working perfectly and everybody reaching nothing, which is
// the failure a green tick would hide.
func TestPlexTestReportsTheLibraryMapping(t *testing.T) {
	world, _ := plexWorld(t, "500001")

	w := as(t, world.h, world.admin, "GET", "/api/v1/plex/test", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("test = %d: %s", w.Code, w.Body)
	}
	var got struct {
		Configured, OK, ServerOK   bool
		ServerName                 string
		People, Pending, Unmatched int
		Libraries                  []struct{ Title, Matched string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Configured || !got.OK || !got.ServerOK {
		t.Fatalf("legs = %+v, want all three up", got)
	}
	// the server's own name, which is what a person recognises
	if got.ServerName != "the basement" {
		t.Errorf("server name = %q, want what the server calls itself", got.ServerName)
	}
	// the fixture: two accepted shares, one invite never taken up, and one
	// row with no account id, which is dropped before any of this
	if got.People != 2 || got.Pending != 1 {
		t.Errorf("people/pending = %d/%d, want 2/1", got.People, got.Pending)
	}
	// two of the three sections have a reely library at the same folder;
	// the documentaries one has none, and saying so is the whole point
	matched := map[string]string{}
	for _, l := range got.Libraries {
		matched[l.Title] = l.Matched
	}
	if matched["Films"] == "" || matched["Series"] == "" {
		t.Errorf("mapping = %v, want both matched", matched)
	}
	if got.Unmatched != 1 || matched["Documentaries"] != "" {
		t.Errorf("mapping = %v (unmatched %d), want the third called out",
			matched, got.Unmatched)
	}
}

// The two legs fail apart. A wrong server address leaves plex.tv
// perfectly happy — people sign in and reach nothing — so reporting one
// number for both would describe the wrong problem.
func TestPlexTestSeparatesTheTwoLegs(t *testing.T) {
	world, _ := plexWorld(t, "500001")
	if err := world.srv.Settings.Set("plex_server_url", ""); err != nil {
		t.Fatal(err)
	}
	w := as(t, world.h, world.admin, "GET", "/api/v1/plex/test", nil)
	var got struct {
		OK, ServerOK bool
		ServerError  string
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.OK {
		t.Error("the plex.tv leg should still be up with no server address")
	}
	if got.ServerOK || got.ServerError == "" {
		t.Errorf("the server leg = %+v, want it down and saying why", got)
	}
}

// And it is the owner's: a requester cannot read the install's Plex
// configuration, nor is it served on the portal.
func TestPlexTestIsAdminOnly(t *testing.T) {
	world, _ := plexWorld(t, "500001")
	jen := login(t, world.h, "jen", "another long one")
	if w := as(t, world.h, jen, "GET", "/api/v1/plex/test", nil); w.Code != http.StatusForbidden {
		t.Errorf("requester = %d, want 403", w.Code)
	}
	if w := as(t, world.srv.ExternalHandler(), world.admin, "GET", "/api/v1/plex/test", nil); w.Code != http.StatusNotFound {
		t.Errorf("on the portal = %d, want 404", w.Code)
	}
}

// Linking ties the owner's Plex identity to their reely account and
// turns its password off — somebody linking their own account is saying
// they want to sign in with Plex, and a password left behind is a second
// door nobody is watching.
func TestLinkingTiesTheOwnerAccountAndDropsItsPassword(t *testing.T) {
	world := requesterWorld(t)
	fake := fakePlexTV(t, "500001")
	world.srv.plexBase = fake.URL

	if w := as(t, world.h, world.admin, "POST", "/api/v1/auth/plex/pin", nil); w.Code != http.StatusOK {
		t.Fatalf("pin = %d: %s", w.Code, w.Body)
	}
	if w := as(t, world.h, world.admin, "POST", "/api/v1/auth/plex/check?link=1",
		map[string]any{"pinId": 77}); w.Code != http.StatusOK {
		t.Fatalf("link = %d: %s", w.Code, w.Body)
	}

	admin := world.srv.Auth.UserByID(1)
	if admin == nil || admin.PlexAccountID != 500001 {
		t.Fatalf("admin = %+v, want their Plex id recorded", admin)
	}
	// the password no longer opens it
	if _, _, err := world.srv.Auth.Login("root", "correct horse battery", "10.0.0.1"); err == nil {
		t.Error("the password still signed in a Plex-linked account")
	}
	// and status says the account is tied, not just the install
	st := as(t, world.h, world.admin, "GET", "/api/v1/plex/status", nil)
	if !bytes.Contains(st.Body.Bytes(), []byte(`"accountLinked":true`)) {
		t.Errorf("status = %s, want the account reported as linked", st.Body)
	}
}

// And then they can actually sign in that way. The owner is never in
// their own sharing list, so this path is the only one that admits them
// — if it did not work, linking would have locked them out.
func TestOwnerSignsInWithPlexAfterLinking(t *testing.T) {
	world := requesterWorld(t)
	fake := fakePlexTV(t, "500001")
	world.srv.plexBase = fake.URL
	as(t, world.h, world.admin, "POST", "/api/v1/auth/plex/pin", nil)
	as(t, world.h, world.admin, "POST", "/api/v1/auth/plex/check?link=1", map[string]any{"pinId": 77})

	// signed out, no cookie: the ordinary sign-in path
	w := as(t, world.h, nil, "POST", "/api/v1/auth/plex/check", map[string]any{"pinId": 77})
	if w.Code != http.StatusOK {
		t.Fatalf("owner signing in with Plex = %d, want 200: %s", w.Code, w.Body)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"role":"admin"`)) {
		t.Errorf("signed in as %s, want their admin account", w.Body)
	}
}

// Unlinking puts the password back. Leaving the account Plex-only after
// the link it depended on is gone would lock somebody out of their own
// install with the button they just pressed.
func TestUnlinkingRestoresThePassword(t *testing.T) {
	world := requesterWorld(t)
	fake := fakePlexTV(t, "500001")
	world.srv.plexBase = fake.URL
	as(t, world.h, world.admin, "POST", "/api/v1/auth/plex/pin", nil)
	as(t, world.h, world.admin, "POST", "/api/v1/auth/plex/check?link=1", map[string]any{"pinId": 77})

	if w := as(t, world.h, world.admin, "DELETE", "/api/v1/plex/link", nil); w.Code != http.StatusOK {
		t.Fatalf("unlink = %d: %s", w.Code, w.Body)
	}
	if _, _, err := world.srv.Auth.Login("root", "correct horse battery", "10.0.0.1"); err != nil {
		t.Errorf("the password did not come back after unlinking: %v", err)
	}
}

// The way back when Plex itself is what broke. Host access is the right
// privilege: whoever can set the variable owns the machine anyway.
func TestRestorePasswordSignInIsTheWayBackIn(t *testing.T) {
	world := requesterWorld(t)
	fake := fakePlexTV(t, "500001")
	world.srv.plexBase = fake.URL
	as(t, world.h, world.admin, "POST", "/api/v1/auth/plex/pin", nil)
	as(t, world.h, world.admin, "POST", "/api/v1/auth/plex/check?link=1", map[string]any{"pinId": 77})
	if _, _, err := world.srv.Auth.Login("root", "correct horse battery", "10.0.0.1"); err == nil {
		t.Fatal("the fixture stopped covering the case — the password still works")
	}

	n, err := world.srv.Auth.RestorePasswordSignIn()
	if err != nil || n != 1 {
		t.Fatalf("restored %d admins (%v), want 1", n, err)
	}
	if _, _, err := world.srv.Auth.Login("root", "correct horse battery", "10.0.0.1"); err != nil {
		t.Errorf("the password did not come back: %v", err)
	}
}

// A sync must never sweep the owner. The sharing list is people you
// shared WITH and so never contains them — an owner who linked their own
// account is absent from every sync, and the first one would have
// deactivated them and deleted their session.
func TestSyncNeverDeactivatesTheOwner(t *testing.T) {
	world, _ := plexWorld(t, "500001")
	as(t, world.h, world.admin, "POST", "/api/v1/auth/plex/pin", nil)
	as(t, world.h, world.admin, "POST", "/api/v1/auth/plex/check?link=1", map[string]any{"pinId": 77})

	if w := as(t, world.h, world.admin, "POST", "/api/v1/plex/sync", nil); w.Code != http.StatusOK {
		t.Fatalf("sync = %d: %s", w.Code, w.Body)
	}
	admin := world.srv.Auth.UserByID(1)
	if admin == nil || !admin.Active {
		t.Fatalf("admin = %+v, want them left active", admin)
	}
	// and their session survived it
	if w := as(t, world.h, world.admin, "GET", "/api/v1/users", nil); w.Code != http.StatusOK {
		t.Errorf("the owner's session died in a sync: %d %s", w.Code, w.Body)
	}
}

// An account's role is changeable whichever way it signs in — how
// somebody authenticates and what they may do are separate questions,
// and a Plex account promoted to admin is an ordinary thing to want.
func TestPlexAccountCanBePromotedAndDemoted(t *testing.T) {
	world, _ := plexWorld(t, "500001")
	if w := plexCheck(t, world); w.Code != http.StatusOK {
		t.Fatalf("sign-in = %d: %s", w.Code, w.Body)
	}
	jen := world.srv.Auth.UserByPlexID(500001)
	if jen == nil || jen.Role != "user" {
		t.Fatalf("account = %+v, want a plain user to start", jen)
	}
	id := strconv.FormatInt(jen.ID, 10)

	if w := as(t, world.h, world.admin, "PUT", "/api/v1/users/"+id+"/role",
		map[string]any{"role": "admin"}); w.Code != http.StatusOK {
		t.Fatalf("promote = %d: %s", w.Code, w.Body)
	}
	if u := world.srv.Auth.UserByID(jen.ID); u == nil || !u.IsAdmin() {
		t.Fatalf("after promotion = %+v, want an admin", u)
	}
	// and back down again
	if w := as(t, world.h, world.admin, "PUT", "/api/v1/users/"+id+"/role",
		map[string]any{"role": "user"}); w.Code != http.StatusOK {
		t.Fatalf("demote = %d: %s", w.Code, w.Body)
	}
	u := world.srv.Auth.UserByID(jen.ID)
	if u == nil || u.IsAdmin() {
		t.Fatalf("after demotion = %+v, want a plain user", u)
	}
	// their library grants were waiting for them: an admin ignores them
	// rather than losing them, so demotion doesn't leave somebody blind
	if len(u.Libraries) == 0 {
		t.Error("demotion left the account with no libraries")
	}
}

// The last admin cannot be demoted, for the reason it cannot be deleted:
// it leaves an install nobody can administer, and no account left with
// the power to undo it.
func TestTheLastAdminCannotBeDemoted(t *testing.T) {
	world := requesterWorld(t)
	w := as(t, world.h, world.admin, "PUT", "/api/v1/users/1/role",
		map[string]any{"role": "user"})
	if w.Code != http.StatusBadRequest {
		t.Fatalf("demoting the last admin = %d, want a refusal: %s", w.Code, w.Body)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("last admin")) {
		t.Errorf("the refusal should say why: %s", w.Body)
	}
	if u := world.srv.Auth.UserByID(1); u == nil || !u.IsAdmin() {
		t.Errorf("the last admin lost their role anyway: %+v", u)
	}
}
