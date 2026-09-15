package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/metadata"
)

func seedMovie(t *testing.T, srv *Server, libraryID int64) int64 {
	t.Helper()
	id, err := srv.Catalog.UpsertMovie(&metadata.MovieDetail{
		TmdbID: 603, Title: "The Matrix", Year: 1999, Overview: "A hacker learns the truth.",
		Runtime: 136, Genres: []string{"Action", "Science Fiction"},
		ReleaseDate: "1999-03-31", Poster: "/matrix.jpg", Backdrop: "/matrix-wide.jpg",
		ImdbID: "tt0133093",
		Cast: []metadata.Person{
			{TmdbID: 6384, Name: "Keanu Reeves", Character: "Neo", Photo: "/keanu.jpg", Order: 0},
			{TmdbID: 2975, Name: "Laurence Fishburne", Character: "Morpheus", Photo: "/fish.jpg", Order: 1},
		},
	}, libraryID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func seedShow(t *testing.T, srv *Server, libraryID int64) int64 {
	t.Helper()
	id, err := srv.Catalog.UpsertShow(&metadata.ShowDetail{
		TmdbID: 1396, Title: "Breaking Bad", Year: 2008, Overview: "A chemistry teacher breaks bad.",
		Status: "Ended", Genres: []string{"Drama"}, Poster: "/bb.jpg", Backdrop: "/bb-wide.jpg",
		ImdbID: "tt0903747",
		Cast: []metadata.Person{
			{TmdbID: 17419, Name: "Bryan Cranston", Character: "Walter White", Order: 0},
		},
		Seasons: []metadata.SeasonDetail{
			{Number: 1, Name: "Season 1", Episodes: []metadata.EpisodeDetail{
				{TmdbID: 62085, Season: 1, Episode: 1, Title: "Pilot", AirDate: "2008-01-20"},
				{TmdbID: 62086, Season: 1, Episode: 2, Title: "Cat's in the Bag...", AirDate: "2008-01-27"},
			}},
			{Number: 2, Name: "Season 2", Episodes: []metadata.EpisodeDetail{
				{TmdbID: 62092, Season: 2, Episode: 1, Title: "Seven Thirty-Seven", AirDate: "2009-03-08"},
			}},
		},
	}, libraryID)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestMovieDetailAndMonitor(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	rec, lib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "Movies", "path": "/data/movies", "kind": "movies"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	movieID := seedMovie(t, srv, int64(lib["id"].(float64)))
	if err := srv.Catalog.AttachMovieFile(movieID, "/data/movies/matrix.mkv", 4<<30, "1080p", ""); err != nil {
		t.Fatal(err)
	}

	rec, _ = doJSON(t, h, "GET", "/api/v1/movies/9999", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("missing movie: %d, want 404", rec.Code)
	}
	rec, _ = doJSON(t, h, "GET", "/api/v1/movies/zero", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("bad id: %d, want 400", rec.Code)
	}

	var out struct {
		Movie     catalog.MovieDetails `json:"movie"`
		ImageBase string               `json:"imageBase"`
	}
	rec, _ = doJSON(t, h, "GET", "/api/v1/movies/1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get movie: %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	m := out.Movie
	if m.Title != "The Matrix" || m.ImdbID != "tt0133093" || m.Backdrop != "/matrix-wide.jpg" {
		t.Fatalf("movie = %+v", m)
	}
	if m.FilePath == "" || m.Quality != "1080p" {
		t.Fatalf("file state missing: %+v", m)
	}
	if len(m.Cast) != 2 || m.Cast[0].Name != "Keanu Reeves" || m.Cast[0].Character != "Neo" {
		t.Fatalf("cast = %+v", m.Cast)
	}
	if out.ImageBase == "" {
		t.Fatal("imageBase missing")
	}

	// toggle off, read back
	rec, _ = doJSON(t, h, "PUT", "/api/v1/movies/1/monitor", map[string]any{"monitored": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("monitor: %d %s", rec.Code, rec.Body)
	}
	rec, _ = doJSON(t, h, "GET", "/api/v1/movies/1", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Movie.Monitored {
		t.Fatal("monitor toggle did not persist")
	}

	// body must actually carry the flag
	rec, _ = doJSON(t, h, "PUT", "/api/v1/movies/1/monitor", map[string]any{})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty monitor body: %d, want 400", rec.Code)
	}
}

func TestShowDetailAndEpisodeMonitor(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	rec, lib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "TV", "path": "/data/tv", "kind": "shows"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	showID := seedShow(t, srv, int64(lib["id"].(float64)))
	epID, err := srv.Catalog.EpisodeID(showID, 1, 1)
	if err != nil || epID == 0 {
		t.Fatalf("episode id: %d %v", epID, err)
	}
	if err := srv.Catalog.AttachEpisodeFile(epID, "/data/tv/bb-s01e01.mkv", 2<<30, "720p", ""); err != nil {
		t.Fatal(err)
	}

	var out struct {
		Show catalog.ShowDetails `json:"show"`
	}
	rec, _ = doJSON(t, h, "GET", "/api/v1/shows/1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get show: %d %s", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	sh := out.Show
	if sh.Title != "Breaking Bad" || sh.ImdbID != "tt0903747" || sh.Status != "Ended" {
		t.Fatalf("show = %+v", sh.Show)
	}
	if sh.Episodes != 3 || sh.OnDisk != 1 {
		t.Fatalf("counts = %d/%d, want 1/3", sh.OnDisk, sh.Episodes)
	}
	if len(sh.Seasons) != 2 || sh.Seasons[0].Number != 1 || len(sh.Seasons[0].Episodes) != 2 ||
		sh.Seasons[1].Number != 2 || len(sh.Seasons[1].Episodes) != 1 {
		t.Fatalf("seasons = %+v", sh.Seasons)
	}
	if got := sh.Seasons[0].Episodes[0]; got.Title != "Pilot" || got.FilePath == "" || got.Quality != "720p" {
		t.Fatalf("s01e01 = %+v", got)
	}
	if len(sh.Cast) != 1 || sh.Cast[0].Character != "Walter White" {
		t.Fatalf("cast = %+v", sh.Cast)
	}

	// show-level toggle cascades to episodes
	rec, _ = doJSON(t, h, "PUT", "/api/v1/shows/1/monitor", map[string]any{"monitored": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("show monitor: %d %s", rec.Code, rec.Body)
	}
	rec, _ = doJSON(t, h, "GET", "/api/v1/shows/1", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Show.Monitored {
		t.Fatal("show monitor did not persist")
	}
	for _, season := range out.Show.Seasons {
		for _, e := range season.Episodes {
			if e.Monitored {
				t.Fatalf("episode s%02de%02d still monitored after show toggle off", e.Season, e.Episode)
			}
		}
	}

	// re-arm one episode
	rec, _ = doJSON(t, h, "PUT", "/api/v1/episodes/1/monitor", map[string]any{"monitored": true})
	if rec.Code != http.StatusOK {
		t.Fatalf("episode monitor: %d %s", rec.Code, rec.Body)
	}
	rec, _ = doJSON(t, h, "GET", "/api/v1/shows/1", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if !out.Show.Seasons[0].Episodes[0].Monitored {
		t.Fatal("episode re-arm did not persist")
	}
	if out.Show.Seasons[0].Episodes[1].Monitored {
		t.Fatal("episode toggle bled into its neighbor")
	}
}

// A season toggle flips exactly its own episodes — the middle ground
// between the show switch and per-episode rows.
func TestSeasonMonitor(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	rec, lib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "TV", "path": "/data/tv", "kind": "shows"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	showID := seedShow(t, srv, int64(lib["id"].(float64)))

	rec, _ = doJSON(t, h, "PUT", "/api/v1/shows/"+itoa(showID)+"/seasons/1/monitor",
		map[string]any{"monitored": false})
	if rec.Code != http.StatusOK {
		t.Fatalf("season monitor: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Show catalog.ShowDetails `json:"show"`
	}
	rec, _ = doJSON(t, h, "GET", "/api/v1/shows/"+itoa(showID), nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	for _, se := range out.Show.Seasons {
		for _, e := range se.Episodes {
			if se.Number == 1 && e.Monitored {
				t.Fatalf("s01e%02d still monitored after season toggle", e.Episode)
			}
			if se.Number == 2 && !e.Monitored {
				t.Fatalf("s02e%02d lost monitoring from another season's toggle", e.Episode)
			}
		}
	}
	if !out.Show.Monitored {
		t.Fatal("show-level flag must not follow a season toggle")
	}

	rec, _ = doJSON(t, h, "PUT", "/api/v1/shows/"+itoa(showID)+"/seasons/9/monitor",
		map[string]any{"monitored": false})
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown season: %d, want 404", rec.Code)
	}
}

// A title in a library a scoped user wasn't granted answers 404 — the id
// alone must not confirm anything exists behind it.
func TestDetailPagesAreLibraryScoped(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	rec, movieLib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "Movies", "path": "/data/movies", "kind": "movies"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	rec, showLib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "TV", "path": "/data/tv", "kind": "shows"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	movieID := seedMovie(t, srv, int64(movieLib["id"].(float64)))
	showID := seedShow(t, srv, int64(showLib["id"].(float64)))
	epID, err := srv.Catalog.EpisodeID(showID, 1, 1)
	if err != nil || epID == 0 {
		t.Fatalf("episode id: %d %v", epID, err)
	}

	if rec, _ := doJSON(t, h, "POST", "/api/v1/users",
		map[string]any{"username": "root", "password": "correct horse battery"}); rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	root := login(t, h, "root", "correct horse battery")

	// sam may only see the show library
	req := httptest.NewRequest("POST", "/api/v1/users", bytes.NewReader(mustJSON(map[string]any{
		"username": "sam", "password": "another passphrase", "role": "user",
		"libraryIds": []any{showLib["id"]},
	})))
	req.AddCookie(root)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusCreated {
		t.Fatalf("create scoped user: %d %s", w.Code, w.Body)
	}
	sam := login(t, h, "sam", "another passphrase")

	get := func(c *http.Cookie, method, path string, body []byte) int {
		req := httptest.NewRequest(method, path, bytes.NewReader(body))
		req.AddCookie(c)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}

	moviePath := "/api/v1/movies/" + itoa(movieID)
	showPath := "/api/v1/shows/" + itoa(showID)
	if code := get(sam, "GET", moviePath, nil); code != http.StatusNotFound {
		t.Errorf("scoped GET movie: %d, want 404", code)
	}
	if code := get(sam, "PUT", moviePath+"/monitor", mustJSON(map[string]any{"monitored": false})); code != http.StatusNotFound {
		t.Errorf("scoped PUT movie monitor: %d, want 404", code)
	}
	if code := get(sam, "GET", showPath, nil); code != http.StatusOK {
		t.Errorf("scoped GET granted show: %d, want 200", code)
	}
	if code := get(sam, "PUT", "/api/v1/episodes/"+itoa(epID)+"/monitor",
		mustJSON(map[string]any{"monitored": false})); code != http.StatusOK {
		t.Errorf("scoped PUT episode monitor in granted library: %d, want 200", code)
	}
	if code := get(root, "GET", moviePath, nil); code != http.StatusOK {
		t.Errorf("admin GET movie: %d, want 200", code)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
