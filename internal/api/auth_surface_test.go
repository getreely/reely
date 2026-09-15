package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"regexp"
	"sort"
	"testing"
)

// The auth surface, checked against the routes that were actually
// registered rather than a re-parse of server.go: every route declares a
// level (router.go), and these tests hold that declaration to it.

var fillPathVars = regexp.MustCompile(`\{[^}]+\}`)

// splitPattern turns `GET /api/v1/shows/{id}` into a method and a path
// with the wildcards filled, ready to request.
func splitPattern(pattern string) (method, path string) {
	for i := 0; i < len(pattern); i++ {
		if pattern[i] == ' ' {
			return pattern[:i], fillPathVars.ReplaceAllString(pattern[i+1:], "1")
		}
	}
	return "GET", pattern
}

// Once any account exists the whole API refuses an unauthenticated
// caller, bar the three deliberately open paths. A route added without a
// thought for auth turns this red instead of shipping open.
func TestEveryRouteRefusesUnauthenticated(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	levels := srv.routes().levels
	if len(levels) < 90 {
		t.Fatalf("route table looks wrong: %d routes", len(levels))
	}

	// turning auth on: the first account
	rec, _ := doJSON(t, h, "POST", "/api/v1/users", map[string]any{
		"username": "root", "password": "correct horse battery"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create admin: %d %s", rec.Code, rec.Body)
	}

	for pattern, lvl := range levels {
		if lvl == levelPublic {
			continue
		}
		method, path := splitPattern(pattern)
		req := httptest.NewRequest(method, path, bytes.NewReader([]byte("{}")))
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s answered %d to an unauthenticated caller, want 401", pattern, w.Code)
		}
	}

	// the open trio behaves — and status keeps the version for signed-in eyes
	rec, out := doJSON(t, h, "GET", "/api/v1/system/status", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status: %d", rec.Code)
	}
	if _, leaked := out["version"]; leaked {
		t.Error("unauthenticated status must not reveal the version")
	}

	rec, out = doJSON(t, h, "GET", "/api/v1/auth/me", nil)
	if rec.Code != http.StatusOK || out["authRequired"] != true {
		t.Fatalf("auth/me: %d %v", rec.Code, out)
	}
	if _, leaked := out["user"]; leaked {
		t.Error("auth/me must not name a user without a session")
	}
}

// Every admin route refuses a signed-in NON-admin. This is the property
// the old arrangement could not guarantee: admin was enforced by each
// handler remembering to ask, so a route that forgot was reachable by
// any account with a password. Now the refusal happens before the
// handler runs, which is why it can be asserted for all of them at once.
func TestEveryAdminRouteRefusesAPlainUser(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()

	rec, _ := doJSON(t, h, "POST", "/api/v1/users", map[string]any{
		"username": "root", "password": "correct horse battery"})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create admin: %d %s", rec.Code, rec.Body)
	}
	admin := login(t, h, "root", "correct horse battery")

	// a plain user needs a library to be scoped to, so make one first
	lib := httptest.NewRequest("POST", "/api/v1/libraries", bytes.NewReader([]byte(
		`{"name":"TV","path":"`+t.TempDir()+`","kind":"shows"}`)))
	lib.AddCookie(admin)
	lw := httptest.NewRecorder()
	h.ServeHTTP(lw, lib)
	if lw.Code != http.StatusCreated && lw.Code != http.StatusOK {
		t.Fatalf("create library: %d %s", lw.Code, lw.Body)
	}

	req := httptest.NewRequest("POST", "/api/v1/users",
		bytes.NewReader([]byte(`{"username":"jen","password":"another long one","role":"user","libraryIds":[1]}`)))
	req.AddCookie(admin)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create plain user: %d %s", w.Code, w.Body)
	}
	jen := login(t, h, "jen", "another long one")

	var checked int
	for pattern, lvl := range srv.routes().levels {
		if lvl != levelAdmin {
			continue
		}
		method, path := splitPattern(pattern)
		req := httptest.NewRequest(method, path, bytes.NewReader([]byte("{}")))
		req.AddCookie(jen)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s answered %d to a plain user, want 403", pattern, w.Code)
		}
		checked++
	}
	if checked < 40 {
		t.Fatalf("only %d admin routes checked — the table looks wrong", checked)
	}
}

