package grab

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getreely/reely/internal/download"
	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/parser"
	"github.com/getreely/reely/internal/quality"
)

func writeFile(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func gone(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("%s still exists (err=%v)", path, err)
	}
}

func present(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("%s missing: %v", path, err)
	}
}

// An upgrade import replaces the primary's file, lifts every lower-quality
// sibling copy, deletes the replaced files, and leaves a better sibling
// alone.
func TestImportReplacesUpgradedMovieEverywhere(t *testing.T) {
	svc, _, cat := testService(t)
	libA := t.TempDir()
	m := seedMovie(t, cat, libA)
	oldA := writeFile(t, filepath.Join(libA, "Inception (2010)", "Inception (2010) [720p].mkv"), "old-a")
	if err := cat.AttachMovieFile(m.ID, oldA, 5, "720p", ""); err != nil {
		t.Fatal(err)
	}

	libB := t.TempDir()
	lib2, err := cat.CreateLibrary("Tommy", libB, "movies")
	if err != nil {
		t.Fatal(err)
	}
	idB, err := cat.UpsertMovie(&metadata.MovieDetail{TmdbID: 27205, Title: "Inception", Year: 2010, Runtime: 148}, lib2.ID)
	if err != nil {
		t.Fatal(err)
	}
	oldB := writeFile(t, filepath.Join(libB, "Inception (2010)", "Inception (2010) [720p].mkv"), "old-b")
	if err := cat.AttachMovieFile(idB, oldB, 5, "720p", ""); err != nil {
		t.Fatal(err)
	}

	libC := t.TempDir()
	lib3, err := cat.CreateLibrary("Sam", libC, "movies")
	if err != nil {
		t.Fatal(err)
	}
	idC, err := cat.UpsertMovie(&metadata.MovieDetail{TmdbID: 27205, Title: "Inception", Year: 2010, Runtime: 148}, lib3.ID)
	if err != nil {
		t.Fatal(err)
	}
	bestC := writeFile(t, filepath.Join(libC, "Inception (2010)", "Inception (2010) [2160p].mkv"), "best-c")
	if err := cat.AttachMovieFile(idC, bestC, 5, "2160p", ""); err != nil {
		t.Fatal(err)
	}

	src := writeFile(t, filepath.Join(t.TempDir(), "Inception.2010.1080p.BluRay.mkv"), "new")
	mm, err := cat.GetMovie(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := svc.placeMovieFile(&mm.Movie, src, "1080p", "bluray", "Inception.2010.1080p.BluRay", download.Usenet); err != nil {
		t.Fatal(err)
	}

	a, _ := cat.GetMovie(m.ID)
	if a.Quality != "1080p" || a.FilePath == oldA {
		t.Fatalf("primary after upgrade: %q %q", a.Quality, a.FilePath)
	}
	present(t, a.FilePath)
	gone(t, oldA)

	b, _ := cat.GetMovie(idB)
	if b.Quality != "1080p" || b.FilePath == oldB {
		t.Fatalf("sibling not upgraded: %q %q", b.Quality, b.FilePath)
	}
	present(t, b.FilePath)
	gone(t, oldB)

	c, _ := cat.GetMovie(idC)
	if c.Quality != "2160p" || c.FilePath != bestC {
		t.Fatalf("better sibling touched: %q %q", c.Quality, c.FilePath)
	}
	present(t, bestC)

	entries, err := cat.ListHistory(10)
	if err != nil {
		t.Fatal(err)
	}
	upgraded := false
	for _, e := range entries {
		var detail map[string]any
		if json.Unmarshal([]byte(e.Detail), &detail) == nil && detail["upgradedFrom"] == "720p" {
			upgraded = true
		}
	}
	if !upgraded {
		t.Fatalf("no history entry records the upgrade: %+v", entries)
	}
}

// A multi-episode file backs several rows; replacing one episode's copy
// must not delete the shared file while its neighbor still points at it.
func TestSpanImportKeepsSharedFileUntilLastReference(t *testing.T) {
	svc, _, cat := testService(t)
	libPath := t.TempDir()
	sh := seedShow(t, cat, libPath)
	old := writeFile(t, filepath.Join(libPath, "double.mkv"), "old")
	id1, err := cat.EpisodeID(sh.ID, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	id2, err := cat.EpisodeID(sh.ID, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.AttachEpisodeFile(id1, old, 3, "720p", ""); err != nil {
		t.Fatal(err)
	}
	if err := cat.AttachEpisodeFile(id2, old, 3, "720p", ""); err != nil {
		t.Fatal(err)
	}

	upgrade := func(name string) {
		t.Helper()
		fresh, err := cat.GetShow(sh.ID)
		if err != nil {
			t.Fatal(err)
		}
		src := writeFile(t, filepath.Join(t.TempDir(), name+".mkv"), "new-"+name)
		if err := svc.placeShowFile(fresh, src, parser.Parse(name), "1080p", "webdl", name, download.Usenet); err != nil {
			t.Fatal(err)
		}
	}

	upgrade("Breaking.Bad.S01E01.1080p.WEB")
	present(t, old) // E02 still lives on the double file

	upgrade("Breaking.Bad.S01E02.1080p.WEB")
	gone(t, old) // last reference replaced — the orphan goes

	fresh, _ := cat.GetShow(sh.ID)
	for _, n := range []int{1, 2} {
		ep := findEpisode(fresh, 1, n)
		if ep.Quality != "1080p" || ep.FilePath == old {
			t.Fatalf("E%02d after upgrade: %q %q", n, ep.Quality, ep.FilePath)
		}
		present(t, ep.FilePath)
	}
}

// A span import never stomps an episode that already holds a strictly
// better file; when nobody in the span wants it, the file is dropped.
func TestSpanImportNeverStompsABetterFile(t *testing.T) {
	svc, _, cat := testService(t)
	libPath := t.TempDir()
	sh := seedShow(t, cat, libPath)
	best := writeFile(t, filepath.Join(libPath, "e3-remux.mkv"), "best")
	id3, err := cat.EpisodeID(sh.ID, 1, 3)
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.AttachEpisodeFile(id3, best, 9, "2160p", ""); err != nil {
		t.Fatal(err)
	}

	fresh, err := cat.GetShow(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	src := writeFile(t, filepath.Join(t.TempDir(), "Breaking.Bad.S01E03.720p.WEB.mkv"), "worse")
	if err := svc.placeShowFile(fresh, src, parser.Parse("Breaking.Bad.S01E03.720p.WEB"), "720p", "webrip", "Breaking.Bad.S01E03.720p.WEB", download.Usenet); err != nil {
		t.Fatal(err)
	}

	after, _ := cat.GetShow(sh.ID)
	ep := findEpisode(after, 1, 3)
	if ep.Quality != "2160p" || ep.FilePath != best {
		t.Fatalf("better file stomped: %q %q", ep.Quality, ep.FilePath)
	}
	// the unwanted download was dropped, not shelved: the library holds
	// exactly the one file it started with
	count := 0
	if err := filepath.WalkDir(libPath, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			count++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("library holds %d files, want 1", count)
	}
}

// The wanted sweep hunts cutoff-unmet titles: below-cutoff files queue a
// search (one per TMDB id across sibling collections), while files at the
// cutoff, of unknown quality, or under a no-upgrades profile stay quiet.
func TestWantedPassSweepsCutoffUnmet(t *testing.T) {
	svc, _, cat := testService(t)
	svc.Usenet = &stubDownloader{}

	lib, err := cat.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	lib2, err := cat.CreateLibrary("Tommy", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	add := func(tmdb int, title string, libID int64, qual string) int64 {
		t.Helper()
		id, err := cat.UpsertMovie(&metadata.MovieDetail{TmdbID: tmdb, Title: title, Year: 2010, Runtime: 100}, libID)
		if err != nil {
			t.Fatal(err)
		}
		if qual != "-" {
			path := filepath.Join("/x", title+"-"+strings.ReplaceAll(qual, "-", "none")+".mkv")
			if err := cat.AttachMovieFile(id, path, 1, qual, ""); err != nil {
				t.Fatal(err)
			}
		}
		return id
	}

	below := add(1, "Below", lib.ID, "720p")
	sibling := add(1, "Below", lib2.ID, "720p") // same title — one search, not two
	add(2, "AtCutoff", lib.ID, "1080p")
	add(3, "Unknown", lib.ID, "")
	frozen := add(4, "Frozen", lib.ID, "480p")
	noUp, err := cat.CreateProfile(&quality.Profile{
		Name: "frozen", Qualities: []string{"480p", "1080p"}, Cutoff: "1080p",
		Upgrades: false, HDR: "allow",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.SetMovieProfile(frozen, noUp.ID); err != nil {
		t.Fatal(err)
	}
	add(5, "Missing", lib.ID, "-")

	// the missing movie + one upgrade search for the below-cutoff pair
	if n := svc.WantedPass(); n != 2 {
		t.Fatalf("swept = %d, want 2", n)
	}
	if got := svc.QueueLen(); got != 2 {
		t.Fatalf("queue holds %d, want 2", got)
	}

	// unmonitoring both sibling rows silences the upgrade search
	svc.queue, svc.queued = nil, nil
	if err := cat.SetMovieMonitored(below, false); err != nil {
		t.Fatal(err)
	}
	if err := cat.SetMovieMonitored(sibling, false); err != nil {
		t.Fatal(err)
	}
	if n := svc.WantedPass(); n != 1 {
		t.Fatalf("after unmonitoring, swept = %d, want 1", n)
	}
}

// Episodes below their show's cutoff join the sweep; at the cutoff they
// drop out.
func TestUpgradeTargetsCoverEpisodes(t *testing.T) {
	svc, _, cat := testService(t)
	sh := seedShow(t, cat, t.TempDir())
	epID, err := cat.EpisodeID(sh.ID, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.AttachEpisodeFile(epID, "/x/bb1.mkv", 1, "720p", ""); err != nil {
		t.Fatal(err)
	}
	targets := svc.upgradeTargets()
	if len(targets) != 1 || targets[0].showID != sh.ID || targets[0].season != 1 || targets[0].episode != 1 {
		t.Fatalf("targets = %+v", targets)
	}
	if err := cat.AttachEpisodeFile(epID, "/x/bb1.mkv", 1, "1080p", ""); err != nil {
		t.Fatal(err)
	}
	if targets := svc.upgradeTargets(); len(targets) != 0 {
		t.Fatalf("cutoff met but targets = %+v", targets)
	}
}

// With a source cutoff, a file at the cutoff resolution keeps hunting until
// its source measures up; without one, the resolution alone satisfies.
func TestUpgradeTargetsHonorSourceCutoff(t *testing.T) {
	svc, _, cat := testService(t)

	lib, err := cat.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	picky, err := cat.CreateProfile(&quality.Profile{
		Name: "bluray or bust", Qualities: []string{"720p", "1080p"}, Cutoff: "1080p",
		SourceCutoff: "bluray", Upgrades: true, HDR: "allow",
	})
	if err != nil {
		t.Fatal(err)
	}
	add := func(tmdb int, title, qual, src string) int64 {
		t.Helper()
		id, err := cat.UpsertMovie(&metadata.MovieDetail{TmdbID: tmdb, Title: title, Year: 2010, Runtime: 100}, lib.ID)
		if err != nil {
			t.Fatal(err)
		}
		if err := cat.AttachMovieFile(id, "/x/"+title+".mkv", 1, qual, src); err != nil {
			t.Fatal(err)
		}
		if err := cat.SetMovieProfile(id, picky.ID); err != nil {
			t.Fatal(err)
		}
		return id
	}

	webID := add(1, "WebCopy", "1080p", "webdl") // below the source cutoff — hunts
	add(2, "BlurayCopy", "1080p", "bluray")      // meets it — rests
	add(3, "RemuxCopy", "1080p", "remux")        // above it — rests
	add(4, "Unlabeled", "1080p", "")             // unknown source — hunts
	add(5, "LowRes", "720p", "bluray")           // below the resolution cutoff — hunts

	targets := svc.upgradeTargets()
	want := map[int64]bool{}
	for _, tgt := range targets {
		want[tgt.movieID] = true
	}
	if len(targets) != 3 {
		t.Fatalf("targets = %d (%v), want 3", len(targets), want)
	}
	if !want[webID] {
		t.Error("web 1080p under a bluray cutoff was not targeted")
	}
}
