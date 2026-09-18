package api

import (
	"net/http"
	"testing"

	"github.com/getreely/reely/internal/catalog"
)

// Approving is a decision about somebody else's ask, so it has to carry
// whose ask it was — wherever the owner happens to be standing when they
// take it.
//
// The Add button next to a pending request did neither: it put the title
// in whichever library the owner was pointed at and granted it to
// nobody, leaving the request pending. Approving goes by the stored row
// instead, so the title lands in the library that was asked for and
// reaches the person who asked.
func TestApprovingEntitlesTheRequesterNotTheAdmin(t *testing.T) {
	srv := testServer(t)
	theirs, err := srv.Catalog.CreateLibrary("Jo", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	// a second library the owner points at, to prove the title does not
	// simply land wherever the admin happens to be
	if _, err := srv.Catalog.CreateLibrary("Owner", t.TempDir(), "movies"); err != nil {
		t.Fatal(err)
	}
	jo, err := srv.Auth.CreateUser("jo", "hunter2hunter2", "user", []int64{theirs.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Auth.CreateUser("owner", "hunter2hunter2", "admin", nil); err != nil {
		t.Fatal(err)
	}
	if err := srv.Catalog.EnsurePersonalGroup(jo, "jo"); err != nil {
		t.Fatal(err)
	}
	reqID, err := srv.Catalog.CreateRequest(catalog.Request{
		UserID: jo, LibraryID: theirs.ID, Kind: "movie", TmdbID: 603, Title: "The Matrix",
	})
	if err != nil {
		t.Fatal(err)
	}

	h := srv.Handler()
	srv.TMDB.SetBaseURL(fakeTMDB(t).URL)
	if err := srv.Settings.Set("tmdb_api_key", "k"); err != nil {
		t.Fatal(err)
	}
	owner := login(t, h, "owner", "hunter2hunter2")
	if w := as(t, h, owner, "POST", "/api/v1/requests/"+itoa(reqID)+"/approve", nil); w.Code != http.StatusOK {
		t.Fatalf("approve: %d %s", w.Code, w.Body)
	}

	// the person who asked can see it, and it is their own group that
	// grants it — not the admin's, and not nobody's
	viewers, err := srv.Catalog.SharedWith("movie", 603, 0)
	if err != nil {
		t.Fatal(err)
	}
	var reached bool
	for _, v := range viewers {
		if v.UserID == jo {
			reached = true
		}
	}
	if !reached {
		t.Errorf("the requester was not granted the title they asked for: %+v", viewers)
	}

	// and it landed in the library the request named
	movies, err := srv.Catalog.ListMovies(theirs.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(movies) != 1 || movies[0].TmdbID != 603 {
		t.Errorf("the title did not land in the library that asked for it: %+v", movies)
	}
}
