package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getreely/reely/internal/metadata"
)

// The dashboard aggregates the libraries correctly and stays admin-only.
func TestStats(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()

	rec, lib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "Movies", "path": "/data/movies", "kind": "movies"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	libID := int64(lib["id"].(float64))
	movieID, err := srv.Catalog.UpsertMovie(&metadata.MovieDetail{
		TmdbID: 27205, Title: "Inception", Year: 2010, Runtime: 148,
	}, libID)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Catalog.AttachMovieFile(movieID, "/data/movies/inception.mkv", 9<<30, "1080p", ""); err != nil {
		t.Fatal(err)
	}
	// a second, monitored and still missing
	if _, err := srv.Catalog.UpsertMovie(&metadata.MovieDetail{
		TmdbID: 78, Title: "Blade Runner", Year: 1982, Runtime: 117,
	}, libID); err != nil {
		t.Fatal(err)
	}
	if err := srv.Catalog.AddHistory("grabbed", movieID, 0, 0, "{}"); err != nil {
		t.Fatal(err)
	}
	if err := srv.Catalog.AddHistory("imported", movieID, 0, 0, "{}"); err != nil {
		t.Fatal(err)
	}

	rec, out := doJSON(t, h, "GET", "/api/v1/stats", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("stats: %d %s", rec.Code, rec.Body)
	}
	if out["movies"] != 2.0 || out["moviesOnDisk"] != 1.0 {
		t.Fatalf("movie counts wrong: %v", out)
	}
	if out["totalBytes"] != float64(9<<30) {
		t.Fatalf("totalBytes = %v", out["totalBytes"])
	}
	if out["grabs30d"] != 1.0 || out["imports30d"] != 1.0 || out["failures30d"] != 0.0 {
		t.Fatalf("history counts wrong: %v", out)
	}
	libs, ok := out["libraries"].([]any)
	if !ok || len(libs) != 1 {
		t.Fatalf("libraries = %v", out["libraries"])
	}
	l0 := libs[0].(map[string]any)
	if l0["titles"] != 2.0 || l0["onDisk"] != 1.0 || l0["missing"] != 1.0 || l0["bytes"] != float64(9<<30) {
		t.Fatalf("library stats wrong: %v", l0)
	}

	// admin-only: a plain user gets 403
	if rec, _ := doJSON(t, h, "POST", "/api/v1/users",
		map[string]any{"username": "root", "password": "correct horse battery"}); rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	root := login(t, h, "root", "correct horse battery")
	req := httptest.NewRequest("POST", "/api/v1/users", bytes.NewReader(mustJSON(map[string]any{
		"username": "sam", "password": "another passphrase", "role": "user", "libraryIds": []any{lib["id"]},
	})))
	req.AddCookie(root)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatal(w.Body)
	}
	sam := login(t, h, "sam", "another passphrase")
	req = httptest.NewRequest("GET", "/api/v1/stats", nil)
	req.AddCookie(sam)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusForbidden {
		t.Fatalf("plain user reads stats: %d", w.Code)
	}
}
