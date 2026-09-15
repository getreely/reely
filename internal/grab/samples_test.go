package grab

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/getreely/reely/internal/download"
)

// writeSized makes a file of a given size so videoFiles' size floor is
// exercised for real rather than mocked.
func writeSized(t *testing.T, path string, size int64) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := f.Truncate(size); err != nil {
		t.Fatal(err)
	}
}

// A scene release ships its sample beside the episode, and the sample
// carries the same episode markers in its name. Importing it is not a
// harmless extra: it resolves to the same episode, renders to the same
// filename, and overwrites the episode that was actually wanted — while
// writing a second "imported" row that reads as a duplicate.
func TestVideoFilesLeavesOutSamplesAndExtras(t *testing.T) {
	root := t.TempDir()
	real := filepath.Join(root, "The.Proof.Is.Out.There.S06E11.1080p.WEB.h264-EDITH.mkv")
	writeSized(t, real, 900<<20)
	writeSized(t, filepath.Join(root, "The.Proof.Is.Out.There.S06E11.1080p.WEB.h264-EDITH-sample.mkv"), 40<<20)
	writeSized(t, filepath.Join(root, "Sample", "the.proof.s06e11.sample.mkv"), 300<<20)
	writeSized(t, filepath.Join(root, "Extras", "behind.the.scenes.mkv"), 400<<20)
	writeSized(t, filepath.Join(root, "trailer.mkv"), 20<<20)

	got, err := videoFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d files, want just the episode: %v", len(got), got)
	}
	if got[0] != real {
		t.Fatalf("kept %q, want %q", got[0], real)
	}
}

// A season pack's episodes must all survive — the filter removes samples,
// not the payload.
func TestVideoFilesKeepsEveryRealEpisodeInAPack(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"E01", "E02", "E03"} {
		writeSized(t, filepath.Join(root, "Show.S01"+n+".1080p.WEB.mkv"), 700<<20)
	}
	writeSized(t, filepath.Join(root, "sample.mkv"), 30<<20)

	got, err := videoFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d files, want all three episodes: %v", len(got), got)
	}
}

// Short-form content is small in absolute terms but its sample is still
// small relative to it, which is why the rule is proportional: a fixed
// size floor would have thrown the whole release away.
func TestVideoFilesHandlesSmallReleases(t *testing.T) {
	root := t.TempDir()
	small := filepath.Join(root, "Short.Film.2020.1080p.WEB.mkv")
	writeSized(t, small, 60<<20)
	writeSized(t, filepath.Join(root, "sample.mkv"), 5<<20)

	got, err := videoFiles(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != small {
		t.Fatalf("got %v, want just %q", got, small)
	}
}

// A sample that ships beside the episode must not produce a second import
// of the same episode — it renders to the same filename, so the second
// import overwrites the real file and leaves a duplicate history row that
// reads as a bug because it is one.
func TestImportSkipsTheSampleBesideTheEpisode(t *testing.T) {
	svc, _, cat := testService(t)
	dl := &stubDownloader{history: map[string][]download.HistoryItem{}}
	svc.Usenet = dl
	libDir := t.TempDir()
	seedShow(t, cat, libDir)

	job := jobDir(t, "Breaking.Bad.S01E01.1080p.WEB-DL", map[string]int{
		"Breaking.Bad.S01E01.1080p.WEB-DL.mkv":        4096,
		"Breaking.Bad.S01E01.1080p.WEB-DL-sample.mkv": 64,
	})
	dl.history["tvshows"] = []download.HistoryItem{{
		ID: "nzo_sample", Name: "Breaking.Bad.S01E01.1080p.WEB-DL",
		Status: "Completed", Category: "tvshows", Storage: job,
	}}

	if n := svc.ImportCompleted(context.Background()); n != 1 {
		t.Fatalf("imported = %d, want 1 — the sample was imported too", n)
	}
	entries, err := cat.ListHistory(10)
	if err != nil {
		t.Fatal(err)
	}
	imports := 0
	for _, e := range entries {
		if e.Kind == "imported" {
			imports++
		}
	}
	if imports != 1 {
		t.Fatalf("%d imported rows for one episode, want 1", imports)
	}
}

// A job whose SAB history entry outlives its import must never be filed
// twice, and must never be recorded as failed: SAB reports "Aborted,
// cannot be completed" once its files have been moved out from under it,
// and believing that would mark a download that actually landed as a
// failure — which then unblocks the pending gate and sends reely hunting
// for a replacement it does not need.
func TestAnImportedJobIsNeverReimportedOrFailed(t *testing.T) {
	svc, _, cat := testService(t)
	dl := &stubDownloader{history: map[string][]download.HistoryItem{}}
	svc.Usenet = dl
	libDir := t.TempDir()
	seedShow(t, cat, libDir)

	job := jobDir(t, "Breaking.Bad.S01E01.1080p.WEB-DL", map[string]int{
		"Breaking.Bad.S01E01.1080p.WEB-DL.mkv": 4096,
	})
	item := download.HistoryItem{
		ID: "nzo_stuck", Name: "Breaking.Bad.S01E01.1080p.WEB-DL",
		Status: "Completed", Category: "tvshows", Storage: job,
	}
	dl.history["tvshows"] = []download.HistoryItem{item}

	if n := svc.ImportCompleted(context.Background()); n != 1 {
		t.Fatalf("first sweep imported %d, want 1", n)
	}

	// SAB kept the entry (a delete that did not take) and has since given
	// up on the job, because reely moved its files away
	item.Status = "Failed"
	item.FailMessage = "Aborted, cannot be completed"
	dl.history["tvshows"] = []download.HistoryItem{item}

	if n := svc.ImportCompleted(context.Background()); n != 0 {
		t.Fatalf("second sweep imported %d, want 0", n)
	}
	entries, err := cat.ListHistory(20)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Kind == "failed" {
			t.Fatal("a download that imported fine was recorded as failed")
		}
	}
}
