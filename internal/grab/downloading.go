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

// settleAfterGrab is how long a recorded grab is believed on its own,
// before the download client gets a say in whether it is still real.
//
// Long enough that no client is still being asked to admit it has the
// job, short enough that a cancelled download stops blocking searches
// within one RSS sweep rather than for the full three days the history
// row survives.
const settleAfterGrab = 10 * time.Minute

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

// queuedTitles maps what the download clients are actually working on
// back to the titles those jobs were grabbed for.
//
// ok is false when a client could not be reached, which is a different
// answer from an empty queue and has to stay that way: "nothing is
// downloading" and "nobody could say" lead to opposite decisions.
func (s *Service) queuedTitles(ctx context.Context) (movies, episodes map[int64]bool, ok bool) {
	var ids []string
	for _, category := range s.categories() {
		items, err := s.inFlight(ctx, category)
		if err != nil {
			return nil, nil, false
		}
		for _, it := range items {
			ids = append(ids, it.ID)
		}
	}
	m, e, _, err := s.Catalog.DownloadingIDs(ids)
	if err != nil {
		return nil, nil, false
	}
	return m, e, true
}

// grabInFlight reports whether this title's recorded grab is still a job
// a download client is actually working on. Exactly one of movieID /
// episodeID is set.
//
// The history row on its own was not enough. It says a grab was sent,
// never that the job survived: cancelling a download deletes it from the
// client and marks it imported in memory, but writes no history, so
// 'grabbed' stays the title's last word. For the next three days every
// search refused with "a download for this is already in flight" while
// no such download existed anywhere — and switching protocols made it
// permanent-looking, because the new client was never going to finish a
// job the old one no longer had.
//
// Cross-referencing the queue is what the detail pages have done all
// along, for the reason DownloadingIDs was written down with: a grab
// with no import yet keeps claiming to be downloading long after the job
// was cancelled or lost. This brings the search guard in line with it.
//
// A client that cannot be reached falls back to trusting the row. Not
// knowing is a reason to hold off, not to fetch a second copy.
func (s *Service) grabInFlight(ctx context.Context, movieID, episodeID int64) bool {
	pending, at, err := s.Catalog.PendingGrab(movieID, episodeID)
	if err != nil {
		return true
	}
	if !pending {
		return false
	}
	// A fresh grab is taken at its word. The client has only just been
	// handed the job and may not list it yet — SAB assigns its slot on
	// its own schedule, a magnet has to find peers before qBittorrent
	// knows its name — and doubting it that early is how the same
	// release gets fetched twice.
	if at.IsZero() || time.Since(at) < settleAfterGrab {
		return true
	}
	movies, episodes, ok := s.queuedTitles(ctx)
	if !ok {
		return true
	}
	if movieID > 0 {
		return movies[movieID]
	}
	return episodes[episodeID]
}
