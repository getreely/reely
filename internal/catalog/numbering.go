package catalog

// Release numbering: the per-show mapping between a show's own (TMDB)
// season numbers and the numbers its releases carry. Release groups
// follow TVDB, and TVDB sometimes disagrees with TMDB about where one
// show ends and another begins — a revival TVDB keeps as S8+ that TMDB
// restarts as a new show's S1. The offset (and a user-supplied TVDB id
// for id-keyed searches) is how such a show stays searchable.

// effectiveTvdbID picks the id the search paths should use: the user's
// override when set, else the id TMDB's external ids carry. The override
// lives in its own column so metadata refreshes never clobber it.
const effectiveTvdbID = `CASE WHEN sh.tvdb_override > 0 THEN sh.tvdb_override ELSE sh.tvdb_id END`

// SetShowNumbering stores a show's release-numbering mapping: the season
// offset (release season = catalog season + offset) and an optional TVDB
// id override for id-keyed searches (0 keeps TMDB's own).
func (s *Store) SetShowNumbering(id int64, seasonOffset, tvdbID int) error {
	_, err := s.db.Exec(`UPDATE shows SET season_offset = ?, tvdb_override = ? WHERE id = ?`,
		seasonOffset, tvdbID, id)
	return err
}

// ShowSeasonOffset answers one show's offset — the library scan asks per
// matched show, without loading the whole detail tree.
func (s *Store) ShowSeasonOffset(id int64) (int, error) {
	var offset int
	err := s.db.QueryRow(`SELECT season_offset FROM shows WHERE id = ?`, id).Scan(&offset)
	return offset, err
}
