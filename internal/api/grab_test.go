package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// fakeSAB accepts every addurl and remembers the last query.
func fakeSAB(t *testing.T) (*httptest.Server, *map[string]string) {
	t.Helper()
	got := map[string]string{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for k, v := range r.URL.Query() {
			got[k] = v[0]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": true, "nzo_ids": []string{"nzo_1"}})
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestGrabEndpoints(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	rec, lib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "Movies", "path": "/data/movies", "kind": "movies"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	movieID := seedMovie(t, srv, int64(lib["id"].(float64)))
	grabBody := map[string]any{
		"title": "The.Matrix.1999.1080p.BluRay", "downloadUrl": "https://indexer/nzb/9",
		"indexer": "nzbs", "size": 8 << 30,
	}

	// SAB unconfigured → 412
	rec, _ = doJSON(t, h, "POST", "/api/v1/movies/"+itoa(movieID)+"/grab", grabBody)
	if rec.Code != http.StatusPreconditionFailed {
		t.Fatalf("unconfigured: %d, want 412", rec.Code)
	}

	sab, got := fakeSAB(t)
	if rec, _ := doJSON(t, h, "PUT", "/api/v1/settings/sab_url",
		map[string]string{"value": sab.URL}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	if rec, _ := doJSON(t, h, "PUT", "/api/v1/settings/sab_api_key",
		map[string]string{"value": "sab-key"}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}

	// bad request bounces before SAB is reached
	rec, _ = doJSON(t, h, "POST", "/api/v1/movies/"+itoa(movieID)+"/grab",
		map[string]any{"title": "x", "downloadUrl": "ftp://nope"})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad grab: %d, want 400", rec.Code)
	}

	rec, _ = doJSON(t, h, "POST", "/api/v1/movies/"+itoa(movieID)+"/grab", grabBody)
	if rec.Code != http.StatusOK {
		t.Fatalf("grab movie: %d %s", rec.Code, rec.Body)
	}
	if (*got)["cat"] != "movies" || (*got)["nzbname"] != "The.Matrix.1999.1080p.BluRay" {
		t.Fatalf("SAB got %v", *got)
	}

	// the grab is on the record
	entries, err := srv.Catalog.ListHistory(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Kind != "grabbed" || entries[0].MovieID != movieID {
		t.Fatalf("history = %+v", entries)
	}

	// shows: an episode grab files under the TV category
	rec, tvLib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "TV", "path": "/data/tv", "kind": "shows"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	showID := seedShow(t, srv, int64(tvLib["id"].(float64)))
	rec, _ = doJSON(t, h, "POST", "/api/v1/shows/"+itoa(showID)+"/grab", map[string]any{
		"title": "Breaking.Bad.S01E01.1080p.WEB", "downloadUrl": "https://indexer/nzb/10",
		"season": 1, "episode": 1,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("grab episode: %d %s", rec.Code, rec.Body)
	}
	if (*got)["cat"] != "tvshows" {
		t.Fatalf("SAB got %v", *got)
	}
	rec, _ = doJSON(t, h, "POST", "/api/v1/shows/"+itoa(showID)+"/grab", map[string]any{
		"title": "x", "downloadUrl": "https://indexer/nzb/11",
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("grab without season: %d, want 400", rec.Code)
	}
}
