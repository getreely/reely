package catalog

import (
	"path/filepath"
	"testing"

	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/metadata"
)

// A show reely found through TVDB alone has no TMDB id, stored as NULL.
// Read straight into an int, that one row failed the whole show list, so
// every show lost its badge on Explore and in the TV app while each one's
// own page, which reads the id safely, still said it was in the library.
func TestShowsWithoutATmdbIDStillList(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	cat := New(conn)
	lib, err := cat.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat.UpsertShow(&metadata.ShowDetail{TmdbID: 1396, Title: "Breaking Bad", Year: 2008}, lib.ID); err != nil {
		t.Fatal(err)
	}
	tvdbOnly, err := cat.UpsertShowTVDB(&metadata.ShowDetail{TvdbID: 80552, Title: "A TVDB-only show", Year: 2001}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}

	shows, err := cat.ListShows(0)
	if err != nil {
		t.Fatalf("listing shows: %v", err)
	}
	if len(shows) != 2 {
		t.Fatalf("listed %d shows, want 2", len(shows))
	}
	for _, sh := range shows {
		if sh.ID == tvdbOnly && (sh.TmdbID != 0 || sh.TvdbID != 80552) {
			t.Errorf("TVDB-only show listed with tmdb %d, tvdb %d", sh.TmdbID, sh.TvdbID)
		}
	}

	if _, err := cat.GetShow(tvdbOnly); err != nil {
		t.Errorf("opening the TVDB-only show: %v", err)
	}
}
