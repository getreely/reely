// Package auth owns accounts and sessions: local username/password users
// (admin/user roles, bcrypt hashes), cookie sessions, and login rate
// limiting. A plain user reaches only the libraries an admin granted them;
// admins reach everything. Grants are read per request rather than cached
// in the session, so changing them takes effect on the next click.
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

const (
	sessionTTL = 30 * 24 * time.Hour
	// after this many consecutive failures a username/IP pair must wait
	maxFailures = 5
	lockout     = 30 * time.Second
)

type Store struct {
	db *sql.DB

	mu       sync.Mutex
	failures map[string]failureState // key: username + "\n" + ip
}

type failureState struct {
	count int
	until time.Time
}

func New(db *sql.DB) *Store {
	return &Store{db: db, failures: map[string]failureState{}}
}

type User struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	CreatedAt string `json:"createdAt"`
	// Libraries is what a 'user'-role account may reach. It is always empty
	// for admins, who reach everything — ask MayAccess rather than reading
	// this directly, or an admin looks like an account with no access at all.
	Libraries []int64 `json:"libraryIds"`

	// MayAdd separates the two kinds of 'user' account: one that adds
	// titles to its libraries the way every account has until now, and a
	// requester, who browses the same libraries and asks instead. It is a
	// column rather than a third role because SQLite cannot alter the CHECK
	// constraint on role in place, and because it composes: an admin is an
	// admin whatever this says.
	MayAdd bool `json:"mayAdd"`
	// Active is false for an account whose Plex access was withdrawn. The
	// row stays so its request history still reads.
	Active bool `json:"active"`

	// AutoApprove skips the queue for an account the owner trusts, per
	// kind: a film is one grab, a series can be two hundred episodes.
	AutoApproveMovies bool `json:"autoApproveMovies"`
	AutoApproveShows  bool `json:"autoApproveShows"`
	// Quotas bound how often an account may ask, per week. nil is no
	// limit, which is what every account has by default — a quota only
	// means something next to auto-approve, where nothing else caps it.
	QuotaMoviesWeek *int `json:"quotaMoviesWeek,omitempty"`
	QuotaShowsWeek  *int `json:"quotaShowsWeek,omitempty"`

	// DefaultLibraryID is where this account's requests land, following
	// the same rule DefaultLibrary reads: the one they chose, or their
	// only library when there is nothing to choose between. Zero means
	// they hold several and have not picked, which is the only case the
	// UI has a question to ask about.
	DefaultLibraryID int64 `json:"defaultLibraryId,omitempty"`

	// AuthProvider is "local" for a password account and "plex" for one
	// that signs in through Plex. The local login path checks it: a Plex
	// account has no password, and must not be reachable by guessing one.
	AuthProvider string `json:"authProvider"`
	// PlexAccountID is the immutable id an account is matched on at
	// login. Never the email — those change hands.
	PlexAccountID int64  `json:"plexAccountId,omitempty"`
	PlexUsername  string `json:"plexUsername,omitempty"`
}

// userColumns is the read shape shared by every account query, so a new
// column lands in all of them at once instead of one of them.
const userColumns = `id, username, role, created_at, may_add, active,
	auto_approve_movies, auto_approve_shows, quota_movies_week, quota_shows_week,
	auth_provider, COALESCE(plex_account_id,0), plex_username`

// scanUser reads userColumns in order.
func scanUser(sc interface{ Scan(...any) error }) (User, error) {
	var u User
	var mayAdd, active, autoMovies, autoShows int
	var quotaMovies, quotaShows sql.NullInt64
	err := sc.Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt, &mayAdd, &active,
		&autoMovies, &autoShows, &quotaMovies, &quotaShows,
		&u.AuthProvider, &u.PlexAccountID, &u.PlexUsername)
	if err != nil {
		return u, err
	}
	u.MayAdd, u.Active = mayAdd != 0, active != 0
	u.AutoApproveMovies, u.AutoApproveShows = autoMovies != 0, autoShows != 0
	if quotaMovies.Valid {
		n := int(quotaMovies.Int64)
		u.QuotaMoviesWeek = &n
	}
	if quotaShows.Valid {
		n := int(quotaShows.Int64)
		u.QuotaShowsWeek = &n
	}
	return u, nil
}

func (u *User) IsAdmin() bool { return u != nil && u.Role == "admin" }

// MayAccess reports whether the account may work in a library. Admins always
// may; a plain user only in the libraries assigned to them.
func (u *User) MayAccess(libraryID int64) bool {
	if u == nil {
		return false
	}
	if u.IsAdmin() {
		return true
	}
	for _, id := range u.Libraries {
		if id == libraryID {
			return true
		}
	}
	return false
}

