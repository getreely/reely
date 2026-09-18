package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/getreely/reely/internal/catalog"
)

// Deleting a title has to take its open requests with it.
//
// Without that, the approved request stayed open forever: the
// requester's browse went on badging a title the install no longer had,
// and nobody could ask for it again, because the library still held an
// open request for it.
func TestDeletingAMovieClosesItsRequest(t *testing.T) {
	srv := testServer(t)
	lib, err := srv.Catalog.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	id := seedMovie(t, srv, lib.ID)
	// the request references a real account — the foreign key is the point
	jo, err := srv.Auth.CreateUser("jo", "hunter2hunter2", "user", []int64{lib.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Auth.CreateUser("owner", "hunter2hunter2", "admin", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Catalog.CreateRequest(catalog.Request{
		UserID: jo, LibraryID: lib.ID, Kind: "movie", TmdbID: 603,
		Title: "The Matrix", Status: "approved",
	}); err != nil {
		t.Fatal(err)
	}

	h := srv.Handler()
	owner := login(t, h, "owner", "hunter2hunter2")
	if n := openRequests(t, h, owner); n != 1 {
		t.Fatalf("the request did not register to begin with: %d", n)
	}

	if w := as(t, h, owner, "DELETE", "/api/v1/movies/"+itoa(id), nil); w.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
	if n := openRequests(t, h, owner); n != 0 {
		t.Errorf("%d request(s) still open after the title was deleted — it goes on badging as requested", n)
	}
}

// A denial already took the badge off: the listing the badge reads only
// carries pending and approved rows. Worth pinning, because it is the
// other way a request stops being live and nothing else states it.
func TestADeniedRequestStopsBadging(t *testing.T) {
	srv := testServer(t)
	lib, err := srv.Catalog.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	jo, err := srv.Auth.CreateUser("jo", "hunter2hunter2", "user", []int64{lib.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Auth.CreateUser("owner", "hunter2hunter2", "admin", nil); err != nil {
		t.Fatal(err)
	}
	reqID, err := srv.Catalog.CreateRequest(catalog.Request{
		UserID: jo, LibraryID: lib.ID, Kind: "movie", TmdbID: 603, Title: "The Matrix",
	})
	if err != nil {
		t.Fatal(err)
	}

	h := srv.Handler()
	owner := login(t, h, "owner", "hunter2hunter2")
	if n := openRequests(t, h, owner); n != 1 {
		t.Fatalf("a pending request should badge: %d", n)
	}
	if w := as(t, h, owner, "POST", "/api/v1/requests/"+itoa(reqID)+"/deny", nil); w.Code != http.StatusOK {
		t.Fatalf("deny: %d %s", w.Code, w.Body)
	}
	if n := openRequests(t, h, owner); n != 0 {
		t.Errorf("%d request(s) still badging after a denial", n)
	}
}

func openRequests(t *testing.T, h http.Handler, c *http.Cookie) int {
	t.Helper()
	w := as(t, h, c, "GET", "/api/v1/requests", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("requests: %d %s", w.Code, w.Body)
	}
	var out struct {
		Requests []catalog.Request `json:"requests"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return len(out.Requests)
}
