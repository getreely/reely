package catalog

// Detaching files from titles: the "delete the file, keep hunting" path,
// and the scan's reconciliation of rows whose files vanished behind
// reely's back.
//
// Quality and source go with the file, because that is what they
// describe. They used to stay, on the reasoning that with no file they
// gate nothing — true of the grab loop, which reads them only where a
// file path is present, and false of the person looking at the library:
// a title whose file had been deleted went on showing a 720P badge,
// which said the opposite of everything else on the card.

// ClearMovieFile drops a movie's file attachment; the row itself stays.
func (s *Store) ClearMovieFile(id int64) error {
	_, err := s.db.Exec(`UPDATE movies SET file_path = NULL, file_size = 0,
		quality = '', source = '' WHERE id = ?`, id)
	return err
}

// ClearEpisodeFile drops one episode's file attachment.
func (s *Store) ClearEpisodeFile(id int64) error {
	_, err := s.db.Exec(`UPDATE episodes SET file_path = NULL, file_size = 0,
		quality = '', source = '' WHERE id = ?`, id)
	return err
}

// ClearShowFiles drops every episode file attachment of one show,
// reporting how many rows held one.
func (s *Store) ClearShowFiles(showID int64) (int64, error) {
	res, err := s.db.Exec(`UPDATE episodes SET file_path = NULL, file_size = 0,
		quality = '', source = ''
		WHERE show_id = ? AND file_path IS NOT NULL AND file_path != ''`, showID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

// AttachedFile is one row currently claiming a file on disk.
type AttachedFile struct {
	ID   int64 // movie or episode row id, per the query that produced it
	Path string
}

// AttachedMovieFiles lists a library's movies that claim a file.
func (s *Store) AttachedMovieFiles(libraryID int64) ([]AttachedFile, error) {
	return s.attachedFiles(`SELECT id, file_path FROM movies
		WHERE library_id = ? AND file_path IS NOT NULL AND file_path != ''`, libraryID)
}

// AttachedEpisodeFiles lists a library's episodes that claim a file.
func (s *Store) AttachedEpisodeFiles(libraryID int64) ([]AttachedFile, error) {
	return s.attachedFiles(`SELECT e.id, e.file_path FROM episodes e
		JOIN shows sh ON sh.id = e.show_id
		WHERE sh.library_id = ? AND e.file_path IS NOT NULL AND e.file_path != ''`, libraryID)
}

func (s *Store) attachedFiles(query string, libraryID int64) ([]AttachedFile, error) {
	rows, err := s.db.Query(query, libraryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AttachedFile
	for rows.Next() {
		var f AttachedFile
		if err := rows.Scan(&f.ID, &f.Path); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}
