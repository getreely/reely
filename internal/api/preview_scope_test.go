package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/getreely/reely/internal/metadata"
)

// "Already in your library" has to mean the viewer's library, not the
// install's.
//
// A shared account reaches a whole library — that is how the Plex share
// matched it — but what they may actually WATCH is decided per title by
// their tags. Reading the preview's "already in" off library access
// instead of entitlement tells somebody a film is theirs when it is not,
// and takes away the Request button that would have got them tagged for
// it. The title is in the house and out of reach, and the one action
// that would fix that is the one the page removes.
func TestPreviewSaysInLibraryOnlyForATitleTheViewerMayWatch(t *testing.T) {
	world := requesterWorld(t)
	srv, h := world.srv, world.h
	srv.TMDB.SetBaseURL(fakeTMDB(t).URL)
	if err := srv.Settings.Set("tmdb_api_key", "k"); err != nil {
		t.Fatal(err)
	}
	// two films in the library jen can reach
	for _, d := range []*metadata.MovieDetail{
		{TmdbID: 603, Title: "The Matrix", Year: 1999},
		{TmdbID: 604, Title: "The Matrix Reloaded", Year: 2003},
	} {
		if _, err := srv.Catalog.UpsertMovie(d, world.family); err != nil {
			t.Fatal(err)
		}
	}
	// she is tagged for one of them only
	group, err := srv.Catalog.CreateGroup("Household")
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Catalog.AddMember(group.ID, world.jen); err != nil {
		t.Fatal(err)
	}
	if err := srv.Catalog.Grant(group.ID, "movie", 603, 0, "The Matrix", world.adminID); err != nil {
		t.Fatal(err)
	}
	if err := srv.Catalog.SetManaged(world.jen, true); err != nil {
		t.Fatal(err)
	}
	jen := login(t, h, "jen", "another long one")

	inLibs := func(tmdbID int) []int64 {
		t.Helper()
		w := as(t, h, jen, "GET", "/api/v1/preview/movie/"+itoa64(int64(tmdbID)), nil)
		if w.Code != http.StatusOK {
			t.Fatalf("preview %d = %d: %s", tmdbID, w.Code, w.Body)
		}
		var out struct {
			InLibraries []int64 `json:"inLibraries"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.InLibraries
	}

	if got := inLibs(603); len(got) == 0 {
		t.Error("a film she IS tagged for should read as already hers")
	}
	if got := inLibs(604); len(got) != 0 {
		t.Errorf("inLibraries = %v for a film she is not tagged for — "+
			"the page will say \"In library\" and drop the Request button", got)
	}

	// and the owner still sees the truth about their own install
	if w := as(t, h, world.admin, "GET", "/api/v1/preview/movie/604", nil); w.Code == http.StatusOK {
		var out struct {
			InLibraries []int64 `json:"inLibraries"`
		}
		json.Unmarshal(w.Body.Bytes(), &out) //nolint:errcheck // asserted below
		if len(out.InLibraries) == 0 {
			t.Error("the owner lost sight of a film that is in their library")
		}
	}
}
