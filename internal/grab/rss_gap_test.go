package grab

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/getreely/reely/internal/prowlarr"
)

// filler makes a valid, parseable movie release that matches nothing in
// the catalog — feed traffic for the release to hide behind.
func filler(n int, ts time.Time) prowlarr.Release {
	return prowlarr.Release{
		GUID:        fmt.Sprintf("guid-%d", n),
		Title:       fmt.Sprintf("Some.Other.Movie.%d.2020.1080p.WEB-DL", n),
		Size:        gb(5),
		Protocol:    "usenet",
		Indexer:     "nzbs",
		PublishDate: ts.Format(time.RFC3339),
		DownloadURL: fmt.Sprintf("https://idx/nzb/f%d", n),
	}
}

// THE case behind Reacher: a wanted release posts on a busy night and a
// hundred other posts land on top of it before the next poll. A sync that
// only ever reads the first page never sees it; the gap-closing sync pages
// back until it reconnects with the previous sync and judges everything in
// between.
func TestRSSSyncPagesBackToCloseTheGap(t *testing.T) {
	svc, idx, dl := rssService(t)
	seedMovie(t, svc.Catalog, t.TempDir()) // Inception 2010, monitored, no file

	t0 := time.Now().Add(-2 * time.Hour)
	t1 := time.Now().Add(-10 * time.Minute)

	// sync one: a hundred fillers, nothing wanted — establishes the cursor
	old := make([]prowlarr.Release, 0, 100)
	for n := 0; n < 100; n++ {
		old = append(old, filler(n, t0))
	}
	idx.releases = old
	if n := svc.RSSSync(context.Background()); n != 0 {
		t.Fatalf("sync one grabbed %d, want 0", n)
	}

	// between syncs: 150 new posts pile on, the wanted one buried at
	// position 120 — past the first page
	wanted := prowlarr.Release{
		GUID: "guid-inception", Title: "Inception.2010.1080p.BluRay.x264",
		Size: gb(9), Protocol: "usenet", Indexer: "nzbs",
		PublishDate: t1.Format(time.RFC3339), DownloadURL: "https://idx/nzb/inception",
	}
	feed := make([]prowlarr.Release, 0, 250)
	for n := 100; n < 220; n++ {
		feed = append(feed, filler(n, t1))
	}
	feed = append(feed, wanted)
	for n := 220; n < 249; n++ {
		feed = append(feed, filler(n, t1))
	}
	feed = append(feed, old...)
	idx.releases = feed

	idx.pageOffsets = nil
	if n := svc.RSSSync(context.Background()); n != 1 {
		t.Fatalf("sync two grabbed %d, want 1", n)
	}
	if dl.lastURL != "https://idx/nzb/inception" {
		t.Fatalf("grabbed %q, want the buried release", dl.lastURL)
	}
	paged := false
	for _, off := range idx.pageOffsets {
		if off == 100 {
			paged = true
		}
	}
	if !paged {
		t.Fatalf("never asked for the second page; offsets: %v", idx.pageOffsets)
	}
}

// A feed read to its end stops there: a short page means there is nothing
// further back, however much the feed churned since last time.
func TestRSSFetchStopsAtAShortPage(t *testing.T) {
	svc, idx, _ := rssService(t)
	t0 := time.Now().Add(-2 * time.Hour)
	t1 := time.Now().Add(-10 * time.Minute)

	idx.releases = []prowlarr.Release{filler(0, t0), filler(1, t0)}
	svc.fetchRecent(context.Background(), "movies", prowlarr.MovieCats)

	// entirely new posts that never reconnect — but the feed is tiny, so
	// one short page covers all of it
	idx.releases = []prowlarr.Release{filler(10, t1), filler(11, t1)}
	idx.pageOffsets = nil
	got := svc.fetchRecent(context.Background(), "movies", prowlarr.MovieCats)
	if len(got) != 2 {
		t.Fatalf("fetched %d releases, want 2", len(got))
	}
	if len(idx.pageOffsets) != 1 {
		t.Fatalf("made %d page calls, want 1 (a short page is the feed's end); offsets: %v", len(idx.pageOffsets), idx.pageOffsets)
	}
}

// An indexer that ignores offset answers every page with its first — the
// walk-back must notice nothing new arrived and stop, not spin to the cap.
func TestRSSFetchStopsWhenOffsetIsIgnored(t *testing.T) {
	svc, idx, _ := rssService(t)
	t0 := time.Now().Add(-2 * time.Hour)
	t1 := time.Now().Add(-10 * time.Minute)

	idx.releases = []prowlarr.Release{filler(0, t0), filler(1, t0)}
	svc.fetchRecent(context.Background(), "movies", prowlarr.MovieCats)

	// a full page of entirely new posts that never reconnects, on an
	// indexer that repeats page one forever
	feed := make([]prowlarr.Release, 0, 120)
	for n := 10; n < 130; n++ {
		feed = append(feed, filler(n, t1))
	}
	idx.releases = feed
	idx.ignorePages = true
	idx.pageOffsets = nil
	got := svc.fetchRecent(context.Background(), "movies", prowlarr.MovieCats)
	if len(got) != rssPageSize {
		t.Fatalf("fetched %d releases, want the one real page of %d", len(got), rssPageSize)
	}
	if len(idx.pageOffsets) != 2 {
		t.Fatalf("made %d page calls, want 2 (the repeat page ends the walk); offsets: %v", len(idx.pageOffsets), idx.pageOffsets)
	}
}

