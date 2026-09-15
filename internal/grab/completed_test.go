package grab

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/getreely/reely/internal/download"
)

// jobDir builds a fake completed-download folder with the given files.
func jobDir(t *testing.T, name string, files map[string]int) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	for rel, size := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, make([]byte, size), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestImportCompletedMovie(t *testing.T) {
	svc, _, cat := testService(t)
	dl := &stubDownloader{history: map[string][]download.HistoryItem{}}
	svc.Usenet = dl
	libDir := t.TempDir()
	m := seedMovie(t, cat, libDir)

	job := jobDir(t, "Inception.2010.1080p.BluRay.x264-SPARKS", map[string]int{
		"Inception.2010.1080p.BluRay.x264-SPARKS.mkv": 4096,
		"sample/sample.mkv":                           64,
		"readme.nfo":                                  10,
	})
	dl.history["movies"] = []download.HistoryItem{{
		ID: "nzo_m1", Name: "Inception.2010.1080p.BluRay.x264-SPARKS",
		Status: "Completed", Category: "movies", Storage: job,
	}}

	if n := svc.ImportCompleted(context.Background()); n != 1 {
		t.Fatalf("imported = %d, want 1", n)
	}

	wantDest := filepath.Join(libDir, "Inception (2010)", "Inception (2010) [1080p].mkv")
	if _, err := os.Stat(wantDest); err != nil {
		t.Fatalf("file not at %s: %v", wantDest, err)
	}
	got, err := cat.GetMovie(m.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.FilePath != wantDest || got.Quality != "1080p" || got.FileSize != 4096 {
		t.Fatalf("attached = %+v", got.Movie)
	}
	if len(dl.deleted) != 1 || dl.deleted[0] != "nzo_m1" {
		t.Fatalf("SAB history not cleared: %v", dl.deleted)
	}
	entries, _ := cat.ListHistory(10)
	if len(entries) != 1 || entries[0].Kind != "imported" || entries[0].MovieID != m.ID {
		t.Fatalf("history = %+v", entries)
	}

	// second sweep: queue is empty, nothing double-imports
	if n := svc.ImportCompleted(context.Background()); n != 0 {
		t.Fatalf("second sweep imported %d", n)
	}
}

func TestImportCompletedSeasonPack(t *testing.T) {
	svc, _, cat := testService(t)
	dl := &stubDownloader{history: map[string][]download.HistoryItem{}}
	svc.Usenet = dl
	libDir := t.TempDir()
	showID := seedShow(t, cat, libDir).ID

	job := jobDir(t, "Breaking.Bad.S01.1080p.WEB-DL", map[string]int{
		"Breaking.Bad.S01E01.1080p.WEB-DL.mkv": 2048,
		"Breaking.Bad.S01E02.1080p.WEB-DL.mkv": 2048,
	})
	dl.history["tvshows"] = []download.HistoryItem{{
		ID: "nzo_s1", Name: "Breaking.Bad.S01.1080p.WEB-DL",
		Status: "Completed", Category: "tvshows", Storage: job,
	}}

	if n := svc.ImportCompleted(context.Background()); n != 2 {
		t.Fatalf("imported = %d, want 2", n)
	}
	wantE1 := filepath.Join(libDir, "Breaking Bad (2008)", "Season 01",
		"Breaking Bad - S01E01 - Pilot.mkv")
	if _, err := os.Stat(wantE1); err != nil {
		t.Fatalf("episode 1 not at %s: %v", wantE1, err)
	}
	sh, err := cat.GetShow(showID)
	if err != nil {
		t.Fatal(err)
	}
	if sh.OnDisk != 2 {
		t.Fatalf("onDisk = %d, want 2", sh.OnDisk)
	}
	for _, e := range sh.Seasons[0].Episodes {
		if e.Episode <= 2 && (e.FilePath == "" || e.Quality != "1080p") {
			t.Fatalf("episode %d not attached: %+v", e.Episode, e)
		}
	}
	if len(dl.deleted) != 1 || dl.deleted[0] != "nzo_s1" {
		t.Fatalf("SAB history not cleared: %v", dl.deleted)
	}
}

func TestImportFailedJobRecordsAndClears(t *testing.T) {
	svc, _, cat := testService(t)
	dl := &stubDownloader{history: map[string][]download.HistoryItem{}}
	svc.Usenet = dl
	seedMovie(t, cat, t.TempDir())

	dl.history["movies"] = []download.HistoryItem{{
		ID: "nzo_f1", Name: "Inception.2010.1080p.WEB", Status: "Failed",
		Category: "movies", FailMessage: "out of retention",
	}}
	if n := svc.ImportCompleted(context.Background()); n != 0 {
		t.Fatalf("imported = %d, want 0", n)
	}
	entries, _ := cat.ListHistory(10)
	if len(entries) != 1 || entries[0].Kind != "failed" ||
		!strings.Contains(entries[0].Detail, "out of retention") {
		t.Fatalf("history = %+v", entries)
	}
	if len(dl.deleted) != 1 {
		t.Fatalf("failed job not cleared: %v", dl.deleted)
	}
}

func TestImportUnmatchedJobBacksOff(t *testing.T) {
	svc, _, cat := testService(t)
	dl := &stubDownloader{history: map[string][]download.HistoryItem{}}
	svc.Usenet = dl
	seedMovie(t, cat, t.TempDir())

	job := jobDir(t, "Some.Other.Movie.2020.1080p", map[string]int{"Some.Other.Movie.2020.1080p.mkv": 128})
	dl.history["movies"] = []download.HistoryItem{{
		ID: "nzo_u1", Name: "Some.Other.Movie.2020.1080p",
		Status: "Completed", Category: "movies", Storage: job,
	}}

	if n := svc.ImportCompleted(context.Background()); n != 0 {
		t.Fatalf("imported = %d, want 0", n)
	}
	if len(dl.deleted) != 0 {
		t.Fatal("unmatched job must stay in SAB history for retry")
	}
	// the very next sweep skips it — it's on backoff, not hammered
	if n := svc.ImportCompleted(context.Background()); n != 0 {
		t.Fatal("backoff sweep imported something")
	}
}

// stubSettings is a naming template and nothing else.
type stubSettings map[string]string

func (s stubSettings) Get(key string) string { return s[key] }

// The naming templates can name the ids a scanner matches on, and an
// import has to honour that as much as an Organize pass does. It did
// not: only Organize passed the ids through, so every freshly imported
// file rendered the token empty — and deleting a title and adding it
// back was enough to lose the id off the folder.
func TestImportWritesTheIdsTheTemplateAsksFor(t *testing.T) {
	svc, _, cat := testService(t)
	svc.Settings = stubSettings{
		"naming_movie": "{Title} ({Year}) - {Tmdb}/{Title} ({Year}) [{Quality}]",
	}
	dl := &stubDownloader{history: map[string][]download.HistoryItem{}}
	svc.Usenet = dl
	libDir := t.TempDir()
	m := seedMovie(t, cat, libDir)

	job := jobDir(t, "Inception.2010.1080p.BluRay.x264-SPARKS", map[string]int{
		"Inception.2010.1080p.BluRay.x264-SPARKS.mkv": 4096,
	})
	dl.history["movies"] = []download.HistoryItem{{
		ID: "nzo_ids", Name: "Inception.2010.1080p.BluRay.x264-SPARKS",
		Status: "Completed", Category: "movies", Storage: job,
	}}

	if n := svc.ImportCompleted(context.Background()); n != 1 {
		t.Fatalf("imported = %d, want 1", n)
	}
	want := filepath.Join(libDir, "Inception (2010) - {tmdb-27205}",
		"Inception (2010) [1080p].mkv")
	if _, err := os.Stat(want); err != nil {
		got, _ := cat.GetMovie(m.ID)
		t.Fatalf("imported to %q, want the id in the path: %q", got.FilePath, want)
	}
}

// A finished sweep names the folders files landed in, once each. This is
// what lets reely point Plex at the folder it just filled instead of
// waiting for Plex's own sweep to notice — and a season pack is one
// folder, not one per episode, or a twelve-episode pack would ask for
// twelve scans of the same place.
func TestASweepNamesTheFoldersItFilled(t *testing.T) {
	svc, _, cat := testService(t)
	dl := &stubDownloader{history: map[string][]download.HistoryItem{}}
	svc.Usenet = dl
	movieLib, showLib := t.TempDir(), t.TempDir()
	seedMovie(t, cat, movieLib)
	seedShow(t, cat, showLib)

	var placed []string
	svc.Placed = func(dirs []string) { placed = dirs }

	dl.history["movies"] = []download.HistoryItem{{
		ID: "nzo_m1", Name: "Inception.2010.1080p.BluRay.x264-SPARKS",
		Status: "Completed", Category: "movies",
		Storage: jobDir(t, "Inception.2010.1080p.BluRay.x264-SPARKS", map[string]int{
			"Inception.2010.1080p.BluRay.x264-SPARKS.mkv": 4096,
		}),
	}}
	dl.history["tvshows"] = []download.HistoryItem{{
		ID: "nzo_s1", Name: "Breaking.Bad.S01.1080p.WEB-DL",
		Status: "Completed", Category: "tvshows",
		Storage: jobDir(t, "Breaking.Bad.S01.1080p.WEB-DL", map[string]int{
			"Breaking.Bad.S01E01.1080p.WEB-DL.mkv": 2048,
			"Breaking.Bad.S01E02.1080p.WEB-DL.mkv": 2048,
		}),
	}}

	if n := svc.ImportCompleted(context.Background()); n != 3 {
		t.Fatalf("imported = %d, want 3", n)
	}
	want := []string{
		filepath.Join(movieLib, "Inception (2010)"),
		filepath.Join(showLib, "Breaking Bad (2008)", "Season 01"),
	}
	sort.Strings(want)
	if len(placed) != len(want) {
		t.Fatalf("folders = %v, want %v", placed, want)
	}
	for i, dir := range want {
		if placed[i] != dir {
			t.Fatalf("folders = %v, want %v", placed, want)
		}
	}
}

// A sweep that imported nothing says nothing: no callback, so no pointless
// round trip to Plex asking it to look at a library that did not change.
func TestASweepThatImportedNothingIsQuiet(t *testing.T) {
	svc, _, _ := testService(t)
	svc.Usenet = &stubDownloader{history: map[string][]download.HistoryItem{}}
	called := false
	svc.Placed = func([]string) { called = true }

	svc.ImportCompleted(context.Background())
	if called {
		t.Error("an empty sweep still asked for a scan")
	}
}
