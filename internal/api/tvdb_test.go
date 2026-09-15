package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/getreely/reely/internal/metadata"
)

// stubTVDBServer speaks enough of the v4 API for the add, search, and
// migration flows: two Kitchen Nightmares (the US series with a 2023-era
// S7, and the UK original) — the split-show scenario end to end.
func stubTVDBServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login":
			fmt.Fprint(w, `{"data":{"token":"tok"}}`)
		case "/search":
			fmt.Fprint(w, `{"data":[
				{"tvdb_id":"80552","name":"Kitchen Nightmares (US)","year":"2007","overview":"US","image_url":"https://artworks.thetvdb.com/kn.jpg"},
				{"tvdb_id":"74584","name":"Ramsay's Kitchen Nightmares","year":"2004","overview":"UK","image_url":""}]}`)
		case "/series/80552/extended":
			fmt.Fprint(w, `{"data":{"name":"Kitchen Nightmares (US)","image":"https://artworks.thetvdb.com/kn.jpg",
				"firstAired":"2007-09-19","year":"2007","status":{"name":"Continuing"},"genres":[{"name":"Reality"}],
				"remoteIds":[{"id":"11294","sourceName":"TheMovieDB.com"}],
				"translations":{"overviewTranslations":[{"language":"eng","overview":"Gordon."}]}}}`)
		case "/series/80552/episodes/default":
			fmt.Fprint(w, `{"data":{"episodes":[
				{"id":1,"seasonNumber":1,"number":1,"name":"Peter's","aired":"2007-09-19","runtime":42},
				{"id":2,"seasonNumber":1,"number":2,"name":"Dillon's","aired":"2007-09-26","runtime":42},
				{"id":3,"seasonNumber":7,"number":1,"name":"Bel Aire","aired":"2023-09-25","runtime":43}]},
				"links":{"next":""}}`)
		case "/series/74584/extended":
			fmt.Fprint(w, `{"data":{"name":"Ramsay's Kitchen Nightmares","image":"",
				"firstAired":"2004-04-27","year":"2004","status":{"name":"Ended"},"genres":[],
				"remoteIds":[],"translations":{"overviewTranslations":[]}}}`)
		case "/series/74584/episodes/default":
			fmt.Fprint(w, `{"data":{"episodes":[
				{"id":9,"seasonNumber":1,"number":1,"name":"Bonapartes","aired":"2004-04-27","runtime":48}]},
				"links":{"next":""}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

// enableTVDB points the test server's TVDB client at the stub.
func enableTVDB(t *testing.T, srv *Server) {
	t.Helper()
	if err := srv.Settings.Set("tvdb_api_key", "good-key"); err != nil {
		t.Fatal(err)
	}
	srv.TVDB.SetBaseURL(stubTVDBServer(t).URL)
}

// With a TVDB key, the single search bar's show half comes from TheTVDB —
// one Kitchen Nightmares (US), carrying its tvdbId for the add flow.
func TestSearchRoutesShowsThroughTVDB(t *testing.T) {
	srv := testServer(t)
	enableTVDB(t, srv)
	if err := srv.Settings.Set("tmdb_api_key", "k"); err != nil {
		t.Fatal(err) // handleSearch gates on the TMDB key either way
	}
	h := srv.Handler()

	rec, _ := doJSON(t, h, "GET", "/api/v1/search?q=kitchen+nightmares&kind=show", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("search: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Results []metadata.SearchResult `json:"results"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 2 || out.Results[0].TvdbID != 80552 || out.Results[0].Title != "Kitchen Nightmares (US)" {
		t.Fatalf("results = %+v", out.Results)
	}
}

// Adding by TVDB id stores a TVDB-sourced show with its native tree, and
// adding the same series again dedupes onto the same row.
func TestAddShowByTvdbID(t *testing.T) {
	srv := testServer(t)
	enableTVDB(t, srv)
	h := srv.Handler()
	lib, err := srv.Catalog.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}

	rec, _ := doJSON(t, h, "POST", "/api/v1/shows",
		map[string]any{"tvdbId": 80552, "libraryId": lib.ID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Show struct {
			ID     int64  `json:"id"`
			Title  string `json:"title"`
			TvdbID int    `json:"tvdbId"`
			TmdbID int    `json:"tmdbId"`
			Source string `json:"source"`
		} `json:"show"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Show.Source != "tvdb" || out.Show.TvdbID != 80552 || out.Show.TmdbID != 11294 ||
		out.Show.Title != "Kitchen Nightmares (US)" {
		t.Fatalf("show = %+v", out.Show)
	}
	sh, err := srv.Catalog.GetShow(out.Show.ID)
	if err != nil {
		t.Fatal(err)
	}
	// the native tree: S1 and S7, the revival continuing the one series
	if len(sh.Seasons) != 2 || sh.Seasons[0].Number != 1 || sh.Seasons[1].Number != 7 {
		t.Fatalf("seasons = %+v", sh.Seasons)
	}

	// adding again lands on the same row, not a duplicate
	rec, _ = doJSON(t, h, "POST", "/api/v1/shows",
		map[string]any{"tvdbId": 80552, "libraryId": lib.ID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("re-add: %d %s", rec.Code, rec.Body)
	}
	var again struct {
		Show struct {
			ID int64 `json:"id"`
		} `json:"show"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &again); err != nil {
		t.Fatal(err)
	}
	if again.Show.ID != out.Show.ID {
		t.Fatalf("re-add made a new row: %d vs %d", again.Show.ID, out.Show.ID)
	}
}

