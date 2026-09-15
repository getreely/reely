package api

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func configureFakes(t *testing.T, h http.Handler) {
	t.Helper()
	fake := fakeProwlarr(t, nil)
	sab, _ := fakeSAB(t)
	for key, value := range map[string]string{
		"prowlarr_url": fake.URL, "prowlarr_api_key": "k",
		"sab_url": sab.URL, "sab_api_key": "k",
	} {
		if rec, _ := doJSON(t, h, "PUT", "/api/v1/settings/"+key,
			map[string]string{"value": value}); rec.Code != http.StatusOK {
			t.Fatal(rec.Body)
		}
	}
}

func TestSearchNowEndpoints(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	rec, lib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "Movies", "path": "/data/movies", "kind": "movies"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	movieID := seedMovie(t, srv, int64(lib["id"].(float64)))

	// unconfigured → 412
	rec, _ = doJSON(t, h, "POST", "/api/v1/movies/"+itoa(movieID)+"/search", map[string]any{})
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("unconfigured: %d, want 412", rec.Code)
	}
	configureFakes(t, h)

	// one title runs the search inline and answers with what it did, so the
	// caller can say "sent to SAB" or explain why nothing was
	rec, out := doJSON(t, h, "POST", "/api/v1/movies/"+itoa(movieID)+"/search", map[string]any{})
	if rec.Code != http.StatusOK {
		t.Fatalf("movie search: %d %v", rec.Code, out)
	}
	if out["grabbed"] == "" && out["reason"] == "" {
		t.Fatalf("movie search reported neither a grab nor a reason: %v", out)
	}
	if srv.Grab.QueueLen() != 0 {
		t.Fatalf("single-title search queued %d instead of running now", srv.Grab.QueueLen())
	}

	rec, tvLib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "TV", "path": "/data/tv", "kind": "shows"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	showID := seedShow(t, srv, int64(tvLib["id"].(float64)))

	rec, out = doJSON(t, h, "POST", "/api/v1/shows/"+itoa(showID)+"/search", map[string]any{})
	if rec.Code != http.StatusOK || out["queued"].(float64) != 3 {
		t.Fatalf("show search: %d %v", rec.Code, out)
	}
	// a whole show stays queued — that can be hundreds of searches
	if srv.Grab.QueueLen() != 3 {
		t.Fatalf("show search queued %d, want 3", srv.Grab.QueueLen())
	}

	rec, out = doJSON(t, h, "POST", "/api/v1/shows/"+itoa(showID)+"/search",
		map[string]any{"season": 1, "episode": 2})
	if rec.Code != http.StatusOK {
		t.Fatalf("episode search: %d %v", rec.Code, out)
	}
	if out["grabbed"] == "" && out["reason"] == "" {
		t.Fatalf("episode search reported neither a grab nor a reason: %v", out)
	}
}

func TestDeleteMovie(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	libDir := t.TempDir()
	rec, lib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "Movies", "path": libDir, "kind": "movies"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	movieID := seedMovie(t, srv, int64(lib["id"].(float64)))
	moviePath := filepath.Join(libDir, "matrix.mkv")
	if err := os.WriteFile(moviePath, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := srv.Catalog.AttachMovieFile(movieID, moviePath, 1, "1080p", ""); err != nil {
		t.Fatal(err)
	}

	// delete keeping the file
	rec, _ = doJSON(t, h, "DELETE", "/api/v1/movies/"+itoa(movieID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(moviePath); err != nil {
		t.Fatal("file deleted without deleteFiles")
	}
	rec, _ = doJSON(t, h, "GET", "/api/v1/movies/"+itoa(movieID), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deleted movie still answers: %d", rec.Code)
	}

	// re-seed and delete WITH the file
	movieID = seedMovie(t, srv, int64(lib["id"].(float64)))
	if err := srv.Catalog.AttachMovieFile(movieID, moviePath, 1, "1080p", ""); err != nil {
		t.Fatal(err)
	}
	rec, _ = doJSON(t, h, "DELETE", "/api/v1/movies/"+itoa(movieID)+"?deleteFiles=true", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete with files: %d %s", rec.Code, rec.Body)
	}
	if _, err := os.Stat(moviePath); !os.IsNotExist(err) {
		t.Fatal("file survived deleteFiles=true")
	}
	entries, _ := srv.Catalog.ListHistory(10)
	if len(entries) == 0 || entries[0].Kind != "removed" {
		t.Fatalf("history = %+v", entries)
	}
}

func TestDeleteShowCascades(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	rec, lib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "TV", "path": t.TempDir(), "kind": "shows"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	showID := seedShow(t, srv, int64(lib["id"].(float64)))

	rec, _ = doJSON(t, h, "DELETE", "/api/v1/shows/"+itoa(showID), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete show: %d %s", rec.Code, rec.Body)
	}
	rec, _ = doJSON(t, h, "GET", "/api/v1/shows/"+itoa(showID), nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("deleted show still answers: %d", rec.Code)
	}
	if id, err := srv.Catalog.EpisodeID(showID, 1, 1); err != nil || id != 0 {
		t.Fatalf("episodes survived the cascade: %d %v", id, err)
	}
}
