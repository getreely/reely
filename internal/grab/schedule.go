package grab

import (
	"context"
	"log"
	"time"
)

// The release-day pass: air and digital dates drive TARGETED searches —
// this is the only thing those dates gate (the RSS sync deliberately has
// no date gate at all).
//
// The hunt TAPERS rather than stopping dead: eagerly on the day and the
// day after, daily through day three, then single looks on days seven and
// fourteen. A pass that covered only today-and-yesterday orphaned every
// title whose acceptable release landed late — posted on day two, delayed
// by indexer lag, or missed because the profile rejected everything during
// a two-day window that never reopened. Fourteen days of taper costs a
// handful of searches per title, ever; a title still missing after that
// belongs to the opt-in wanted sweep, which is real recurring indexer
// traffic and stays a deliberate choice.

// taperWindowDays is how far back the pass looks. Beyond it, the wanted
// sweep owns the backlog.
const taperWindowDays = 14

// taperGate says whether a target whose date is age days old should be
// searched now, given when it was last searched (zero when never, or when
// a restart emptied the in-memory ledger). Day 0-1: every six hours — a
// handful of tries while the release is actually landing. Days 2-3: once
// a day. Days 7 and 14: one look each, catching stragglers and repacks.
// A restart therefore re-tries at most once per eligible day instead of
// stampeding the whole window.
func taperGate(age int, last time.Time, now time.Time) bool {
	var gate time.Duration
	switch {
	case age < 0 || age > taperWindowDays:
		return false
	case age <= 1:
		gate = 6 * time.Hour
	case age <= 3:
		gate = 24 * time.Hour
	case age == 7 || age == 14:
		gate = 24 * time.Hour
	default:
		return false // the quiet days between taper points
	}
	return last.IsZero() || now.Sub(last) >= gate
}

// ReleaseDayPass queues searches for everything monitored and missing
// whose date falls inside the taper window. Returns how many were queued.
func (s *Service) ReleaseDayPass(ctx context.Context) int {
	if !s.Indexer.Configured() || !s.haveClient() {
		return 0
	}
	now := time.Now()
	from := now.AddDate(0, 0, -taperWindowDays).Format("2006-01-02")
	today := now.Format("2006-01-02")
	// the release-day pass acts for the install, not for a person, so it
	// works over every library
	libs, err := s.Catalog.AllLibraryIDs()
	if err != nil {
		log.Printf("reely: release-day pass: %v", err)
		return 0
	}
	items, err := s.Catalog.Calendar(from, today, libs)
	if err != nil {
		log.Printf("reely: release-day pass: %v", err)
		return 0
	}
	queued := 0
	for _, it := range items {
		if it.OnDisk {
			continue
		}
		date, err := time.ParseInLocation("2006-01-02", it.Date[:min(10, len(it.Date))], now.Location())
		if err != nil {
			continue
		}
		age := int(now.Sub(date).Hours() / 24)
		var t searchTarget
		if it.Kind == "movie" {
			t = searchTarget{movieID: it.MovieID}
		} else {
			t = searchTarget{showID: it.ShowID, season: it.Season, episode: it.Episode}
		}
		if !s.dueForScheduledSearch(t, age, now) {
			continue
		}
		s.enqueue(t, "release day")
		queued++
	}
	return queued
}

// dueForScheduledSearch applies the taper gate under the lock and prunes
// entries older than the window, so the ledger only ever holds the taper's
// worth of targets.
func (s *Service) dueForScheduledSearch(t searchTarget, age int, now time.Time) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.scheduled == nil {
		s.scheduled = map[searchTarget]time.Time{}
	}
	if !taperGate(age, s.scheduled[t], now) {
		return false
	}
	for k, v := range s.scheduled {
		if now.Sub(v) > (taperWindowDays+1)*24*time.Hour {
			delete(s.scheduled, k)
		}
	}
	s.scheduled[t] = now
	return true
}