// Enabled reports whether any account exists — before the first user is
// created (first run / wizard) the UI and API are open.
func (s *Store) Enabled() bool {
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return false
	}
	return n > 0
}

// CreateUser adds an account. libraryIDs is the set of libraries a 'user'
// may reach and is ignored for admins, who reach everything. Creating a user
// with no libraries is allowed — they can sign in and read nothing until an
// admin grants one.
func (s *Store) CreateUser(username, password, role string, libraryIDs []int64) (int64, error) {
	username = strings.TrimSpace(username)
	if username == "" || len(password) < 8 {
		return 0, fmt.Errorf("username required and password must be at least 8 characters")
	}
	if role != "admin" && role != "user" {
		return 0, fmt.Errorf("role must be admin or user")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return 0, err
	}
	res, err := s.db.Exec(`INSERT INTO users (username, password_hash, role) VALUES (?, ?, ?)`,
		username, string(hash), role)
	if err != nil {
		return 0, fmt.Errorf("create user: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if role == "user" {
		if err := s.SetUserLibraries(id, libraryIDs); err != nil {
			// the account exists but reaches nothing — undo it rather than
			// leave a half-configured user behind
			_, _ = s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
			return 0, fmt.Errorf("assign libraries: %w", err)
		}
	}
	return id, nil
}

// SetUserLibraries replaces an account's library grants. A nonexistent
// library id fails the whole call (foreign key), so a typo can't half-apply.
//
// The grants are rewritten wholesale, so the account's chosen default
// would go with them — it is carried across when the library survives
// the change. Losing it silently would mean an admin editing somebody's
// access also, invisibly, moved where their requests land.
func (s *Store) SetUserLibraries(userID int64, libraryIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed
	var wasDefault sql.NullInt64
	if err := tx.QueryRow(`SELECT library_id FROM user_libraries
		WHERE user_id = ? AND is_default = 1`, userID).Scan(&wasDefault); err != nil &&
		!errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM user_libraries WHERE user_id = ?`, userID); err != nil {
		return err
	}
	seen := map[int64]bool{}
	for _, libID := range libraryIDs {
		if seen[libID] {
			continue
		}
		seen[libID] = true
		if _, err := tx.Exec(`INSERT INTO user_libraries (user_id, library_id, is_default)
			VALUES (?, ?, ?)`, userID, libID,
			boolToInt(wasDefault.Valid && wasDefault.Int64 == libID)); err != nil {
			return fmt.Errorf("library %d: %w", libID, err)
		}
	}
	return tx.Commit()
}

// DefaultLibrary is where an account's requests land: the one they
// chose, or their only library when there is nothing to choose between.
// Zero means they have several and have not picked — the caller has to
// ask rather than guess.
func (s *Store) DefaultLibrary(userID int64) (int64, error) {
	var chosen int64
	err := s.db.QueryRow(`SELECT library_id FROM user_libraries
		WHERE user_id = ? AND is_default = 1`, userID).Scan(&chosen)
	if err == nil {
		return chosen, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return 0, err
	}
	var only int64
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*), COALESCE(MIN(library_id),0)
		FROM user_libraries WHERE user_id = ?`, userID).Scan(&n, &only); err != nil {
		return 0, err
	}
	if n == 1 {
		return only, nil
	}
	return 0, nil
}

