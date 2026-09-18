package catalog

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Requests: somebody who may browse a library but not add to it asks for
// a title, and the owner decides. An approved request runs the ordinary
// add — nothing here reimplements it.
//
// A request is scoped to ONE library, which is what makes the answer to
// "has this been asked for already?" cheap and unambiguous. Three people
// sharing a library share its requests; somebody whose own library is
// separate sees the same title as still askable there, because it does
// still have to be added there.

// ErrAlreadyRequested is a title with an open request in that library.
// Not an error the asker did anything wrong — the UI reads it as
// "already requested" and shows the badge.
var ErrAlreadyRequested = errors.New("already requested for this library")

// Request is one ask. Title, year and poster are copied rather than
// looked up: a denied request still has to read as something, and the
// title it named may never be added to this install at all.
type Request struct {
	ID        int64  `json:"id"`
	UserID    int64  `json:"userId"`
	LibraryID int64  `json:"libraryId"`
	Kind      string `json:"kind"` // movie | show
	TmdbID    int    `json:"tmdbId"`
	TvdbID    int    `json:"tvdbId,omitempty"`
	Title     string `json:"title"`
	Year      int    `json:"year,omitempty"`
	Poster    string `json:"poster"`
	// Audience is which of the asker's groups a fulfilled request
	// reaches. Nil means all of them, which is the ordinary case; an
	// empty slice keeps it to the asker alone. Their own group is always
	// included either way.
	Audience []int64 `json:"audience,omitempty"`
	// Seasons is which seasons were asked for; nil means the whole show,
	// and it is always nil for a movie.
	Seasons   []int  `json:"seasons,omitempty"`
	Status    string `json:"status"` // pending | approved | denied
	CreatedAt string `json:"createdAt"`
	DecidedAt string `json:"decidedAt,omitempty"`
	// Username and LibraryName are joined for the owner's queue, where a
	// request has to say who asked and where it would land. They stay
	// empty on the requester's own listing, which names nobody.
	Username    string `json:"username,omitempty"`
	LibraryName string `json:"libraryName,omitempty"`
}

// CreateRequest records an ask. ErrAlreadyRequested when the library
// already has an open one for that title — the partial unique index is
// what decides, so two people asking at the same moment resolve to one
// request rather than a race.
func (s *Store) CreateRequest(r Request) (int64, error) {
	if r.Kind != "movie" && r.Kind != "show" {
		return 0, errors.New("kind must be movie or show")
	}
	if r.Status == "" {
		r.Status = "pending"
	}
	var seasons any
	if len(r.Seasons) > 0 {
		b, err := json.Marshal(r.Seasons)
		if err != nil {
			return 0, err
		}
		seasons = string(b)
	}
	// nil and empty mean different things — all their groups, and none
	// but their own — so the column stays NULL for one and "[]" for the
	// other rather than collapsing both to absent
	var audience any
	if r.Audience != nil {
		blob, err := json.Marshal(r.Audience)
		if err != nil {
			return 0, fmt.Errorf("request audience: %w", err)
		}
		audience = string(blob)
	}
	res, err := s.db.Exec(`INSERT INTO requests
		(user_id, library_id, kind, tmdb_id, tvdb_id, title, year, poster_path,
		 seasons, status, audience)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.UserID, r.LibraryID, r.Kind, nullInt(r.TmdbID), nullInt(r.TvdbID),
		r.Title, nullInt(r.Year), r.Poster, seasons, r.Status, audience)
	if err != nil {
		// sqlite reports the partial unique index by name; nothing else on
		// this table is unique, so a constraint failure is this one
		if strings.Contains(strings.ToLower(err.Error()), "unique") {
			return 0, ErrAlreadyRequested
		}
		return 0, err
	}
	return res.LastInsertId()
}

// HeldAnywhere reports whether any library on this install already
// carries the title. A request for something already here needs no
// decision: the file exists, and adding it to a second library hardlinks
// rather than downloads, so there is nothing for the owner to weigh.
//
// Any library, deliberately — including ones the asker cannot see. What
// they can see is already answered by the "In library" mark, so this
// rule only ever fires on a copy that lives somewhere else.
func (s *Store) HeldAnywhere(kind string, tmdbID, tvdbID int) (bool, error) {
	var n int
	var err error
	if kind == "movie" {
		if tmdbID == 0 {
			return false, nil
		}
		err = s.db.QueryRow(`SELECT COUNT(*) FROM movies WHERE tmdb_id = ?`, tmdbID).Scan(&n)
	} else {
		// a show can arrive under either id: search returns TMDB, and TVDB
		// is what a matched show is keyed on
		if tmdbID == 0 && tvdbID == 0 {
			return false, nil
		}
		err = s.db.QueryRow(`SELECT COUNT(*) FROM shows
			WHERE (? > 0 AND tmdb_id = ?) OR (? > 0 AND tvdb_id = ?)`,
			tmdbID, tmdbID, tvdbID, tvdbID).Scan(&n)
	}
	return n > 0, err
}

// EverDenied reports whether this title was ever turned down for this
// library. Only a watched list asks: a person who was denied may ask
// again, which is the whole point of denial returning a title to
// askable, but a list that re-files every half hour is nagging rather
// than asking, and the owner already answered it.
func (s *Store) EverDenied(libraryID int64, kind string, tmdbID int) (bool, error) {
	if tmdbID == 0 {
		return false, nil
	}
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM requests
		WHERE library_id = ? AND kind = ? AND tmdb_id = ? AND status = 'denied'`,
		libraryID, kind, tmdbID).Scan(&n)
	return n > 0, err
}

