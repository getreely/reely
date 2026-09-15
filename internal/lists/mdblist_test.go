package lists

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/getreely/reely/internal/auth"
	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/grab"
	"github.com/getreely/reely/internal/mdblist"
	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/trakt"
)

type mapSettings map[string]string

func (m mapSettings) Get(key string) string { return m[key] }

// fakeMdblist serves both address styles and enforces WHICH key each list
// must arrive with — the id list belongs to user 7 (personal key), the
// slug list to nobody (install-wide fallback).
func fakeMdblist(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/lists/14/items":
			if r.URL.Query().Get("apikey") != "personal-key" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = fmt.Fprint(w, `[
				{"id":603,"rank":1,"title":"The Matrix","mediatype":"movie","release_year":1999},
				{"id":1396,"rank":2,"title":"Breaking Bad","mediatype":"show","release_year":2008},
				{"id":0,"rank":3,"title":"Mystery","mediatype":"podcast","release_year":2020}]`)
		case "/api/lists/tommy/imdb-top/items":
			if r.URL.Query().Get("apikey") != "global-key" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			_, _ = fmt.Fprint(w, `{"movies":[
				{"id":27205,"rank":1,"title":"Inception","mediatype":"movie","release_year":2010}],
				"shows":[]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestMdblistSyncUsesCreatorsKeyStrictly(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	cat := catalog.New(conn)
	tmdb := metadata.NewTMDB(func() string { return "k" })
	tmdb.SetBaseURL(fakeTMDB(t).URL)
	userID, err := auth.New(conn).CreateUser("tommy", "a long passphrase", "admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	mdb := mdblist.New()
	mdb.SetBaseURL(fakeMdblist(t).URL)
	s := &Syncer{
		Catalog: cat, TMDB: tmdb, Trakt: trakt.New(func() string { return "" }),
		Mdblist: mdb, Grab: &grab.Service{Catalog: cat},
		Settings: mapSettings{
			"mdblist_api_key":      "global-key",
			MdblistUserKey(userID): "personal-key",
		},
	}

	lib, err := cat.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	// created by tommy → syncs with his personal key; the show and the
	// unknown mediatype are filtered, only the movie lands
	id, err := cat.CreateList(&catalog.List{
		Name: "By id", Source: "mdblist", Config: `{"listId":14}`,
		LibraryID: lib.ID, Enabled: true, CreatedBy: userID,
	})
	if err != nil {
		t.Fatal(err)
	}
	list, err := cat.GetList(id)
	if err != nil || list.CreatedBy != userID {
		t.Fatalf("created_by not stored: %+v (%v)", list, err)
	}
	if added, _, err := s.SyncList(context.Background(), list); err != nil || added != 1 {
		t.Fatalf("personal-key list added = %d (%v), want 1", added, err)
	}
	movies, _ := cat.ListMovies(lib.ID)
	if len(movies) != 1 || movies[0].Title != "The Matrix" {
		t.Fatalf("movies = %+v", movies)
	}

	// no creator (the open, pre-account install) → the shared key; classic
	// URL shape
	id, err = cat.CreateList(&catalog.List{
		Name: "By slug", Source: "mdblist", Config: `{"user":"tommy","slug":"imdb-top"}`,
		LibraryID: lib.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	list, _ = cat.GetList(id)
	if added, _, err := s.SyncList(context.Background(), list); err != nil || added != 1 {
		t.Fatalf("open-install list added = %d (%v), want 1", added, err)
	}

	// a user WITHOUT a personal key gets no fallback — their list refuses
	// to sync rather than borrowing someone else's key
	keyless, err := auth.New(conn).CreateUser("sam", "another passphrase", "admin", nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err = cat.CreateList(&catalog.List{
		Name: "No key", Source: "mdblist", Config: `{"listId":14}`,
		LibraryID: lib.ID, Enabled: true, CreatedBy: keyless,
	})
	if err != nil {
		t.Fatal(err)
	}
	list, _ = cat.GetList(id)
	if _, _, err := s.SyncList(context.Background(), list); err == nil {
		t.Fatal("keyless user's list synced — a fallback key leaked in")
	}
}
