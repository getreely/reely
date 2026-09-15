package catalog

// Refresh bookkeeping: which titles' TMDB records are due a re-fetch.
// Two tiers — things whose dates still matter refresh daily; settled
// things weekly. The weekly pass is what catches an ended show being
// resurrected: its status flips on TMDB, and from then on it's daily.

// RefreshTarget addresses one title due a metadata refresh.
type RefreshTarget struct {
	Kind      string // movie | show
	ID        int64
	TmdbID    int
	LibraryID int64
	// Source and TvdbID route shows: a 'tvdb' show re-fetches from TheTVDB
	// by its series id, everything else from TMDB. Movies are always tmdb.
	Source string
	TvdbID int
}

// DueForRefresh lists the stalest titles due a refresh, oldest first:
//   - continuing shows: daily (new episodes feed the calendar)
//   - ended/canceled shows: weekly (resurrections still get noticed)
//   - movies missing their file or digital date: daily (the release-day
//     pass hunts by that date)
//   - settled movies: weekly (artwork and metadata drift)
//
// NULL refreshed_at (rows from before the column) sorts first.
func (s *Store) DueForRefresh(limit int) ([]RefreshTarget, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(`
		SELECT kind, id, tmdb_id, library_id, source, tvdb_id, refreshed_at FROM (
			SELECT 'movie' AS kind, m.id, m.tmdb_id, COALESCE(m.library_id, 0) AS library_id,
				'tmdb' AS source, 0 AS tvdb_id,
				COALESCE(m.refreshed_at, '') AS refreshed_at
			FROM movies m
			WHERE m.tmdb_id IS NOT NULL AND COALESCE(m.refreshed_at, '') <= CASE
				WHEN m.file_path IS NULL OR COALESCE(m.digital_release, '') = ''
					THEN datetime('now', '-1 day') ELSE datetime('now', '-7 days') END
			UNION ALL
			SELECT 'show', sh.id, COALESCE(sh.tmdb_id, 0), COALESCE(sh.library_id, 0),
				sh.source, COALESCE(sh.tvdb_id, 0),
				COALESCE(sh.refreshed_at, '')
			FROM shows sh
			WHERE (sh.tmdb_id IS NOT NULL OR (sh.source = 'tvdb' AND sh.tvdb_id > 0))
				AND COALESCE(sh.refreshed_at, '') <= CASE
				WHEN sh.status IN ('Ended', 'Canceled')
					THEN datetime('now', '-7 days') ELSE datetime('now', '-1 day') END
		) ORDER BY refreshed_at, id LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RefreshTarget
	for rows.Next() {
		var t RefreshTarget
		var stamp string
		if err := rows.Scan(&t.Kind, &t.ID, &t.TmdbID, &t.LibraryID, &t.Source, &t.TvdbID, &stamp); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// TouchRefreshed stamps a title's refresh time without changing anything
// else — used when the refresh attempt failed, so one broken title
// rotates to the back of the queue instead of starving the rest.
func (s *Store) TouchRefreshed(kind string, id int64) error {
	q := `UPDATE movies SET refreshed_at = datetime('now') WHERE id = ?`
	if kind == "show" {
		q = `UPDATE shows SET refreshed_at = datetime('now') WHERE id = ?`
	}
	_, err := s.db.Exec(q, id)
	return err
}

// RefreshTargetFor loads one title's refresh identity — the on-demand
// counterpart of DueForRefresh, for the per-title refresh button.
func (s *Store) RefreshTargetFor(kind string, id int64) (RefreshTarget, error) {
	t := RefreshTarget{Kind: kind, ID: id, Source: "tmdb"}
	var err error
	if kind == "movie" {
		err = s.db.QueryRow(`SELECT COALESCE(tmdb_id, 0), COALESCE(library_id, 0) FROM movies WHERE id = ?`, id).
			Scan(&t.TmdbID, &t.LibraryID)
	} else {
		err = s.db.QueryRow(`SELECT COALESCE(tmdb_id, 0), COALESCE(library_id, 0), source, COALESCE(tvdb_id, 0)
			FROM shows WHERE id = ?`, id).
			Scan(&t.TmdbID, &t.LibraryID, &t.Source, &t.TvdbID)
	}
	return t, err
}
