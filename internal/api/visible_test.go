package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/getreely/reely/internal/metadata"
)

// What a managed viewer sees on the portal is decided by matching their
// grants against the library, and a title carries TWO ids. Plex may know
// a series by only one of them, so a grant seeded from Plex often names
// one id where reely's row has both — and the two must still be
// recognised as the same show.
func TestAShowIsVisibleWhenTheGrantNamesEitherOfItsIds(t *testing.T) {
	world := requesterWorld(t)
	srv, h := world.srv, world.h
	series, err := srv.Catalog.CreateLibrary("Series", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Auth.SetUserLibraries(world.jen, []int64{world.family, series.ID}); err != nil {
		t.Fatal(err)
	}
	// two shows reely holds with both ids
	for _, d := range []*metadata.ShowDetail{
		{TmdbID: 213713, TvdbID: 451136, Title: "By TMDB", Year: 2024},
		{TmdbID: 999001, TvdbID: 999002, Title: "By TVDB", Year: 2024},
	} {
		if _, err := srv.Catalog.UpsertShow(d, series.ID); err != nil {
			t.Fatal(err)
		}
	}

	group, err := srv.Catalog.CreateGroup("Household")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Catalog.AddMember(group.ID, world.jen); err != nil {
		t.Fatal(err)
	}
	// one grant names only the TMDB id, as a grant seeded from a Plex
	// item matched by a TMDB agent does; the other names only the TVDB id
	if err := srv.Catalog.Grant(group.ID, "show", 213713, 0, "By TMDB", world.adminID); err != nil {
		t.Fatal(err)
	}
	if err := srv.Catalog.Grant(group.ID, "show", 0, 999002, "By TVDB", world.adminID); err != nil {
		t.Fatal(err)
	}
	if err := srv.Catalog.SetManaged(world.jen, true); err != nil {
		t.Fatal(err)
	}

	jen := login(t, h, "jen", "another long one")
	w := as(t, h, jen, "GET", "/api/v1/shows", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("shows = %d: %s", w.Code, w.Body)
	}
	var out struct {
		Shows []struct {
			Title string `json:"title"`
		} `json:"shows"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, sh := range out.Shows {
		seen[sh.Title] = true
	}
	if !seen["By TMDB"] {
		t.Error("a show granted by its TMDB id is invisible because reely's row also has a TVDB id")
	}
	if !seen["By TVDB"] {
		t.Error("a show granted by its TVDB id is invisible")
	}
}
