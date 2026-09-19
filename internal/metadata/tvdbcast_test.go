package metadata

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// castTVDB serves one series plus the people endpoint, counting how
// often the per-person lookup is reached — that count is the whole point
// of the hybrid.
func castTVDB(t *testing.T, remoteIDs string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	people := &atomic.Int32{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login":
			fmt.Fprint(w, `{"status":"success","data":{"token":"tok"}}`)
		case "/series/999/extended":
			_, _ = fmt.Fprintf(w, `{"data":{
				"name":"MobLand","year":"2025","status":{"name":"Continuing"},
				"remoteIds":[%s],
				"characters":[
					{"peopleId":373728,"peopleType":"Actor","personName":"Tom Hardy",
					 "name":"Harry Da Souza","personImgURL":"https://art/th.jpg","sort":1},
					{"peopleId":296967,"peopleType":"Actor","personName":"Helen Mirren",
					 "name":"Maeve Harrigan","personImgURL":"https://art/hm.jpg","sort":2},
					{"peopleId":378153,"peopleType":"Actor","personName":"Geoff Bell",
					 "name":"Richie Stevenson","sort":8},
					{"peopleId":999001,"peopleType":"Executive Producer","personName":"Ron Burkle",
					 "name":"","sort":3},
					{"peopleId":999002,"peopleType":"Creator","personName":"Ronan Bennett",
					 "name":"","sort":4}]}}`, remoteIDs)
		case "/people/373728/extended":
			people.Add(1)
			fmt.Fprint(w, `{"data":{"name":"Tom Hardy","remoteIds":[
				{"id":"2524","sourceName":"TheMovieDB.com"},{"id":"nm0362766","sourceName":"IMDB"}]}}`)
		case "/people/296967/extended":
			people.Add(1)
			fmt.Fprint(w, `{"data":{"name":"Helen Mirren","remoteIds":[
				{"id":"15735","sourceName":"TheMovieDB.com"}]}}`)
		case "/people/378153/extended":
			// a real credited actor TMDB has never heard of
			people.Add(1)
			fmt.Fprint(w, `{"data":{"name":"Geoff Bell","remoteIds":[
				{"id":"nm0068232","sourceName":"IMDB"}]}}`)
		case "/series/999/episodes/default":
			fmt.Fprint(w, `{"data":{"episodes":[]},"links":{"next":""}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, people
}

// A series TMDB has no id for used to get no cast at all — the enrich
// pass bailed on the missing id and nothing else read TVDB's people.
func TestCastComesFromTVDBWhenTMDBHasNoSuchShow(t *testing.T) {
	srv, lookups := castTVDB(t, "")
	tv := NewTVDB(func() string { return "good-key" })
	tv.SetBaseURL(srv.URL)

	d, err := tv.Show(context.Background(), 999)
	if err != nil {
		t.Fatal(err)
	}
	// producers and creators are credited on TVDB but are not cast
	if len(d.Cast) != 3 {
		t.Fatalf("cast = %d, want the 3 actors: %+v", len(d.Cast), d.Cast)
	}
	if d.Cast[0].Name != "Tom Hardy" || d.Cast[0].Character != "Harry Da Souza" {
		t.Errorf("first billed = %+v", d.Cast[0])
	}
	if d.Cast[0].TmdbID != 2524 || d.Cast[0].TvdbID != 373728 {
		t.Errorf("Tom Hardy ids = tmdb %d tvdb %d, want both resolved",
			d.Cast[0].TmdbID, d.Cast[0].TvdbID)
	}
	// the one TMDB has never heard of keeps their TVDB id alone
	geoff := d.Cast[2]
	if geoff.Name != "Geoff Bell" || geoff.TmdbID != 0 || geoff.TvdbID != 378153 {
		t.Errorf("Geoff Bell = %+v, want the TVDB id alone", geoff)
	}
	if n := lookups.Load(); n != 3 {
		t.Errorf("%d per-person lookups, want one per actor", n)
	}
}

// The expensive half must not run where TMDB can answer instead: one
// call there returns the whole cast with ids already attached, and this
// cast is replaced wholesale, so any lookup here is wasted.
func TestNoPerPersonLookupsWhenTheShowHasATMDBID(t *testing.T) {
	srv, lookups := castTVDB(t, `{"id":"12345","sourceName":"TheMovieDB.com"}`)
	tv := NewTVDB(func() string { return "good-key" })
	tv.SetBaseURL(srv.URL)

	d, err := tv.Show(context.Background(), 999)
	if err != nil {
		t.Fatal(err)
	}
	if d.TmdbID != 12345 {
		t.Fatalf("tmdbId = %d", d.TmdbID)
	}
	if n := lookups.Load(); n != 0 {
		t.Errorf("%d per-person lookups on a show TMDB carries — that work is thrown away", n)
	}
	// the cast is still there, unresolved, in case the enrich never runs
	if len(d.Cast) != 3 || d.Cast[0].TmdbID != 0 {
		t.Errorf("cast = %+v, want the TVDB names kept but unresolved", d.Cast)
	}
}

// TMDB's cast wins where it has one. An empty answer does not: a show
// TMDB lists without credits must not wipe what TVDB gave.
func TestAnEmptyTMDBCastDoesNotWipeTVDBs(t *testing.T) {
	tmdbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"name":"MobLand","credits":{"cast":[]},"seasons":[]}`)
	}))
	defer tmdbSrv.Close()
	tmdb := NewTMDB(func() string { return "k" })
	tmdb.SetBaseURL(tmdbSrv.URL)

	d := &ShowDetail{TmdbID: 12345, Cast: []Person{{TvdbID: 378153, Name: "Geoff Bell"}}}
	EnrichShowFromTMDB(context.Background(), tmdb, d)
	if len(d.Cast) != 1 || d.Cast[0].Name != "Geoff Bell" {
		t.Errorf("cast = %+v, want TVDB's kept", d.Cast)
	}
}
