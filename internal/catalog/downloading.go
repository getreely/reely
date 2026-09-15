package catalog

import (
	"fmt"
	"strings"
)

// Which titles have a download in flight. A grab records its SAB job id in
// the history row it writes, so cross-referencing those ids against what
// SAB is actually working on says which titles are mid-download — rather
// than inferring it from a grab with no import yet, which would keep
// claiming "downloading" long after a job was cancelled or lost.

// DownloadingIDs maps the given SAB job ids back to the titles they were
// grabbed for. Returns the movie ids and episode ids with a job in flight,
// plus the show ids — a season pack is grabbed against a show with no
// single episode to point at.
func (s *Store) DownloadingIDs(nzoIDs []string) (movies, episodes, shows map[int64]bool, err error) {
	movies, episodes, shows = map[int64]bool{}, map[int64]bool{}, map[int64]bool{}
	if len(nzoIDs) == 0 {
		return movies, episodes, shows, nil
	}
	args := make([]any, len(nzoIDs))
	for i, id := range nzoIDs {
		args[i] = id
	}
	marks := strings.TrimSuffix(strings.Repeat("?,", len(nzoIDs)), ",")
	// the job id lives in the row's detail JSON, written by recordGrab
	rows, err := s.db.Query( //nolint:gosec // G202: only "?" placeholders are joined in
		fmt.Sprintf(`SELECT COALESCE(movie_id,0), COALESCE(episode_id,0), COALESCE(show_id,0)
			FROM history
			WHERE kind = 'grabbed'
			  AND json_extract(detail, '$.nzoId') IN (%s)`, marks), args...)
	if err != nil {
		return nil, nil, nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var movieID, episodeID, showID int64
		if err := rows.Scan(&movieID, &episodeID, &showID); err != nil {
			return nil, nil, nil, err
		}
		if movieID > 0 {
			movies[movieID] = true
		}
		if episodeID > 0 {
			episodes[episodeID] = true
		}
		if showID > 0 {
			shows[showID] = true
		}
	}
	return movies, episodes, shows, rows.Err()
}