// The levels themselves are pinned. Changing who may reach a route is a
// decision, so it should turn this red and be re-approved rather than
// slipping through inside an unrelated change. A NEW route lands here as
// a one-line diff naming its level; that is the whole intent.
func TestRouteLevelsArePinned(t *testing.T) {
	levels := testServer(t).routes().levels

	got := map[level][]string{}
	for pattern, lvl := range levels {
		got[lvl] = append(got[lvl], pattern)
	}
	for _, list := range got {
		sort.Strings(list)
	}

	// Public means reachable with no session, which is what signing in
	// needs by definition. The two Plex routes are here deliberately: the
	// gate they enforce is inside them, not in front of them — plex.tv
	// says who the caller is, and the owner's sharing list says whether
	// that account reaches this server. Neither hands back anything about
	// the install to a caller who fails that check.
	//
	// The Plex webhook is the odd one and belongs to a machine rather
	// than a person: the media server cannot hold a session, so the
	// token in its URL is the gate, checked inside the handler in
	// constant time. Registered with lan() rather than public(), so it
	// is the one public route the internet-facing listener does NOT
	// serve — nothing out there should be able to ask reely to walk its
	// whole library.
	wantPublic := []string{
		"GET /api/v1/auth/me",
		"GET /api/v1/system/status",
		"POST /api/v1/auth/login",
		"POST /api/v1/auth/plex/check",
		"POST /api/v1/auth/plex/pin",
		"POST /api/v1/plex/webhook",
	}
	if len(got[levelPublic]) != len(wantPublic) {
		t.Errorf("public routes = %v, want exactly %v", got[levelPublic], wantPublic)
	} else {
		for i, p := range wantPublic {
			if got[levelPublic][i] != p {
				t.Errorf("public routes = %v, want %v", got[levelPublic], wantPublic)
				break
			}
		}
	}

	// Anything touching accounts, libraries, settings, backups, the
	// system operations or a stored credential is admin. Spot-checked by
	// prefix rather than listed in full: the full list is the route table
	// in server.go, and duplicating it here would only rot.
	mustBeAdmin := []string{
		"GET /api/v1/users",
		"POST /api/v1/users",
		"DELETE /api/v1/users/{id}",
		"PUT /api/v1/users/{id}/libraries",
		"POST /api/v1/libraries",
		"DELETE /api/v1/libraries/{id}",
		"GET /api/v1/settings/{key}",
		"PUT /api/v1/settings/{key}",
		"GET /api/v1/backups",
		"POST /api/v1/restore",
		"GET /api/v1/system/browse",
		"POST /api/v1/system/tvdb-migration",
		"GET /api/v1/activity",
		"POST /api/v1/activity/delete",
	}
	for _, pattern := range mustBeAdmin {
		if lvl, ok := levels[pattern]; !ok {
			t.Errorf("%s: no longer registered", pattern)
		} else if lvl != levelAdmin {
			t.Errorf("%s is %s, must be admin", pattern, lvl)
		}
	}

	// Adding a NEW title is the actor tier — that is what may_add means,
	// and it is deliberately only these two. Curation of a library
	// somebody holds (deleting, monitoring, searching for a better
	// release) stays at the user tier, scoped by library: those act on
	// what they were already given rather than adding to the install.
	mustAct := []string{
		"POST /api/v1/movies",
		"POST /api/v1/shows",
	}
	for _, pattern := range mustAct {
		if lvl, ok := levels[pattern]; !ok {
			t.Errorf("%s: no longer registered", pattern)
		} else if lvl != levelActor {
			t.Errorf("%s is %s, must be actor", pattern, lvl)
		}
	}
	// And these must NOT be: a person curates the libraries they hold.
	for _, pattern := range []string{
		"DELETE /api/v1/movies/{id}",
		"DELETE /api/v1/movies/{id}/file",
		"PUT /api/v1/movies/{id}/monitor",
		"POST /api/v1/movies/{id}/grab",
		"DELETE /api/v1/shows/{id}",
		"PUT /api/v1/episodes/{id}/monitor",
	} {
		if lvl, ok := levels[pattern]; !ok {
			t.Errorf("%s: no longer registered", pattern)
		} else if lvl != levelUser {
			t.Errorf("%s is %s, must stay user — it curates a library already held", pattern, lvl)
		}
	}

	// And the reverse: routes a plain user genuinely needs. If one of
	// these becomes admin the app breaks for everyone who isn't one.
	mustBeUser := []string{
		"GET /api/v1/movies",
		"GET /api/v1/shows",
		"GET /api/v1/shows/{id}",
		"GET /api/v1/calendar",
		"GET /api/v1/search",
		"GET /api/v1/explore",
		"POST /api/v1/auth/logout",
	}
	for _, pattern := range mustBeUser {
		if lvl, ok := levels[pattern]; !ok {
			t.Errorf("%s: no longer registered", pattern)
		} else if lvl != levelUser {
			t.Errorf("%s is %s, must stay reachable by a plain user", pattern, lvl)
		}
	}
}

// A path under /api/ that no route claimed is a typo, and says so. It
// used to fall through to the SPA, which answered a mistyped endpoint
// with the index page and an HTTP 200.
func TestUnknownAPIPathIs404(t *testing.T) {
	h := testServer(t).Handler()
	rec, _ := doJSON(t, h, "GET", "/api/v1/no-such-thing", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown API path answered %d, want 404", rec.Code)
	}
}
