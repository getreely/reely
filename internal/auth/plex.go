package auth

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Accounts that sign in through Plex.
//
// Such an account has no password. The local login path refuses it
// outright — a Plex account must not be reachable by guessing a
// password it does not have — and this file is the only way one is
// created or resumed.
//
// Matching is on the Plex account id and nothing else. Emails and
// usernames change hands, and matching on either would hand somebody's
// library access to whoever registers that address at Plex next.

// PlexIdentity is what plex.tv said about somebody who just signed in.
type PlexIdentity struct {
	AccountID int64
	Username  string
	Email     string
}

// UserByPlexID resolves a Plex-linked account, or nil.
func (s *Store) UserByPlexID(accountID int64) *User {
	if accountID == 0 {
		return nil
	}
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM users WHERE plex_account_id = ?`,
		accountID).Scan(&id); err != nil {
		return nil
	}
	return s.UserByID(id)
}

// UpsertPlexUser creates or refreshes the account behind a Plex identity
// and returns its id.
//
// A new one is created as a requester — it may browse the libraries it
// is given and ask for titles, not add them. That is the safe default
// for somebody who arrived by being on a sharing list rather than by an
// admin deciding anything, and it is one switch to change.
//
// The username is only used when creating: renaming an existing account
// because somebody changed their Plex handle would break every reference
// a person recognises. The Plex username is kept alongside regardless.
func (s *Store) UpsertPlexUser(id PlexIdentity) (int64, error) {
	if id.AccountID == 0 {
		return 0, errors.New("a Plex account needs an id")
	}
	if existing := s.UserByPlexID(id.AccountID); existing != nil {
		// reactivate: somebody unshared and shared again is the same
		// person, and their request history is still theirs
		if _, err := s.db.Exec(`UPDATE users SET active = 1, plex_username = ?
			WHERE id = ?`, id.Username, existing.ID); err != nil {
			return 0, err
		}
		return existing.ID, nil
	}
	username, err := s.freePlexUsername(id)
	if err != nil {
		return 0, err
	}
	// password_hash is NOT NULL and this account has no password; the
	// column holds a value no bcrypt comparison can ever match, and the
	// local login path refuses the account by provider before reaching it
	res, err := s.db.Exec(`INSERT INTO users
		(username, password_hash, role, auth_provider, plex_account_id, plex_username, may_add)
		VALUES (?, '', 'user', 'plex', ?, ?, 0)`,
		username, id.AccountID, id.Username)
	if err != nil {
		return 0, fmt.Errorf("create Plex account: %w", err)
	}
	return res.LastInsertId()
}

// freePlexUsername picks a reely username that isn't taken. Plex handles
// and reely usernames are separate namespaces, so a collision with an
// existing local account is ordinary rather than an error.
func (s *Store) freePlexUsername(id PlexIdentity) (string, error) {
	base := strings.TrimSpace(id.Username)
	if base == "" {
		base = strings.TrimSpace(strings.Split(id.Email, "@")[0])
	}
	if base == "" {
		base = fmt.Sprintf("plex-%d", id.AccountID)
	}
	for attempt := range 50 {
		candidate := base
		if attempt > 0 {
			candidate = fmt.Sprintf("%s-%d", base, attempt+1)
		}
		var n int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM users WHERE username = ?`,
			candidate).Scan(&n); err != nil {
			return "", err
		}
		if n == 0 {
			return candidate, nil
		}
	}
	// distinct by construction — the Plex id is unique across accounts
	return fmt.Sprintf("plex-%d", id.AccountID), nil
}

