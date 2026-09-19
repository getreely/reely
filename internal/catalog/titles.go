package catalog

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/getreely/reely/internal/metadata"
)

// Movie is a stored movie row with its on-disk state.
type Movie struct {
	ID       int64    `json:"id"`
	TmdbID   int      `json:"tmdbId"`
	Title    string   `json:"title"`
	Year     int      `json:"year"`
	Overview string   `json:"overview"`
	Runtime  int      `json:"runtime"`
	Genres   []string `json:"genres"`
	// Aliases are the movie's other names (TMDB alternative + original
	// titles) — what release names carry when they don't use the
	// canonical one.
	Aliases        []string `json:"aliases,omitempty"`
	ReleaseDate    string   `json:"releaseDate"`
	DigitalRelease string   `json:"digitalRelease"`
	// MinAvailability gates the automatic paths: 'released' (default)
	// waits for the movie to actually be out before RSS/auto search may
	// grab; 'announced' lifts the gate. Manual grabs are never gated.
	MinAvailability string `json:"minAvailability"`
	Poster          string `json:"poster"`
	Backdrop        string `json:"backdrop"`
	ImdbID          string `json:"imdbId"`
	LibraryID       int64  `json:"libraryId"`
	ProfileID       int64  `json:"qualityProfileId"`
	Monitored       bool   `json:"monitored"`
	// Downloading is filled in by the API from the download client's
	// queue, never stored: a job in flight is not a property of the row.
	Downloading bool   `json:"downloading,omitempty"`
	FilePath    string `json:"filePath"`
	FileSize    int64  `json:"fileSize"`
	Quality     string `json:"quality"`
	Source      string `json:"source"` // dvd/hdtv/web/bluray/remux, "" unknown
	AddedAt     string `json:"addedAt"`
	// ImportedAt is when this movie's file landed, empty if none has. It is
	// not the same question as AddedAt: a title monitored months before it
	// releases becomes newly watchable the day it imports, which is when
	// Home should surface it.
	ImportedAt string `json:"importedAt"`
	// GroupIDs is which groups may see this title. Attached for the
	// owner only, and only where the grid needs it — it is the shape of
	// every household on the install, which is not a requester's to see.
	GroupIDs []int64 `json:"groupIds,omitempty"`
}

// movieColumns is the select list scanMovies expects, shared so the two
// queries feeding it cannot drift apart.
const movieColumns = `id, tmdb_id, title, COALESCE(year,0), overview, COALESCE(runtime,0), genres, aliases,
	COALESCE(release_date,''), COALESCE(digital_release,''), COALESCE(min_availability,'released'), poster_path, backdrop_path,
	imdb_id, COALESCE(library_id,0), COALESCE(quality_profile_id,0), monitored,
	COALESCE(file_path,''), COALESCE(file_size,0), quality, COALESCE(source,''), COALESCE(added_at,''),
	COALESCE((SELECT MAX(h.created_at) FROM history h
		WHERE h.movie_id = movies.id AND h.kind = 'imported'), '')`

