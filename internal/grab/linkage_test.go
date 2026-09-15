package grab

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getreely/reely/internal/download"
)

func recordGrab(t *testing.T, svc *Service, id string, movieID, showID, episodeID int64) {
	t.Helper()
	detail, _ := json.Marshal(map[string]string{"nzoId": id})
	if err := svc.Catalog.AddHistory("grabbed", movieID, showID, episodeID, string(detail)); err != nil {
		t.Fatal(err)
	}
}

// An obfuscated release — a bare hash for a name — imports onto the title
// it was grabbed for. Without the linkage this exact job can only fail.
func TestObfuscatedGrabImportsOntoItsTarget(t *testing.T) {
	svc, _, cat := testService(t)
	dl := &stubDownloader{history: map[string][]download.HistoryItem{}}
	svc.Usenet = dl
	libDir := t.TempDir()
	m := seedMovie(t, cat, libDir)
	recordGrab(t, svc, "nzo_ob1", m.ID, 0, 0)

	job := jobDir(t, "a8f3kq99x", map[string]int{"a8f3kq99x.mkv": 4096})
	dl.history["movies"] = []download.HistoryItem{{
		ID: "nzo_ob1", Name: "a8f3kq99x", Status: "Completed", Category: "movies", Storage: job,
	}}

	if n := svc.ImportCompleted(context.Background()); n != 1 {
		t.Fatalf("imported = %d, want 1 (problems: %+v)", n, svc.Problems())
	}
	got, err := cat.GetMovie(m.ID)
	if err != nil || got.FilePath == "" {
		t.Fatalf("movie not attached: %+v (%v)", got, err)
	}
	if len(dl.deleted) != 1 {
		t.Fatalf("SAB history not cleared: %v", dl.deleted)
	}
}

// A release that parses confidently to a DIFFERENT title than it was
// grabbed for never imports onto either guess — it stops as a visible
// problem for a human call.
func TestMislabeledGrabStopsForAHuman(t *testing.T) {
	svc, _, cat := testService(t)
	dl := &stubDownloader{history: map[string][]download.HistoryItem{}}
	svc.Usenet = dl
	m := seedMovie(t, cat, t.TempDir()) // Inception
	recordGrab(t, svc, "nzo_bad", m.ID, 0, 0)

	job := jobDir(t, "Interstellar.2014.1080p.BluRay", map[string]int{"Interstellar.2014.1080p.BluRay.mkv": 4096})
	dl.history["movies"] = []download.HistoryItem{{
		ID: "nzo_bad", Name: "Interstellar.2014.1080p.BluRay", Status: "Completed", Category: "movies", Storage: job,
	}}

	if n := svc.ImportCompleted(context.Background()); n != 0 {
		t.Fatalf("mislabeled release imported %d files", n)
	}
	got, _ := cat.GetMovie(m.ID)
	if got.FilePath != "" {
		t.Fatalf("mislabeled file attached to grab target: %s", got.FilePath)
	}
	problems := svc.Problems()
	if len(problems) != 1 || !strings.Contains(problems[0].Error, "Interstellar") ||
		!strings.Contains(problems[0].Error, "Inception") {
		t.Fatalf("problem row = %+v", problems)
	}
	if len(dl.deleted) != 0 {
		t.Fatal("mislabeled job cleared from SAB before a human saw it")
	}

	// the human call: resolving onto the grab target imports it anyway
	svc.RetryNow("nzo_bad")
	if err := svc.ImportJobAs(context.Background(), "nzo_bad", m.ID, 0, 0, 0); err != nil {
		t.Fatal(err)
	}
	got, _ = cat.GetMovie(m.ID)
	if got.FilePath == "" {
		t.Fatal("hand-resolve did not import")
	}
}

// A single-episode grab with a completely unparseable file lands on the
// exact episode it went hunting for.
func TestObfuscatedEpisodeGrabLandsOnItsEpisode(t *testing.T) {
	svc, _, cat := testService(t)
	dl := &stubDownloader{history: map[string][]download.HistoryItem{}}
	svc.Usenet = dl
	sh := seedShow(t, cat, t.TempDir())
	epID, err := cat.EpisodeID(sh.ID, 1, 2)
	if err != nil || epID == 0 {
		t.Fatalf("episode id: %d (%v)", epID, err)
	}
	recordGrab(t, svc, "nzo_ep", 0, sh.ID, epID)

	job := jobDir(t, "7c2f81b4e9", map[string]int{"7c2f81b4e9.mkv": 2048})
	dl.history["tvshows"] = []download.HistoryItem{{
		ID: "nzo_ep", Name: "7c2f81b4e9", Status: "Completed", Category: "tvshows", Storage: job,
	}}

	if n := svc.ImportCompleted(context.Background()); n != 1 {
		t.Fatalf("imported = %d, want 1 (problems: %+v)", n, svc.Problems())
	}
	fresh, err := cat.GetShow(sh.ID)
	if err != nil {
		t.Fatal(err)
	}
	ep := findEpisode(fresh, 1, 2)
	if ep == nil || ep.FilePath == "" {
		t.Fatalf("S01E02 not attached: %+v", ep)
	}
	if _, err := os.Stat(ep.FilePath); err != nil {
		t.Fatalf("file missing at %s: %v", ep.FilePath, err)
	}
	// its neighbors stayed empty — the linkage never sprays a whole job
	// across a season
	if e1 := findEpisode(fresh, 1, 1); e1.FilePath != "" {
		t.Fatalf("S01E01 wrongly attached: %s", e1.FilePath)
	}
}

// A grabbed job whose name AGREES with its target imports normally — the
// linkage only changes who decides, not what a good release does.
func TestGrabbedJobWithHonestNameImports(t *testing.T) {
	svc, _, cat := testService(t)
	dl := &stubDownloader{history: map[string][]download.HistoryItem{}}
	svc.Usenet = dl
	libDir := t.TempDir()
	m := seedMovie(t, cat, libDir)
	recordGrab(t, svc, "nzo_ok", m.ID, 0, 0)

	job := jobDir(t, "Inception.2010.1080p.BluRay.x264", map[string]int{"Inception.2010.1080p.BluRay.x264.mkv": 4096})
	dl.history["movies"] = []download.HistoryItem{{
		ID: "nzo_ok", Name: "Inception.2010.1080p.BluRay.x264", Status: "Completed", Category: "movies", Storage: job,
	}}
	if n := svc.ImportCompleted(context.Background()); n != 1 {
		t.Fatalf("imported = %d, want 1 (problems: %+v)", n, svc.Problems())
	}
	got, _ := cat.GetMovie(m.ID)
	if got.Quality != "1080p" || !strings.HasPrefix(got.FilePath, filepath.Join(libDir, "Inception (2010)")) {
		t.Fatalf("attached = %+v", got.Movie)
	}
}