// SetDefaultLibrary records where an account's own requests should land.
// This is the one thing a requester may write about themselves, so it is
// deliberately narrow: it can only name a library they already hold, and
// it changes nothing else. sql.ErrNoRows when they don't hold it.
func (s *Store) SetDefaultLibrary(userID, libraryID int64) error {
	var held int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM user_libraries
		WHERE user_id = ? AND library_id = ?`, userID, libraryID).Scan(&held); err != nil {
		return err
	}
	if held == 0 {
		return sql.ErrNoRows
	}
	_, err := s.db.Exec(`UPDATE user_libraries
		SET is_default = CASE library_id WHEN ? THEN 1 ELSE 0 END
		WHERE user_id = ?`, libraryID, userID)
	return err
}

// SetRequestSettings records what an account may do without asking:
// whether it adds titles directly at all, which kinds skip the queue,
// and how often it may ask. Admin-set — none of it is the account's to
// change. A nil quota is no limit.
func (s *Store) SetRequestSettings(userID int64, mayAdd, autoMovies, autoShows bool, quotaMovies, quotaShows *int) error {
	res, err := s.db.Exec(`UPDATE users SET may_add = ?, auto_approve_movies = ?,
		auto_approve_shows = ?, quota_movies_week = ?, quota_shows_week = ?
		WHERE id = ?`,
		boolToInt(mayAdd), boolToInt(autoMovies), boolToInt(autoShows),
		nullableInt(quotaMovies), nullableInt(quotaShows), userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetActive enables or disables an account, deleting its sessions when
// it goes: a live cookie outlives the flag otherwise, and "revoked" that
// leaves somebody signed in is not revoked.
func (s *Store) SetActive(userID int64, active bool) error {
	if _, err := s.db.Exec(`UPDATE users SET active = ? WHERE id = ?`,
		boolToInt(active), userID); err != nil {
		return err
	}
	if active {
		return nil
	}
	_, err := s.db.Exec(`DELETE FROM sessions WHERE user_id = ?`, userID)
	return err
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func nullableInt(v *int) any {
	if v == nil {
		return nil
	}
	return *v
}

// libraries reads one account's grants and which of them their requests
// land in. Admins are never stored, so this correctly returns nothing for
// them — MayAccess short-circuits instead.
//
// The default follows DefaultLibrary's rule rather than the raw column,
// so the two never disagree: one library is its own default, and only an
// account holding several with none picked reads as zero.
func (s *Store) libraries(userID int64) ([]int64, int64, error) {
	rows, err := s.db.Query(`SELECT library_id, is_default FROM user_libraries
		WHERE user_id = ? ORDER BY library_id`, userID)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []int64
	var chosen int64
	for rows.Next() {
		var id int64
		var isDefault int
		if err := rows.Scan(&id, &isDefault); err != nil {
			return nil, 0, err
		}
		out = append(out, id)
		if isDefault != 0 {
			chosen = id
		}
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if chosen == 0 && len(out) == 1 {
		chosen = out[0]
	}
	return out, chosen, nil
}

func (s *Store) ListUsers() ([]User, error) {
	rows, err := s.db.Query(`SELECT ` + userColumns + ` FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// grants are read after the cursor closes — SQLite allows one statement
	// per connection at a time under the pooled driver
	for i := range out {
		if out[i].Role == "admin" {
			continue
		}
		libs, def, err := s.libraries(out[i].ID)
		if err != nil {
			return nil, err
		}
		out[i].Libraries, out[i].DefaultLibraryID = libs, def
	}
	return out, nil
}

// UserByID resolves an account without a session — the admin UI reads back
// a freshly created user this way, library grants included. Returns nil
// when the account is gone.
func (s *Store) UserByID(id int64) *User {
	u, err := scanUser(s.db.QueryRow(`SELECT `+userColumns+` FROM users WHERE id = ?`, id))
	if err != nil {
		return nil
	}
	if u.Role != "admin" {
		libs, def, err := s.libraries(u.ID)
		if err != nil {
			return nil
		}
		u.Libraries, u.DefaultLibraryID = libs, def
	}
	return &u
}

// DeleteUser removes an account, refusing to remove the last admin — that
// would lock everyone out permanently.
// SetRole promotes an account to admin or puts it back to a plain user.
//
// Demoting the last admin is refused for the same reason deleting one
// is: it leaves an install nobody can administer, and no account left
// with the power to undo it.
//
// The account's library grants are untouched either way. An admin
// reaches every library and ignores them, so they sit there unread and
// are simply themselves again on the way back down — which is what
// somebody demoting an account expects, rather than an account that can
// suddenly see nothing.
func (s *Store) SetRole(id int64, role string) error {
	if role != "admin" && role != "user" {
		return fmt.Errorf("role must be admin or user")
	}
	var current string
	if err := s.db.QueryRow(`SELECT role FROM users WHERE id = ?`, id).Scan(&current); err != nil {
		return fmt.Errorf("user not found")
	}
	if current == "admin" && role == "user" {
		var admins int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'admin'`).Scan(&admins); err != nil {
			return err
		}
		if admins <= 1 {
			return fmt.Errorf("cannot remove the last admin")
		}
	}
	_, err := s.db.Exec(`UPDATE users SET role = ? WHERE id = ?`, role, id)
	return err
}

func (s *Store) DeleteUser(id int64) error {
	var role string
	if err := s.db.QueryRow(`SELECT role FROM users WHERE id = ?`, id).Scan(&role); err != nil {
		return fmt.Errorf("user not found")
	}
	if role == "admin" {
		var admins int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE role = 'admin'`).Scan(&admins); err != nil {
			return err
		}
		if admins <= 1 {
			return fmt.Errorf("cannot delete the last admin")
		}
	}
	_, err := s.db.Exec(`DELETE FROM users WHERE id = ?`, id)
	return err
}

