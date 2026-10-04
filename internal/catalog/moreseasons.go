package catalog

import (
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
)

// Asking for more of a show that is already here: somebody asked for
// season 1, it arrived, and now they want season 2. Holding a show is not
// holding every season of it, so "in the library" can't be the end of
// asking, the way it is for a film.

// SeasonsAsked is which of a show's seasons have been asked for: those
// with any episode monitored. A season somebody switched off, or never
// asked for, is the part of the show that can still be asked for.
func (s *Store) SeasonsAsked(showID int64) ([]int, error) {
	rows, err := s.db.Query(`SELECT DISTINCT season FROM episodes
		WHERE show_id = ? AND monitored = 1 ORDER BY season`, showID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []int{}
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// HeldShowID is the row for a show already in a library, found by either
// of its ids; 0 when it isn't there.
func (s *Store) HeldShowID(libraryID int64, tmdbID, tvdbID int) (int64, error) {
	if tmdbID > 0 {
		id, err := s.ShowIDByTmdb(libraryID, tmdbID)
		if err != nil || id != 0 {
			return id, err
		}
	}
	return s.ShowIDByTvdb(libraryID, tvdbID)
}

// OpenRequestFor is the pending or approved request for a title in one
// library — the row the unique index lets there be only one of — or nil.
func (s *Store) OpenRequestFor(libraryID int64, kind string, tmdbID int) (*Request, error) {
	if tmdbID == 0 {
		return nil, nil
	}
	row := s.db.QueryRow(`SELECT id, user_id, library_id, kind, COALESCE(tmdb_id,0), COALESCE(tvdb_id,0),
		title, COALESCE(year,0), poster_path, seasons, status, created_at,
		COALESCE(decided_at,''), audience
		FROM requests
		WHERE library_id = ? AND kind = ? AND tmdb_id = ? AND status IN ('pending','approved')
		LIMIT 1`, libraryID, kind, tmdbID)
	r, err := scanRequest(row, false)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// SetRequestSeasons records what a request now covers, after more of the
// show was asked for. Nil is the whole show.
func (s *Store) SetRequestSeasons(id int64, seasons []int) error {
	var v any
	if seasons != nil {
		b, err := json.Marshal(seasons)
		if err != nil {
			return err
		}
		v = string(b)
	}
	_, err := s.db.Exec(`UPDATE requests SET seasons = ? WHERE id = ?`, v, id)
	return err
}

// UnionSeasons is every season in either list, in order. Nil on either
// side is the whole show, so the union of it with anything is too.
func UnionSeasons(a, b []int) []int {
	if a == nil || b == nil {
		return nil
	}
	seen := map[int]bool{}
	out := []int{}
	for _, list := range [][]int{a, b} {
		for _, n := range list {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
	}
	sort.Ints(out)
	return out
}

// SeasonsNotIn is which of [want] aren't in [have].
func SeasonsNotIn(want, have []int) []int {
	held := map[int]bool{}
	for _, n := range have {
		held[n] = true
	}
	out := []int{}
	for _, n := range want {
		if !held[n] {
			out = append(out, n)
		}
	}
	return out
}
