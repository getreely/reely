package catalog

// Organize: reading every file a library holds alongside the facts its
// name should be built from, and repointing rows when a file moves.

// OrganizeFile is one file on disk with everything the naming template
// needs. Episodes sharing a path (a multi-episode file) come back once per
// row; the planner groups them.
type OrganizeFile struct {
	Kind         string // movie | episode
	LibraryID    int64
	LibraryPath  string
	Path         string
	Title        string
	Year         int
	Season       int
	Episode      int
	EpisodeTitle string
	Quality      string
	Source       string
	// The title's ids, so a naming template can put them in the path. A
	// scanner reading a folder called "Pinocchio (2022)" has two films to
	// choose between and no way to choose; one reading "{tmdb-532639}"
	// has none.
	TmdbID int
	TvdbID int
	ImdbID string
}

// OrganizeFilesFor lists the on-disk files of ONE title.
//
// Re-matching a single film should rename that film, not walk the whole
// library: an owner correcting one entry has not asked for six hundred
// others to move, and making them wait for that is how a small fix stops
// being worth doing.
func (s *Store) OrganizeFilesFor(kind string, id int64) ([]OrganizeFile, error) {
	if kind == "movie" {
		return s.organizeQuery(`SELECT l.id, l.path, m.file_path, m.title,
				COALESCE(m.year,0), COALESCE(m.quality,''), COALESCE(m.source,''),
				COALESCE(m.tmdb_id,0), 0, COALESCE(m.imdb_id,'')
			FROM movies m JOIN libraries l ON l.id = m.library_id
			WHERE m.id = ? AND m.file_path IS NOT NULL AND m.file_path != ''`,
			false, id)
	}
	return s.organizeQuery(`SELECT l.id, l.path, e.file_path, sh.title,
			COALESCE(sh.year,0), e.season, e.episode, COALESCE(e.title,''),
			COALESCE(e.quality,''), COALESCE(e.source,''),
			COALESCE(sh.tmdb_id,0), COALESCE(sh.tvdb_id,0), COALESCE(sh.imdb_id,'')
		FROM episodes e
		JOIN shows sh ON sh.id = e.show_id
		JOIN libraries l ON l.id = sh.library_id
		WHERE sh.id = ? AND e.file_path IS NOT NULL AND e.file_path != ''`,
		true, id)
}

// organizeQuery runs one of the two shapes above and scans it.
func (s *Store) organizeQuery(q string, episodes bool, args ...any) ([]OrganizeFile, error) {
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []OrganizeFile{}
	for rows.Next() {
		f := OrganizeFile{Kind: "movie"}
		if episodes {
			f.Kind = "episode"
			if err := rows.Scan(&f.LibraryID, &f.LibraryPath, &f.Path, &f.Title, &f.Year,
				&f.Season, &f.Episode, &f.EpisodeTitle, &f.Quality, &f.Source,
				&f.TmdbID, &f.TvdbID, &f.ImdbID); err != nil {
				return nil, err
			}
		} else if err := rows.Scan(&f.LibraryID, &f.LibraryPath, &f.Path, &f.Title, &f.Year,
			&f.Quality, &f.Source, &f.TmdbID, &f.TvdbID, &f.ImdbID); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// OrganizeFiles lists every on-disk file in one library, or in all of them
// when libraryID is 0.
func (s *Store) OrganizeFiles(libraryID int64) ([]OrganizeFile, error) {
	var out []OrganizeFile
	movieQ := `SELECT l.id, l.path, m.file_path, m.title, COALESCE(m.year,0),
			COALESCE(m.quality,''), COALESCE(m.source,''),
			COALESCE(m.tmdb_id,0), 0, COALESCE(m.imdb_id,'')
		FROM movies m JOIN libraries l ON l.id = m.library_id
		WHERE m.file_path IS NOT NULL AND m.file_path != ''`
	epQ := `SELECT l.id, l.path, e.file_path, sh.title, COALESCE(sh.year,0),
			e.season, e.episode, COALESCE(e.title,''),
			COALESCE(e.quality,''), COALESCE(e.source,''),
			COALESCE(sh.tmdb_id,0), COALESCE(sh.tvdb_id,0), COALESCE(sh.imdb_id,'')
		FROM episodes e
		JOIN shows sh ON sh.id = e.show_id
		JOIN libraries l ON l.id = sh.library_id
		WHERE e.file_path IS NOT NULL AND e.file_path != ''`
	args := []any{}
	if libraryID > 0 {
		movieQ += ` AND l.id = ?`
		epQ += ` AND l.id = ?`
		args = append(args, libraryID)
	}

	rows, err := s.db.Query(movieQ, args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		f := OrganizeFile{Kind: "movie"}
		if err := rows.Scan(&f.LibraryID, &f.LibraryPath, &f.Path, &f.Title, &f.Year,
			&f.Quality, &f.Source, &f.TmdbID, &f.TvdbID, &f.ImdbID); err != nil {
			rows.Close()
			return nil, err
		}
		out = append(out, f)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	rows, err = s.db.Query(epQ, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		f := OrganizeFile{Kind: "episode"}
		if err := rows.Scan(&f.LibraryID, &f.LibraryPath, &f.Path, &f.Title, &f.Year,
			&f.Season, &f.Episode, &f.EpisodeTitle, &f.Quality, &f.Source,
			&f.TmdbID, &f.TvdbID, &f.ImdbID); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// RepathFile repoints every row holding oldPath at newPath. A multi-episode
// file backs several rows and a moved file must not leave any of them
// pointing at a path that no longer exists. Returns how many rows moved.
func (s *Store) RepathFile(oldPath, newPath string) (int, error) {
	moved := 0
	res, err := s.db.Exec(`UPDATE movies SET file_path = ? WHERE file_path = ?`, newPath, oldPath)
	if err != nil {
		return 0, err
	}
	if n, err := res.RowsAffected(); err == nil {
		moved += int(n)
	}
	res, err = s.db.Exec(`UPDATE episodes SET file_path = ? WHERE file_path = ?`, newPath, oldPath)
	if err != nil {
		return moved, err
	}
	if n, err := res.RowsAffected(); err == nil {
		moved += int(n)
	}
	return moved, nil
}
