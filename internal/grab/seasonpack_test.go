package grab

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getreely/reely/internal/download"
)

// A season pack must land one file per episode, each from its own source
// file — never one episode's file attached to the whole season.
//
// Every file in a job folder is parsed on its own name, so the numbers
// come from the file rather than from the pack's. This pins that: three
// distinct sources, three distinct destinations, each carrying the
// episode its own name declared and nothing shared between them.
func TestASeasonPackImportsEachEpisodeFromItsOwnFile(t *testing.T) {
	svc, _, cat := testService(t)
	dl := &stubDownloader{history: map[string][]download.HistoryItem{}}
	svc.Usenet = dl
	libDir := t.TempDir()
	showID := seedShow(t, cat, libDir).ID

	// distinct sizes, so a file reused across episodes shows up as a
	// repeated size rather than hiding behind identical bytes
	job := jobDir(t, "Breaking.Bad.S01.1080p.WEB-DL", map[string]int{
		"Breaking.Bad.S01E01.1080p.WEB-DL.mkv": 4096,
		"Breaking.Bad.S01E02.1080p.WEB-DL.mkv": 5120,
		"Breaking.Bad.S01E03.1080p.WEB-DL.mkv": 6144,
	})
	dl.history["tvshows"] = []download.HistoryItem{{
		ID: "nzo_s1", Name: "Breaking.Bad.S01.1080p.WEB-DL",
		Status: "Completed", Category: "tvshows", Storage: job,
	}}

	if n := svc.ImportCompleted(context.Background()); n != 3 {
		t.Fatalf("imported = %d, want 3", n)
	}
	sh, err := cat.GetShow(showID)
	if err != nil {
		t.Fatal(err)
	}

	wantSize := map[int]int64{1: 4096, 2: 5120, 3: 6144}
	seenPath := map[string]int{}
	for _, e := range sh.Seasons[0].Episodes {
		if e.FilePath == "" {
			t.Fatalf("S01E%02d got no file", e.Episode)
		}
		// the destination names the episode it belongs to
		want := filepath.Join(libDir, "Breaking Bad (2008)", "Season 01")
		if filepath.Dir(e.FilePath) != want {
			t.Errorf("S01E%02d landed in %s, want %s", e.Episode, filepath.Dir(e.FilePath), want)
		}
		base := filepath.Base(e.FilePath)
		if marker := fmt.Sprintf("S%02dE%02d", e.Season, e.Episode); !strings.Contains(base, marker) {
			t.Errorf("S01E%02d is at %q, which does not name that episode", e.Episode, base)
		}
		// two episodes sharing one file is the failure this guards
		if prev, dup := seenPath[e.FilePath]; dup {
			t.Fatalf("S01E%02d and S01E%02d share the file %s", prev, e.Episode, e.FilePath)
		}
		seenPath[e.FilePath] = e.Episode

		info, err := os.Stat(e.FilePath)
		if err != nil {
			t.Fatalf("S01E%02d file missing: %v", e.Episode, err)
		}
		if info.Size() != wantSize[e.Episode] {
			t.Errorf("S01E%02d is %d bytes, want %d — it has another episode's file",
				e.Episode, info.Size(), wantSize[e.Episode])
		}
	}
	if len(seenPath) != 3 {
		t.Fatalf("got %d distinct files across the season, want 3", len(seenPath))
	}
}
