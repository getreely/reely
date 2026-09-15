package catalog

import (
	"database/sql"
	"time"
)

// The history table finally earns its keep: every grab (and later import,
// rename, removal) leaves a row, so "what did reely do" always has an answer.

// HistoryEntry is one recorded action, labeled with its title for display.
type HistoryEntry struct {
	ID        int64  `json:"id"`
	Kind      string `json:"kind"` // grabbed / imported / failed / …
	MovieID   int64  `json:"movieId,omitempty"`
	ShowID    int64  `json:"showId,omitempty"`
	EpisodeID int64  `json:"episodeId,omitempty"`
	Detail    string `json:"detail"`
	CreatedAt string `json:"createdAt"`
	Title     string `json:"title,omitempty"`   // movie or show title
	Season    int    `json:"season,omitempty"`  // set for episode rows
	Episode   int    `json:"episode,omitempty"` // set for episode rows
}

// GrabTarget answers what a SAB job was grabbed FOR: the newest "grabbed"
// history row carrying this nzo id. All zeros means reely never grabbed
// this job — someone queued it in SAB by hand.
func (s *Store) GrabTarget(nzoID string) (movieID, showID, episodeID int64, err error) {
	err = s.db.QueryRow(`SELECT COALESCE(movie_id,0), COALESCE(show_id,0), COALESCE(episode_id,0)
		FROM history WHERE kind = 'grabbed' AND json_extract(detail, '$.nzoId') = ?
		ORDER BY id DESC LIMIT 1`, nzoID).Scan(&movieID, &showID, &episodeID)
	if err == sql.ErrNoRows {
		return 0, 0, 0, nil
	}
	return movieID, showID, episodeID, err
}

// GrabIndexer answers which indexer a SAB job's release came from, so a
// failure can be blocked at that indexer alone. Empty means reely never
// grabbed the job, or grabbed it before indexers were recorded.
func (s *Store) GrabIndexer(nzoID string) (string, error) {
	var indexer string
	err := s.db.QueryRow(`SELECT COALESCE(json_extract(detail, '$.indexer'), '')
		FROM history WHERE kind = 'grabbed' AND json_extract(detail, '$.nzoId') = ?
		ORDER BY id DESC LIMIT 1`, nzoID).Scan(&indexer)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return indexer, err
}

// GrabRelease answers which release a SAB job carried: the title and
// indexer off the newest "grabbed" history row for the nzo id. Both empty
// means reely never grabbed this job.
func (s *Store) GrabRelease(nzoID string) (title, indexer string, err error) {
	err = s.db.QueryRow(`SELECT COALESCE(json_extract(detail, '$.title'), ''),
		COALESCE(json_extract(detail, '$.indexer'), '')
		FROM history WHERE kind = 'grabbed' AND json_extract(detail, '$.nzoId') = ?
		ORDER BY id DESC LIMIT 1`, nzoID).Scan(&title, &indexer)
	if err == sql.ErrNoRows {
		return "", "", nil
	}
	return title, indexer, err
}

// HistoryByID loads one history row, or sql.ErrNoRows.
func (s *Store) HistoryByID(id int64) (*HistoryEntry, error) {
	h := &HistoryEntry{}
	err := s.db.QueryRow(`SELECT id, kind, COALESCE(movie_id,0), COALESCE(show_id,0),
		COALESCE(episode_id,0), detail, created_at FROM history WHERE id = ?`, id).
		Scan(&h.ID, &h.Kind, &h.MovieID, &h.ShowID, &h.EpisodeID, &h.Detail, &h.CreatedAt)
	if err != nil {
		return nil, err
	}
	return h, nil
}

// EpisodeNumbers resolves an episode row to its season/episode pair.
func (s *Store) EpisodeNumbers(id int64) (season, episode int, err error) {
	err = s.db.QueryRow(`SELECT season, episode FROM episodes WHERE id = ?`, id).Scan(&season, &episode)
	return season, episode, err
}

// AddHistory records one action. Zero ids stay NULL.
func (s *Store) AddHistory(kind string, movieID, showID, episodeID int64, detail string) error {
	_, err := s.db.Exec(`INSERT INTO history (kind, movie_id, show_id, episode_id, detail)
		VALUES (?, ?, ?, ?, ?)`,
		kind, nullID(movieID), nullID(showID), nullID(episodeID), detail)
	return err
}

// ListHistory returns the newest entries first, each labeled with the
// title it belongs to so the activity feed reads as prose.
func (s *Store) ListHistory(limit int) ([]HistoryEntry, error) {
	return s.listHistory(limit, 0)
}