// Show is a stored show row; episode counts are filled by the read path.
type Show struct {
	ID       int64    `json:"id"`
	TmdbID   int      `json:"tmdbId"`
	Title    string   `json:"title"`
	Year     int      `json:"year"`
	Overview string   `json:"overview"`
	Status   string   `json:"status"`
	Genres   []string `json:"genres"`
	// Aliases are the show's other names (from TVDB) — what release names
	// carry when they don't use the canonical title.
	Aliases  []string `json:"aliases,omitempty"`
	Poster   string   `json:"poster"`
	Backdrop string   `json:"backdrop"`
	ImdbID   string   `json:"imdbId"`
	// TvdbID is the id used for id-keyed searches: the user's override when
	// one is set, else what TMDB's external ids carry.
	TvdbID int `json:"tvdbId"`
	// SeasonOffset maps this show's own season numbers onto the numbers its
	// releases carry (TVDB/scene numbering): release season = season +
	// offset. Nonzero only for shows TMDB splits differently than the
	// release scene does — a revival TMDB restarts at S1 that releases as S8
	// carries offset 7.
	SeasonOffset int `json:"seasonOffset"`
	// Source is which provider this show's metadata comes from: "tmdb"
	// (the default and fallback) or "tvdb".
	Source    string `json:"source"`
	LibraryID int64  `json:"libraryId"`
	ProfileID int64  `json:"qualityProfileId"`
	Monitored bool   `json:"monitored"`
	// Downloading comes from the download client's queue, not the row.
	Downloading bool   `json:"downloading,omitempty"`
	AddedAt     string `json:"addedAt"`
	Episodes    int    `json:"episodes"` // every episode TMDB knows about
	Aired       int    `json:"aired"`    // episodes whose air date has passed
	OnDisk      int    `json:"onDisk"`   // episodes with a file
	// Wanted is what "missing" is allowed to mean: monitored, aired, and
	// no file. An episode somebody un-monitored isn't missing — it's
	// declined — and counting it invited a search for it.
	Wanted int `json:"wanted"`
	// MonitoredAired is the denominator that judgment runs against:
	// monitored episodes whose air date has passed. The card's fraction is
	// (MonitoredAired - Wanted) / MonitoredAired, so "6/6" means "you have
	// everything you asked for", not "TMDB has nothing further".
	MonitoredAired int `json:"monitoredAired"`
	// GroupIDs is which groups may see this title. Attached for the
	// owner only, and only where the grid needs it — it is the shape of
	// every household on the install, which is not a requester's to see.
	GroupIDs []int64 `json:"groupIds,omitempty"`
}

// airedEpisodes counts what a show should plausibly have by now, using the
// same rule the backlog search uses: an episode counts once its air date
// has passed, and an episode with no date at all doesn't count. That match
// matters — "3 missing" should mean three things reely will actually go
// looking for, not three that TMDB has merely announced.
const airedEpisodes = `(SELECT COUNT(*) FROM episodes e WHERE e.show_id = sh.id
	AND e.air_date IS NOT NULL AND substr(e.air_date,1,10) <= date('now'))`

// wantedEpisodes is the same rule the backlog search applies per episode:
// monitored, aired, nothing on disk. The counts the UI calls "missing"
// come from here, so "3 missing" stays three things reely will actually
// go looking for — an un-monitored gap is a choice, not a shortfall.
const wantedEpisodes = `(SELECT COUNT(*) FROM episodes e WHERE e.show_id = sh.id
	AND e.monitored = 1 AND e.file_path IS NULL
	AND e.air_date IS NOT NULL AND substr(e.air_date,1,10) <= date('now'))`

// monitoredAiredEpisodes is wantedEpisodes without the file clause — the
// denominator a monitored-basis fraction runs against.
const monitoredAiredEpisodes = `(SELECT COUNT(*) FROM episodes e WHERE e.show_id = sh.id
	AND e.monitored = 1
	AND e.air_date IS NOT NULL AND substr(e.air_date,1,10) <= date('now'))`

// Episode is a stored episode row.
type Episode struct {
	ID        int64  `json:"id"`
	ShowID    int64  `json:"showId"`
	Season    int    `json:"season"`
	Episode   int    `json:"episode"`
	Title     string `json:"title"`
	Overview  string `json:"overview"`
	AirDate   string `json:"airDate"`
	Runtime   int    `json:"runtime"` // minutes; 0 = unknown
	Monitored bool   `json:"monitored"`
	// Downloading, like the movie field, comes from the queue not the row.
	Downloading bool   `json:"downloading,omitempty"`
	FilePath    string `json:"filePath"`
	FileSize    int64  `json:"fileSize"`
	Quality     string `json:"quality"`
	Source      string `json:"source"`
	// SceneSeason and SceneEpisode are the numbers this episode's RELEASES
	// carry, from TheXEM, when the scene disagrees with TheTVDB about the
	// numbering. Zero — the overwhelming majority — means the episode's own
	// numbers are what its releases use.
	SceneSeason  int `json:"sceneSeason,omitempty"`
	SceneEpisode int `json:"sceneEpisode,omitempty"`
}