// Login verifies credentials (rate-limited per username+IP) and mints a
// session token.
func (s *Store) Login(username, password, ip string) (token string, user *User, err error) {
	key := username + "\n" + ip
	s.mu.Lock()
	st := s.failures[key]
	if time.Now().Before(st.until) {
		s.mu.Unlock()
		return "", nil, fmt.Errorf("too many attempts — try again shortly")
	}
	s.mu.Unlock()

	fail := func() (string, *User, error) {
		s.mu.Lock()
		st := s.failures[key]
		st.count++
		if st.count >= maxFailures {
			st.until = time.Now().Add(lockout)
			st.count = 0
		}
		s.failures[key] = st
		s.mu.Unlock()
		return "", nil, fmt.Errorf("wrong username or password")
	}

	var u User
	var hash, provider string
	var active int
	err = s.db.QueryRow(`SELECT id, username, role, created_at, password_hash, auth_provider, active
		FROM users WHERE username = ?`, strings.TrimSpace(username)).
		Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt, &hash, &provider, &active)
	if err != nil {
		// burn comparable time so a missing user isn't distinguishable
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$7EqJtq98hPqEX7fNZaFWoOhi5B1Nl0jGVSyKyOaUmYQx1lWFZK1WS"), []byte(password))
		return fail()
	}
	// An account that signs in through Plex has no password, and its hash
	// column holds "". bcrypt refuses an empty hash on its own, so this is
	// the second of two independent reasons a password can never open one
	// — and the one that says why. An account whose Plex access was
	// withdrawn is refused here too, however it authenticates.
	if provider != "local" || active == 0 {
		_ = bcrypt.CompareHashAndPassword([]byte("$2a$10$7EqJtq98hPqEX7fNZaFWoOhi5B1Nl0jGVSyKyOaUmYQx1lWFZK1WS"), []byte(password))
		return fail()
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) != nil {
		return fail()
	}

	s.mu.Lock()
	delete(s.failures, key)
	s.mu.Unlock()

	if u.Role != "admin" {
		if u.Libraries, u.DefaultLibraryID, err = s.libraries(u.ID); err != nil {
			return "", nil, err
		}
	}

	token = randomToken()
	expires := time.Now().UTC().Add(sessionTTL).Format(time.RFC3339)
	// only the hash touches the database — the cookie carries the raw token,
	// so a stolen reely.db can't ride anyone's session
	if _, err := s.db.Exec(`INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)`,
		HashToken(token), u.ID, expires); err != nil {
		return "", nil, err
	}
	return token, &u, nil
}

func (s *Store) Logout(token string) {
	_, _ = s.db.Exec(`DELETE FROM sessions WHERE token = ?`, HashToken(token))
}

// SessionUser resolves a session token to its user; expired sessions are
// pruned as they're seen.
func (s *Store) SessionUser(token string) *User {
	if token == "" {
		return nil
	}
	var userID int64
	var expires string
	err := s.db.QueryRow(`SELECT user_id, expires_at FROM sessions WHERE token = ?`,
		HashToken(token)).Scan(&userID, &expires)
	if err != nil {
		return nil
	}
	if t, err := time.Parse(time.RFC3339, expires); err != nil || time.Now().After(t) {
		s.Logout(token)
		return nil
	}
	// The account is read by id rather than joined in, so there is ONE
	// query that knows what columns a User has. Joining a second copy of
	// that list is how the session came to carry a User with every
	// request-related field left at its zero value — an account that may
	// add reading as one that may not.
	//
	// Grants and settings are read per request rather than cached in the
	// session, so an admin changing either takes effect on the user's
	// next click instead of at their next login.
	u := s.UserByID(userID)
	if u == nil || !u.Active {
		// deactivating an account deletes its sessions; this is the second
		// reason a withdrawn account stops working, not the only one
		return nil
	}
	return u
}

func randomToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("crypto/rand unavailable: %v", err)) // never in practice
	}
	return hex.EncodeToString(b)
}

// HashToken is the storage form of a session token: a plain SHA-256.
// Deliberately NOT bcrypt — these are 256-bit crypto/rand values, so there
// is no guessable space for a slow hash to defend, and session validation
// runs on every request where bcrypt's ~100ms cost would be a
// self-inflicted denial of service. Slow hashes are for human-chosen
// passwords, which is why CreateUser and Login use bcrypt directly.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
