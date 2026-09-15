package importer

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/parser"
)

func TestApplyFolderIdentity(t *testing.T) {
	root := string(filepath.Separator) + filepath.Join("data", "tv")
	cases := []struct {
		name      string
		path      string
		fileTitle string
		fileYear  int
		wantTitle string
		wantYear  int
	}{
		{"year folder names the show",
			filepath.Join(root, "WIFE SWAP (2019)", "Season 01", "Wife Swap - S01E01.mkv"),
			"Wife Swap", 0, "WIFE SWAP", 2019},
		{"genre nesting still finds the year folder",
			filepath.Join(root, "Reality", "Wife Swap (2019)", "Season 01", "Wife Swap - S01E01.mkv"),
			"Wife Swap", 0, "Wife Swap", 2019},
		{"yearless folder never overrides a real filename title",
			filepath.Join(root, "Reality", "Wife Swap - S01E01.mkv"),
			"Wife Swap", 0, "Wife Swap", 0},
		{"yearless folder fills a titleless filename",
			filepath.Join(root, "Wife Swap UK", "Season 01", "S01E01.mkv"),
			"", 0, "Wife Swap UK", 0},
		{"flat layout changes nothing",
			filepath.Join(root, "Wife.Swap.S01E01.mkv"),
			"Wife Swap", 0, "Wife Swap", 0},
		{"season folder alone carries no identity",
			filepath.Join(root, "Season 01", "Wife Swap - S01E01.mkv"),
			"Wife Swap", 0, "Wife Swap", 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := parser.Result{Title: c.fileTitle, Year: c.fileYear}
			applyFolderIdentity(root, c.path, &p)
			if p.Title != c.wantTitle || p.Year != c.wantYear {
				t.Fatalf("got %q/%d, want %q/%d", p.Title, p.Year, c.wantTitle, c.wantYear)
			}
		})
	}
}

// twoWifeSwapsTMDB serves both shows: a year-less search ranks the 2004
// original first (the trap), a year-scoped search answers correctly.
func twoWifeSwapsTMDB(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/search/tv":
			if r.URL.Query().Get("first_air_date_year") == "2019" {
				_, _ = fmt.Fprint(w, `{"results":[{"id":200,"name":"Wife Swap","first_air_date":"2019-04-04","popularity":5}]}`)
				return
			}
			_, _ = fmt.Fprint(w, `{"results":[
				{"id":100,"name":"Wife Swap","first_air_date":"2004-09-26","popularity":9},
				{"id":200,"name":"Wife Swap","first_air_date":"2019-04-04","popularity":5}]}`)
		case "/tv/100":
			_, _ = fmt.Fprint(w, `{"name":"Wife Swap","first_air_date":"2004-09-26","status":"Ended",
				"credits":{"cast":[]},"genres":[],"external_ids":{},"seasons":[{"season_number":1}]}`)
		case "/tv/100/season/1":
			_, _ = fmt.Fprint(w, `{"name":"Season 1","episodes":[
				{"id":101,"episode_number":1,"name":"Pitts/Polchios","air_date":"2004-09-26","runtime":60}]}`)
		case "/tv/200":
			_, _ = fmt.Fprint(w, `{"name":"Wife Swap","first_air_date":"2019-04-04","status":"Ended",
				"credits":{"cast":[]},"genres":[],"external_ids":{},"seasons":[{"season_number":1}]}`)
		case "/tv/200/season/1":
			_, _ = fmt.Fprint(w, `{"name":"Season 1","episodes":[
				{"id":201,"episode_number":1,"name":"Wells/Fambro","air_date":"2019-04-04","runtime":42}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// The reported bug end to end: files living in "WIFE SWAP (2019)" were
// scanned by filename alone ("Wife Swap - S01E01…", no year), matched to
// TMDB's more popular 2004 original, and attached there — while the 2019
// show's own claim stood too, the same bytes on disk twice. The scan must
// file them under the 2019 show and strip the 2004 show's false claim.
func TestScanRoutesSharedTitleByFolderAndStealsTheClaim(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	cat := catalog.New(conn)
	tmdb := metadata.NewTMDB(func() string { return "k" })
	tmdb.SetBaseURL(twoWifeSwapsTMDB(t).URL)

	root := t.TempDir()
	lib, err := cat.CreateLibrary("TV", root, "shows")
	if err != nil {
		t.Fatal(err)
	}

	// both shows already in the library, matching the reported install
	imp := New(cat, tmdb, nil)
	d2004, err := tmdb.Show(context.Background(), 100)
	if err != nil {
		t.Fatal(err)
	}
	id2004, err := cat.UpsertShow(d2004, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	d2019, err := tmdb.Show(context.Background(), 200)
	if err != nil {
		t.Fatal(err)
	}
	id2019, err := cat.UpsertShow(d2019, lib.ID)
	if err != nil {
		t.Fatal(err)
	}

	// the file on disk, inside the 2019 show's folder
	dir := filepath.Join(root, "WIFE SWAP (2019)", "Season 01")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "Wife Swap - S01E01 - Wells-Fambro [1080p WEB-DL].mkv")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	// today's damage: the 2004 show already claims that path
	ep2004, err := cat.EpisodeID(id2004, 1, 1)
	if err != nil || ep2004 == 0 {
		t.Fatalf("2004 episode id: %v", err)
	}
	if err := cat.AttachScannedEpisodeFile(ep2004, file, 1, "1080p", ""); err != nil {
		t.Fatal(err)
	}

	if _, err := imp.ScanLibrary(context.Background(), lib); err != nil {
		t.Fatal(err)
	}

	sh2019, err := cat.GetShow(id2019)
	if err != nil {
		t.Fatal(err)
	}
	if got := sh2019.Seasons[0].Episodes[0].FilePath; got != file {
		t.Fatalf("2019 S01E01 file = %q, want the scanned path", got)
	}
	sh2004, err := cat.GetShow(id2004)
	if err != nil {
		t.Fatal(err)
	}
	if got := sh2004.Seasons[0].Episodes[0].FilePath; got != "" {
		t.Fatalf("2004 S01E01 still claims %q — the false claim survived", got)
	}
}
