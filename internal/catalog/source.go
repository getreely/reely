package catalog

import (
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/getreely/reely/internal/metadata"
)

// Show sources: 'tmdb' rows live and refresh against TMDB (the original
// behavior, and the fallback when no TVDB key is configured); 'tvdb' rows
// get identity and episode tree from TheTVDB. The tmdb_id stays on tvdb
// rows when known — cast, discovery, and person pages join through it.

// ShowIDByTvdb finds the show claiming a TVDB series in one library —
// the dedupe the TVDB add and migration paths key on. 0 when none.
func (s *Store) ShowIDByTvdb(libraryID int64, tvdbID int) (int64, error) {
	if tvdbID == 0 {
		return 0, nil
	}
	var id int64
	err := s.db.QueryRow(`SELECT id FROM shows
		WHERE library_id = ? AND (tvdb_id = ? OR tvdb_override = ?) LIMIT 1`,
		libraryID, tvdbID, tvdbID).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

// MarkShowSource stamps which provider a show's metadata comes from,
// clearing the manual numbering overrides when the source becomes tvdb —
// a TVDB-sourced tree IS scene numbering, so an old offset would break
// every search. tmdbID > 0 also records the cross-provider join id.
func (s *Store) MarkShowSource(id int64, source string, tmdbID int) error {
	if source != "tmdb" && source != "tvdb" {
		return errors.New("source must be tmdb or tvdb")
	}
	if _, err := s.db.Exec(`UPDATE shows SET source = ? WHERE id = ?`, source, id); err != nil {
		return err
	}
	if tmdbID > 0 {
		if _, err := s.db.Exec(`UPDATE shows SET tmdb_id = ? WHERE id = ?`, tmdbID, id); err != nil {
			return err
		}
	}
	if source == "tvdb" {
		if _, err := s.db.Exec(`UPDATE shows SET season_offset = 0, tvdb_override = 0 WHERE id = ?`, id); err != nil {
			return err
		}
	}
	return nil
}

// UpsertShowTVDB writes a TVDB-sourced show into a library: refreshed in
// place when the series (or its TMDB twin) is already there, inserted
// otherwise. Keyed by the TVDB series id — the (tmdb, library) upsert
// can't dedupe rows whose TMDB id is unknown.
func (s *Store) UpsertShowTVDB(d *metadata.ShowDetail, libraryID int64) (int64, error) {
	if d.TvdbID == 0 {
		return 0, errors.New("a TVDB-sourced show needs its TVDB id")
	}
	id, err := s.ShowIDByTvdb(libraryID, d.TvdbID)
	if err != nil {
		return 0, err
	}
	if id == 0 && d.TmdbID > 0 {
		// the same series may already be here as a TMDB-sourced row —
		// adding it via TVDB upgrades that row rather than duplicating it
		if err := s.db.QueryRow(`SELECT id FROM shows WHERE tmdb_id = ? AND library_id = ?`,
			d.TmdbID, libraryID).Scan(&id); err != nil && err != sql.ErrNoRows {
			return 0, err
		}
	}
	if id > 0 {
		if _, err := s.RefreshShow(id, d); err != nil {
			return 0, err
		}
		return id, s.MarkShowSource(id, "tvdb", d.TmdbID)
	}

	genres, _ := json.Marshal(d.Genres)
	aliases, _ := json.Marshal(orEmpty(d.Aliases))
	res, err := s.db.Exec(`
		INSERT INTO shows (tmdb_id, title, year, overview, status, genres, aliases, poster_path,
			backdrop_path, imdb_id, tvdb_id, library_id, quality_profile_id, source, refreshed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			(SELECT quality_profile_id FROM libraries WHERE id = ?), 'tvdb', datetime('now'))`,
		nullInt(d.TmdbID), d.Title, nullInt(d.Year), d.Overview, d.Status, string(genres),
		string(aliases), d.Poster, d.Backdrop, d.ImdbID, d.TvdbID, libraryID, libraryID)
	if err != nil {
		return 0, err
	}
	id, err = res.LastInsertId()
	if err != nil {
		return 0, err
	}
	if err := s.saveSeasons(id, d); err != nil {
		return 0, err
	}
	return id, s.saveCast(d.Cast, 0, id)
}

// LibrariesHoldingTvdb lists the library ids carrying a TVDB series —
// the preview page's "already in" marks when shows are TVDB-sourced.
func (s *Store) LibrariesHoldingTvdb(tvdbID int) ([]int64, error) {
	rows, err := s.db.Query(`SELECT COALESCE(library_id, 0) FROM shows
		WHERE tvdb_id = ? OR tvdb_override = ?`, tvdbID, tvdbID)
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

// ShowSeasonNumbers lists the seasons a show currently has — the
// migration snapshots them so seasons that arrive WITH the migration can
// start unmonitored instead of triggering a download sweep.
func (s *Store) ShowSeasonNumbers(showID int64) (map[int]bool, error) {
	rows, err := s.db.Query(`SELECT DISTINCT season FROM episodes WHERE show_id = ?`, showID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int]bool{}
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out[n] = true
	}
	return out, rows.Err()
}

// UnmonitorSeasonsExcept turns monitoring off for every season NOT in
// keep — how migration-discovered seasons arrive quiet.
func (s *Store) UnmonitorSeasonsExcept(showID int64, keep map[int]bool) (int, error) {
	rows, err := s.db.Query(`SELECT DISTINCT season FROM episodes WHERE show_id = ?`, showID)
	if err != nil {
		return 0, err
	}
	var newSeasons []int
	for rows.Next() {
		var n int
		if err := rows.Scan(&n); err != nil {
			rows.Close()
			return 0, err
		}
		if !keep[n] {
			newSeasons = append(newSeasons, n)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, err
	}
	for _, n := range newSeasons {
		if _, err := s.db.Exec(`UPDATE episodes SET monitored = 0 WHERE show_id = ? AND season = ?`,
			showID, n); err != nil {
			return len(newSeasons), err
		}
	}
	return len(newSeasons), nil
}

// MigrationShow is one TMDB-sourced show as the migration planner sees it.
type MigrationShow struct {
	ID        int64  `json:"id"`
	Title     string `json:"title"`
	Year      int    `json:"year"`
	LibraryID int64  `json:"libraryId"`
	TvdbID    int    `json:"tvdbId"` // effective: override first, else TMDB's mapping
}

// TmdbSourcedShows lists every show still on TMDB, with the TVDB id each
// would migrate to (0 = unknown, needs a search or a human).
func (s *Store) TmdbSourcedShows() ([]MigrationShow, error) {
	rows, err := s.db.Query(`SELECT id, title, COALESCE(year, 0), COALESCE(library_id, 0),
		CASE WHEN tvdb_override > 0 THEN tvdb_override ELSE COALESCE(tvdb_id, 0) END
		FROM shows WHERE source = 'tmdb' ORDER BY title`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MigrationShow
	for rows.Next() {
		var m MigrationShow
		if err := rows.Scan(&m.ID, &m.Title, &m.Year, &m.LibraryID, &m.TvdbID); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// CountShowsBySource answers the migration panel's headline numbers.
func (s *Store) CountShowsBySource() (tmdb, tvdb int, err error) {
	err = s.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM shows WHERE source = 'tmdb'),
		(SELECT COUNT(*) FROM shows WHERE source = 'tvdb')`).Scan(&tmdb, &tvdb)
	return tmdb, tvdb, err
}