// OpenRequests lists the pending and approved requests in a set of
// libraries — what the requester's own view needs to mark titles as
// asked for. It names nobody: the rows carry no username, because a
// requester is told a title has been requested and never by whom.
func (s *Store) OpenRequests(libraryIDs []int64) ([]Request, error) {
	if len(libraryIDs) == 0 {
		return nil, nil
	}
	// The id list binds as ONE parameter — a JSON array unpacked by
	// json_each — so the SQL is a constant string with nothing
	// concatenated into it. Building an IN clause by repeating "?" would
	// have been safe too, but "safe because of what the helper can emit"
	// is a thing a reader has to verify, and this is a thing they don't.
	ids, err := json.Marshal(libraryIDs)
	if err != nil {
		return nil, err
	}
	const q = `SELECT id, user_id, library_id, kind, COALESCE(tmdb_id,0), COALESCE(tvdb_id,0),
		title, COALESCE(year,0), poster_path, seasons, status, created_at,
		COALESCE(decided_at,''), audience
		FROM requests
		WHERE status IN ('pending','approved')
		  AND library_id IN (SELECT value FROM json_each(?))
		ORDER BY created_at DESC`
	rows, err := s.db.Query(q, string(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRequests(rows)
}

// ClearRequestsFor drops the open requests for one title in one library
// — what a removal leaves behind.
//
// A request is a live thing: somebody asked, and until it is decided or
// the title arrives it is the reason a second person is told not to ask
// again. Removing the title ends that, and an approved row that outlives
// its title went on marking it "requested" for everybody, forever, with
// nothing left to fulfil it.
//
// The rows are deleted rather than given a status of their own. The
// alternative needs a migration to widen a CHECK constraint, and there
// is nothing to keep: the removal itself is what history records, and
// leaving a decided-and-undone request behind would only be a second
// answer to whether the title may be asked for again. Denied rows are
// left exactly where they are — a refusal outlives the title it was
// about, which is the point of it.
func (s *Store) ClearRequestsFor(libraryID int64, kind string, tmdbID, tvdbID int64) (int, error) {
	res, err := s.db.Exec(`DELETE FROM requests
		WHERE library_id = ? AND kind = ? AND status IN ('pending','approved')
		  AND ((? > 0 AND tmdb_id = ?) OR (? > 0 AND tvdb_id = ?))`,
		libraryID, kind, tmdbID, tmdbID, tvdbID, tvdbID)
	if err != nil {
		return 0, fmt.Errorf("clearing requests: %w", err)
	}
	n, err := res.RowsAffected()
	return int(n), err
}

// QueuedRequests is the owner's queue: everything still pending, oldest
// first, with the asker and the library named.
func (s *Store) QueuedRequests() ([]Request, error) {
	rows, err := s.db.Query(`SELECT r.id, r.user_id, r.library_id, r.kind,
		COALESCE(r.tmdb_id,0), COALESCE(r.tvdb_id,0), r.title, COALESCE(r.year,0),
		r.poster_path, r.seasons, r.status, r.created_at, COALESCE(r.decided_at,''),
		r.audience, COALESCE(u.username,''), COALESCE(l.name,'')
		FROM requests r
		LEFT JOIN users u ON u.id = r.user_id
		LEFT JOIN libraries l ON l.id = r.library_id
		WHERE r.status = 'pending'
		ORDER BY r.created_at`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Request
	for rows.Next() {
		r, err := scanRequest(rows, true)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetRequest reads one, or sql.ErrNoRows.
func (s *Store) GetRequest(id int64) (Request, error) {
	row := s.db.QueryRow(`SELECT id, user_id, library_id, kind, COALESCE(tmdb_id,0),
		COALESCE(tvdb_id,0), title, COALESCE(year,0), poster_path, seasons, status,
		created_at, COALESCE(decided_at,''), audience
		FROM requests WHERE id = ?`, id)
	return scanRequest(row, false)
}

// DecideRequest approves or denies a pending request. Denying leaves the
// row for the history but drops it out of the open index, which is what
// lets the title be asked for again.
func (s *Store) DecideRequest(id, adminID int64, status string) error {
	if status != "approved" && status != "denied" {
		return errors.New("status must be approved or denied")
	}
	res, err := s.db.Exec(`UPDATE requests
		SET status = ?, decided_at = datetime('now'), decided_by = ?
		WHERE id = ? AND status = 'pending'`, status, nullInt64(adminID), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// RequestsThisWeek counts what an account has asked for in the last
// seven days, for the quota check. Denied requests count: a quota is
// there to bound how often somebody asks, and refusing then re-asking
// would otherwise be free.
func (s *Store) RequestsThisWeek(userID int64, kind string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM requests
		WHERE user_id = ? AND kind = ? AND created_at >= datetime('now','-7 days')`,
		userID, kind).Scan(&n)
	return n, err
}

// scanner is the shared shape of *sql.Row and *sql.Rows.
type scanner interface{ Scan(...any) error }

func scanRequest(sc scanner, joined bool) (Request, error) {
	var r Request
	var seasons, audience sql.NullString
	dest := []any{&r.ID, &r.UserID, &r.LibraryID, &r.Kind, &r.TmdbID, &r.TvdbID,
		&r.Title, &r.Year, &r.Poster, &seasons, &r.Status, &r.CreatedAt, &r.DecidedAt,
		&audience}
	if joined {
		dest = append(dest, &r.Username, &r.LibraryName)
	}
	if err := sc.Scan(dest...); err != nil {
		return r, err
	}
	if seasons.Valid && seasons.String != "" {
		if err := json.Unmarshal([]byte(seasons.String), &r.Seasons); err != nil {
			return r, fmt.Errorf("request %d: seasons: %w", r.ID, err)
		}
	}
	// NULL means every group the asker is in; "[]" means their own only.
	// Collapsing those to one value would lose the difference.
	if audience.Valid {
		r.Audience = []int64{}
		if audience.String != "" {
			if err := json.Unmarshal([]byte(audience.String), &r.Audience); err != nil {
				return r, fmt.Errorf("request %d: audience: %w", r.ID, err)
			}
		}
	}
	return r, nil
}

func scanRequests(rows *sql.Rows) ([]Request, error) {
	var out []Request
	for rows.Next() {
		r, err := scanRequest(rows, false)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func nullInt64(v int64) any {
	if v == 0 {
		return nil
	}
	return v
}
