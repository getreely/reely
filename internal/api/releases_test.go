package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getreely/reely/internal/grab"
)

// fakeProwlarr serves the two calls the client makes: a search returning
// canned releases.
func fakeProwlarr(t *testing.T, releases []map[string]any) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/search" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("X-Api-Key") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_ = json.NewEncoder(w).Encode(releases)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMovieReleasesSearch(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	rec, lib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "Movies", "path": "/data/movies", "kind": "movies"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	movieID := seedMovie(t, srv, int64(lib["id"].(float64)))

	// unconfigured indexer answers 412, mirroring TMDB search
	rec, _ = doJSON(t, h, "GET", "/api/v1/movies/"+itoa(movieID)+"/releases", nil)
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("unconfigured: %d, want 412", rec.Code)
	}

	fake := fakeProwlarr(t, []map[string]any{
		{"title": "The.Matrix.1999.1080p.BluRay.x264", "size": 8 << 30, "protocol": "usenet", "indexer": "nzbs"},
		{"title": "The.Matrix.1999.1080p.REMUX", "size": 30 << 30, "protocol": "usenet", "indexer": "nzbs"},
		{"title": "The.Matrix.Reloaded.2003.1080p.BluRay", "size": 8 << 30, "protocol": "usenet", "indexer": "nzbs"},
	})
	if rec, _ := doJSON(t, h, "PUT", "/api/v1/settings/prowlarr_url",
		map[string]string{"value": fake.URL}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	if rec, _ := doJSON(t, h, "PUT", "/api/v1/settings/prowlarr_api_key",
		map[string]string{"value": "test-key"}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}

	rec, _ = doJSON(t, h, "GET", "/api/v1/movies/"+itoa(movieID)+"/releases", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Releases []grab.ReleaseView `json:"releases"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Releases) != 3 {
		t.Fatalf("releases = %d", len(out.Releases))
	}
	top := out.Releases[0]
	if !top.Accepted || top.Title != "The.Matrix.1999.1080p.BluRay.x264" {
		t.Fatalf("top = %+v", top)
	}
	for _, v := range out.Releases[1:] {
		if v.Accepted {
			t.Fatalf("unexpected acceptance: %+v", v)
		}
		if v.Reason == "" {
			t.Fatalf("rejection without reason: %+v", v)
		}
	}
}

func TestShowReleasesSearch(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	rec, lib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "TV", "path": "/data/tv", "kind": "shows"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	showID := seedShow(t, srv, int64(lib["id"].(float64)))

	fake := fakeProwlarr(t, []map[string]any{
		{"title": "Breaking.Bad.S01E01.1080p.WEB-DL", "size": 2 << 30, "protocol": "usenet", "indexer": "nzbs"},
		{"title": "Breaking.Bad.S01.1080p.WEB-DL", "size": 6 << 30, "protocol": "usenet", "indexer": "nzbs"},
	})
	if rec, _ := doJSON(t, h, "PUT", "/api/v1/settings/prowlarr_url",
		map[string]string{"value": fake.URL}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	if rec, _ := doJSON(t, h, "PUT", "/api/v1/settings/prowlarr_api_key",
		map[string]string{"value": "test-key"}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}

	// missing season → 400
	rec, _ = doJSON(t, h, "GET", "/api/v1/shows/"+itoa(showID)+"/releases", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("no season: %d, want 400", rec.Code)
	}
	// unknown season → 404
	rec, _ = doJSON(t, h, "GET", "/api/v1/shows/"+itoa(showID)+"/releases?season=9", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown season: %d, want 404", rec.Code)
	}

	var out struct {
		Releases []grab.ReleaseView `json:"releases"`
	}
	rec, _ = doJSON(t, h, "GET", "/api/v1/shows/"+itoa(showID)+"/releases?season=1&episode=1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("episode search: %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Releases[0].Accepted || out.Releases[0].Title != "Breaking.Bad.S01E01.1080p.WEB-DL" {
		t.Fatalf("episode top = %+v", out.Releases[0])
	}

	rec, _ = doJSON(t, h, "GET", "/api/v1/shows/"+itoa(showID)+"/releases?season=1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("season search: %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Releases[0].Accepted || out.Releases[0].Title != "Breaking.Bad.S01.1080p.WEB-DL" {
		t.Fatalf("season top = %+v", out.Releases[0])
	}
}
