package grab

import (
	"context"
	"time"
)

// Downloading: which titles SAB is working on right now, for the detail
// pages. A title with a job in flight should say so rather than reading as
// missing — "missing" invites a search for something already on its way.

// downloadingTTL keeps a burst of detail-page views to one SAB call. Short
// enough that a finished download stops claiming to be in flight almost
// at once.
const downloadingTTL = 5 * time.Second

type downloadingCache struct {
	movies, episodes, shows map[int64]bool
	at                      time.Time
}

// Downloading reports which movies, episodes and shows have a SAB job in
// flight. Errors are answered with empty sets: a detail page that can't
// reach SAB should read as it always did, not fail.
func (s *Service) Downloading(ctx context.Context) (movies, episodes, shows map[int64]bool) {
	s.mu.Lock()
	if c := s.downloading; c != nil && time.Since(c.at) < downloadingTTL {
		m, e, sh := c.movies, c.episodes, c.shows
		s.mu.Unlock()
		return m, e, sh
	}
	s.mu.Unlock()

	empty := map[int64]bool{}
	if !s.haveClient() {
		return empty, empty, empty
	}
	items, _ := s.Queue(ctx, "", 0, 0)
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	m, e, sh, err := s.Catalog.DownloadingIDs(ids)
	if err != nil {
		return empty, empty, empty
	}
	s.mu.Lock()
	s.downloading = &downloadingCache{movies: m, episodes: e, shows: sh, at: time.Now()}
	s.mu.Unlock()
	return m, e, sh
}
