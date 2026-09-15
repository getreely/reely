package catalog

import (
	"fmt"
	"strings"
)

// The blocklist: release names that were grabbed and then failed. The
// automatic grab paths refuse to fetch these again; deleting the row is
// the pardon. Manual grabs stay an override — a deliberate click may know
// better.

// BlocklistEntry is one banned release, with its title's name for display.
type BlocklistEntry struct {
	ID           int64  `json:"id"`
	ReleaseTitle string `json:"releaseTitle"`
	Indexer      string `json:"indexer,omitempty"`
	MovieID      int64  `json:"movieId,omitempty"`
	ShowID       int64  `json:"showId,omitempty"`
	ForTitle     string `json:"forTitle,omitempty"`
	Reason       string `json:"reason,omitempty"`
	CreatedAt    string `json:"createdAt"`
}

// AddBlocklist bans one release name. Re-banning refreshes the reason and
// timestamp. movieID/showID are best-effort context — zero is fine.
func (s *Store) AddBlocklist(releaseTitle, indexer string, movieID, showID int64, reason string) error {
	_, err := s.db.Exec(`INSERT INTO blocklist (release_title, indexer, movie_id, show_id, reason)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(release_title, indexer) DO UPDATE SET
			reason = excluded.reason, created_at = datetime('now')`,
		releaseTitle, indexer, nullID(movieID), nullID(showID), reason)
	return err
}

// ListBlocklist returns the blocklist newest first.
func (s *Store) ListBlocklist() ([]BlocklistEntry, error) {
	return s.listBlocklist(1000000, 0)
}

// ListBlocklistPage is one page of the blocklist plus the total count.
func (s *Store) ListBlocklistPage(limit, offset int) ([]BlocklistEntry, int, error) {
	entries, err := s.listBlocklist(limit, offset)
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM blocklist`).Scan(&total); err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}

func (s *Store) listBlocklist(limit, offset int) ([]BlocklistEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(`
		SELECT b.id, b.release_title, b.indexer, COALESCE(b.movie_id, 0), COALESCE(b.show_id, 0),
			COALESCE(m.title, sh.title, ''), b.reason, b.created_at
		FROM blocklist b
			LEFT JOIN movies m ON m.id = b.movie_id
			LEFT JOIN shows sh ON sh.id = b.show_id
		ORDER BY b.created_at DESC, b.id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []BlocklistEntry
	for rows.Next() {
		var e BlocklistEntry
		if err := rows.Scan(&e.ID, &e.ReleaseTitle, &e.Indexer, &e.MovieID, &e.ShowID,
			&e.ForTitle, &e.Reason, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// RemoveBlocklist pardons the given entries. Returns how many rows went.
func (s *Store) RemoveBlocklist(ids []int64) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	args := make([]any, len(ids))
	marks := make([]string, len(ids))
	for i, id := range ids {
		args[i], marks[i] = id, "?"
	}
	res, err := s.db.Exec( //nolint:gosec // G202: only "?" placeholders are joined in
		fmt.Sprintf(`DELETE FROM blocklist WHERE id IN (%s)`, strings.Join(marks, ",")), args...)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// Blocked is the blocklist in the shape the grab paths ask about it. A ban
// is scoped to the indexer the release came from: indexers carry each
// other's posts under identical names, and one bad upload says nothing
// about another indexer's copy. A ban with no indexer recorded applies
// everywhere — the safe reading when the origin is unknown.
type Blocked struct {
	anywhere   map[string]bool
	perIndexer map[string]bool // release title + "\n" + indexer
}

// Has reports whether this release, from this indexer, is banned.
func (b Blocked) Has(releaseTitle, indexer string) bool {
	return b.anywhere[releaseTitle] || b.perIndexer[releaseTitle+"\n"+indexer]
}

// BlockedReleases loads the blocklist for the grab paths to consult.
func (s *Store) BlockedReleases() (Blocked, error) {
	b := Blocked{anywhere: map[string]bool{}, perIndexer: map[string]bool{}}
	rows, err := s.db.Query(`SELECT release_title, indexer FROM blocklist`)
	if err != nil {
		return b, err
	}
	defer rows.Close()
	for rows.Next() {
		var title, indexer string
		if err := rows.Scan(&title, &indexer); err != nil {
			return b, err
		}
		if indexer == "" {
			b.anywhere[title] = true
			continue
		}
		b.perIndexer[title+"\n"+indexer] = true
	}
	return b, rows.Err()
}