// The migration sweep: a show with a stored TVDB id moves automatically —
// source flips, title takes TVDB's form, the revival's season arrives
// unmonitored, and the manual numbering overrides are cleared. A show
// with no id lands in the unresolved list; the manual endpoint finishes
// it. A second entry claiming the same series is refused as a duplicate.
func TestTvdbMigrationSweep(t *testing.T) {
	srv := testServer(t)
	enableTVDB(t, srv)
	h := srv.Handler()
	lib, err := srv.Catalog.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	knID, err := srv.Catalog.UpsertShow(&metadata.ShowDetail{
		TmdbID: 11294, Title: "Kitchen Nightmares", Year: 2007, TvdbID: 80552,
		Seasons: []metadata.SeasonDetail{{Number: 1, Name: "Season 1", Episodes: []metadata.EpisodeDetail{
			{TmdbID: 1, Season: 1, Episode: 1, Title: "Peter's", AirDate: "2007-09-19"},
		}}},
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	// a stale manual offset from the pre-TVDB days must not survive the
	// migration — a native tree with an offset would break every search
	if err := srv.Catalog.SetShowNumbering(knID, 6, 80552); err != nil {
		t.Fatal(err)
	}
	mysteryID, err := srv.Catalog.UpsertShow(&metadata.ShowDetail{
		TmdbID: 999, Title: "Totally Unfindable", Year: 2001,
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}

	rec, _ := doJSON(t, h, "POST", "/api/v1/system/tvdb-migration", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("migrate: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Migrated   []migrationRow `json:"migrated"`
		Unresolved []migrationRow `json:"unresolved"`
		Failed     []migrationRow `json:"failed"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Migrated) != 1 || out.Migrated[0].ID != knID || out.Migrated[0].NewTitle != "Kitchen Nightmares (US)" {
		t.Fatalf("migrated = %+v", out.Migrated)
	}
	if out.Migrated[0].NewSeasons != 1 {
		t.Fatalf("newSeasons = %d, want the revival's S7", out.Migrated[0].NewSeasons)
	}
	if len(out.Unresolved) != 1 || out.Unresolved[0].ID != mysteryID {
		t.Fatalf("unresolved = %+v", out.Unresolved)
	}

	sh, err := srv.Catalog.GetShow(knID)
	if err != nil {
		t.Fatal(err)
	}
	if sh.Source != "tvdb" || sh.Title != "Kitchen Nightmares (US)" || sh.TvdbID != 80552 {
		t.Fatalf("after migrate: %+v", sh.Show)
	}
	if sh.SeasonOffset != 0 {
		t.Fatalf("season offset survived migration: %d", sh.SeasonOffset)
	}
	for _, se := range sh.Seasons {
		for _, ep := range se.Episodes {
			if se.Number == 7 && ep.Monitored {
				t.Fatal("a migration-discovered season arrived monitored — that's an unasked download sweep")
			}
			if se.Number == 1 && !ep.Monitored {
				t.Fatal("an existing season lost its monitoring")
			}
		}
	}

	// manual fix for the unresolved one
	rec, body := doJSON(t, h, "POST", "/api/v1/shows/"+strconv.FormatInt(mysteryID, 10)+"/migrate-tvdb",
		map[string]any{"tvdbId": 74584})
	if rec.Code != http.StatusOK || body["newTitle"] != "Ramsay's Kitchen Nightmares" {
		t.Fatalf("manual migrate: %d %v", rec.Code, body)
	}

	// a split duplicate pointing at an already-claimed series is refused
	dupID, err := srv.Catalog.UpsertShow(&metadata.ShowDetail{
		TmdbID: 235884, Title: "Kitchen Nightmares", Year: 2023,
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	rec, body = doJSON(t, h, "POST", "/api/v1/shows/"+strconv.FormatInt(dupID, 10)+"/migrate-tvdb",
		map[string]any{"tvdbId": 80552})
	if rec.Code != http.StatusConflict {
		t.Fatalf("duplicate migrate: %d %v", rec.Code, body)
	}
}

// With TVDB enabled, an add that arrives with only a TMDB id (Explore,
// lists, an old preview link) is steered onto the TVDB series its
// external id names — the split entry lands on the one continuing series.
func TestAddShowByTmdbIDSteersToTVDB(t *testing.T) {
	srv := testServer(t)
	enableTVDB(t, srv)
	// a TMDB stub that knows the show and its external TVDB id
	tmdbStub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/tv/11294":
			fmt.Fprint(w, `{"name":"Kitchen Nightmares","first_air_date":"2007-09-19","status":"Ended",
				"credits":{"cast":[]},"genres":[],"external_ids":{"tvdb_id":80552},"seasons":[{"season_number":1}]}`)
		case "/tv/11294/season/1":
			fmt.Fprint(w, `{"name":"Season 1","episodes":[
				{"id":1,"episode_number":1,"name":"Peter's","air_date":"2007-09-19","runtime":42}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(tmdbStub.Close)
	if err := srv.Settings.Set("tmdb_api_key", "k"); err != nil {
		t.Fatal(err)
	}
	srv.TMDB.SetBaseURL(tmdbStub.URL)
	h := srv.Handler()
	lib, err := srv.Catalog.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}

	rec, _ := doJSON(t, h, "POST", "/api/v1/shows",
		map[string]any{"tmdbId": 11294, "libraryId": lib.ID})
	if rec.Code != http.StatusCreated {
		t.Fatalf("add: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Show struct {
			Source string `json:"source"`
			TvdbID int    `json:"tvdbId"`
			Title  string `json:"title"`
		} `json:"show"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Show.Source != "tvdb" || out.Show.TvdbID != 80552 || out.Show.Title != "Kitchen Nightmares (US)" {
		t.Fatalf("show = %+v — the TMDB add wasn't steered onto TVDB", out.Show)
	}
}
