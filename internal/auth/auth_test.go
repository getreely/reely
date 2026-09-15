package auth

import (
	"path/filepath"
	"testing"

	"github.com/getreely/reely/internal/db"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return New(conn)
}

func TestUsersAndLogin(t *testing.T) {
	s := testStore(t)
	if s.Enabled() {
		t.Fatal("auth must start disabled with no users")
	}
	if _, err := s.CreateUser("alex", "short", "admin", nil); err == nil {
		t.Fatal("short passwords must be rejected")
	}
	if _, err := s.CreateUser("alex", "correct horse battery", "admin", nil); err != nil {
		t.Fatal(err)
	}
	if !s.Enabled() {
		t.Fatal("auth must be enabled once a user exists")
	}

	if _, _, err := s.Login("alex", "wrong password!", "1.2.3.4"); err == nil {
		t.Fatal("wrong password must fail")
	}
	token, user, err := s.Login("alex", "correct horse battery", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if user.Username != "alex" || user.Role != "admin" || token == "" {
		t.Fatalf("bad login result: %+v token=%q", user, token)
	}

	if got := s.SessionUser(token); got == nil || got.ID != user.ID {
		t.Fatalf("session did not resolve: %+v", got)
	}
	s.Logout(token)
	if s.SessionUser(token) != nil {
		t.Fatal("session must die on logout")
	}
}

func TestLoginRateLimit(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateUser("alex", "correct horse battery", "admin", nil); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxFailures; i++ {
		_, _, _ = s.Login("alex", "nope nope nope", "9.9.9.9")
	}
	// even the right password is refused while locked out
	if _, _, err := s.Login("alex", "correct horse battery", "9.9.9.9"); err == nil {
		t.Fatal("lockout must refuse logins")
	}
	// a different IP is unaffected
	if _, _, err := s.Login("alex", "correct horse battery", "8.8.8.8"); err != nil {
		t.Fatalf("other IP should still log in: %v", err)
	}
}

func TestLastAdminUndeletable(t *testing.T) {
	s := testStore(t)
	adminID, err := s.CreateUser("alex", "correct horse battery", "admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	userID, err := s.CreateUser("sam", "another passphrase", "user", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteUser(adminID); err == nil {
		t.Fatal("must refuse to delete the last admin")
	}
	if err := s.DeleteUser(userID); err != nil {
		t.Fatalf("plain users must be deletable: %v", err)
	}
}

// A user's library grants come back on every read path — the session lookup
// especially, since that's what every API request goes through. Admins carry
// no list at all and MayAccess answers yes regardless, so an admin is never
// mistaken for an account with no access.
func TestUserLibraryGrants(t *testing.T) {
	s := testStore(t)
	if _, err := s.db.Exec(`INSERT INTO libraries (id, name, path, kind)
		VALUES (1, 'Movies', '/data/movies', 'movies'), (2, 'Private', '/data/private', 'shows')`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateUser("root", "correct horse battery", "admin", nil); err != nil {
		t.Fatal(err)
	}
	userID, err := s.CreateUser("sam", "another passphrase", "user", []int64{1})
	if err != nil {
		t.Fatal(err)
	}

	token, user, err := s.Login("sam", "another passphrase", "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	if !user.MayAccess(1) || user.MayAccess(2) {
		t.Fatalf("login grants = %v, want library 1 only", user.Libraries)
	}

	session := s.SessionUser(token)
	if session == nil || !session.MayAccess(1) || session.MayAccess(2) {
		t.Fatalf("session grants = %v, want library 1 only", session)
	}

	// re-scoping lands on the next request, not the next login
	if err := s.SetUserLibraries(userID, []int64{2}); err != nil {
		t.Fatal(err)
	}
	session = s.SessionUser(token)
	if session == nil || session.MayAccess(1) || !session.MayAccess(2) {
		t.Fatalf("grants must be re-read per request, got %v", session)
	}

	// an admin reaches everything without holding a single grant row
	admin := s.SessionUser(mustLogin(t, s, "root", "correct horse battery"))
	if len(admin.Libraries) != 0 || !admin.MayAccess(1) || !admin.MayAccess(2) {
		t.Fatalf("admin = %v, want no stored grants but access to all", admin)
	}

	// a nonexistent library is refused outright rather than half-applied
	if err := s.SetUserLibraries(userID, []int64{2, 999}); err == nil {
		t.Fatal("granting a library that doesn't exist must fail")
	}
	if session = s.SessionUser(token); !session.MayAccess(2) {
		t.Fatal("a failed re-scope must leave the previous grants intact")
	}
}

func mustLogin(t *testing.T, s *Store, username, password string) string {
	t.Helper()
	token, _, err := s.Login(username, password, "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// Sessions are stored hashed: the cookie's raw token must never appear in
// the database, while login/lookup/logout all keep working through it.
func TestSessionTokensStoredHashed(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	s := New(conn)
	if _, err := s.CreateUser("root", "correct horse battery", "admin", nil); err != nil {
		t.Fatal(err)
	}
	token, _, lerr := s.Login("root", "correct horse battery", "127.0.0.1")
	if lerr != nil {
		t.Fatal(lerr)
	}
	var stored string
	if err := conn.QueryRow(`SELECT token FROM sessions`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored == token {
		t.Fatal("raw session token stored in the database")
	}
	if stored != HashToken(token) {
		t.Fatalf("stored token is not the hash: %q", stored)
	}
	if s.SessionUser(token) == nil {
		t.Fatal("raw token must still resolve the session")
	}
	s.Logout(token)
	if s.SessionUser(token) != nil {
		t.Fatal("logout by raw token must kill the session")
	}
}

// A Plex account has no password. Two independent things refuse one:
// bcrypt cannot match an empty hash, and the provider check says so
// outright. Both are tested, because the whole point of having two is
// that neither is allowed to be the only one.
func TestPasswordLoginRefusesAPlexAccount(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateUser("root", "correct horse battery", "admin", nil); err != nil {
		t.Fatal(err)
	}
	// the shape a Plex sign-in will create: no password, provider plex
	if _, err := s.db.Exec(`INSERT INTO users
		(username, password_hash, role, auth_provider, plex_account_id, may_add)
		VALUES ('jen', '', 'user', 'plex', 4242, 0)`); err != nil {
		t.Fatal(err)
	}
	for _, password := range []string{"", " ", "password", "jen"} {
		if _, _, err := s.Login("jen", password, "10.0.0.9"); err == nil {
			t.Fatalf("password %q opened a Plex account", password)
		}
	}
}

// Losing Plex access has to end the account's ability to sign in, not
// merely mark it. A deactivated account is refused whatever it presents.
func TestLoginRefusesADeactivatedAccount(t *testing.T) {
	s := testStore(t)
	if _, err := s.CreateUser("root", "correct horse battery", "admin", nil); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Login("root", "correct horse battery", "10.0.0.9"); err != nil {
		t.Fatalf("active account should sign in: %v", err)
	}
	if _, err := s.db.Exec(`UPDATE users SET active = 0 WHERE username = 'root'`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.Login("root", "correct horse battery", "10.0.0.9"); err == nil {
		t.Fatal("a deactivated account signed in")
	}
}

// The session must carry the WHOLE account, not the four columns it
// happened to be joined on. When it didn't, every request-related field
// arrived at its zero value: an account that may add titles read as one
// that may not, quotas never applied, and auto-approve never fired.
func TestSessionCarriesTheWholeAccount(t *testing.T) {
	s := testStore(t)
	id, err := s.CreateUser("root", "correct horse battery", "admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	five := 5
	if err := s.SetRequestSettings(id, true, true, false, &five, nil); err != nil {
		t.Fatal(err)
	}
	token, _, err := s.Login("root", "correct horse battery", "10.0.0.9")
	if err != nil {
		t.Fatal(err)
	}
	u := s.SessionUser(token)
	if u == nil {
		t.Fatal("no session user")
	}
	switch {
	case !u.MayAdd:
		t.Error("mayAdd lost between the account and the session")
	case !u.Active:
		t.Error("active lost between the account and the session")
	case !u.AutoApproveMovies || u.AutoApproveShows:
		t.Errorf("auto-approve lost: movies=%v shows=%v", u.AutoApproveMovies, u.AutoApproveShows)
	case u.QuotaMoviesWeek == nil || *u.QuotaMoviesWeek != 5:
		t.Errorf("quota lost: %v", u.QuotaMoviesWeek)
	case u.AuthProvider != "local":
		t.Errorf("authProvider = %q, want local", u.AuthProvider)
	}
}

// Deactivating an account ends its sessions. A flag that leaves somebody
// signed in has not revoked anything.
func TestDeactivatingEndsTheSession(t *testing.T) {
	s := testStore(t)
	id, err := s.CreateUser("root", "correct horse battery", "admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	token, _, err := s.Login("root", "correct horse battery", "10.0.0.9")
	if err != nil {
		t.Fatal(err)
	}
	if s.SessionUser(token) == nil {
		t.Fatal("session should work before deactivation")
	}
	if err := s.SetActive(id, false); err != nil {
		t.Fatal(err)
	}
	if u := s.SessionUser(token); u != nil {
		t.Errorf("a deactivated account kept its session: %+v", u)
	}
}
