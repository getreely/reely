package catalog

import (
	"database/sql"
	"encoding/json"
)

// The detail read path: one movie or show with everything its page shows —
// cast in billing order, and for shows the full season/episode tree.

// CastMember is one row of a title's stored cast (top billing only; saveCast
// keeps order < 15).
type CastMember struct {
	// ID is the person's own row. It identifies somebody TMDB has never
	// heard of, who has no tmdbId to be told apart by — several of them
	// on one title would otherwise all look like person 0.
	ID int64 `json:"id"`
	// TmdbID is 0 for a person reely only knows through TVDB. There is no
	// filmography to open for them.
	TmdbID    int    `json:"tmdbId"`
	Name      string `json:"name"`
	Character string `json:"character"`
	Photo     string `json:"photo"`
}

// MovieDetails is a movie plus its cast.
type MovieDetails struct {
	Movie
	Cast []CastMember `json:"cast"`
}

// SeasonEpisodes is one season with its episodes in order.
type SeasonEpisodes struct {
	Number   int       `json:"number"`
	Name     string    `json:"name"`
	Episodes []Episode `json:"episodes"`
}

// ShowDetails is a show plus its cast and full season/episode tree.
type ShowDetails struct {
	Show
	Seasons []SeasonEpisodes `json:"seasons"`
	Cast    []CastMember     `json:"cast"`
}

// GetMovie returns one movie with its cast, or sql.ErrNoRows.
func (s *Store) GetMovie(id int64) (*MovieDetails, error) {
	d := &MovieDetails{}
	var genres string
	var mon int
	var aliases string
	err := s.db.QueryRow(`SELECT id, tmdb_id, title, COALESCE(year,0), overview, COALESCE(runtime,0),
		genres, aliases, COALESCE(release_date,''), COALESCE(digital_release,''),
		COALESCE(min_availability,'released'), poster_path, backdrop_path,
		imdb_id, COALESCE(library_id,0), COALESCE(quality_profile_id,0), monitored,
		COALESCE(file_path,''), COALESCE(file_size,0), quality, COALESCE(source,'')
		FROM movies WHERE id = ?`, id).
		Scan(&d.ID, &d.TmdbID, &d.Title, &d.Year, &d.Overview, &d.Runtime, &genres, &aliases,
			&d.ReleaseDate, &d.DigitalRelease, &d.MinAvailability, &d.Poster, &d.Backdrop, &d.ImdbID,
			&d.LibraryID, &d.ProfileID, &mon, &d.FilePath, &d.FileSize, &d.Quality, &d.Source)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(genres), &d.Genres)   //nolint:errcheck // empty/legacy → nil genres is fine
	json.Unmarshal([]byte(aliases), &d.Aliases) //nolint:errcheck // empty/legacy → nil aliases is fine
	d.Monitored = mon != 0
	d.Cast, err = s.castFor(id, 0)
	return d, err
}

