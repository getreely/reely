package catalog

import "strings"

// WantedTarget addresses one monitored, missing, findable title: a movie,
// or one aired episode.
type WantedTarget struct {
	MovieID int64
	ShowID  int64
	Season  int
	Episode int
}

// Wanted lists everything the backlog search would go hunting for:
// monitored movies with no file, and monitored aired episodes with no file
// on monitored shows. Unaired episodes are excluded — nothing to find yet,
// and their day belongs to the release-day pass.
func (s *Store) Wanted(today string) ([]WantedTarget, error) {
	return s.WantedIn(today, "", nil)
}

// WantedIn is Wanted narrowed to what someone is actually looking at: one
// kind ("movies" or "shows", empty for both) within a set of libraries
// (nil for all of them). It backs the per-library "search missing" button,
// where the sweep should cover exactly the titles on screen and nothing
// the viewer can't see.
func (s *Store) WantedIn(today, kind string, libraryIDs []int64) ([]WantedTarget, error) {
	// an empty (but non-nil) library set means "you may see nothing", which
	// must find nothing rather than quietly widening to everything
	if libraryIDs != nil && len(libraryIDs) == 0 {
		return nil, nil
	}
	movieWhere, showWhere := "", ""
	args := []any{}
	if libraryIDs != nil {
		in := "(" + strings.TrimSuffix(strings.Repeat("?,", len(libraryIDs)), ",") + ")"
		movieWhere = " AND m.library_id IN " + in
		showWhere = " AND sh.library_id IN " + in
	}
	q := ""
	if kind != "shows" {
		q = `SELECT m.id, 0, 0, 0
			FROM movies m
			WHERE m.monitored = 1 AND m.file_path IS NULL` + movieWhere
		for _, id := range libraryIDs {
			args = append(args, id)
		}
	}
	if kind != "movies" {
		if q != "" {
			q += "\nUNION ALL\n"
		}
		q += `SELECT 0, e.show_id, e.season, e.episode
			FROM episodes e JOIN shows sh ON sh.id = e.show_id
			WHERE e.monitored = 1 AND sh.monitored = 1 AND e.file_path IS NULL
				AND e.air_date IS NOT NULL AND substr(e.air_date, 1, 10) <= ?` + showWhere
		args = append(args, today)
		for _, id := range libraryIDs {
			args = append(args, id)
		}
	}
	if q == "" {
		return nil, nil
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []WantedTarget
	for rows.Next() {
		var t WantedTarget
		if err := rows.Scan(&t.MovieID, &t.ShowID, &t.Season, &t.Episode); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}
