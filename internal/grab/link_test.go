package grab

import (
	"context"
	"os"
	"testing"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/download"
	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/prowlarr"
)

// seedSecondMovieLib puts the SAME movie (by TMDB id) into a second
// library — the per-user-collections shape.
func seedSecondMovieLib(t *testing.T, cat *catalog.Store) (libPath string, movieID int64) {
	t.Helper()
	libPath = t.TempDir()
	lib, err := cat.CreateLibrary("Guest Movies", libPath, "movies")
	if err != nil {
		t.Fatal(err)
	}
	movieID, err = cat.UpsertMovie(&metadata.MovieDetail{
		TmdbID: 27205, Title: "Inception", Year: 2010, Runtime: 148,
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	return libPath, movieID
}

func sameFile(t *testing.T, a, b string) bool {
	t.Helper()
	ia, err := os.Stat(a)
	if err != nil {
		t.Fatalf("stat %s: %v", a, err)
	}
	ib, err := os.Stat(b)
	if err != nil {
		t.Fatalf("stat %s: %v", b, err)
	}
	return os.SameFile(ia, ib)
}

func TestImportFansOutAcrossLibraries(t *testing.T) {
	svc, _, cat := testService(t)
	dl := &stubDownloader{history: map[string][]download.HistoryItem{}}
	svc.Usenet = dl
	primary := seedMovie(t, cat, t.TempDir()) // library "Movies"
	_, guestID := seedSecondMovieLib(t, cat)  // same movie, library "Guest Movies"

	job := jobDir(t, "Inception.2010.1080p.BluRay.x264-SPARKS", map[string]int{
		"Inception.2010.1080p.BluRay.x264-SPARKS.mkv": 4096,
	})
	dl.history["movies"] = []download.HistoryItem{{
		ID: "nzo_m1", Name: "Inception.2010.1080p.BluRay.x264-SPARKS",
		Status: "Completed", Category: "movies", Storage: job,
	}}
	if n := svc.ImportCompleted(context.Background()); n != 1 {
		t.Fatalf("imported = %d", n)
	}

	a, err := cat.GetMovie(primary.ID)
	if err != nil || a.FilePath == "" {
		t.Fatalf("primary not attached: %+v (%v)", a, err)
	}
	b, err := cat.GetMovie(guestID)
	if err != nil || b.FilePath == "" {
		t.Fatalf("sibling not attached: %+v (%v)", b, err)
	}
	if a.FilePath == b.FilePath {
		t.Fatalf("sibling shares the primary's path instead of its own library slot: %s", a.FilePath)
	}
	if !sameFile(t, a.FilePath, b.FilePath) {
		t.Fatal("sibling file is not a hardlink of the primary")
	}
}

func TestAddTimeLinkFromSiblings(t *testing.T) {
	svc, _, cat := testService(t)
	svc.Usenet = &stubDownloader{}
	primary := seedMovie(t, cat, t.TempDir())

	// the primary has a real file on disk
	src := jobDir(t, "library", map[string]int{"Inception.2010.1080p.mkv": 2048}) + "/Inception.2010.1080p.mkv"
	if err := cat.AttachMovieFile(primary.ID, src, 2048, "1080p", ""); err != nil {
		t.Fatal(err)
	}

	guestPath, guestID := seedSecondMovieLib(t, cat)
	if !svc.LinkMovieFromSiblings(guestID) {
		t.Fatal("sibling file not linked on add")
	}
	g, err := cat.GetMovie(guestID)
	if err != nil || g.FilePath == "" {
		t.Fatalf("guest row not attached: %+v (%v)", g, err)
	}
	if !sameFile(t, src, g.FilePath) {
		t.Fatal("guest file is not a hardlink of the library file")
	}
	if len(g.FilePath) <= len(guestPath) || g.FilePath[:len(guestPath)] != guestPath {
		t.Fatalf("guest file %q not under its own library %q", g.FilePath, guestPath)
	}
	// nothing to link twice
	if svc.LinkMovieFromSiblings(guestID) {
		t.Fatal("re-link reported a change")
	}
}

func TestGrabsDedupeAcrossSiblingLibraries(t *testing.T) {
	svc, idx, dl := rssService(t)
	seedMovie(t, svc.Catalog, t.TempDir())
	seedSecondMovieLib(t, svc.Catalog)

	idx.releases = []prowlarr.Release{
		{Title: "Inception.2010.1080p.WEB-DL", Size: gb(5), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/1", Indexer: "nzbs"},
	}
	// both rows want it, but one download will satisfy both via hardlinks —
	// the pending check spans siblings, so exactly one grab goes out
	if n := svc.RSSSync(context.Background()); n != 1 {
		t.Fatalf("rss grabbed %d, want 1", n)
	}
	if dl.calls != 1 {
		t.Fatalf("downloader called %d times, want 1", dl.calls)
	}
}

func TestShowImportFansOutAcrossLibraries(t *testing.T) {
	svc, _, cat := testService(t)
	dl := &stubDownloader{history: map[string][]download.HistoryItem{}}
	svc.Usenet = dl
	primary := seedShow(t, cat, t.TempDir())

	guestLib, err := cat.CreateLibrary("Guest TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	guestID, err := cat.UpsertShow(&metadata.ShowDetail{
		TmdbID: 1396, Title: "Breaking Bad", Year: 2008,
		Seasons: []metadata.SeasonDetail{{Number: 1, Name: "Season 1", Episodes: []metadata.EpisodeDetail{
			{TmdbID: 1, Season: 1, Episode: 1, Title: "Pilot", Runtime: 58},
			{TmdbID: 2, Season: 1, Episode: 2, Title: "Cat's in the Bag...", Runtime: 48},
		}}},
	}, guestLib.ID)
	if err != nil {
		t.Fatal(err)
	}

	job := jobDir(t, "Breaking.Bad.S01E02.1080p.WEB-DL", map[string]int{
		"Breaking.Bad.S01E02.1080p.WEB-DL.mkv": 2048,
	})
	dl.history["tvshows"] = []download.HistoryItem{{
		ID: "nzo_e2", Name: "Breaking.Bad.S01E02.1080p.WEB-DL",
		Status: "Completed", Category: "tvshows", Storage: job,
	}}
	if n := svc.ImportCompleted(context.Background()); n != 1 {
		t.Fatalf("imported = %d", n)
	}

	a, err := cat.GetShow(primary.ID)
	if err != nil {
		t.Fatal(err)
	}
	b, err := cat.GetShow(guestID)
	if err != nil {
		t.Fatal(err)
	}
	epA := findEpisode(a, 1, 2)
	epB := findEpisode(b, 1, 2)
	if epA == nil || epA.FilePath == "" || epB == nil || epB.FilePath == "" {
		t.Fatalf("episodes not attached: %+v / %+v", epA, epB)
	}
	if !sameFile(t, epA.FilePath, epB.FilePath) {
		t.Fatal("sibling episode is not a hardlink")
	}
}
