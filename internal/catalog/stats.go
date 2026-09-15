package catalog

// Install-wide statistics for the admin dashboard: what the libraries hold,
// how much disk it costs, and how the loop has been doing lately.

// LibraryStats is one library's slice of the collection.
type LibraryStats struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Path    string `json:"path"`
	Titles  int    `json:"titles"`  // movies, or shows
	OnDisk  int    `json:"onDisk"`  // movies with a file, or episodes with one
	Missing int    `json:"missing"` // monitored and still wanted
	Bytes   int64  `json:"bytes"`   // hard-linked copies count once per library
}

// Stats is the whole install at a glance.
type Stats struct {
	Movies         int            `json:"movies"`
	MoviesOnDisk   int            `json:"moviesOnDisk"`
	Shows          int            `json:"shows"`
	Episodes       int            `json:"episodes"`
	EpisodesOnDisk int            `json:"episodesOnDisk"`
	TotalBytes     int64          `json:"totalBytes"`
	Grabs30d       int            `json:"grabs30d"`
	Imports30d     int            `json:"imports30d"`
	Failures30d    int            `json:"failures30d"`
	Blocklisted    int            `json:"blocklisted"`
	Libraries      []LibraryStats `json:"libraries"`
}

// GetStats gathers the dashboard numbers in a handful of aggregate
// queries. Bytes count every directory entry — a title hard-linked into
// three libraries appears in each library's total, matching what a folder
// scan of that library would report.
func (s *Store) GetStats() (*Stats, error) {
	st := &Stats{Libraries: []LibraryStats{}}
	err := s.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM movies),
		(SELECT COUNT(*) FROM movies WHERE file_path IS NOT NULL),
		(SELECT COUNT(*) FROM shows),
		(SELECT COUNT(*) FROM episodes),
		(SELECT COUNT(*) FROM episodes WHERE file_path IS NOT NULL),
		(SELECT COALESCE(SUM(file_size),0) FROM movies) + (SELECT COALESCE(SUM(file_size),0) FROM episodes),
		(SELECT COUNT(*) FROM history WHERE kind = 'grabbed'  AND created_at > datetime('now','-30 days')),
		(SELECT COUNT(*) FROM history WHERE kind = 'imported' AND created_at > datetime('now','-30 days')),
		(SELECT COUNT(*) FROM history WHERE kind = 'failed'   AND created_at > datetime('now','-30 days')),
		(SELECT COUNT(*) FROM blocklist)`).Scan(
		&st.Movies, &st.MoviesOnDisk, &st.Shows, &st.Episodes, &st.EpisodesOnDisk,
		&st.TotalBytes, &st.Grabs30d, &st.Imports30d, &st.Failures30d, &st.Blocklisted)
	if err != nil {
		return nil, err
	}

	rows, err := s.db.Query(`SELECT l.id, l.name, l.kind, l.path,
		CASE l.kind WHEN 'movies'
			THEN (SELECT COUNT(*) FROM movies m WHERE m.library_id = l.id)
			ELSE (SELECT COUNT(*) FROM shows sh WHERE sh.library_id = l.id) END,
		CASE l.kind WHEN 'movies'
			THEN (SELECT COUNT(*) FROM movies m WHERE m.library_id = l.id AND m.file_path IS NOT NULL)
			ELSE (SELECT COUNT(*) FROM episodes e JOIN shows sh ON sh.id = e.show_id
				WHERE sh.library_id = l.id AND e.file_path IS NOT NULL) END,
		CASE l.kind WHEN 'movies'
			THEN (SELECT COUNT(*) FROM movies m WHERE m.library_id = l.id AND m.monitored = 1 AND m.file_path IS NULL)
			ELSE (SELECT COUNT(*) FROM episodes e JOIN shows sh ON sh.id = e.show_id
				WHERE sh.library_id = l.id AND sh.monitored = 1 AND e.monitored = 1 AND e.file_path IS NULL
				AND e.air_date IS NOT NULL AND substr(e.air_date,1,10) <= date('now')) END,
		CASE l.kind WHEN 'movies'
			THEN (SELECT COALESCE(SUM(m.file_size),0) FROM movies m WHERE m.library_id = l.id)
			ELSE (SELECT COALESCE(SUM(e.file_size),0) FROM episodes e JOIN shows sh ON sh.id = e.show_id
				WHERE sh.library_id = l.id) END
		FROM libraries l ORDER BY l.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var ls LibraryStats
		if err := rows.Scan(&ls.ID, &ls.Name, &ls.Kind, &ls.Path, &ls.Titles, &ls.OnDisk, &ls.Missing, &ls.Bytes); err != nil {
			return nil, err
		}
		st.Libraries = append(st.Libraries, ls)
	}
	return st, rows.Err()
}