// UpsertMovie writes a movie from TMDB into a library, keyed by (tmdb_id,
// library_id) — the same title may live in several libraries, each row its
// own collection entry. An existing row keeps its file/monitor state;
// metadata refreshes. Returns the movie's local id.
func (s *Store) UpsertMovie(d *metadata.MovieDetail, libraryID int64) (int64, error) {
	genres, _ := json.Marshal(d.Genres)
	aliases, _ := json.Marshal(orEmpty(d.Aliases))
	// a new title inherits its library's quality profile; a refresh keeps
	// whatever profile the title was given since
	_, err := s.db.Exec(`
		INSERT INTO movies (tmdb_id, title, year, overview, runtime, genres, aliases,
			release_date, digital_release, poster_path, backdrop_path, imdb_id, library_id,
			quality_profile_id, refreshed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			(SELECT quality_profile_id FROM libraries WHERE id = ?), datetime('now'))
		ON CONFLICT(tmdb_id, library_id) DO UPDATE SET
			title = excluded.title, year = excluded.year, overview = excluded.overview,
			runtime = excluded.runtime, genres = excluded.genres, aliases = excluded.aliases,
			release_date = excluded.release_date, digital_release = excluded.digital_release,
			poster_path = excluded.poster_path, backdrop_path = excluded.backdrop_path,
			imdb_id = excluded.imdb_id, refreshed_at = datetime('now')`,
		d.TmdbID, d.Title, nullInt(d.Year), d.Overview, nullInt(d.Runtime), string(genres),
		string(aliases), nullStr(d.ReleaseDate), nullStr(d.DigitalRelease), d.Poster, d.Backdrop,
		d.ImdbID, libraryID, libraryID)
	if err != nil {
		return 0, err
	}
	// resolve by the conflict key rather than LastInsertId: on the
	// conflict-update path last_insert_rowid() is stale (it keeps whatever
	// insert ran last on this connection), so trusting it would hang the
	// cast on a wrong row
	var id int64
	if err := s.db.QueryRow(`SELECT id FROM movies WHERE tmdb_id = ? AND library_id = ?`,
		d.TmdbID, libraryID).Scan(&id); err != nil {
		return 0, err
	}
	return id, s.saveCast(d.Cast, id, 0)
}

