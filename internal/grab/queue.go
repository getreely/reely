package grab

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/getreely/reely/internal/catalog"
)

// The search queue: adding or monitoring a title kicks off an active
// indexer search for it — the RSS sync only sees releases that appear
// FROM NOW ON, so anything already released needs one deliberate look.
// The queue is paced (a few searches per watcher tick) so monitoring a
// two-hundred-episode show doesn't hammer the indexers in one burst.

type searchTarget struct {
	movieID int64
	showID  int64
	season  int
	episode int
}

// EnqueueMovie queues one movie for an active search. Monitored and
// missing/upgradeable is re-checked at processing time, so a stale queue
// entry is harmless.
func (s *Service) EnqueueMovie(movieID int64) int {
	s.enqueue(searchTarget{movieID: movieID}, "search")
	return 1
}

// EnqueueShow queues a show's wanted episodes — all seasons when season is
// 0, one season otherwise. Unaired episodes are skipped: nothing to find
// yet, and their day belongs to the scheduled search. Returns how many
// searches were queued.
func (s *Service) EnqueueShow(showID int64, season int) int {
	sh, err := s.Catalog.GetShow(showID)
	if err != nil {
		return 0
	}
	today := time.Now().Format("2006-01-02")
	queued := 0
	for _, se := range sh.Seasons {
		if season > 0 && se.Number != season {
			continue
		}
		for _, ep := range se.Episodes {
			if !ep.Monitored || ep.FilePath != "" {
				continue
			}
			if ep.AirDate != "" && ep.AirDate[:min(10, len(ep.AirDate))] > today {
				continue
			}
			s.enqueue(searchTarget{showID: showID, season: se.Number, episode: ep.Episode}, "search")
			queued++
		}
	}
	return queued
}

// EnqueueEpisode queues one episode, aired or not — a deliberate ask
// searches regardless.
func (s *Service) EnqueueEpisode(showID int64, season, episode int) int {
	s.enqueue(searchTarget{showID: showID, season: season, episode: episode}, "search")
	return 1
}

func (s *Service) enqueue(t searchTarget, origin string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.queued == nil {
		s.queued = map[searchTarget]bool{}
	}
	if s.queued[t] {
		return
	}
	if s.origins == nil {
		s.origins = map[searchTarget]string{}
	}
	s.queued[t] = true
	s.origins[t] = origin // first asker wins; dedupe keeps one entry anyway
	s.queue = append(s.queue, t)
}

// QueueLen reports how many searches are waiting.
func (s *Service) QueueLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.queue)
}

// key is the stable handle a client uses to point at one queued search.
// It is derived from the target rather than stored, so it survives the
// queue being rebuilt and never needs its own bookkeeping.
func (t searchTarget) key() string {
	if t.movieID != 0 {
		return fmt.Sprintf("m%d", t.movieID)
	}
	return fmt.Sprintf("s%d.%d.%d", t.showID, t.season, t.episode)
}

// QueuedSearch is one waiting search, in the order it will run. Titles are
// not resolved here — the queue holds ids, and turning those into names is
// the API layer's job.
type QueuedSearch struct {
	Key     string `json:"key"`
	Kind    string `json:"kind"` // movie | episode
	MovieID int64  `json:"movieId,omitempty"`
	ShowID  int64  `json:"showId,omitempty"`
	Season  int    `json:"season,omitempty"`
	Episode int    `json:"episode,omitempty"`
}

// QueuedSearches returns every waiting search, oldest first — the order
// ProcessQueue will take them in. The whole list comes back because the
// caller narrows by title, which only it can resolve, and the queue is
// bounded by what was deliberately enqueued.
func (s *Service) QueuedSearches() []QueuedSearch {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]QueuedSearch, 0, len(s.queue))
	for _, t := range s.queue {
		q := QueuedSearch{Key: t.key(), Kind: "movie", MovieID: t.movieID}
		if t.movieID == 0 {
			q.Kind, q.ShowID, q.Season, q.Episode = "episode", t.showID, t.season, t.episode
		}
		out = append(out, q)
	}
	return out
}

// CancelSearches drops waiting searches by key and reports how many went.
// A key that isn't queued any more is not an error: the paced worker may
// simply have run it between the page being drawn and the cancel landing.
func (s *Service) CancelSearches(keys []string) int {
	if len(keys) == 0 {
		return 0
	}
	drop := make(map[string]bool, len(keys))
	for _, k := range keys {
		drop[k] = true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	kept := s.queue[:0]
	removed := 0
	for _, t := range s.queue {
		if drop[t.key()] {
			delete(s.queued, t)
			removed++
			continue
		}
		kept = append(kept, t)
	}
	// the tail now holds duplicates of entries kept earlier in the slice;
	// clearing it lets them be collected and keeps len honest
	for i := len(kept); i < len(s.queue); i++ {
		s.queue[i] = searchTarget{}
	}
	s.queue = kept
	return removed
}

// ClearSearchQueue empties the queue outright, reporting how many went.
func (s *Service) ClearSearchQueue() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.queue)
	s.queue = nil
	s.queued = map[searchTarget]bool{}
	return n
}

// ProcessQueue runs up to limit queued searches — the watcher calls it
// every tick, which is what paces the queue.
func (s *Service) ProcessQueue(ctx context.Context, limit int) int {
	grabbed := 0
	for range limit {
		s.mu.Lock()
		if len(s.queue) == 0 {
			s.mu.Unlock()
			return grabbed
		}
		t := s.queue[0]
		s.queue = s.queue[1:]
		delete(s.queued, t)
		origin := s.origins[t]
		delete(s.origins, t)
		s.mu.Unlock()

		if err := ctx.Err(); err != nil {
			return grabbed
		}
		// the queue is a background pass: the outcome's reason has no one
		// to read it, so only the count matters here
		if s.searchTarget(ctx, t, origin).Grabbed != "" {
			grabbed++
		}
	}
	return grabbed
}

