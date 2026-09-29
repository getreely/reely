package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Signing in with a Plex token the caller already holds — the TV app,
// which has signed in to Plex on its own. The decision has to be exactly
// the PIN sign-in's: the owner, or somebody the server is shared with.

func plexToken(t *testing.T, world *testWorld, token string) *httptest.ResponseRecorder {
	t.Helper()
	return as(t, world.h, nil, "POST", "/api/v1/auth/plex/token", map[string]any{"token": token})
}

// A shared account gets in as a requester, with a session, and its
// libraries follow the sharing list — the same as a PIN sign-in.
func TestPlexTokenSignsInASharedAccount(t *testing.T) {
	world, _ := plexWorld(t, "500001") // ana: accepted, allLibraries
	w := plexToken(t, world, "tv-app-token")
	if w.Code != http.StatusOK {
		t.Fatalf("sign-in = %d: %s", w.Code, w.Body)
	}
	if len(w.Result().Cookies()) == 0 {
		t.Error("no session cookie was set")
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"mayAdd":false`)) {
		t.Errorf("a Plex account arrived able to add: %s", w.Body)
	}
	if u := world.srv.Auth.UserByPlexID(500001); u == nil || len(u.Libraries) != 2 {
		t.Fatalf("account = %+v, want both sections granted", u)
	}
}

// Somebody the server isn't shared with is refused, with the same
// nothing-in-particular answer the PIN path gives.
func TestPlexTokenRefusesAnUnsharedAccount(t *testing.T) {
	world, _ := plexWorld(t, "999999")
	w := plexToken(t, world, "tv-app-token")
	if w.Code != http.StatusForbidden {
		t.Fatalf("sign-in = %d, want 403: %s", w.Code, w.Body)
	}
	if world.srv.Auth.UserByPlexID(999999) != nil {
		t.Error("an unshared account was created")
	}
}

// An invitation nobody accepted reaches nothing, by token as by PIN.
func TestPlexTokenRefusesAnUnacceptedInvite(t *testing.T) {
	world, _ := plexWorld(t, "500003")
	if w := plexToken(t, world, "tv-app-token"); w.Code != http.StatusForbidden {
		t.Fatalf("sign-in = %d, want 403: %s", w.Code, w.Body)
	}
}

// The owner, once linked, signs in as their own admin account.
func TestPlexTokenSignsInTheOwner(t *testing.T) {
	world := requesterWorld(t)
	fake := fakePlexTV(t, "500001")
	world.srv.plexBase = fake.URL
	as(t, world.h, world.admin, "POST", "/api/v1/auth/plex/pin", nil)
	as(t, world.h, world.admin, "POST", "/api/v1/auth/plex/check?link=1", map[string]any{"pinId": 77})

	w := plexToken(t, world, "tv-app-token")
	if w.Code != http.StatusOK {
		t.Fatalf("owner signing in by token = %d, want 200: %s", w.Code, w.Body)
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"role":"admin"`)) {
		t.Errorf("signed in as %s, want their admin account", w.Body)
	}
}

// Before the owner links, there is no sharing list to check anybody
// against, so nobody gets in this way either.
func TestPlexTokenIsClosedUntilTheOwnerLinks(t *testing.T) {
	world := requesterWorld(t)
	fake := fakePlexTV(t, "500001")
	world.srv.plexBase = fake.URL
	if w := plexToken(t, world, "tv-app-token"); w.Code != http.StatusPreconditionFailed {
		t.Fatalf("sign-in = %d, want 412: %s", w.Code, w.Body)
	}
}

// A token plex.tv doesn't recognise is a refusal, not a 500 or a 502.
func TestPlexTokenPlexRejectsIsUnauthorized(t *testing.T) {
	world, _ := plexWorld(t, "500001")
	refusing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	t.Cleanup(refusing.Close)
	world.srv.plexBase = refusing.URL
	if w := plexToken(t, world, "stale"); w.Code != http.StatusUnauthorized {
		t.Fatalf("sign-in = %d, want 401: %s", w.Code, w.Body)
	}
}

// No token, no sign-in, and no call to plex.tv to find that out.
func TestPlexTokenNeedsAToken(t *testing.T) {
	world, _ := plexWorld(t, "500001")
	if w := plexToken(t, world, ""); w.Code != http.StatusBadRequest {
		t.Fatalf("sign-in = %d, want 400: %s", w.Code, w.Body)
	}
}

// It can't be used to link: ?link=1 on this route is ignored, even from
// an admin's session, so an owner's account is only ever linked through
// the PIN flow they started themselves.
func TestPlexTokenNeverLinks(t *testing.T) {
	world := requesterWorld(t)
	fake := fakePlexTV(t, "500001")
	world.srv.plexBase = fake.URL
	// the same fixture every other case here uses, through a variable so
	// the linter doesn't take a map entry named "token" for a credential
	signIn := "tv-app-token"
	w := as(t, world.h, world.admin, "POST", "/api/v1/auth/plex/token?link=1", map[string]any{"token": signIn})
	if w.Code == http.StatusOK {
		t.Fatalf("token sign-in linked or admitted on an unlinked install: %s", w.Body)
	}
	if world.srv.Settings.Get("plex_owner_token") != "" {
		t.Error("the owner token was set by a token sign-in")
	}
}

// Guessing tokens is rate-limited per caller, like guessing PINs.
func TestPlexTokenIsRateLimited(t *testing.T) {
	world, _ := plexWorld(t, "999999")
	var last int
	for range 12 {
		last = plexToken(t, world, "guess").Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("12th attempt = %d, want 429", last)
	}
}