// SetMovieMinAvailability stores a movie's availability gate for the
// automatic paths: 'released' waits for the movie to be out, 'announced'
// lifts the gate.
func (s *Store) SetMovieMinAvailability(id int64, v string) error {
	if v != "released" && v != "announced" {
		return fmt.Errorf("minimum availability must be 'released' or 'announced', not %q", v)
	}
	res, err := s.db.Exec(`UPDATE movies SET min_availability = ? WHERE id = ?`, v, id)
	if err != nil {
		return err
	}
	if n, err := res.RowsAffected(); err == nil && n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// AttachMovieFile records the file backing a movie.
func (s *Store) AttachMovieFile(movieID int64, path string, size int64, quality, source string) error {
	// an empty path means "no file" and must store as NULL — every on-disk
	// count and calendar check asks file_path IS NOT NULL
	_, err := s.db.Exec(`UPDATE movies SET file_path = NULLIF(?, ''), file_size = ?, quality = ?, source = ? WHERE id = ?`,
		path, size, quality, source, movieID)
	return err
}

// FileReferences counts the catalog rows — movies and episodes — still
// backed by this exact path. Zero means deleting the file orphans nothing.
// Hard-linked sibling copies live at their own per-library paths, so each
// directory entry is judged independently.
func (s *Store) FileReferences(path string) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT
		(SELECT COUNT(*) FROM movies WHERE file_path = ?) +
		(SELECT COUNT(*) FROM episodes WHERE file_path = ?)`, path, path).Scan(&n)
	return n, err
}

// UpsertShow writes a show and its full season/episode tree from TMDB. New
// episodes are added monitored; existing rows keep file/monitor state.
func (s *Store) UpsertShow(d *metadata.ShowDetail, libraryID int64) (int64, error) {
	genres, _ := json.Marshal(d.Genres)
	aliases, _ := json.Marshal(orEmpty(d.Aliases))
	if _, err := s.db.Exec(`
		INSERT INTO shows (tmdb_id, title, year, overview, status, genres, aliases, poster_path,
			backdrop_path, imdb_id, tvdb_id, library_id, quality_profile_id, refreshed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?,
			(SELECT quality_profile_id FROM libraries WHERE id = ?), datetime('now'))
		ON CONFLICT(tmdb_id, library_id) DO UPDATE SET
			title = excluded.title, year = excluded.year, overview = excluded.overview,
			status = excluded.status, genres = excluded.genres, aliases = excluded.aliases,
			poster_path = excluded.poster_path,
			backdrop_path = excluded.backdrop_path, imdb_id = excluded.imdb_id,
			tvdb_id = excluded.tvdb_id, refreshed_at = datetime('now')`,
		d.TmdbID, d.Title, nullInt(d.Year), d.Overview, d.Status, string(genres), string(aliases),
		d.Poster, d.Backdrop, d.ImdbID, d.TvdbID, libraryID, libraryID); err != nil {
		return 0, err
	}
	var showID int64
	if err := s.db.QueryRow(`SELECT id FROM shows WHERE tmdb_id = ? AND library_id = ?`,
		d.TmdbID, libraryID).Scan(&showID); err != nil {
		return 0, err
	}
	if err := s.saveSeasons(showID, d); err != nil {
		return 0, err
	}
	return showID, s.saveCast(d.Cast, 0, showID)
}

// saveSeasons upserts a show's season and episode tree — shared by the
// (tmdb, library)-keyed upsert and the id-keyed refresh.
func (s *Store) saveSeasons(showID int64, d *metadata.ShowDetail) error {
	for _, season := range d.Seasons {
		if _, err := s.db.Exec(`INSERT INTO seasons (show_id, number, name) VALUES (?, ?, ?)
			ON CONFLICT(show_id, number) DO UPDATE SET name = excluded.name`,
			showID, season.Number, season.Name); err != nil {
			return err
		}
		for _, e := range season.Episodes {
			// a late-arriving episode inherits its season's monitor state: a
			// season someone silenced stays silent when TMDB adds stragglers,
			// while a brand-new season arrives monitored
			if _, err := s.db.Exec(`
				INSERT INTO episodes (show_id, season, episode, tmdb_id, title, overview, air_date, runtime, monitored)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?,
					COALESCE((SELECT MAX(monitored) FROM episodes WHERE show_id = ? AND season = ?), 1))
				ON CONFLICT(show_id, season, episode) DO UPDATE SET
					tmdb_id = excluded.tmdb_id, title = excluded.title,
					overview = excluded.overview, air_date = excluded.air_date,
					runtime = excluded.runtime`,
				showID, e.Season, e.Episode, nullInt(e.TmdbID), e.Title, e.Overview, nullStr(e.AirDate),
				nullInt(e.Runtime), showID, e.Season); err != nil {
				return err
			}
		}
	}
	return nil
}

// RefreshMovie and RefreshShow update a title's metadata KEYED ON THE ROW
// ID, and report whether the row was still there to update.
//
// The refresher must never use the (tmdb_id, library_id) upsert: a pass
// captures its targets and then spends the better part of a minute on
// TMDB fetches, and a title deleted mid-pass would be quietly re-inserted
// — resurrected with every episode monitored and no files, which the
// search loop then treats as an entire show worth downloading. An UPDATE
// against a deleted id is an atomic no-op, so there is no window at all.
func (s *Store) RefreshMovie(id int64, d *metadata.MovieDetail) (bool, error) {
	genres, _ := json.Marshal(d.Genres)
	aliases, _ := json.Marshal(orEmpty(d.Aliases))
	res, err := s.db.Exec(`UPDATE movies SET
			title = ?, year = ?, overview = ?, runtime = ?, genres = ?, aliases = ?,
			release_date = ?, digital_release = ?, poster_path = ?, backdrop_path = ?,
			imdb_id = ?, refreshed_at = datetime('now')
		WHERE id = ?`,
		d.Title, nullInt(d.Year), d.Overview, nullInt(d.Runtime), string(genres), string(aliases),
		nullStr(d.ReleaseDate), nullStr(d.DigitalRelease), d.Poster, d.Backdrop, d.ImdbID, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	return true, s.saveCast(d.Cast, id, 0)
}

func (s *Store) RefreshShow(id int64, d *metadata.ShowDetail) (bool, error) {
	genres, _ := json.Marshal(d.Genres)
	aliases, _ := json.Marshal(orEmpty(d.Aliases))
	res, err := s.db.Exec(`UPDATE shows SET
			title = ?, year = ?, overview = ?, status = ?, genres = ?, aliases = ?,
			poster_path = ?, backdrop_path = ?, imdb_id = ?, tvdb_id = ?,
			refreshed_at = datetime('now')
		WHERE id = ?`,
		d.Title, nullInt(d.Year), d.Overview, d.Status, string(genres), string(aliases),
		d.Poster, d.Backdrop, d.ImdbID, d.TvdbID, id)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	if err != nil || n == 0 {
		return false, err
	}
	// the show can still vanish between the UPDATE and these inserts; the
	// episodes' foreign key then fails the write instead of resurrecting
	// anything, which is the right way for that sliver of a race to end
	if err := s.saveSeasons(id, d); err != nil {
		return true, err
	}
	return true, s.saveCast(d.Cast, 0, id)
}

// LibrariesHolding lists the library ids carrying a TMDB title — the
// preview page's "already in" marks. Table is "movies" or "shows".
func (s *Store) LibrariesHolding(table string, tmdbID int) ([]int64, error) {
	q := `SELECT COALESCE(library_id,0) FROM movies WHERE tmdb_id = ?`
	if table == "shows" {
		q = `SELECT COALESCE(library_id,0) FROM shows WHERE tmdb_id = ?`
	}
	rows, err := s.db.Query(q, tmdbID)
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
		if id > 0 {
			out = append(out, id)
		}
	}
	return out, rows.Err()
}

// GetEpisode returns one episode row, or sql.ErrNoRows.
func (s *Store) GetEpisode(id int64) (*Episode, error) {
	var e Episode
	var mon int
	err := s.db.QueryRow(`SELECT id, show_id, season, episode, COALESCE(title,''), COALESCE(overview,''),
		COALESCE(air_date,''), COALESCE(runtime,0), monitored, COALESCE(file_path,''),
		COALESCE(file_size,0), quality, COALESCE(source,'') FROM episodes WHERE id = ?`, id).
		Scan(&e.ID, &e.ShowID, &e.Season, &e.Episode, &e.Title, &e.Overview, &e.AirDate,
			&e.Runtime, &mon, &e.FilePath, &e.FileSize, &e.Quality, &e.Source)
	if err != nil {
		return nil, err
	}
	e.Monitored = mon != 0
	return &e, nil
}

// EpisodeID resolves a show's local episode id by season/episode, or 0 if the
// show doesn't carry that episode (TMDB never listed it).
func (s *Store) EpisodeID(showID int64, season, episode int) (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM episodes WHERE show_id = ? AND season = ? AND episode = ?`,
		showID, season, episode).Scan(&id)
	if err == sql.ErrNoRows {
		return 0, nil
	}
	return id, err
}

