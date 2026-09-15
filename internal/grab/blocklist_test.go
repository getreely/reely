package grab

import (
	"context"
	"testing"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/download"
	"github.com/getreely/reely/internal/prowlarr"
)

func TestBlocklistedReleaseIsSkippedByAutomaticPaths(t *testing.T) {
	svc, idx, dl := rssService(t)
	m := seedMovie(t, svc.Catalog, t.TempDir())

	idx.releases = []prowlarr.Release{
		{Title: "Inception.2010.1080p.BluRay.x264", Size: gb(9), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/1", Indexer: "nzbs"},
	}
	if err := svc.Catalog.AddBlocklist("Inception.2010.1080p.BluRay.x264", "nzbs", m.ID, 0, "crc errors"); err != nil {
		t.Fatal(err)
	}

	// the RSS sync refuses it
	if n := svc.RSSSync(context.Background()); n != 0 {
		t.Fatal("rss grabbed a blocklisted release")
	}
	// the active search queue refuses it too
	svc.EnqueueMovie(m.ID)
	if n := svc.ProcessQueue(context.Background(), 5); n != 0 {
		t.Fatal("search grabbed a blocklisted release")
	}
	if dl.calls != 0 {
		t.Fatalf("downloader reached %d times", dl.calls)
	}

	// the pardon: delete the entry and the same release grabs again
	entries, err := svc.Catalog.ListBlocklist()
	if err != nil || len(entries) != 1 {
		t.Fatalf("blocklist = %v (%v)", entries, err)
	}
	if entries[0].ForTitle != "Inception" {
		t.Fatalf("entry context = %+v", entries[0])
	}
	if _, err := svc.Catalog.RemoveBlocklist([]int64{entries[0].ID}); err != nil {
		t.Fatal(err)
	}
	if n := svc.RSSSync(context.Background()); n != 1 {
		t.Fatal("pardoned release not grabbed")
	}
}

func TestFailedDownloadLandsOnBlocklist(t *testing.T) {
	svc, _, _ := testService(t)
	dl := &stubDownloader{history: map[string][]download.HistoryItem{}}
	svc.Usenet = dl
	m := seedMovie(t, svc.Catalog, t.TempDir())

	dl.history["movies"] = []download.HistoryItem{{
		ID: "nzo_bad", Name: "Inception.2010.1080p.WEB-DL.x264",
		Status: "Failed", Category: "movies", FailMessage: "unpack failed: CRC error",
	}}
	if n := svc.ImportCompleted(context.Background()); n != 0 {
		t.Fatalf("imported %d from a failed job", n)
	}
	entries, err := svc.Catalog.ListBlocklist()
	if err != nil || len(entries) != 1 {
		t.Fatalf("blocklist = %v (%v)", entries, err)
	}
	e := entries[0]
	if e.ReleaseTitle != "Inception.2010.1080p.WEB-DL.x264" || e.MovieID != m.ID ||
		e.Reason != "unpack failed: CRC error" {
		t.Fatalf("entry = %+v", e)
	}
	// the failed job is cleared from SAB so it doesn't come around again
	if len(dl.deleted) != 1 || dl.deleted[0] != "nzo_bad" {
		t.Fatalf("SAB history not cleared: %v", dl.deleted)
	}
}

// Cancelling a download with "and blocklist" bans exactly what the grab
// record says was fetched — release title, indexer, and target title.
func TestBlocklistJobUsesTheGrabRecord(t *testing.T) {
	svc, _, _ := testService(t)
	m := seedMovie(t, svc.Catalog, t.TempDir())
	if err := svc.Catalog.AddHistory("grabbed", m.ID, 0, 0,
		`{"title":"Inception.2010.1080p.WEB-DL.x264","indexer":"nzbs","nzoId":"nzo_1"}`); err != nil {
		t.Fatal(err)
	}

	svc.BlocklistJob("nzo_1", "SAB job display name", "cancelled and blocked")

	entries, err := svc.Catalog.ListBlocklist()
	if err != nil || len(entries) != 1 {
		t.Fatalf("blocklist = %v (%v)", entries, err)
	}
	e := entries[0]
	if e.ReleaseTitle != "Inception.2010.1080p.WEB-DL.x264" || e.Indexer != "nzbs" ||
		e.MovieID != m.ID || e.Reason != "cancelled and blocked" {
		t.Fatalf("entry = %+v", e)
	}
}

// A job reely never grabbed still gets banned: the SAB job name stands in
// for the release, and parsing it finds the title it was for.
func TestBlocklistJobFallsBackToTheJobName(t *testing.T) {
	svc, _, _ := testService(t)
	m := seedMovie(t, svc.Catalog, t.TempDir())

	svc.BlocklistJob("nzo_unknown", "Inception.2010.720p.WEB.x264", "cancelled and blocked")

	entries, err := svc.Catalog.ListBlocklist()
	if err != nil || len(entries) != 1 {
		t.Fatalf("blocklist = %v (%v)", entries, err)
	}
	e := entries[0]
	if e.ReleaseTitle != "Inception.2010.720p.WEB.x264" || e.MovieID != m.ID || e.Indexer != "" {
		t.Fatalf("entry = %+v", e)
	}
}

// A failed grab re-searches its target right away: the failed release is
// blocklisted, so the queued search lands on the next best one. Jobs reely
// never grabbed stay out of the queue.
func TestFailedGrabRetriesItsTarget(t *testing.T) {
	svc, _, _ := testService(t)
	dl := &stubDownloader{history: map[string][]download.HistoryItem{}}
	svc.Usenet = dl
	m := seedMovie(t, svc.Catalog, t.TempDir())

	// the grab record ties the job to the movie, like a real grab would
	if err := svc.Catalog.AddHistory("grabbed", m.ID, 0, 0, `{"title":"Inception.2010.1080p.WEB-DL.x264","nzoId":"nzo_bad"}`); err != nil {
		t.Fatal(err)
	}
	dl.history["movies"] = []download.HistoryItem{{
		ID: "nzo_bad", Name: "Inception.2010.1080p.WEB-DL.x264",
		Status: "Failed", Category: "movies", FailMessage: "unpack failed",
	}}
	if n := svc.ImportCompleted(context.Background()); n != 0 {
		t.Fatalf("imported %d from a failed job", n)
	}
	if got := svc.QueueLen(); got != 1 {
		t.Fatalf("re-search queue holds %d, want 1", got)
	}

	// a failed job with no grab record (queued by hand) is left to the sweep
	svc.queue, svc.queued = nil, nil
	dl.history["movies"] = []download.HistoryItem{{
		ID: "nzo_hand", Name: "Some.Other.Thing.2020.1080p.WEB",
		Status: "Failed", Category: "movies", FailMessage: "unpack failed",
	}}
	svc.ImportCompleted(context.Background())
	if got := svc.QueueLen(); got != 0 {
		t.Fatalf("hand-queued failure enqueued %d searches, want 0", got)
	}
}

// The retry has to actually reach SAB, not merely reach the queue. The
// grab that failed is the title's newest event, so unless the failure
// clears it the queued search is dropped by the pending-grab gate and the
// title silently stops being hunted — while manual search still works.
func TestFailedGrabRetryReachesTheDownloader(t *testing.T) {
	svc, idx, _ := testService(t)
	dl := &stubDownloader{history: map[string][]download.HistoryItem{}}
	svc.Usenet = dl
	m := seedMovie(t, svc.Catalog, t.TempDir())
	// a healthy alternative is available for the retry to land on
	idx.releases = []prowlarr.Release{
		{Title: "Inception.2010.1080p.WEB-DL.GOOD", Size: gb(5), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/good", Indexer: "nzbs"},
	}

	if err := svc.Catalog.AddHistory("grabbed", m.ID, 0, 0,
		`{"title":"Inception.2010.1080p.WEB-DL.BAD","nzoId":"nzo_bad"}`); err != nil {
		t.Fatal(err)
	}
	dl.history["movies"] = []download.HistoryItem{{
		ID: "nzo_bad", Name: "Inception.2010.1080p.WEB-DL.BAD",
		Status: "Failed", Category: "movies", FailMessage: "too many missing articles",
	}}
	svc.ImportCompleted(context.Background())

	// the failure must be filed against the movie, or it cannot clear the
	// dead grab that is blocking the retry
	entries, err := svc.Catalog.ListHistory(10)
	if err != nil {
		t.Fatal(err)
	}
	var failed *catalog.HistoryEntry
	for i := range entries {
		if entries[i].Kind == "failed" {
			failed = &entries[i]
			break
		}
	}
	if failed == nil {
		t.Fatal("no failure recorded")
	}
	if failed.MovieID != m.ID {
		t.Fatalf("failure filed against movie %d, want %d — an unattributed failure clears nothing",
			failed.MovieID, m.ID)
	}

	if n := svc.ProcessQueue(context.Background(), 5); n != 1 {
		t.Fatalf("retry grabbed %d releases, want 1", n)
	}
	if dl.calls != 1 {
		t.Fatalf("downloader reached %d times, want 1 — the retry never made it to SAB", dl.calls)
	}
}

// Indexers carry each other's posts under identical names. Banning a
// release at the indexer whose copy failed must leave another indexer's
// copy of the same name grabbable — otherwise one bad upload burns the
// release everywhere.
func TestBlocklistIsScopedToItsIndexer(t *testing.T) {
	svc, idx, dl := rssService(t)
	m := seedMovie(t, svc.Catalog, t.TempDir())
	const name = "Inception.2010.1080p.BluRay.x264"
	idx.releases = []prowlarr.Release{
		{Title: name, Size: gb(9), Protocol: "usenet", DownloadURL: "https://a/nzb", Indexer: "badidx"},
		{Title: name, Size: gb(9), Protocol: "usenet", DownloadURL: "https://b/nzb", Indexer: "goodidx"},
	}
	if err := svc.Catalog.AddBlocklist(name, "badidx", m.ID, 0, "crc errors"); err != nil {
		t.Fatal(err)
	}

	svc.EnqueueMovie(m.ID)
	if n := svc.ProcessQueue(context.Background(), 5); n != 1 {
		t.Fatalf("grabbed %d, want 1 — the other indexer's copy is not banned", n)
	}
	if dl.calls != 1 {
		t.Fatalf("downloader reached %d times, want 1", dl.calls)
	}
	if dl.lastURL != "https://b/nzb" {
		t.Fatalf("grabbed from %q, want the unbanned indexer's copy", dl.lastURL)
	}
}

// A ban with no indexer recorded — every row that existed before bans were
// scoped — still blocks the name everywhere. That is the safe reading when
// the ban's origin is unknown.
func TestLegacyBlocklistRowBlocksEveryIndexer(t *testing.T) {
	svc, idx, dl := rssService(t)
	m := seedMovie(t, svc.Catalog, t.TempDir())
	const name = "Inception.2010.1080p.BluRay.x264"
	idx.releases = []prowlarr.Release{
		{Title: name, Size: gb(9), Protocol: "usenet", DownloadURL: "https://a/nzb", Indexer: "one"},
		{Title: name, Size: gb(9), Protocol: "usenet", DownloadURL: "https://b/nzb", Indexer: "two"},
	}
	if err := svc.Catalog.AddBlocklist(name, "", m.ID, 0, "banned before indexers were recorded"); err != nil {
		t.Fatal(err)
	}
	svc.EnqueueMovie(m.ID)
	if n := svc.ProcessQueue(context.Background(), 5); n != 0 {
		t.Fatalf("grabbed %d despite an unscoped ban, want 0", n)
	}
	if dl.calls != 0 {
		t.Fatalf("downloader reached %d times, want 0", dl.calls)
	}
}
