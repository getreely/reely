package grab

import (
	"context"
	"strings"
	"testing"

	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/prowlarr"
)

func TestQueueSearchesAndGrabsMovie(t *testing.T) {
	svc, idx, dl := rssService(t)
	m := seedMovie(t, svc.Catalog, t.TempDir())

	idx.releases = []prowlarr.Release{
		{Title: "Inception.2010.1080p.BluRay.x264", Size: gb(9), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/best", Indexer: "nzbs"},
		{Title: "Inception.2010.720p.WEB", Size: gb(2), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/worse", Indexer: "nzbs"},
	}
	svc.EnqueueMovie(m.ID)
	svc.EnqueueMovie(m.ID) // dedupe
	if svc.QueueLen() != 1 {
		t.Fatalf("queue = %d, want 1 (deduped)", svc.QueueLen())
	}

	if n := svc.ProcessQueue(context.Background(), 5); n != 1 {
		t.Fatalf("grabbed = %d, want 1", n)
	}
	if dl.lastURL != "https://idx/nzb/best" {
		t.Fatalf("grabbed %q, want the best release", dl.lastURL)
	}
	if svc.QueueLen() != 0 {
		t.Fatalf("queue not drained: %d", svc.QueueLen())
	}
	// searched with the movie's query, not an rss sweep
	if idx.lastQ != "Inception 2010" {
		t.Fatalf("query = %q", idx.lastQ)
	}
}

func TestQueueShowSkipsOnDiskAndUnaired(t *testing.T) {
	svc, idx, dl := rssService(t)
	cat := svc.Catalog
	lib, err := cat.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	showID, err := cat.UpsertShow(&metadata.ShowDetail{
		TmdbID: 1396, Title: "Breaking Bad", Year: 2008,
		Seasons: []metadata.SeasonDetail{{Number: 1, Name: "Season 1", Episodes: []metadata.EpisodeDetail{
			{TmdbID: 1, Season: 1, Episode: 1, Title: "Pilot", AirDate: "2008-01-20", Runtime: 48},
			{TmdbID: 2, Season: 1, Episode: 2, Title: "On disk", AirDate: "2008-01-27", Runtime: 48},
			{TmdbID: 3, Season: 1, Episode: 3, Title: "Unaired", AirDate: "2099-01-01", Runtime: 48},
		}}},
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	epID, err := cat.EpisodeID(showID, 1, 2)
	if err != nil || epID == 0 {
		t.Fatal("episode id")
	}
	if err := cat.AttachEpisodeFile(epID, "/x/e2.mkv", 1, "1080p", ""); err != nil {
		t.Fatal(err)
	}

	svc.EnqueueShow(showID, 0)
	// only the pilot qualifies: e2 is on disk, e3 hasn't aired
	if svc.QueueLen() != 1 {
		t.Fatalf("queue = %d, want 1", svc.QueueLen())
	}

	idx.releases = []prowlarr.Release{
		{Title: "Breaking.Bad.S01E01.1080p.WEB-DL", Size: gb(2), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/e1", Indexer: "nzbs"},
	}
	if n := svc.ProcessQueue(context.Background(), 5); n != 1 {
		t.Fatalf("grabbed = %d, want 1", n)
	}
	if dl.lastURL != "https://idx/nzb/e1" || dl.lastCat != "tvshows" {
		t.Fatalf("grabbed %q into %q", dl.lastURL, dl.lastCat)
	}
}

func TestQueuePacingRespectsLimit(t *testing.T) {
	svc, idx, _ := rssService(t)
	sh := seedShow(t, svc.Catalog, t.TempDir()) // three missing monitored episodes
	idx.releases = nil                          // indexers empty — searches run, nothing grabs

	svc.EnqueueShow(sh.ID, 0)
	if svc.QueueLen() != 3 {
		t.Fatalf("queue = %d, want 3", svc.QueueLen())
	}
	svc.ProcessQueue(context.Background(), 2)
	if svc.QueueLen() != 1 {
		t.Fatalf("after limited pass queue = %d, want 1", svc.QueueLen())
	}
}

func TestQueueSkipsPendingAndUnmonitored(t *testing.T) {
	svc, idx, dl := rssService(t)
	m := seedMovie(t, svc.Catalog, t.TempDir())
	idx.releases = []prowlarr.Release{
		{Title: "Inception.2010.1080p.WEB-DL", Size: gb(5), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/1", Indexer: "nzbs"},
	}

	// a pending grab blocks the search-grab
	if err := svc.Catalog.AddHistory("grabbed", m.ID, 0, 0, "{}"); err != nil {
		t.Fatal(err)
	}
	svc.EnqueueMovie(m.ID)
	if n := svc.ProcessQueue(context.Background(), 5); n != 0 {
		t.Fatal("grabbed despite pending grab")
	}

	// an unmonitored movie is never searched
	if err := svc.Catalog.AddHistory("imported", m.ID, 0, 0, "{}"); err != nil {
		t.Fatal(err) // clears pending
	}
	if err := svc.Catalog.SetMovieMonitored(m.ID, false); err != nil {
		t.Fatal(err)
	}
	svc.EnqueueMovie(m.ID)
	if n := svc.ProcessQueue(context.Background(), 5); n != 0 {
		t.Fatal("grabbed for unmonitored movie")
	}
	if dl.calls != 0 {
		t.Fatalf("downloader reached %d times", dl.calls)
	}
}

// A failed download must not leave the title stuck. The grab writes a
// "grabbed" event; the failure that follows has to clear it, or the
// pending-grab gate silently blocks every automatic search for three days
// while manual search still works — which is exactly how it looks from the
// outside: "auto search does nothing, manual search grabs fine".
func TestFailedDownloadClearsPendingGrab(t *testing.T) {
	svc, idx, _ := rssService(t)
	m := seedMovie(t, svc.Catalog, t.TempDir())
	idx.releases = []prowlarr.Release{
		{Title: "Inception.2010.1080p.WEB-DL", Size: gb(5), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/1", Indexer: "nzbs"},
	}

	// reely grabbed something for this title…
	if err := svc.Catalog.AddHistory("grabbed", m.ID, 0, 0,
		`{"title":"Inception.2010.1080p.WEB-DL.BAD"}`); err != nil {
		t.Fatal(err)
	}
	// …and the download failed, recorded the way the SAB poller records it
	if err := svc.Catalog.AddHistory("failed", m.ID, 0, 0,
		`{"title":"Inception.2010.1080p.WEB-DL.BAD","error":"too many missing articles"}`); err != nil {
		t.Fatal(err)
	}

	pending, err := svc.Catalog.HasPendingGrab(m.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if pending {
		t.Fatal("still pending after the download failed — automatic search stays blocked")
	}

	svc.EnqueueMovie(m.ID)
	if n := svc.ProcessQueue(context.Background(), 5); n != 1 {
		t.Fatalf("grabbed %d after a failure, want 1 — the next best release should be taken", n)
	}
}

// The interactive search answers with what it did, so a click can say
// "sent to SABnzbd — <release>" instead of promising a search that may
// have been silently skipped. Both kinds report the same way.
func TestSearchNowReportsItsOutcome(t *testing.T) {
	svc, idx, dl := rssService(t)
	m := seedMovie(t, svc.Catalog, t.TempDir())
	idx.releases = []prowlarr.Release{
		{Title: "Inception.2010.1080p.WEB-DL", Size: gb(5), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/1", Indexer: "nzbs"},
	}

	out := svc.SearchNow(context.Background(), m.ID, 0, 0, 0)
	if out.Grabbed != "Inception.2010.1080p.WEB-DL" {
		t.Fatalf("grabbed %q, reason %q", out.Grabbed, out.Reason)
	}
	if dl.calls != 1 {
		t.Fatalf("downloader reached %d times, want 1", dl.calls)
	}

	// searching again finds the grab in flight and says so, rather than
	// reporting a search that quietly did nothing
	out = svc.SearchNow(context.Background(), m.ID, 0, 0, 0)
	if out.Grabbed != "" || out.Reason == "" {
		t.Fatalf("second search: grabbed %q, reason %q", out.Grabbed, out.Reason)
	}

	// an unmonitored title explains itself too
	if err := svc.Catalog.SetMovieMonitored(m.ID, false); err != nil {
		t.Fatal(err)
	}
	if out = svc.SearchNow(context.Background(), m.ID, 0, 0, 0); out.Reason == "" {
		t.Fatal("unmonitored movie gave no reason")
	}
}

// Blocklisted-only results are the case manual search cannot show: it
// ignores the blocklist entirely, so the releases look grabbable there
// while automatic search refuses them. The reason has to say so.
func TestSearchNowNamesTheBlocklistAsTheBlocker(t *testing.T) {
	svc, idx, _ := rssService(t)
	m := seedMovie(t, svc.Catalog, t.TempDir())
	idx.releases = []prowlarr.Release{
		{Title: "Inception.2010.1080p.WEB-DL", Size: gb(5), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/1", Indexer: "nzbs"},
	}
	if err := svc.Catalog.AddBlocklist("Inception.2010.1080p.WEB-DL", "nzbs", m.ID, 0, "crc"); err != nil {
		t.Fatal(err)
	}
	out := svc.SearchNow(context.Background(), m.ID, 0, 0, 0)
	if out.Grabbed != "" {
		t.Fatalf("grabbed a blocklisted release: %q", out.Grabbed)
	}
	if !strings.Contains(out.Reason, "blocklist") {
		t.Fatalf("reason %q does not point at the blocklist", out.Reason)
	}
}