// AttachEpisodeFile records the file backing one episode.
func (s *Store) AttachEpisodeFile(episodeID int64, path string, size int64, quality, source string) error {
	// an empty path means "no file" and must store as NULL — see AttachMovieFile
	_, err := s.db.Exec(`UPDATE episodes SET file_path = NULLIF(?, ''), file_size = ?, quality = ?, source = ? WHERE id = ?`,
		path, size, quality, source, episodeID)
	return err
}

// upsertPerson stores one cast member and returns their row id, or 0 for
// somebody there is no way to identify.
//
// A TMDB id is the key wherever there is one, so an actor met through
// TVDB lands on the same row as when they arrive from a movie — TVDB
// carries TMDB ids for most people, which is what keeps one actor from
// becoming two. Only where TMDB has never heard of them does the TVDB id
// key the row instead.
//
// Both ids go in when both are known, so the next sighting matches on
// either. Somebody with neither is skipped rather than stored: they
// would land on tmdb_id = 0 along with everyone else in the same
// position and collide on the unique index.
func (s *Store) upsertPerson(p metadata.Person) (int64, error) {
	var column string
	switch {
	case p.TmdbID > 0:
		column = "tmdb_id"
	case p.TvdbID > 0:
		column = "tvdb_id"
	default:
		return 0, nil
	}
	// the conflict target is the id this person is keyed on; the other id
	// is filled in where it is known and never cleared by a sighting that
	// lacks it
	//nolint:gosec // G202: column is one of two literals chosen above
	if _, err := s.db.Exec(`INSERT INTO people (tmdb_id, tvdb_id, name, photo_path)
		VALUES (?1, ?2, ?3, ?4)
		ON CONFLICT(`+column+`) DO UPDATE SET
			name = excluded.name,
			photo_path = excluded.photo_path,
			tmdb_id = COALESCE(excluded.tmdb_id, people.tmdb_id),
			tvdb_id = COALESCE(excluded.tvdb_id, people.tvdb_id)`,
		nullInt(p.TmdbID), nullInt(p.TvdbID), p.Name, p.Photo); err != nil {
		return 0, err
	}
	key := p.TmdbID
	if column == "tvdb_id" {
		key = p.TvdbID
	}
	var id int64
	//nolint:gosec // G202: as above
	if err := s.db.QueryRow(`SELECT id FROM people WHERE `+column+` = ?`, key).Scan(&id); err != nil {
		return 0, err
	}
	return id, nil
}

