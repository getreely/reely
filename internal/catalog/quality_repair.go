package catalog

// Rows holding a file the loop can't fully reason about: a blank quality
// used to read as "no file" to the RSS sync, and a blank source counts as
// below any source cutoff. Both are repairable from the file itself to a
// degree — pixels always, source only when an MKV's title tag carries a
// release name.

// RepairableFile is one on-disk file missing its quality, its source, or
// both.
type RepairableFile struct {
	Kind        string // movie | episode
	ID          int64
	Path        string
	NeedQuality bool
	NeedSource  bool
}

// FilesNeedingRepair lists every attached file whose quality or source
// column is empty. Bounded by limit (0 for all) so a repair pass can work
// in chunks rather than holding a whole library in memory.
func (s *Store) FilesNeedingRepair(limit int) ([]RepairableFile, error) {
	q := `SELECT 'movie', id, file_path, COALESCE(quality,'') = '', COALESCE(source,'') = '' FROM movies
			WHERE file_path IS NOT NULL AND file_path != ''
			  AND (COALESCE(quality,'') = '' OR COALESCE(source,'') = '')
		UNION ALL
		SELECT 'episode', id, file_path, COALESCE(quality,'') = '', COALESCE(source,'') = '' FROM episodes
			WHERE file_path IS NOT NULL AND file_path != ''
			  AND (COALESCE(quality,'') = '' OR COALESCE(source,'') = '')`
	args := []any{}
	if limit > 0 {
		q += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []RepairableFile
	for rows.Next() {
		var f RepairableFile
		if err := rows.Scan(&f.Kind, &f.ID, &f.Path, &f.NeedQuality, &f.NeedSource); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// SetQuality and SetSource record one value discovered by probing the
// file itself. Each touches only its column — the path, size and the
// other value stay as they are, since a probe learns one thing at a time.
func (s *Store) SetQuality(kind string, id int64, quality string) error {
	table := "movies"
	if kind == "episode" {
		table = "episodes"
	}
	// table is one of two constants chosen here, never caller input
	_, err := s.db.Exec(`UPDATE `+table+` SET quality = ? WHERE id = ?`, quality, id) //nolint:gosec // G202: constant table name, parameterised values
	return err
}

func (s *Store) SetSource(kind string, id int64, source string) error {
	table := "movies"
	if kind == "episode" {
		table = "episodes"
	}
	_, err := s.db.Exec(`UPDATE `+table+` SET source = ? WHERE id = ?`, source, id) //nolint:gosec // G202: constant table name, parameterised values
	return err
}