// SearchOutcome is what one active search did. Grabbed names the release
// sent to the download client; when nothing was, Reason says why in words
// a person can act on. Every path that declines to grab owes a reason —
// a silent skip is indistinguishable from a broken search, which is
// exactly how a stuck pending grab hid for so long.
type SearchOutcome struct {
	Grabbed string `json:"grabbed,omitempty"`
	Reason  string `json:"reason,omitempty"`
}

// SearchNow runs one search immediately rather than queueing it — the
// interactive path, where someone is watching the button and deserves an
// answer instead of a promise.
func (s *Service) SearchNow(ctx context.Context, movieID, showID int64, season, episode int) SearchOutcome {
	return s.searchTarget(ctx, searchTarget{
		movieID: movieID, showID: showID, season: season, episode: episode,
	}, "manual")
}

// searchTarget runs one active search and grabs the best acceptable
// release, if any.
func (s *Service) searchTarget(ctx context.Context, t searchTarget, origin string) SearchOutcome {
	if !s.Indexer.Configured() || !s.haveClient() {
		return SearchOutcome{Reason: "Prowlarr and SABnzbd both need to be configured in Settings"}
	}
	if t.movieID > 0 {
		return s.searchMovie(ctx, t.movieID, origin)
	}
	return s.searchEpisode(ctx, t, origin)
}

func (s *Service) searchMovie(ctx context.Context, movieID int64, origin string) SearchOutcome {
	m, err := s.Catalog.GetMovie(movieID)
	if err != nil {
		return SearchOutcome{Reason: "this movie is gone"}
	}
	if !m.Monitored {
		return SearchOutcome{Reason: "not monitored — turn monitoring on to hunt for it"}
	}
	if pending, err := s.Catalog.HasPendingGrab(movieID, 0); err != nil || pending {
		return SearchOutcome{Reason: "a download for this is already in flight"}
	}
	views, err := s.MovieReleases(ctx, m)
	if err != nil {
		log.Printf("reely: search %q: %v", m.Title, err)
		return SearchOutcome{Reason: "the indexer search failed — see Settings → Health"}
	}
	best := bestAcceptedUnblocked(views, s.blockedReleases())
	if best == nil {
		return SearchOutcome{Reason: noCandidateReason(views, s.blockedReleases())}
	}
	if err := s.GrabMovie(ctx, m, grabRequestForView(*best, origin)); err != nil {
		log.Printf("reely: search grab %q: %v", best.Title, err)
		return SearchOutcome{Reason: "sending it to SABnzbd failed: " + err.Error()}
	}
	log.Printf("reely: search grabbed %q for %s", best.Title, m.Title)
	return SearchOutcome{Grabbed: best.Title}
}

func (s *Service) searchEpisode(ctx context.Context, t searchTarget, origin string) SearchOutcome {
	sh, err := s.Catalog.GetShow(t.showID)
	if err != nil {
		return SearchOutcome{Reason: "this show is gone"}
	}
	if !sh.Monitored {
		return SearchOutcome{Reason: "the show is not monitored — turn monitoring on to hunt for it"}
	}
	ep := findEpisode(sh, t.season, t.episode)
	if ep == nil {
		return SearchOutcome{Reason: "no such episode on this show"}
	}
	if !ep.Monitored {
		return SearchOutcome{Reason: "this episode is not monitored — turn its switch on to hunt for it"}
	}
	if pending, err := s.Catalog.HasPendingGrab(0, ep.ID); err != nil || pending {
		return SearchOutcome{Reason: "a download for this episode is already in flight"}
	}
	views, err := s.EpisodeReleases(ctx, sh, t.season, t.episode)
	if err != nil {
		log.Printf("reely: search %s S%02dE%02d: %v", sh.Title, t.season, t.episode, err)
		return SearchOutcome{Reason: "the indexer search failed — see Settings → Health"}
	}
	best := bestAcceptedUnblocked(views, s.blockedReleases())
	if best == nil {
		return SearchOutcome{Reason: noCandidateReason(views, s.blockedReleases())}
	}
	if err := s.GrabForShow(ctx, sh, t.season, t.episode, grabRequestForView(*best, origin)); err != nil {
		log.Printf("reely: search grab %q: %v", best.Title, err)
		return SearchOutcome{Reason: "sending it to SABnzbd failed: " + err.Error()}
	}
	log.Printf("reely: search grabbed %q for %s S%02dE%02d", best.Title, sh.Title, t.season, t.episode)
	return SearchOutcome{Grabbed: best.Title}
}

// noCandidateReason separates "the profile rejected everything" from "the
// only acceptable ones are blocklisted" — very different problems, and the
// second one is invisible in manual search, which ignores the blocklist.
func noCandidateReason(views []ReleaseView, blocked catalog.Blocked) string {
	accepted, banned := 0, 0
	for i := range views {
		if !views[i].Accepted {
			continue
		}
		accepted++
		if blocked.Has(views[i].Title, views[i].Indexer) {
			banned++
		}
	}
	switch {
	case len(views) == 0:
		return "the indexers came back empty"
	case accepted == 0:
		return fmt.Sprintf("none of the %d releases passed the quality profile — check manual search for the reasons", len(views))
	case banned == accepted:
		return fmt.Sprintf("every acceptable release (%d) is on the blocklist — pardon one in Activity → Blocklist", banned)
	}
	return "nothing acceptable was found"
}

func grabRequestForView(v ReleaseView, origin string) GrabRequest {
	return GrabRequest{Title: v.Title, DownloadURL: v.DownloadURL, Indexer: v.Indexer, Size: v.Size, Origin: origin}
}
