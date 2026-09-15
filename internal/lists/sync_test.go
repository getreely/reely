package lists

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/grab"
	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/trakt"
)

// fakeTMDB serves a trending chart plus the two movie details the sync
// will fetch.
func fakeTMDB(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/trending/movie/week":
			fmt.Fprint(w, `{"results":[
				{"id":603,"title":"The Matrix","release_date":"1999-03-31"},
				{"id":27205,"title":"Inception","release_date":"2010-07-15"}]}`)
		case "/movie/603":
			fmt.Fprint(w, `{"title":"The Matrix","release_date":"1999-03-31","runtime":136,
				"credits":{"cast":[]},"genres":[],"external_ids":{},"release_dates":{"results":[]}}`)
		case "/movie/27205":
			fmt.Fprint(w, `{"title":"Inception","release_date":"2010-07-15","runtime":148,
				"credits":{"cast":[]},"genres":[],"external_ids":{},"release_dates":{"results":[]}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fakeTrakt(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("trakt-api-key") == "" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/users/tommy/lists/best-heists/items":
			fmt.Fprint(w, `[
				{"type":"movie","movie":{"title":"The Matrix","year":1999,"ids":{"tmdb":603}}},
				{"type":"show","show":{"title":"Breaking Bad","year":2008,"ids":{"tmdb":1396}}}]`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testSyncer(t *testing.T) (*Syncer, *catalog.Store) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	cat := catalog.New(conn)
	tmdb := metadata.NewTMDB(func() string { return "k" })
	tmdb.SetBaseURL(fakeTMDB(t).URL)
	tr := trakt.New(func() string { return "cid" })
	tr.SetBaseURL(fakeTrakt(t).URL)
	return &Syncer{
		Catalog: cat, TMDB: tmdb, Trakt: tr,
		Grab: &grab.Service{Catalog: cat},
	}, cat
}

func TestSyncChartAddsAndDedupes(t *testing.T) {
	s, cat := testSyncer(t)
	lib, err := cat.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	id, err := cat.CreateList(&catalog.List{
		Name: "Trending", Source: "tmdb_chart", Config: `{"chart":"trending"}`,
		LibraryID: lib.ID, ItemLimit: 0, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	list, err := cat.GetList(id)
	if err != nil {
		t.Fatal(err)
	}

	added, _, err := s.SyncList(context.Background(), list)
	if err != nil {
		t.Fatal(err)
	}
	if added != 2 {
		t.Fatalf("added = %d, want 2", added)
	}
	movies, err := cat.ListMovies(lib.ID)
	if err != nil || len(movies) != 2 {
		t.Fatalf("movies = %v (%v)", movies, err)
	}
	for _, m := range movies {
		if !m.Monitored {
			t.Fatalf("list add not monitored: %+v", m)
		}
	}
	// the search queue got both
	if got := s.Grab.QueueLen(); got != 2 {
		t.Fatalf("queue = %d, want 2", got)
	}

	// a second sync adds nothing new
	if added, _, err = s.SyncList(context.Background(), list); err != nil || added != 0 {
		t.Fatalf("re-sync added = %d (%v)", added, err)
	}
	if list, err = cat.GetList(id); err != nil || list.LastSynced == "" {
		t.Fatalf("last_synced not stamped: %+v (%v)", list, err)
	}
}

func TestSyncItemLimitCapsChart(t *testing.T) {
	s, cat := testSyncer(t)
	lib, err := cat.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	id, err := cat.CreateList(&catalog.List{
		Name: "Trending", Source: "tmdb_chart", Config: `{"chart":"trending"}`,
		LibraryID: lib.ID, ItemLimit: 1, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := cat.GetList(id)
	if added, _, err := s.SyncList(context.Background(), list); err != nil || added != 1 {
		t.Fatalf("added = %d (%v), want 1", added, err)
	}
}

func TestSyncTraktListFiltersToLibraryKind(t *testing.T) {
	s, cat := testSyncer(t)
	lib, err := cat.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	id, err := cat.CreateList(&catalog.List{
		Name: "Best Heists", Source: "trakt_list",
		Config:    `{"user":"tommy","slug":"best-heists"}`,
		LibraryID: lib.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	list, _ := cat.GetList(id)
	// the list carries a movie and a show; a movie library takes only the movie
	added, _, err := s.SyncList(context.Background(), list)
	if err != nil || added != 1 {
		t.Fatalf("added = %d (%v), want 1", added, err)
	}
	movies, _ := cat.ListMovies(lib.ID)
	if len(movies) != 1 || movies[0].Title != "The Matrix" {
		t.Fatalf("movies = %+v", movies)
	}
}
