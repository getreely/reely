package catalog

// Sibling rows: the same TMDB title living in other libraries. One file on
// disk (hard-linked into each collection) can back all of them, and a grab
// for one satisfies the rest.

// SiblingMovies lists the other library rows carrying this movie's TMDB id.
func (s *Store) SiblingMovies(movieID int64) ([]Movie, error) {
	rows, err := s.db.Query(`SELECT `+movieColumns+`
		FROM movies
		WHERE id != ? AND tmdb_id IS NOT NULL
			AND tmdb_id = (SELECT tmdb_id FROM movies WHERE id = ?)`, movieID, movieID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMovies(rows)
}

// SiblingShowIDs lists the other library rows carrying this show's TMDB id.
func (s *Store) SiblingShowIDs(showID int64) ([]int64, error) {
	rows, err := s.db.Query(`SELECT id FROM shows
		WHERE id != ? AND tmdb_id IS NOT NULL
			AND tmdb_id = (SELECT tmdb_id FROM shows WHERE id = ?)`, showID, showID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