// LinkPlexAccount ties a Plex identity to an account that already
// exists — the owner's own, which is never in its own sharing list and
// so can never arrive through UpsertPlexUser.
//
// It does NOT touch auth_provider. Linking says "this account may also
// sign in with Plex"; taking the password away is a separate decision,
// made after the Plex path has been shown to work.
func (s *Store) LinkPlexAccount(userID int64, id PlexIdentity) error {
	if id.AccountID == 0 {
		return errors.New("a Plex account needs an id")
	}
	res, err := s.db.Exec(`UPDATE users SET plex_account_id = ?, plex_username = ?
		WHERE id = ?`, id.AccountID, id.Username, userID)
	if err != nil {
		return fmt.Errorf("link Plex account: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetPasswordSignIn turns the password path on or off for one account,
// which is what auth_provider means: "may a password open this?".
//
// Turning it off is refused unless the account has a Plex id, because
// the alternative is an account nothing can open. That is the whole
// safety property here — somebody disabling their own password must
// already have a working way back in.
func (s *Store) SetPasswordSignIn(userID int64, enabled bool) error {
	if !enabled {
		var plexID sql.NullInt64
		if err := s.db.QueryRow(`SELECT plex_account_id FROM users WHERE id = ?`,
			userID).Scan(&plexID); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("no such account")
			}
			return err
		}
		if !plexID.Valid || plexID.Int64 == 0 {
			return errors.New("link a Plex account first — otherwise nothing could sign in")
		}
	}
	provider := "plex"
	if enabled {
		provider = "local"
	}
	res, err := s.db.Exec(`UPDATE users SET auth_provider = ? WHERE id = ?`, provider, userID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// StartPlexSession issues a session for a Plex account the caller has
// already checked — against the sharing list for a guest, against the
// stored owner id for the owner. It is deliberately separate from
// Login: there is no password to verify and no rate limit to apply,
// because the credential was plex.tv's to check, not ours.
//
// What it requires is a linked Plex id, not a particular provider. The
// two are independent: the id says Plex may open this account, and
// auth_provider says whether a password may as well. An account can
// have both while somebody satisfies themselves the Plex path works.
//
// An inactive account gets nothing. That is how revocation works —
// somebody unshared is deactivated, and their next sign-in stops here
// even though plex.tv still knows them perfectly well.
func (s *Store) StartPlexSession(userID int64) (string, *User, error) {
	var active int
	var plexID sql.NullInt64
	if err := s.db.QueryRow(`SELECT active, plex_account_id FROM users WHERE id = ?`,
		userID).Scan(&active, &plexID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil, errors.New("no such account")
		}
		return "", nil, err
	}
	if !plexID.Valid || plexID.Int64 == 0 {
		return "", nil, errors.New("that account has no Plex account linked")
	}
	if active == 0 {
		return "", nil, errors.New("this server is no longer shared with your Plex account")
	}
	u := s.UserByID(userID)
	if u == nil {
		return "", nil, errors.New("no such account")
	}
	token := randomToken()
	expires := time.Now().UTC().Add(sessionTTL).Format(time.RFC3339)
	if _, err := s.db.Exec(`INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)`,
		HashToken(token), userID, expires); err != nil {
		return "", nil, err
	}
	return token, u, nil
}

// DeactivatePlexUsersExcept marks every Plex account absent from a sync
// inactive and drops their sessions, returning how many were closed out.
//
// The sessions matter as much as the flag: a person unshared halfway
// through an afternoon keeps a valid cookie otherwise, and would go on
// browsing and requesting until it expired.
//
// Rows are kept rather than deleted so their request history still
// reads, and so re-sharing the same person resumes their account
// instead of building a stranger.
func (s *Store) DeactivatePlexUsersExcept(keep []int64) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	// the keep list binds as one JSON parameter, so the statement stays a
	// constant string with nothing built into it
	ids, err := jsonInts(keep)
	if err != nil {
		return 0, err
	}
	// Admins are never swept. The sharing list is people you shared WITH
	// and so never contains the owner — an owner who links their own
	// account would be absent from every sync and deactivated by the
	// first one, losing their session and their way in. Guests are what
	// this is for.
	const q = `UPDATE users SET active = 0
		WHERE auth_provider = 'plex' AND active = 1 AND role != 'admin'
		  AND plex_account_id NOT IN (SELECT value FROM json_each(?))`
	res, err := tx.Exec(q, ids)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	if _, err := tx.Exec(`DELETE FROM sessions WHERE user_id IN
		(SELECT id FROM users WHERE auth_provider = 'plex' AND active = 0
		   AND role != 'admin')`); err != nil {
		return 0, err
	}
	return int(n), tx.Commit()
}

// jsonInts renders an id list as one bindable parameter, so a statement
// with an IN clause stays a constant string rather than something built
// per call. An empty list is a valid empty array, which matches nothing.
func jsonInts(ids []int64) (string, error) {
	if ids == nil {
		ids = []int64{}
	}
	b, err := json.Marshal(ids)
	return string(b), err
}

// NewClientID generates this install's identifier for plex.tv. Stored
// once and never regenerated: a PIN is bound to the identifier that
// created it, so a new one strands every sign-in in progress.
func NewClientID() string { return randomToken()[:32] }

// RestorePasswordSignIn puts every admin back on the password path.
//
// The escape hatch for Plex-only sign-in. Linking takes the password
// away automatically, which is what somebody who wants Plex-only auth
// means — but "the only way in is a third party" needs a way back that
// does not go through the thing that broke. Plex being down, an account
// deleted, a tailnet misconfigured: none of those should cost somebody
// their own server.
//
// It is reachable only from the host, by starting the container with an
// environment variable set, which is the right privilege level: whoever
// can set that already owns the machine.
func (s *Store) RestorePasswordSignIn() (int, error) {
	res, err := s.db.Exec(`UPDATE users SET auth_provider = 'local', active = 1
		WHERE role = 'admin' AND auth_provider != 'local'`)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// UnlinkPlexAccount forgets an account's Plex identity and puts it back
// on the password path. Leaving it Plex-only after the link it depended
// on is gone would lock somebody out of their own install.
func (s *Store) UnlinkPlexAccount(userID int64) error {
	_, err := s.db.Exec(`UPDATE users
		SET plex_account_id = NULL, plex_username = '', auth_provider = 'local'
		WHERE id = ?`, userID)
	return err
}
