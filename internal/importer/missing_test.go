package importer

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/metadata"
)

func missingTestStore(t *testing.T) *catalog.Store {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return catalog.New(conn)
}

// A file deleted outside reely must stop reading "on disk" after a scan:
// the row whose file is gone gets its attachment cleared, and the one
// whose file survives keeps it.
func TestScanClearsAttachmentsWhoseFilesAreGone(t *testing.T) {
	cat := missingTestStore(t)
	tmdb := metadata.NewTMDB(func() string { return "k" })
	// the surviving file re-scans through TMDB; an empty answer just leaves
	// its existing attachment alone, which is all this test needs
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	t.Cleanup(stub.Close)
	tmdb.SetBaseURL(stub.URL)

	root := t.TempDir()
	lib, err := cat.CreateLibrary("Movies", root, "movies")
	if err != nil {
		t.Fatal(err)
	}
	goneID, err := cat.UpsertMovie(&metadata.MovieDetail{TmdbID: 1, Title: "Gone", Year: 2020}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	keptID, err := cat.UpsertMovie(&metadata.MovieDetail{TmdbID: 2, Title: "Kept", Year: 2021}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	gonePath := filepath.Join(root, "Gone (2020).mkv")
	if err := cat.AttachMovieFile(goneID, gonePath, 1, "1080p", ""); err != nil {
		t.Fatal(err)
	}
	// the kept file genuinely exists on disk
	keptPath := filepath.Join(root, "Kept (2021).mkv")
	if err := os.WriteFile(keptPath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := cat.AttachMovieFile(keptID, keptPath, 1, "1080p", ""); err != nil {
		t.Fatal(err)
	}

	// scanning re-attaches Kept from disk and finds Gone's file missing
	res, err := New(cat, tmdb, nil).ScanLibrary(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}
	if res.Missing != 1 {
		t.Fatalf("missing = %d, want 1", res.Missing)
	}
	gone, err := cat.GetMovie(goneID)
	if err != nil || gone.FilePath != "" {
		t.Fatalf("gone file still attached: %q (%v)", gone.FilePath, err)
	}
	kept, err := cat.GetMovie(keptID)
	if err != nil || kept.FilePath != keptPath {
		t.Fatalf("surviving file lost its attachment: %q (%v)", kept.FilePath, err)
	}
}

// An unreachable library root stats exactly like a deleted file. The scan
// must not clear anything then — wiping attachments over a downed mount
// would re-download the entire library.
func TestScanKeepsAttachmentsWhenRootUnreachable(t *testing.T) {
	cat := missingTestStore(t)
	tmdb := metadata.NewTMDB(func() string { return "k" })

	root := filepath.Join(t.TempDir(), "mount", "movies") // never created
	lib, err := cat.CreateLibrary("Movies", root, "movies")
	if err != nil {
		t.Fatal(err)
	}
	id, err := cat.UpsertMovie(&metadata.MovieDetail{TmdbID: 1, Title: "Held", Year: 2020}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "Held (2020).mkv")
	if err := cat.AttachMovieFile(id, path, 1, "1080p", ""); err != nil {
		t.Fatal(err)
	}

	res, err := New(cat, tmdb, nil).ScanLibrary(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}
	if res.Missing != 0 {
		t.Fatalf("missing = %d over an unreachable root", res.Missing)
	}
	m, err := cat.GetMovie(id)
	if err != nil || m.FilePath != path {
		t.Fatalf("attachment cleared over an unreachable root: %q (%v)", m.FilePath, err)
	}
	if len(res.Warnings) == 0 {
		t.Fatal("skipping the file check silently hides a downed mount")
	}
}

// A show with a release-numbering offset (a TMDB-split revival) may have
// scene-numbered files on disk: "S08E01" belongs to the show's own S01E01
// when no literal S08 exists. Literal numbering still wins when it does.
func TestScanMapsSceneNumberedFilesThroughTheOffset(t *testing.T) {
	cat := missingTestStore(t)
	tmdb := metadata.NewTMDB(func() string { return "k" })
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/search/tv":
			_, _ = w.Write([]byte(`{"results":[{"id":235884,"name":"Kitchen Nightmares","first_air_date":"2023-09-25","popularity":5}]}`))
		case "/tv/235884":
			_, _ = w.Write([]byte(`{"name":"Kitchen Nightmares","first_air_date":"2023-09-25","status":"Returning Series",
				"credits":{"cast":[]},"genres":[],"external_ids":{},"seasons":[{"season_number":1}]}`))
		case "/tv/235884/season/1":
			_, _ = w.Write([]byte(`{"name":"Season 1","episodes":[
				{"id":1,"episode_number":1,"name":"One","air_date":"2023-09-24","runtime":42}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(stub.Close)
	tmdb.SetBaseURL(stub.URL)

	root := t.TempDir()
	lib, err := cat.CreateLibrary("TV", root, "shows")
	if err != nil {
		t.Fatal(err)
	}
	d, err := tmdb.Show(context.Background(), 235884)
	if err != nil {
		t.Fatal(err)
	}
	showID, err := cat.UpsertShow(d, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.SetShowNumbering(showID, 7, 0); err != nil {
		t.Fatal(err)
	}

	dir := filepath.Join(root, "Kitchen Nightmares (2023)", "Season 01")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "Kitchen.Nightmares.US.S08E01.1080p.WEB.mkv")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, err := New(cat, tmdb, nil).ScanLibrary(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}
	if res.Imported != 1 || len(res.Warnings) != 0 {
		t.Fatalf("imported %d, warnings %v", res.Imported, res.Warnings)
	}
	sh, err := cat.GetShow(showID)
	if err != nil {
		t.Fatal(err)
	}
	if got := sh.Seasons[0].Episodes[0].FilePath; got != file {
		t.Fatalf("S01E01 file = %q, want the scene-numbered file mapped onto it", got)
	}
}

// Show libraries reconcile too — an episode whose file vanished goes back
// to wanted.
func TestScanClearsMissingEpisodeFiles(t *testing.T) {
	cat := missingTestStore(t)
	tmdb := metadata.NewTMDB(func() string { return "k" })

	root := t.TempDir()
	lib, err := cat.CreateLibrary("TV", root, "shows")
	if err != nil {
		t.Fatal(err)
	}
	showID, err := cat.UpsertShow(&metadata.ShowDetail{
		TmdbID: 9, Title: "Some Show", Year: 2020,
		Seasons: []metadata.SeasonDetail{{Number: 1, Episodes: []metadata.EpisodeDetail{
			{TmdbID: 91, Season: 1, Episode: 1, Title: "One"},
		}}},
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	epID, err := cat.EpisodeID(showID, 1, 1)
	if err != nil || epID == 0 {
		t.Fatalf("episode id: %v", err)
	}
	if err := cat.AttachEpisodeFile(epID, filepath.Join(root, "Some Show - S01E01.mkv"), 1, "1080p", ""); err != nil {
		t.Fatal(err)
	}

	res, err := New(cat, tmdb, nil).ScanLibrary(context.Background(), lib)
	if err != nil {
		t.Fatal(err)
	}
	if res.Missing != 1 {
		t.Fatalf("missing = %d, want 1", res.Missing)
	}
	sh, err := cat.GetShow(showID)
	if err != nil || sh.Seasons[0].Episodes[0].FilePath != "" {
		t.Fatalf("episode still claims a gone file (%v)", err)
	}
}
