package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeTMDB serves the detail endpoints the add flow fetches.
func fakeTMDB(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/movie/603":
			_, _ = w.Write([]byte(`{"title":"The Matrix","release_date":"1999-03-31",
				"overview":"o","runtime":136,"poster_path":"/m.jpg","backdrop_path":"/mb.jpg",
				"genres":[{"name":"Action"}],"external_ids":{"imdb_id":"tt0133093"},
				"credits":{"cast":[]}}`))
		// a second film, so a test can move a row from one to the other
		case r.URL.Path == "/movie/604":
			_, _ = w.Write([]byte(`{"title":"The Matrix Reloaded","release_date":"2003-05-15",
				"overview":"o","runtime":138,"poster_path":"/m2.jpg","backdrop_path":"/mb2.jpg",
				"genres":[{"name":"Action"}],"external_ids":{"imdb_id":"tt0234215"},
				"credits":{"cast":[]}}`))
		case r.URL.Path == "/tv/1396":
			_, _ = w.Write([]byte(`{"name":"Breaking Bad","first_air_date":"2008-01-20",
				"overview":"o","status":"Ended","poster_path":"/b.jpg","backdrop_path":"/bb.jpg",
				"genres":[{"name":"Drama"}],"external_ids":{"imdb_id":"tt0903747"},
				"credits":{"cast":[]},"seasons":[{"season_number":1}]}`))
		case strings.HasPrefix(r.URL.Path, "/tv/1396/season/1"):
			_, _ = w.Write([]byte(`{"name":"Season 1","episodes":[
				{"id":1,"episode_number":1,"name":"Pilot","overview":"","air_date":"2008-01-20","runtime":58},
				{"id":2,"episode_number":2,"name":"Two","overview":"","air_date":"2008-01-27","runtime":48}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestAddFromSearchQueuesASearch(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	srv.TMDB.SetBaseURL(fakeTMDB(t).URL)
	if rec, _ := doJSON(t, h, "PUT", "/api/v1/settings/tmdb_api_key",
		map[string]string{"value": "k"}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}

	rec, lib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "Movies", "path": "/data/movies", "kind": "movies"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	movieLib := int64(lib["id"].(float64))
	rec, lib = doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "TV", "path": "/data/tv", "kind": "shows"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	showLib := int64(lib["id"].(float64))

	// wrong-kind library bounces
	rec, _ = doJSON(t, h, "POST", "/api/v1/movies", map[string]any{"tmdbId": 603, "libraryId": showLib})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("movie into show library: %d, want 400", rec.Code)
	}

	rec, out := doJSON(t, h, "POST", "/api/v1/movies", map[string]any{"tmdbId": 603, "libraryId": movieLib})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add movie: %d %s", rec.Code, rec.Body)
	}
	var added struct {
		Movie struct {
			ID        int64  `json:"id"`
			Title     string `json:"title"`
			Monitored bool   `json:"monitored"`
			LibraryID int64  `json:"libraryId"`
		} `json:"movie"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &added); err != nil {
		t.Fatal(err)
	}
	if added.Movie.Title != "The Matrix" || !added.Movie.Monitored || added.Movie.LibraryID != movieLib {
		t.Fatalf("added = %+v", added.Movie)
	}
	if srv.Grab.QueueLen() != 1 {
		t.Fatalf("queue = %d, want the added movie's search", srv.Grab.QueueLen())
	}
	_ = out

	rec, _ = doJSON(t, h, "POST", "/api/v1/shows", map[string]any{"tmdbId": 1396, "libraryId": showLib})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add show: %d %s", rec.Code, rec.Body)
	}
	// both aired episodes queued behind the movie
	if srv.Grab.QueueLen() != 3 {
		t.Fatalf("queue = %d, want 3", srv.Grab.QueueLen())
	}
}

func TestMonitorTogglesQueueSearches(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	rec, lib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "Movies", "path": "/data/movies", "kind": "movies"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	movieID := seedMovie(t, srv, int64(lib["id"].(float64)))

	// off → no queueing; on while missing → queued
	if rec, _ := doJSON(t, h, "PUT", "/api/v1/movies/"+itoa(movieID)+"/monitor",
		map[string]any{"monitored": false}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	if srv.Grab.QueueLen() != 0 {
		t.Fatal("unmonitor queued a search")
	}
	if rec, _ := doJSON(t, h, "PUT", "/api/v1/movies/"+itoa(movieID)+"/monitor",
		map[string]any{"monitored": true}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	if srv.Grab.QueueLen() != 1 {
		t.Fatalf("queue = %d after monitoring a missing movie", srv.Grab.QueueLen())
	}
}