func (s *Store) saveCast(cast []metadata.Person, movieID, showID int64) error {
	for _, p := range cast {
		personID, err := s.upsertPerson(p)
		if err != nil {
			return err
		}
		if personID == 0 {
			continue // nothing to key them on; see upsertPerson
		}
		// credits carry no natural key, so clear this title's rows for the
		// person before re-inserting — keeps a refresh idempotent
		if movieID > 0 {
			if _, err := s.db.Exec(`DELETE FROM credits WHERE person_id = ? AND movie_id = ?`, personID, movieID); err != nil {
				return err
			}
			if _, err := s.db.Exec(`INSERT INTO credits (person_id, movie_id, character, ord) VALUES (?, ?, ?, ?)`,
				personID, movieID, p.Character, p.Order); err != nil {
				return err
			}
		} else {
			if _, err := s.db.Exec(`DELETE FROM credits WHERE person_id = ? AND show_id = ?`, personID, showID); err != nil {
				return err
			}
			if _, err := s.db.Exec(`INSERT INTO credits (person_id, show_id, character, ord) VALUES (?, ?, ?, ?)`,
				personID, showID, p.Character, p.Order); err != nil {
				return err
			}
		}
	}
	return nil
}

// ListMovies returns a library's movies (or all movie libraries when
// libraryID is 0), newest first.
func (s *Store) ListMovies(libraryID int64) ([]Movie, error) {
	q := `SELECT ` + movieColumns + ` FROM movies`
	args := []any{}
	if libraryID > 0 {
		q += ` WHERE library_id = ?`
		args = append(args, libraryID)
	}
	q += ` ORDER BY added_at DESC, id DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMovies(rows)
}

// scanMovies drains a query whose columns match ListMovies' select list.
func scanMovies(rows *sql.Rows) ([]Movie, error) {
	var out []Movie
	for rows.Next() {
		var m Movie
		var genres, aliases string
		var mon int
		if err := rows.Scan(&m.ID, &m.TmdbID, &m.Title, &m.Year, &m.Overview, &m.Runtime, &genres,
			&aliases, &m.ReleaseDate, &m.DigitalRelease, &m.MinAvailability, &m.Poster, &m.Backdrop,
			&m.ImdbID, &m.LibraryID,
			&m.ProfileID, &mon, &m.FilePath, &m.FileSize, &m.Quality, &m.Source, &m.AddedAt,
			&m.ImportedAt); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(genres), &m.Genres)   //nolint:errcheck // empty/legacy → nil genres is fine
		json.Unmarshal([]byte(aliases), &m.Aliases) //nolint:errcheck // empty/legacy → nil aliases is fine
		m.Monitored = mon != 0
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListShows returns a library's shows (or all show libraries when libraryID
// is 0) with episode/on-disk counts, newest first.
func (s *Store) ListShows(libraryID int64) ([]Show, error) {
	q := `SELECT sh.id, sh.tmdb_id, sh.title, COALESCE(sh.year,0), sh.overview, sh.status,
		sh.genres, sh.aliases, sh.poster_path, sh.backdrop_path, sh.imdb_id, ` + effectiveTvdbID + `,
		sh.season_offset, sh.source, COALESCE(sh.library_id,0),
		COALESCE(sh.quality_profile_id,0), sh.monitored, COALESCE(sh.added_at,''),
		(SELECT COUNT(*) FROM episodes e WHERE e.show_id = sh.id),
		` + airedEpisodes + `,
		(SELECT COUNT(*) FROM episodes e WHERE e.show_id = sh.id AND e.file_path IS NOT NULL),
		` + wantedEpisodes + `,
		` + monitoredAiredEpisodes + `
		FROM shows sh`
	args := []any{}
	if libraryID > 0 {
		q += ` WHERE sh.library_id = ?`
		args = append(args, libraryID)
	}
	q += ` ORDER BY sh.added_at DESC, sh.id DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Show
	for rows.Next() {
		var sh Show
		var genres, aliases string
		var mon int
		if err := rows.Scan(&sh.ID, &sh.TmdbID, &sh.Title, &sh.Year, &sh.Overview, &sh.Status,
			&genres, &aliases, &sh.Poster, &sh.Backdrop, &sh.ImdbID, &sh.TvdbID, &sh.SeasonOffset, &sh.Source,
			&sh.LibraryID, &sh.ProfileID, &mon,
			&sh.AddedAt, &sh.Episodes, &sh.Aired, &sh.OnDisk, &sh.Wanted, &sh.MonitoredAired); err != nil {
			return nil, err
		}
		json.Unmarshal([]byte(genres), &sh.Genres)   //nolint:errcheck // empty/legacy → nil genres is fine
		json.Unmarshal([]byte(aliases), &sh.Aliases) //nolint:errcheck // empty/legacy → nil aliases is fine
		sh.Monitored = mon != 0
		out = append(out, sh)
	}
	return out, rows.Err()
}

