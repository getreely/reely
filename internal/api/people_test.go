package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type personResp struct {
	Person struct {
		TmdbID    int    `json:"tmdbId"`
		Name      string `json:"name"`
		Biography string `json:"biography"`
		KnownFor  string `json:"knownFor"`
		Photo     string `json:"photo"`
	} `json:"person"`
	Credits []struct {
		Kind    string `json:"kind"`
		TmdbID  int    `json:"tmdbId"`
		Title   string `json:"title"`
		Year    int    `json:"year"`
		LocalID int64  `json:"localId"`
		OnDisk  bool   `json:"onDisk"`
	} `json:"credits"`
}

func getPerson(t *testing.T, h http.Handler, id string) (int, personResp) {
	t.Helper()
	rec, _ := doJSON(t, h, "GET", "/api/v1/people/"+id, nil)
	var out personResp
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
	}
	return rec.Code, out
}

func TestPersonMergesTMDBWithLibrary(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/person/6384" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"name":"Keanu Reeves","biography":"An actor.",
			"birthday":"1964-09-02","place_of_birth":"Beirut, Lebanon",
			"known_for_department":"Acting","profile_path":"/keanu.jpg",
			"combined_credits":{"cast":[
				{"media_type":"movie","id":603,"title":"The Matrix","release_date":"1999-03-31",
					"character":"Neo","poster_path":"/matrix.jpg"},
				{"media_type":"movie","id":245891,"title":"John Wick","release_date":"2014-10-24",
					"character":"John Wick","poster_path":"/wick.jpg"},
				{"media_type":"tv","id":2337,"name":"Swedish Dicks","first_air_date":"2016-01-01",
					"character":"Tex","poster_path":""},
				{"media_type":"movie","id":603,"title":"The Matrix","release_date":"1999-03-31",
					"character":"Additional Voice","poster_path":"/matrix.jpg"}
			]}}`))
	}))
	t.Cleanup(fake.Close)
	srv.TMDB.SetBaseURL(fake.URL)
	if rec, _ := doJSON(t, h, "PUT", "/api/v1/settings/tmdb_api_key",
		map[string]string{"value": "k"}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}

	rec, lib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "Movies", "path": "/data/movies", "kind": "movies"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	movieID := seedMovie(t, srv, int64(lib["id"].(float64)))
	if err := srv.Catalog.AttachMovieFile(movieID, "/x/matrix.mkv", 1, "1080p", ""); err != nil {
		t.Fatal(err)
	}

	code, out := getPerson(t, h, "6384")
	if code != http.StatusOK {
		t.Fatalf("person: %d", code)
	}
	if out.Person.Name != "Keanu Reeves" || out.Person.Biography != "An actor." {
		t.Fatalf("person = %+v", out.Person)
	}
	// duplicate roles on one title collapse into one card
	if len(out.Credits) != 3 {
		t.Fatalf("credits = %d, want 3: %+v", len(out.Credits), out.Credits)
	}
	// newest first: Swedish Dicks 2016, John Wick 2014, The Matrix 1999
	if out.Credits[0].Title != "Swedish Dicks" || out.Credits[2].Title != "The Matrix" {
		t.Fatalf("order = %+v", out.Credits)
	}
	matrix := out.Credits[2]
	if matrix.LocalID != movieID || !matrix.OnDisk {
		t.Fatalf("library credit not linked: %+v", matrix)
	}
	if out.Credits[1].Title != "John Wick" || out.Credits[1].LocalID != 0 {
		t.Fatalf("non-library credit carries a local id: %+v", out.Credits[1])
	}
}

func TestPersonStoredFallbackWithoutTMDB(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	rec, lib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "Movies", "path": "/data/movies", "kind": "movies"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	movieID := seedMovie(t, srv, int64(lib["id"].(float64))) // bills Keanu (6384)

	code, out := getPerson(t, h, "6384")
	if code != http.StatusOK {
		t.Fatalf("stored person: %d", code)
	}
	if out.Person.Name != "Keanu Reeves" || out.Person.Photo != "/keanu.jpg" {
		t.Fatalf("person = %+v", out.Person)
	}
	if len(out.Credits) != 1 || out.Credits[0].LocalID != movieID || out.Credits[0].Kind != "movie" {
		t.Fatalf("credits = %+v", out.Credits)
	}

	// a person nobody in the library bills is a 404, not an empty page
	if code, _ := getPerson(t, h, "99999"); code != http.StatusNotFound {
		t.Fatalf("unknown person: %d, want 404", code)
	}
}
