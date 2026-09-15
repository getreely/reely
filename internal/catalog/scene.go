package catalog

import "github.com/getreely/reely/internal/scene"

// Scene numbering: what a show's releases call its episodes, when that
// differs from what TheTVDB calls them. The numbers come from TheXEM (see
// package scene) and are stored per episode, because that is how the
// divergences happen — a season shift is the common case of an episode
// shift that never comes back.
//
// The show-level SeasonOffset stays what it always was: the manual
// mapping for shows TMDB splits differently than the scene does. Scene
// numbering is consulted first and the offset is the fallback, so a show
// XEM covers needs no manual anything.

// SetSceneNumbering replaces a show's scene numbering wholesale. It is a
// full replacement rather than a merge because XEM's map is authoritative
// per fetch: an episode dropped from the map has had its mapping
// withdrawn, and a stale row would keep searching for a number nobody
// publishes. Reports how many episodes carry a mapping afterwards.
func (s *Store) SetSceneNumbering(showID int64, ns []scene.Numbering) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed
	if _, err := tx.Exec(`UPDATE episodes SET scene_season = NULL, scene_episode = NULL
		WHERE show_id = ?`, showID); err != nil {
		return 0, err
	}
	set := 0
	for _, n := range ns {
		res, err := tx.Exec(`UPDATE episodes SET scene_season = ?, scene_episode = ?
			WHERE show_id = ? AND season = ? AND episode = ?`,
			n.SceneSeason, n.SceneEpisode, showID, n.Season, n.Episode)
		if err != nil {
			return 0, err
		}
		if rows, _ := res.RowsAffected(); rows > 0 {
			set++
		}
	}
	return set, tx.Commit()
}

// SceneEpisodes lists a show's episodes in the form the resolver wants —
// its own numbering, nothing else.
func (s *Store) SceneEpisodes(showID int64) ([]scene.Episode, error) {
	rows, err := s.db.Query(`SELECT season, episode FROM episodes
		WHERE show_id = ? ORDER BY season, episode`, showID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []scene.Episode
	for rows.Next() {
		var e scene.Episode
		if err := rows.Scan(&e.Season, &e.Episode); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
