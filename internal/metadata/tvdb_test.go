package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// stubTVDB fakes the v4 API: login mints a token, every data endpoint
// checks it. Modeled on the real payloads.
func stubTVDB(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	logins := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/login" {
			var body struct {
				APIKey string `json:"apikey"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.APIKey != "good-key" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			logins.Add(1)
			_, _ = fmt.Fprintf(w, `{"status":"success","data":{"token":"tok-%d"}}`, logins.Load())
			return
		}
		if r.Header.Get("Authorization") != "Bearer tok-"+fmt.Sprint(logins.Load()) {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.URL.Path {
		case "/search":
			fmt.Fprint(w, `{"data":[
				{"tvdb_id":"80552","name":"Kitchen Nightmares (US)","year":"2007","overview":"Gordon fixes restaurants.","image_url":"https://artworks.thetvdb.com/banners/kn.jpg","country":"usa"},
				{"tvdb_id":"74584","name":"Ramsay's Kitchen Nightmares","year":"2004","overview":"","image_url":"","country":"gbr"},
				{"tvdb_id":"","name":"broken row","year":"x"}]}`)
		case "/series/80552/extended":
			fmt.Fprint(w, `{"data":{
				"name":"Kitchen Nightmares (US)","image":"https://artworks.thetvdb.com/banners/kn.jpg",
				"firstAired":"2007-09-19","year":"2007","status":{"name":"Continuing"},
				"genres":[{"name":"Reality"}],
				"remoteIds":[{"id":"11294","sourceName":"TheMovieDB.com"},{"id":"tt0983514","sourceName":"IMDB"}],
				"translations":{"overviewTranslations":[
					{"language":"fra","overview":"non"},{"language":"eng","overview":"Gordon fixes restaurants."}]}}}`)
		case "/series/80552/episodes/default":
			// two pages, with a specials row to skip
			if r.URL.Query().Get("page") == "0" {
				fmt.Fprint(w, `{"data":{"episodes":[
					{"id":1,"seasonNumber":0,"number":1,"name":"Special","aired":"2007-09-30","runtime":45},
					{"id":2,"seasonNumber":1,"number":1,"name":"Peter's","aired":"2007-09-19","runtime":42},
					{"id":3,"seasonNumber":1,"number":2,"name":"Dillon's","aired":"2007-09-26","runtime":42}]},
					"links":{"next":"page1"}}`)
				return
			}
			fmt.Fprint(w, `{"data":{"episodes":[
				{"id":4,"seasonNumber":7,"number":1,"name":"Bel Aire","aired":"2023-09-25","runtime":43}]},
				"links":{"next":""}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, logins
}

func TestTVDBSearchAndShow(t *testing.T) {
	srv, _ := stubTVDB(t)
	tv := NewTVDB(func() string { return "good-key" })
	tv.SetBaseURL(srv.URL)

	hits, err := tv.SearchShows(context.Background(), "Kitchen Nightmares")
	if err != nil {
		t.Fatal(err)
	}
	// the id-less row is dropped, not passed along broken
	if len(hits) != 2 {
		t.Fatalf("hits = %+v", hits)
	}
	h := hits[0]
	if h.TvdbID != 80552 || h.Kind != "show" || h.Title != "Kitchen Nightmares (US)" ||
		h.Year != 2007 || h.Poster != "https://artworks.thetvdb.com/banners/kn.jpg" {
		t.Fatalf("first hit = %+v", h)
	}

	d, err := tv.Show(context.Background(), 80552)
	if err != nil {
		t.Fatal(err)
	}
	if d.TvdbID != 80552 || d.TmdbID != 11294 || d.ImdbID != "tt0983514" ||
		d.Title != "Kitchen Nightmares (US)" || d.Year != 2007 || d.Status != "Continuing" {
		t.Fatalf("show = %+v", d)
	}
	if d.Overview != "Gordon fixes restaurants." {
		t.Fatalf("overview = %q — the English translation must win", d.Overview)
	}
	// specials skipped; both pages merged; seasons in aired order
	if len(d.Seasons) != 2 || d.Seasons[0].Number != 1 || d.Seasons[1].Number != 7 {
		t.Fatalf("seasons = %+v", d.Seasons)
	}
	if len(d.Seasons[0].Episodes) != 2 || d.Seasons[0].Episodes[0].Title != "Peter's" {
		t.Fatalf("s1 = %+v", d.Seasons[0].Episodes)
	}
	if d.Seasons[1].Episodes[0].AirDate != "2023-09-25" || d.Seasons[1].Episodes[0].Runtime != 43 {
		t.Fatalf("s7e1 = %+v", d.Seasons[1].Episodes[0])
	}
}

// A token TVDB revokes early must trigger exactly one re-login, not a
// failure and not a login storm.
func TestTVDBReloginOnExpiredToken(t *testing.T) {
	srv, logins := stubTVDB(t)
	tv := NewTVDB(func() string { return "good-key" })
	tv.SetBaseURL(srv.URL)

	if _, err := tv.SearchShows(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	// the server mints tok-2 on the next login; the cached tok-1 now 401s
	logins.Add(1)
	logins.Add(-1) // no-op, keep the counter honest
	tv.token = "stale"
	if _, err := tv.SearchShows(context.Background(), "x"); err != nil {
		t.Fatalf("re-login didn't recover: %v", err)
	}
	if logins.Load() != 2 {
		t.Fatalf("logins = %d, want 2", logins.Load())
	}
}

func TestTVDBUnconfigured(t *testing.T) {
	tv := NewTVDB(func() string { return "" })
	if tv.Configured() {
		t.Fatal("empty key reads as configured")
	}
	if _, err := tv.SearchShows(context.Background(), "x"); err == nil {
		t.Fatal("search without a key must error, not hang")
	}
}