// A feed that churned past the ceiling since the last sync stops at the
// ceiling — Sonarr's same 1000-result guardrail.
func TestRSSFetchStopsAtTheCeiling(t *testing.T) {
	svc, idx, _ := rssService(t)
	t1 := time.Now().Add(-10 * time.Minute)

	// a cursor that nothing in the feed reconnects with
	svc.rssSeen = map[string]map[string]rssCursor{
		"movies": {"nzbs": {ids: map[string]bool{"long-gone": true}}},
	}
	feed := make([]prowlarr.Release, 0, 1100)
	for n := 0; n < 1100; n++ {
		feed = append(feed, filler(n, t1))
	}
	idx.releases = feed

	got := svc.fetchRecent(context.Background(), "movies", prowlarr.MovieCats)
	if len(got) != rssMaxResults {
		t.Fatalf("fetched %d releases, want the %d ceiling", len(got), rssMaxResults)
	}
	if len(idx.pageOffsets) != rssMaxResults/rssPageSize {
		t.Fatalf("made %d page calls, want %d; offsets: %v", len(idx.pageOffsets), rssMaxResults/rssPageSize, idx.pageOffsets)
	}
}

// A fresh install has no cursor anywhere: the first sync takes one page
// and builds its memory rather than walking back blind.
func TestRSSFetchFreshInstallTakesOnePage(t *testing.T) {
	svc, idx, _ := rssService(t)
	feed := make([]prowlarr.Release, 0, 300)
	for n := 0; n < 300; n++ {
		feed = append(feed, filler(n, time.Now()))
	}
	idx.releases = feed

	got := svc.fetchRecent(context.Background(), "movies", prowlarr.MovieCats)
	if len(got) != rssPageSize {
		t.Fatalf("fresh install fetched %d, want one page of %d", len(got), rssPageSize)
	}
	if len(idx.pageOffsets) != 1 {
		t.Fatalf("fresh install made %d calls, want 1", len(idx.pageOffsets))
	}
}

// Cursors are per indexer and never overwrite each other: an indexer that
// answers nothing for a sync keeps its place, so when it comes back the
// walk-back still knows where it left off.
func TestRSSCursorKeepsQuietIndexers(t *testing.T) {
	svc, idx, _ := rssService(t)
	t0 := time.Now().Add(-2 * time.Hour)
	t1 := time.Now().Add(-10 * time.Minute)

	a0, b0 := filler(0, t0), filler(1, t0)
	a0.Indexer, b0.Indexer = "alpha", "beta"
	idx.releases = []prowlarr.Release{a0, b0}
	svc.fetchRecent(context.Background(), "movies", prowlarr.MovieCats)

	// beta answers nothing this sync
	a1 := filler(2, t1)
	a1.Indexer = "alpha"
	idx.releases = []prowlarr.Release{a1, a0}
	svc.fetchRecent(context.Background(), "movies", prowlarr.MovieCats)

	cur := svc.rssSeen["movies"]
	if _, ok := cur["beta"]; !ok {
		t.Fatal("beta's cursor was dropped while it was quiet")
	}
	if !cur["beta"].ids[b0.GUID] {
		t.Fatal("beta's remembered releases were lost")
	}
	if !cur["alpha"].ids[a1.GUID] {
		t.Fatal("alpha's cursor did not advance")
	}
	// and the DB copy agrees, so a restart keeps beta's place too
	fromDB := (&Service{Indexer: idx, Catalog: svc.Catalog}).loadRSSCursor("movies")
	if !fromDB["beta"].ids[b0.GUID] {
		t.Fatal("beta's cursor missing from the persisted copy")
	}
}

// The cursor is written through to the DB: a new process over the same
// catalog resumes the walk-back, so a release that posted while reely was
// down — and scrolled past the first page before it came back — is still
// seen and grabbed.
func TestRSSCursorSurvivesRestart(t *testing.T) {
	svc, idx, _ := rssService(t)
	seedMovie(t, svc.Catalog, t.TempDir()) // Inception 2010, monitored, no file

	t0 := time.Now().Add(-2 * time.Hour)
	t1 := time.Now().Add(-10 * time.Minute)

	old := make([]prowlarr.Release, 0, 100)
	for n := 0; n < 100; n++ {
		old = append(old, filler(n, t0))
	}
	idx.releases = old
	if n := svc.RSSSync(context.Background()); n != 0 {
		t.Fatalf("sync one grabbed %d, want 0", n)
	}

	// reely restarts; while it was down the feed moved on past a page
	wanted := prowlarr.Release{
		GUID: "guid-inception", Title: "Inception.2010.1080p.BluRay.x264",
		Size: gb(9), Protocol: "usenet", Indexer: "nzbs",
		PublishDate: t1.Format(time.RFC3339), DownloadURL: "https://idx/nzb/inception",
	}
	feed := make([]prowlarr.Release, 0, 250)
	for n := 100; n < 230; n++ {
		feed = append(feed, filler(n, t1))
	}
	feed = append(feed, wanted)
	feed = append(feed, old...)
	idx.releases = feed
	idx.pageOffsets = nil

	dl := &stubDownloader{}
	restarted := &Service{Indexer: idx, Usenet: dl, Catalog: svc.Catalog}
	if n := restarted.RSSSync(context.Background()); n != 1 {
		t.Fatalf("post-restart sync grabbed %d, want 1", n)
	}
	if dl.lastURL != "https://idx/nzb/inception" {
		t.Fatalf("grabbed %q, want the release that posted during the downtime", dl.lastURL)
	}
}
