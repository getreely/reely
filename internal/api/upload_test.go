package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/getreely/reely/internal/grab"
)

// uploadFiles POSTs a multipart body with the given files, in a stable order.
func uploadFiles(t *testing.T, h http.Handler, path string, names []string) *httptest.ResponseRecorder {
	t.Helper()
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for _, name := range names {
		fw, err := mw.CreateFormFile("files", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(make([]byte, 1024)); err != nil {
			t.Fatal(err)
		}
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", path, &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestUploadMovie(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	libDir := t.TempDir()
	rec, lib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "Movies", "path": libDir, "kind": "movies"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	movieID := seedMovie(t, srv, int64(lib["id"].(float64)))

	w := uploadFiles(t, h, "/api/v1/movies/"+itoa(movieID)+"/upload",
		[]string{"The.Matrix.1999.1080p.BluRay.mkv"})
	if w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}

	dest := filepath.Join(libDir, "The Matrix (1999)", "The Matrix (1999) [1080p].mkv")
	if _, err := os.Stat(dest); err != nil {
		t.Fatalf("file not at %s: %v", dest, err)
	}
	m, err := srv.Catalog.GetMovie(movieID)
	if err != nil {
		t.Fatal(err)
	}
	if m.FilePath != dest || m.Quality != "1080p" {
		t.Fatalf("attached = %+v", m.Movie)
	}
	entries, _ := srv.Catalog.ListHistory(10)
	if len(entries) != 1 || entries[0].Kind != "imported" || entries[0].Title != "The Matrix" {
		t.Fatalf("history = %+v", entries)
	}
}

func TestUploadShowMultipleFiles(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	libDir := t.TempDir()
	rec, lib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "TV", "path": libDir, "kind": "shows"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	showID := seedShow(t, srv, int64(lib["id"].(float64)))

	w := uploadFiles(t, h, "/api/v1/shows/"+itoa(showID)+"/upload", []string{
		"Breaking.Bad.S01E01.1080p.WEB.mkv",
		"Breaking.Bad.S01E02.720p.WEB.mkv",
		"no-episode-marker.mkv",
	})
	if w.Code != http.StatusOK {
		t.Fatalf("upload: %d %s", w.Code, w.Body)
	}
	var out struct {
		Results []grab.UploadResult `json:"results"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 3 {
		t.Fatalf("results = %+v", out.Results)
	}
	okCount := 0
	for _, res := range out.Results {
		if res.Error == "" {
			okCount++
		} else if res.File != "no-episode-marker.mkv" {
			t.Fatalf("unexpected failure: %+v", res)
		}
	}
	if okCount != 2 {
		t.Fatalf("ok = %d, want 2 (%+v)", okCount, out.Results)
	}

	if _, err := os.Stat(filepath.Join(libDir, "Breaking Bad (2008)", "Season 01",
		"Breaking Bad - S01E01 - Pilot.mkv")); err != nil {
		t.Fatalf("episode 1 missing: %v", err)
	}
	sh, err := srv.Catalog.GetShow(showID)
	if err != nil {
		t.Fatal(err)
	}
	if sh.OnDisk != 2 {
		t.Fatalf("onDisk = %d, want 2", sh.OnDisk)
	}
}

func TestActivityEndpointAndResolve(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()

	rec, _ := doJSON(t, h, "GET", "/api/v1/activity", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("activity: %d %s", rec.Code, rec.Body)
	}

	// bad resolve requests bounce
	rec, _ = doJSON(t, h, "POST", "/api/v1/activity/resolve", map[string]any{"nzoId": "x"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("resolve without target: %d, want 400", rec.Code)
	}
	rec, _ = doJSON(t, h, "POST", "/api/v1/activity/retry", map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("retry without nzo: %d, want 400", rec.Code)
	}
}
