package catalog

// The people read path: a person as the library knows them — their stored
// row plus every title in the library they're billed in. TMDB fills in the
// biography and full filmography; this is the part that works offline and
// the part that says "you already have these".

// LocalCredit is one library title a person is credited on.
type LocalCredit struct {
	Kind      string `json:"kind"` // movie | show
	ID        int64  `json:"id"`
	LibraryID int64  `json:"libraryId"`
	TmdbID    int    `json:"tmdbId"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	Poster    string `json:"poster"`
	Character string `json:"character"`
	OnDisk    bool   `json:"onDisk"`
}

// StoredPerson returns a person's stored name and photo by TMDB id, or
// sql.ErrNoRows if no library title has ever billed them.
func (s *Store) StoredPerson(tmdbID int) (name, photo string, err error) {
	err = s.db.QueryRow(`SELECT name, photo_path FROM people WHERE tmdb_id = ?`, tmdbID).
		Scan(&name, &photo)
	return name, photo, err
}

// PersonCredits lists every library title a person is billed on, newest
// first. A show counts as on disk once any episode has a file.
func (s *Store) PersonCredits(tmdbID int) ([]LocalCredit, error) {
	rows, err := s.db.Query(`
		SELECT 'movie', m.id, COALESCE(m.library_id, 0), m.tmdb_id, m.title,
			COALESCE(m.year, 0), m.poster_path, c.character, m.file_path IS NOT NULL
		FROM credits c
			JOIN people p ON p.id = c.person_id
			JOIN movies m ON m.id = c.movie_id
		WHERE p.tmdb_id = ?
		UNION ALL
		SELECT 'show', sh.id, COALESCE(sh.library_id, 0), sh.tmdb_id, sh.title,
			COALESCE(sh.year, 0), sh.poster_path, c.character,
			EXISTS (SELECT 1 FROM episodes e WHERE e.show_id = sh.id AND e.file_path IS NOT NULL)
		FROM credits c
			JOIN people p ON p.id = c.person_id
			JOIN shows sh ON sh.id = c.show_id
		WHERE p.tmdb_id = ?
		ORDER BY 6 DESC, 5`, tmdbID, tmdbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LocalCredit
	for rows.Next() {
		var lc LocalCredit
		var onDisk int
		if err := rows.Scan(&lc.Kind, &lc.ID, &lc.LibraryID, &lc.TmdbID, &lc.Title,
			&lc.Year, &lc.Poster, &lc.Character, &onDisk); err != nil {
			return nil, err
		}
		lc.OnDisk = onDisk != 0
		out = append(out, lc)
	}
	return out, rows.Err()
}