// ListHistoryPage is ListHistory plus an offset and the total row count —
// the Activity page reads it one page at a time.
func (s *Store) ListHistoryPage(limit, offset int) ([]HistoryEntry, int, error) {
	entries, err := s.listHistory(limit, offset)
	if err != nil {
		return nil, 0, err
	}
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM history`).Scan(&total); err != nil {
		return nil, 0, err
	}
	return entries, total, nil
}

func (s *Store) listHistory(limit, offset int) ([]HistoryEntry, error) {
	if limit <= 0 {
		limit = 100
	}
	if offset < 0 {
		offset = 0
	}
	rows, err := s.db.Query(`SELECT h.id, h.kind, COALESCE(h.movie_id,0), COALESCE(h.show_id,0),
		COALESCE(h.episode_id,0), h.detail, h.created_at,
		COALESCE(m.title, sh.title, ''),
		COALESCE(e.season, 0), COALESCE(e.episode, 0)
		FROM history h
		LEFT JOIN movies m ON m.id = h.movie_id
		LEFT JOIN shows sh ON sh.id = h.show_id
		LEFT JOIN episodes e ON e.id = h.episode_id
		ORDER BY h.id DESC LIMIT ? OFFSET ?`, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []HistoryEntry
	for rows.Next() {
		var h HistoryEntry
		if err := rows.Scan(&h.ID, &h.Kind, &h.MovieID, &h.ShowID, &h.EpisodeID, &h.Detail, &h.CreatedAt,
			&h.Title, &h.Season, &h.Episode); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// pendingGrabStmt: one fully-literal statement per id column (gosec G202).
// A grab is pending while it is the title's newest grab/import/failure
// event and still fresh — after three days an unimported grab is presumed
// dead and the title becomes grabbable again. A failure must be in the
// running: without it the dead grab stays newest and blocks every
// automatic search for three days, while manual search still works.
// The check spans sibling rows (the same TMDB title in other libraries):
// one download satisfies every collection via hardlinks, so a grab for any
// sibling holds them all.
var pendingGrabStmt = map[string]string{
	"movie_id": `SELECT kind FROM history
		WHERE movie_id IN (
				SELECT m2.id FROM movies m2
				WHERE m2.id = ?1 OR (m2.tmdb_id IS NOT NULL
					AND m2.tmdb_id = (SELECT tmdb_id FROM movies WHERE id = ?1)))
			AND kind IN ('grabbed', 'imported', 'failed')
			AND created_at > datetime('now', '-3 days')
		ORDER BY id DESC LIMIT 1`,
	"episode_id": `SELECT kind FROM history
		WHERE episode_id IN (
				SELECT e2.id FROM episodes e2
					JOIN shows s2 ON s2.id = e2.show_id
					JOIN episodes e ON e.id = ?1
					JOIN shows s ON s.id = e.show_id
				WHERE e2.season = e.season AND e2.episode = e.episode
					AND (e2.id = ?1 OR (s2.tmdb_id IS NOT NULL AND s2.tmdb_id = s.tmdb_id)))
			AND kind IN ('grabbed', 'imported', 'failed')
			AND created_at > datetime('now', '-3 days')
		ORDER BY id DESC LIMIT 1`,
}

// HasPendingGrab reports whether a download for this title is presumably
// still in flight — the RSS loop's guard against grabbing the same thing
// every sweep. Exactly one of movieID / episodeID is set.
func (s *Store) HasPendingGrab(movieID, episodeID int64) (bool, error) {
	col, id := "movie_id", movieID
	if episodeID > 0 {
		col, id = "episode_id", episodeID
	}
	var kind string
	err := s.db.QueryRow(pendingGrabStmt[col], id).Scan(&kind)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return kind == "grabbed", nil
}

func nullID(id int64) any {
	if id == 0 {
		return nil
	}
	return id
}

// RecentShowImport is one show's recent arrivals for the home view: a
// single card per show, whether one episode landed or a whole season did.
type RecentShowImport struct {
	ShowID       int64  `json:"showId"`
	ShowTitle    string `json:"showTitle"`
	Poster       string `json:"poster"`
	LibraryID    int64  `json:"libraryId"`
	EpisodeID    int64  `json:"episodeId"` // the newest one
	Season       int    `json:"season"`
	Episode      int    `json:"episode"`
	EpisodeTitle string `json:"episodeTitle"`
	// Count is how many distinct episodes of this show imported in the
	// last week — 1 reads "S03E04", 12 reads "12 new episodes"
	Count      int    `json:"count"`
	ImportedAt string `json:"importedAt"`
}

// RecentShowImports lists shows by their latest imported episode, newest
// first — one row per show, so a fresh series pulling a whole season is one
// card, not thirty. Only episodes still holding files count, and the limit
// applies to SHOWS, not import events: a bulk backfill that writes hundreds
// of events for a few shows must never evict everyone else off the row.
// Count is how many distinct episodes of the show imported in the last
// week (floored at 1 when the newest import is older than that).
func (s *Store) RecentShowImports(limit int) ([]RecentShowImport, error) {
	weekAgo := time.Now().AddDate(0, 0, -7).UTC().Format("2006-01-02 15:04:05")
	rows, err := s.db.Query(`
		SELECT e.id, e.show_id, e.season, e.episode, COALESCE(e.title,''),
			sh.title, sh.poster_path, COALESCE(sh.library_id,0), h.created_at,
			(SELECT COUNT(DISTINCT h3.episode_id) FROM history h3
				JOIN episodes e3 ON e3.id = h3.episode_id
				WHERE e3.show_id = e.show_id AND h3.kind = 'imported'
					AND e3.file_path IS NOT NULL AND e3.file_path != ''
					AND h3.created_at >= ?)
		FROM history h
		JOIN episodes e ON e.id = h.episode_id
		JOIN shows sh ON sh.id = e.show_id
		WHERE h.id IN (
			SELECT MAX(h2.id) FROM history h2
			JOIN episodes e2 ON e2.id = h2.episode_id
			WHERE h2.kind = 'imported'
				AND e2.file_path IS NOT NULL AND e2.file_path != ''
			GROUP BY e2.show_id)
		ORDER BY h.id DESC
		LIMIT ?`, weekAgo, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []RecentShowImport
	for rows.Next() {
		var c RecentShowImport
		if err := rows.Scan(&c.EpisodeID, &c.ShowID, &c.Season, &c.Episode, &c.EpisodeTitle,
			&c.ShowTitle, &c.Poster, &c.LibraryID, &c.ImportedAt, &c.Count); err != nil {
			return nil, err
		}
		if c.Count == 0 {
			c.Count = 1 // latest import predates the week window — still one card
		}
		out = append(out, c)
	}
	return out, rows.Err()
}