// orEmpty keeps stored JSON arrays as [] rather than null.
func orEmpty(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

func nullInt(n int) any {
	if n == 0 {
		return nil
	}
	return n
}

func nullStr(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// TitlesByID resolves movie and show row ids to display titles in two
// bounded queries. The search queue holds ids; the Activity view shows
// names, and looking each one up through GetMovie/GetShow would run a
// details join per row on every poll.
func (s *Store) TitlesByID(movieIDs, showIDs []int64) (map[int64]string, map[int64]string, error) {
	movies, err := s.movieTitles(movieIDs)
	if err != nil {
		return nil, nil, err
	}
	shows, err := s.showTitles(showIDs)
	if err != nil {
		return nil, nil, err
	}
	return movies, shows, nil
}

func (s *Store) movieTitles(ids []int64) (map[int64]string, error) {
	if len(ids) == 0 {
		return map[int64]string{}, nil
	}
	rows, err := s.db.Query( //nolint:gosec // G202: only "?" placeholders are joined in
		fmt.Sprintf(`SELECT id, title FROM movies WHERE id IN (%s)`, idMarks(len(ids))), idArgs(ids)...)
	if err != nil {
		return nil, err
	}
	return scanTitles(rows)
}

func (s *Store) showTitles(ids []int64) (map[int64]string, error) {
	if len(ids) == 0 {
		return map[int64]string{}, nil
	}
	rows, err := s.db.Query( //nolint:gosec // G202: only "?" placeholders are joined in
		fmt.Sprintf(`SELECT id, title FROM shows WHERE id IN (%s)`, idMarks(len(ids))), idArgs(ids)...)
	if err != nil {
		return nil, err
	}
	return scanTitles(rows)
}

// idMarks builds the "?,?,?" body of an IN clause — placeholders only,
// never values, which is what keeps the queries above parameterised.
func idMarks(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

func idArgs(ids []int64) []any {
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return args
}

func scanTitles(rows *sql.Rows) (map[int64]string, error) {
	defer rows.Close()
	out := map[int64]string{}
	for rows.Next() {
		var id int64
		var title string
		if err := rows.Scan(&id, &title); err != nil {
			return nil, err
		}
		out[id] = title
	}
	return out, rows.Err()
}

// AttachScannedMovieFile and AttachScannedEpisodeFile are the library
// scan's version of attaching a file. A scan learns what it can from a
// filename, and must not erase what it cannot: a name carrying no source
// says nothing about the file's source, which is not the same as saying
// it has none. An empty quality or source therefore leaves the stored
// value alone.
//
// The grab path keeps the plain Attach*File, where the release name is
// authoritative and a replacement file genuinely does change both.
func (s *Store) AttachScannedMovieFile(movieID int64, path string, size int64, quality, source string) error {
	if err := s.releaseMovieFileClaim(path, movieID); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE movies SET file_path = NULLIF(?, ''), file_size = ?,
		quality = CASE WHEN ? = '' THEN quality ELSE ? END,
		source  = CASE WHEN ? = '' THEN source  ELSE ? END
		WHERE id = ?`,
		path, size, quality, quality, source, source, movieID)
	return err
}

func (s *Store) AttachScannedEpisodeFile(episodeID int64, path string, size int64, quality, source string) error {
	if err := s.releaseEpisodeFileClaim(path, episodeID); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE episodes SET file_path = NULLIF(?, ''), file_size = ?,
		quality = CASE WHEN ? = '' THEN quality ELSE ? END,
		source  = CASE WHEN ? = '' THEN source  ELSE ? END
		WHERE id = ?`,
		path, size, quality, quality, source, source, episodeID)
	return err
}

// One file, one owner: attaching a path to a row strips any OTHER row's
// claim on that same path. Two shows sharing a name once let a scan file
// the reboot's episodes under the original AND leave the reboot's own
// attachments standing — the same bytes "on disk" twice, and the loser's
// real gaps hidden behind them. Quality and source go too: they describe
// a file this row no longer has, and a badge left behind reads as a
// title that is present when it is not.
func (s *Store) releaseEpisodeFileClaim(path string, keep int64) error {
	if path == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE episodes SET file_path = NULL, file_size = 0,
		quality = '', source = ''
		WHERE file_path = ? AND id != ?`, path, keep)
	return err
}

func (s *Store) releaseMovieFileClaim(path string, keep int64) error {
	if path == "" {
		return nil
	}
	_, err := s.db.Exec(`UPDATE movies SET file_path = NULL, file_size = 0,
		quality = '', source = ''
		WHERE file_path = ? AND id != ?`, path, keep)
	return err
}

// ErrTitleTaken is another row in the same library already holding the
// id a re-match is trying to move to.
var ErrTitleTaken = errors.New("that title is already in this library")

// RematchMovie changes which film a row IS, keeping everything about it
// that is yours.
//
// The identity and the metadata that follows from it are replaced; the
// file, the library, the quality profile, the monitored flag and the
// history stay exactly as they were. That is the whole point: the file
// on disk was always this film, and reely was calling it another one.
//
// It is a change of identity rather than a delete and re-add because
// those are not the same thing — a re-add loses the file's history, its
// profile, and everything else the row has accumulated, in service of a
// mistake that was only ever about a number.
func (s *Store) RematchMovie(id int64, d *metadata.MovieDetail) error {
	var libraryID int64
	if err := s.db.QueryRow(`SELECT library_id FROM movies WHERE id = ?`,
		id).Scan(&libraryID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("no such movie")
		}
		return fmt.Errorf("rematch: %w", err)
	}
	var taken int64
	err := s.db.QueryRow(`SELECT id FROM movies WHERE tmdb_id = ? AND library_id = ? AND id <> ?`,
		d.TmdbID, libraryID, id).Scan(&taken)
	if err == nil {
		return ErrTitleTaken
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("rematch: %w", err)
	}

	genres, _ := json.Marshal(d.Genres)
	aliases, _ := json.Marshal(orEmpty(d.Aliases))
	if _, err := s.db.Exec(`UPDATE movies SET
			tmdb_id = ?, title = ?, year = ?, overview = ?, runtime = ?, genres = ?,
			aliases = ?, release_date = ?, digital_release = ?, poster_path = ?,
			backdrop_path = ?, imdb_id = ?, refreshed_at = datetime('now')
		WHERE id = ?`,
		d.TmdbID, d.Title, nullInt(d.Year), d.Overview, nullInt(d.Runtime), string(genres),
		string(aliases), nullStr(d.ReleaseDate), nullStr(d.DigitalRelease), d.Poster,
		d.Backdrop, d.ImdbID, id); err != nil {
		return fmt.Errorf("rematch: %w", err)
	}
	return nil
}

// ShowIDByTmdb finds a show in a library by its TMDB id, or 0.
func (s *Store) ShowIDByTmdb(libraryID int64, tmdbID int) (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM shows WHERE library_id = ? AND tmdb_id = ?`,
		libraryID, tmdbID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("show by tmdb: %w", err)
	}
	return id, nil
}
