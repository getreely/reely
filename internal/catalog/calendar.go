package catalog

import "encoding/json"

// The calendar is a read view over dates already stored: episode air dates
// and movie digital releases, for monitored titles.

// CalendarItem is one dated row with its on-disk state.
type CalendarItem struct {
	Kind         string `json:"kind"` // episode | movie
	Date         string `json:"date"`
	Title        string `json:"title"` // the show's or movie's title
	Season       int    `json:"season,omitempty"`
	Episode      int    `json:"episode,omitempty"`
	EpisodeTitle string `json:"episodeTitle,omitempty"`
	MovieID      int64  `json:"movieId,omitempty"`
	ShowID       int64  `json:"showId,omitempty"`
	EpisodeID    int64  `json:"episodeId,omitempty"`
	LibraryID    int64  `json:"libraryId"`
	OnDisk       bool   `json:"onDisk"`
	// Poster is the title's artwork path, so a row can be recognised at a
	// glance rather than read. An episode carries its show's.
	Poster string `json:"poster,omitempty"`
	// The external ids, because a row has to be openable from the portal
	// too — out there a title is reached by its metadata id rather than
	// by reely's own, which the portal is not served.
	TmdbID int `json:"tmdbId,omitempty"`
	TvdbID int `json:"tvdbId,omitempty"`
}

// Calendar returns monitored episodes airing and monitored movies going
// digital between from and to (inclusive, YYYY-MM-DD), date order.
// Calendar lists what is due between two dates, in the given libraries.
//
// The library filter is not optional. This used to answer with every
// library on the install whoever asked, which reads as harmless until an
// account that holds one library is shown the titles in somebody else's
// — and worse on a portal published to the internet.
//
// The ids bind as one JSON parameter, so the statement stays a constant
// string with nothing built into it.
func (s *Store) Calendar(from, to string, libraryIDs []int64) ([]CalendarItem, error) {
	if len(libraryIDs) == 0 {
		return nil, nil
	}
	ids, err := json.Marshal(libraryIDs)
	if err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`
		SELECT 'episode', substr(e.air_date, 1, 10), sh.title, e.season, e.episode, e.title,
			0, sh.id, e.id, COALESCE(sh.library_id, 0), e.file_path IS NOT NULL,
			COALESCE(sh.poster_path, ''), COALESCE(sh.tmdb_id, 0), COALESCE(sh.tvdb_id, 0)
		FROM episodes e JOIN shows sh ON sh.id = e.show_id
		WHERE e.monitored = 1 AND sh.monitored = 1
			AND substr(e.air_date, 1, 10) BETWEEN ? AND ?
			AND sh.library_id IN (SELECT value FROM json_each(?))
		UNION ALL
		SELECT 'movie', substr(m.digital_release, 1, 10), m.title, 0, 0, '',
			m.id, 0, 0, COALESCE(m.library_id, 0), m.file_path IS NOT NULL,
			COALESCE(m.poster_path, ''), COALESCE(m.tmdb_id, 0), 0
		FROM movies m
		WHERE m.monitored = 1 AND substr(m.digital_release, 1, 10) BETWEEN ? AND ?
			AND m.library_id IN (SELECT value FROM json_each(?))
		ORDER BY 2, 3`, from, to, string(ids), from, to, string(ids))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []CalendarItem
	for rows.Next() {
		var it CalendarItem
		var onDisk int
		if err := rows.Scan(&it.Kind, &it.Date, &it.Title, &it.Season, &it.Episode, &it.EpisodeTitle,
			&it.MovieID, &it.ShowID, &it.EpisodeID, &it.LibraryID, &onDisk,
			&it.Poster, &it.TmdbID, &it.TvdbID); err != nil {
			return nil, err
		}
		it.OnDisk = onDisk != 0
		out = append(out, it)
	}
	return out, rows.Err()
}

// AllLibraryIDs is every library on the install — what a background pass
// works over, since it acts for the install rather than for anybody.
//
// It exists so Calendar can require a library list rather than treating
// an empty one as "all". A filter that means everything when you forget
// to set it is the shape of the bug this replaced.
func (s *Store) AllLibraryIDs() ([]int64, error) {
	rows, err := s.db.Query(`SELECT id FROM libraries`)
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
