// Package watcher is reely's background heartbeat. It ticks every 15
// seconds so a download that finishes in SAB is on the
// library within seconds, whether or not anyone has the UI open. Slower work
// — the RSS sync, the release-day search pass — rides the same ticker,
// each gated to its own cadence.
package watcher

import (
	"context"
	"log"
	"time"

	"github.com/getreely/reely/internal/backup"
	"github.com/getreely/reely/internal/grab"
	"github.com/getreely/reely/internal/lists"
	"github.com/getreely/reely/internal/refresh"
)

const tick = 15 * time.Second

type Watcher struct {
	grab            *grab.Service
	lists           *lists.Syncer
	refresh         *refresh.Refresher
	backup          *backup.Service
	lastRSS         time.Time
	lastReleasePass time.Time
	lastWanted      time.Time
	lastLists       time.Time
	lastRefresh     time.Time
	lastBackup      time.Time
}

// New builds a watcher. The wanted and lists clocks start at now, not
// zero: both are scheduled bulk traffic, and a restart shouldn't count as
// a scheduled occasion (Sync Now covers the impatient case).
func New(g *grab.Service, l *lists.Syncer, r *refresh.Refresher, b *backup.Service) *Watcher {
	return &Watcher{grab: g, lists: l, refresh: r, backup: b, lastWanted: time.Now(), lastLists: time.Now()}
}

// Run ticks until ctx is cancelled.
func (w *Watcher) Run(ctx context.Context) {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	log.Printf("reely: watcher running (every %s, rss sync every %s)", tick, w.grab.RSSInterval())
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			w.tickOnce(ctx)
		}
	}
}

func (w *Watcher) tickOnce(ctx context.Context) {
	if n := w.grab.ImportCompleted(ctx); n > 0 {
		log.Printf("reely: imported %d completed download(s)", n)
	}
	// a few active searches per tick — adding a whole show queues every
	// missing episode, and this pacing is what keeps the indexers happy
	w.grab.ProcessQueue(ctx, 3)
	// the RSS sync rides the same ticker on its own cadence; the zero
	// lastRSS means the first tick after boot syncs immediately
	if time.Since(w.lastRSS) >= w.grab.RSSInterval() {
		w.lastRSS = time.Now()
		if n := w.grab.RSSSync(ctx); n > 0 {
			log.Printf("reely: rss sync grabbed %d release(s)", n)
		}
	}
	// the release-day pass hunts for today's and yesterday's arrivals once
	// an hour; its own per-target gate spaces the retries out further
	if time.Since(w.lastReleasePass) >= time.Hour {
		w.lastReleasePass = time.Now()
		if n := w.grab.ReleaseDayPass(ctx); n > 0 {
			log.Printf("reely: release-day pass queued %d search(es)", n)
		}
	}
	// the opt-in wanted sweep: 0 interval means off; re-read every tick so
	// flipping the setting takes effect without a restart
	if iv := w.grab.WantedSearchInterval(); iv > 0 && time.Since(w.lastWanted) >= iv {
		w.lastWanted = time.Now()
		if n := w.grab.WantedPass(); n > 0 {
			log.Printf("reely: wanted sweep queued %d search(es)", n)
		}
	}
	// watched lists: each list carries its own cadence — check for due
	// ones every minute
	if w.lists != nil && time.Since(w.lastLists) >= time.Minute {
		w.lastLists = time.Now()
		if n := w.lists.SyncDue(ctx); n > 0 {
			log.Printf("reely: list sync added %d title(s)", n)
		}
	}
	// the hourly metadata refresh: due titles re-fetch from TMDB (tiered
	// daily/weekly inside), so continuing shows learn their new episodes
	if w.refresh != nil && time.Since(w.lastRefresh) >= time.Hour {
		w.lastRefresh = time.Now()
		if n := w.refresh.Pass(ctx); n > 0 {
			log.Printf("reely: metadata refresh updated %d title(s)", n)
		}
	}
	// a rotating daily database backup; the service itself decides whether
	// one is due, this gate just keeps the check off the hot path
	if w.backup != nil && time.Since(w.lastBackup) >= time.Hour {
		w.lastBackup = time.Now()
		w.backup.MaybeDaily(ctx)
	}
}