// GetShow returns one show with cast, seasons, and episodes, or sql.ErrNoRows.
func (s *Store) GetShow(id int64) (*ShowDetails, error) {
	d := &ShowDetails{}
	var genres string
	var mon int
	var aliases string
	err := s.db.QueryRow(`SELECT sh.id, sh.tmdb_id, sh.title, COALESCE(sh.year,0), sh.overview,
		sh.status, sh.genres, sh.aliases, sh.poster_path, sh.backdrop_path, sh.imdb_id, `+effectiveTvdbID+`,
		sh.season_offset, sh.source,
		COALESCE(sh.library_id,0), COALESCE(sh.quality_profile_id,0), sh.monitored,
		(SELECT COUNT(*) FROM episodes e WHERE e.show_id = sh.id),
		`+airedEpisodes+`,
		(SELECT COUNT(*) FROM episodes e WHERE e.show_id = sh.id AND e.file_path IS NOT NULL),
		`+wantedEpisodes+`,
		`+monitoredAiredEpisodes+`
		FROM shows sh WHERE sh.id = ?`, id).
		Scan(&d.ID, &d.TmdbID, &d.Title, &d.Year, &d.Overview, &d.Status, &genres, &aliases,
			&d.Poster, &d.Backdrop, &d.ImdbID, &d.TvdbID, &d.SeasonOffset, &d.Source,
			&d.LibraryID, &d.ProfileID, &mon,
			&d.Episodes, &d.Aired, &d.OnDisk, &d.Wanted, &d.MonitoredAired)
	if err != nil {
		return nil, err
	}
	json.Unmarshal([]byte(genres), &d.Genres)   //nolint:errcheck // empty/legacy → nil genres is fine
	json.Unmarshal([]byte(aliases), &d.Aliases) //nolint:errcheck // empty/legacy → nil aliases is fine
	d.Monitored = mon != 0

	names := map[int]string{}
	rows, err := s.db.Query(`SELECT number, name FROM seasons WHERE show_id = ? ORDER BY number`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var number int
		var name string
		if err := rows.Scan(&number, &name); err != nil {
			return nil, err
		}
		names[number] = name
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	episodes, err := s.ListEpisodes(id)
	if err != nil {
		return nil, err
	}
	// episodes arrive season-ordered, so grouping is a single pass; an episode
	// whose season row went missing still gets a group (name stays empty)
	for _, e := range episodes {
		if n := len(d.Seasons); n == 0 || d.Seasons[n-1].Number != e.Season {
			d.Seasons = append(d.Seasons, SeasonEpisodes{Number: e.Season, Name: names[e.Season]})
		}
		last := &d.Seasons[len(d.Seasons)-1]
		last.Episodes = append(last.Episodes, e)
	}

	d.Cast, err = s.castFor(0, id)
	return d, err
}

// ListEpisodes returns a show's episodes ordered by season then episode.
func (s *Store) ListEpisodes(showID int64) ([]Episode, error) {
	rows, err := s.db.Query(`SELECT id, show_id, season, episode, title, overview,
		COALESCE(air_date,''), COALESCE(runtime,0), monitored, COALESCE(file_path,''),
		COALESCE(file_size,0), quality, COALESCE(source,''),
		COALESCE(scene_season,0), COALESCE(scene_episode,0)
		FROM episodes WHERE show_id = ? ORDER BY season, episode`, showID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Episode
	for rows.Next() {
		var e Episode
		var mon int
		if err := rows.Scan(&e.ID, &e.ShowID, &e.Season, &e.Episode, &e.Title, &e.Overview,
			&e.AirDate, &e.Runtime, &mon, &e.FilePath, &e.FileSize, &e.Quality, &e.Source,
			&e.SceneSeason, &e.SceneEpisode); err != nil {
			return nil, err
		}
		e.Monitored = mon != 0
		out = append(out, e)
	}
	return out, rows.Err()
}

// castFor loads a title's stored cast in billing order. Exactly one of
// movieID / showID is set, mirroring saveCast.
func (s *Store) castFor(movieID, showID int64) ([]CastMember, error) {
	// tmdb_id is NULL for somebody reely met through TVDB alone, so it
	// cannot be scanned straight into an int — every detail page for a
	// title with one such actor would fail to load.
	q := `SELECT p.id, COALESCE(p.tmdb_id,0), p.name, c.character, p.photo_path
		FROM credits c JOIN people p ON p.id = c.person_id
		WHERE c.movie_id = ? ORDER BY c.ord`
	id := movieID
	if showID > 0 {
		q = `SELECT p.id, COALESCE(p.tmdb_id,0), p.name, c.character, p.photo_path
			FROM credits c JOIN people p ON p.id = c.person_id
			WHERE c.show_id = ? ORDER BY c.ord`
		id = showID
	}
	rows, err := s.db.Query(q, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CastMember
	for rows.Next() {
		var m CastMember
		if err := rows.Scan(&m.ID, &m.TmdbID, &m.Name, &m.Character, &m.Photo); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	return out, rows.Err()
}

// SetMovieMonitored flips a movie's monitor flag.
func (s *Store) SetMovieMonitored(id int64, monitored bool) error {
	_, err := s.db.Exec(`UPDATE movies SET monitored = ? WHERE id = ?`, boolInt(monitored), id)
	return err
}

// SetShowMonitored flips a show's monitor flag and cascades it to every
// episode — the show page's header switch means "want this show or not".
// Individual episodes can be re-toggled afterwards.
func (s *Store) SetShowMonitored(id int64, monitored bool) error {
	if _, err := s.db.Exec(`UPDATE shows SET monitored = ? WHERE id = ?`, boolInt(monitored), id); err != nil {
		return err
	}
	_, err := s.db.Exec(`UPDATE episodes SET monitored = ? WHERE show_id = ?`, boolInt(monitored), id)
	return err
}

// SetSeasonMonitored flips every episode in one season. A season is not a
// monitored entity of its own — "monitor season 2" simply means its
// episodes — so the show-level flag stays untouched. sql.ErrNoRows if the
// show has no such season.
func (s *Store) SetSeasonMonitored(showID int64, season int, monitored bool) error {
	res, err := s.db.Exec(`UPDATE episodes SET monitored = ? WHERE show_id = ? AND season = ?`,
		boolInt(monitored), showID, season)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetEpisodeMonitored flips one episode's monitor flag.
func (s *Store) SetEpisodeMonitored(id int64, monitored bool) error {
	_, err := s.db.Exec(`UPDATE episodes SET monitored = ? WHERE id = ?`, boolInt(monitored), id)
	return err
}

// RemoveMovie deletes a movie row; credits cascade, history rows keep
// their detail with the id nulled. The file on disk is the caller's call.
func (s *Store) RemoveMovie(id int64) error {
	res, err := s.db.Exec(`DELETE FROM movies WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// RemoveShow deletes a show row; seasons, episodes, and credits cascade.
func (s *Store) RemoveShow(id int64) error {
	res, err := s.db.Exec(`DELETE FROM shows WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// EpisodeAddress resolves an episode id back to its show and numbering.
func (s *Store) EpisodeAddress(id int64) (showID int64, season, episode int, err error) {
	err = s.db.QueryRow(`SELECT show_id, season, episode FROM episodes WHERE id = ?`, id).
		Scan(&showID, &season, &episode)
	return showID, season, episode, err
}

// EpisodeLibrary reports which show and library an episode belongs to — the
// scoping facts an episode-level endpoint needs. sql.ErrNoRows if no such
// episode.
func (s *Store) EpisodeLibrary(id int64) (showID, libraryID int64, err error) {
	err = s.db.QueryRow(`SELECT e.show_id, COALESCE(sh.library_id, 0)
		FROM episodes e JOIN shows sh ON sh.id = e.show_id WHERE e.id = ?`, id).
		Scan(&showID, &libraryID)
	return showID, libraryID, err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
