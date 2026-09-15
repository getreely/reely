package grab

import (
	"context"
	"testing"

	"github.com/getreely/reely/internal/prowlarr"
)

func rssService(t *testing.T) (*Service, *stubIndexer, *stubDownloader) {
	t.Helper()
	svc, idx, _ := testService(t)
	dl := &stubDownloader{}
	svc.Usenet = dl
	return svc, idx, dl
}

func TestRSSSyncGrabsMonitoredMissingMovie(t *testing.T) {
	svc, idx, dl := rssService(t)
	seedMovie(t, svc.Catalog, t.TempDir()) // Inception 2010, monitored, no file

	idx.releases = []prowlarr.Release{
		{Title: "Inception.2010.1080p.WEB-DL", Size: gb(5), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/1", Indexer: "nzbs"},
		{Title: "Inception.2010.1080p.BluRay.x264", Size: gb(9), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/2", Indexer: "nzbs"},
		{Title: "Tenet.2020.1080p.WEB-DL", Size: gb(5), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/3", Indexer: "nzbs"}, // not in catalog
	}
	if n := svc.RSSSync(context.Background()); n != 1 {
		t.Fatalf("grabbed = %d, want 1", n)
	}
	// of the two matches the better source won
	if dl.lastURL != "https://idx/nzb/2" {
		t.Fatalf("grabbed %q, want the bluray", dl.lastURL)
	}

	// second sweep: the grab is pending — nothing re-grabs
	if n := svc.RSSSync(context.Background()); n != 0 {
		t.Fatalf("re-grabbed while pending: %d", n)
	}
	if dl.calls != 1 {
		t.Fatalf("downloader called %d times", dl.calls)
	}
}

func TestRSSSyncSkipsUnmonitoredAndSatisfied(t *testing.T) {
	svc, idx, dl := rssService(t)
	cat := svc.Catalog
	m := seedMovie(t, cat, t.TempDir())

	// satisfied at cutoff: 1080p on disk
	if err := cat.AttachMovieFile(m.ID, "/x/inception.mkv", 1, "1080p", ""); err != nil {
		t.Fatal(err)
	}
	idx.releases = []prowlarr.Release{
		{Title: "Inception.2010.1080p.WEB-DL.PROPER", Size: gb(5), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/1"},
	}
	if n := svc.RSSSync(context.Background()); n != 0 {
		t.Fatal("grabbed for a movie already at cutoff")
	}

	// unmonitored: even a missing movie stays untouched
	if err := cat.SetMovieMonitored(m.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := cat.AttachMovieFile(m.ID, "", 0, "", ""); err != nil {
		t.Fatal(err)
	}
	if n := svc.RSSSync(context.Background()); n != 0 {
		t.Fatal("grabbed for an unmonitored movie")
	}
	if dl.calls != 0 {
		t.Fatalf("downloader reached %d times", dl.calls)
	}
}

func TestRSSSyncUpgradesBelowCutoff(t *testing.T) {
	svc, idx, dl := rssService(t)
	m := seedMovie(t, svc.Catalog, t.TempDir())
	if err := svc.Catalog.AttachMovieFile(m.ID, "/x/inception.mkv", 1, "720p", ""); err != nil {
		t.Fatal(err)
	}
	idx.releases = []prowlarr.Release{
		{Title: "Inception.2010.1080p.WEB-DL", Size: gb(5), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/up"},
	}
	if n := svc.RSSSync(context.Background()); n != 1 {
		t.Fatalf("upgrade not grabbed: %d", n)
	}
	if dl.lastURL != "https://idx/nzb/up" {
		t.Fatalf("grabbed %q", dl.lastURL)
	}
}

func TestRSSSyncGrabsWantedEpisode(t *testing.T) {
	svc, idx, dl := rssService(t)
	sh := seedShow(t, svc.Catalog, t.TempDir()) // Breaking Bad S01E01-03, all missing

	idx.releases = []prowlarr.Release{
		{Title: "Breaking.Bad.S01E02.1080p.WEB-DL", Size: gb(2), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/e2", Indexer: "nzbs"},
		{Title: "Better.Call.Saul.S01E01.1080p.WEB-DL", Size: gb(2), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/x"}, // unknown show
		{Title: "Breaking.Bad.S09E01.1080p.WEB-DL", Size: gb(2), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/y"}, // no such episode
	}
	if n := svc.RSSSync(context.Background()); n != 1 {
		t.Fatalf("grabbed = %d, want 1", n)
	}
	if dl.lastURL != "https://idx/nzb/e2" || dl.lastCat != "tvshows" {
		t.Fatalf("grabbed %q into %q", dl.lastURL, dl.lastCat)
	}
	entries, _ := svc.Catalog.ListHistory(10)
	if len(entries) != 1 || entries[0].ShowID != sh.ID || entries[0].EpisodeID == 0 {
		t.Fatalf("history = %+v", entries)
	}

	// pending guard holds per episode
	if n := svc.RSSSync(context.Background()); n != 0 {
		t.Fatal("re-grabbed a pending episode")
	}

	// an unmonitored episode is not wanted, even via a spanning release
	if err := svc.Catalog.SetSeasonMonitored(sh.ID, 1, false); err != nil {
		t.Fatal(err)
	}
	idx.releases = []prowlarr.Release{
		{Title: "Breaking.Bad.S01E03.1080p.WEB-DL", Size: gb(2), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/e3"},
	}
	if n := svc.RSSSync(context.Background()); n != 0 {
		t.Fatal("grabbed for an unmonitored episode")
	}
}

func TestRSSIntervalFloorsAndDefaults(t *testing.T) {
	svc, _, _ := rssService(t)
	if got := svc.RSSInterval().Seconds(); got != 900 {
		t.Fatalf("default interval = %v", got)
	}
	svc.Settings = settingsMap{"rss_poll_seconds": "60"}
	if got := svc.RSSInterval().Seconds(); got != 300 {
		t.Fatalf("floored interval = %v", got)
	}
	svc.Settings = settingsMap{"rss_poll_seconds": "1800"}
	if got := svc.RSSInterval().Seconds(); got != 1800 {
		t.Fatalf("custom interval = %v", got)
	}
}

type settingsMap map[string]string

func (m settingsMap) Get(key string) string { return m[key] }
