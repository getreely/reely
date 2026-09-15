package grab

import (
	"strconv"
	"time"
)

// The wanted search: an OPT-IN periodic sweep that re-searches the whole
// backlog of monitored titles that are missing or below their profile's
// upgrade cutoff. Off by default — the RSS sync and the release-day pass
// carry the normal load, and a scheduled full sweep is real indexer
// traffic. When enabled, everything still flows through the paced queue
// (a few searches per watcher tick), so even a large backlog spreads out
// instead of bursting.

// wantedFloorHours is the tightest allowed cadence — anything more
// frequent is pounding the indexers for a backlog that hasn't changed.
const wantedFloorHours = 6

// WantedSearchInterval reads the wanted_search_hours setting: 0 (the
// default) disables the sweep entirely; anything else is floored at six
// hours.
func (s *Service) WantedSearchInterval() time.Duration {
	hours, err := strconv.Atoi(s.setting("wanted_search_hours", "0"))
	if err != nil || hours <= 0 {
		return 0
	}
	if hours < wantedFloorHours {
		hours = wantedFloorHours
	}
	return time.Duration(hours) * time.Hour
}

// SearchMissing enqueues a search for everything monitored and still
// missing in one kind and set of libraries — the "search missing" button
// on a library page. Unlike WantedPass it leaves upgrades alone: the
// button says missing, so it fills gaps rather than quietly re-grabbing
// files that are merely below cutoff. Everything goes through the paced
// queue, so a large backlog spreads out instead of bursting.
func (s *Service) SearchMissing(kind string, libraryIDs []int64) int {
	if !s.Indexer.Configured() || !s.haveClient() {
		return 0
	}
	targets, err := s.Catalog.WantedIn(time.Now().Format("2006-01-02"), kind, libraryIDs)
	if err != nil {
		return 0
	}
	for _, t := range targets {
		if t.MovieID > 0 {
			s.enqueue(searchTarget{movieID: t.MovieID}, "search missing")
		} else {
			s.enqueue(searchTarget{showID: t.ShowID, season: t.Season, episode: t.Episode}, "search missing")
		}
	}
	return len(targets)
}

// WantedPass enqueues an active search for every monitored, missing, aired
// title, plus everything on disk below its profile's upgrade cutoff.
// Returns how many targets the sweep covered; the queue dedupes and
// re-checks state at processing time, so stale entries are harmless.
func (s *Service) WantedPass() int {
	if !s.Indexer.Configured() || !s.haveClient() {
		return 0
	}
	targets, err := s.Catalog.Wanted(time.Now().Format("2006-01-02"))
	if err != nil {
		return 0
	}
	for _, t := range targets {
		if t.MovieID > 0 {
			s.enqueue(searchTarget{movieID: t.MovieID}, "backlog")
		} else {
			s.enqueue(searchTarget{showID: t.ShowID, season: t.Season, episode: t.Episode}, "backlog")
		}
	}
	upgrades := s.upgradeTargets()
	for _, t := range upgrades {
		s.enqueue(t, "backlog")
	}
	return len(targets) + len(upgrades)
}
