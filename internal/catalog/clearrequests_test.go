package catalog

import "testing"

// A request outliving the title it was for is what made a deleted movie
// go on reading as "requested" for everybody: the approved row stayed
// open, the badge kept drawing, and nobody could ask for it again
// because the library already had an open request.
func TestRemovingATitleClosesItsOpenRequests(t *testing.T) {
	s, family, jen := twoLibraries(t)

	approved, err := s.CreateRequest(Request{
		UserID: 1, LibraryID: family, Kind: "movie", TmdbID: 27205,
		Title: "Inception", Status: "approved",
	})
	if err != nil {
		t.Fatal(err)
	}
	// the same title asked for in a different library is a different ask,
	// and must survive
	if _, err := s.CreateRequest(Request{
		UserID: 2, LibraryID: jen, Kind: "movie", TmdbID: 27205, Title: "Inception",
	}); err != nil {
		t.Fatal(err)
	}

	n, err := s.ClearRequestsFor(family, "movie", 27205, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("closed %d requests, want 1", n)
	}

	open, err := s.OpenRequests([]int64{family, jen})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range open {
		if r.ID == approved {
			t.Error("the removed title's request is still open, so it still badges as requested")
		}
		if r.LibraryID != jen {
			t.Errorf("a request in another library was closed too: %+v", r)
		}
	}
	if len(open) != 1 {
		t.Fatalf("%d requests left open, want the other library's one", len(open))
	}
}

// A denial outlives the title it was about. It is the record of an
// answer, not a thing waiting to happen, and a watched list still reads
// it to know not to re-file the same ask.
func TestRemovingATitleLeavesADenialAlone(t *testing.T) {
	s, family, _ := twoLibraries(t)
	if _, err := s.CreateRequest(Request{
		UserID: 1, LibraryID: family, Kind: "movie", TmdbID: 27205,
		Title: "Inception", Status: "denied",
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := s.ClearRequestsFor(family, "movie", 27205, 0); err != nil {
		t.Fatal(err)
	}
	denied, err := s.EverDenied(family, "movie", 27205)
	if err != nil {
		t.Fatal(err)
	}
	if !denied {
		t.Error("removing the title erased the record that it had been turned down")
	}
}

// A show can be asked for under either id, so clearing has to match the
// one the request carries.
func TestClearingAShowMatchesEitherID(t *testing.T) {
	s, family, _ := twoLibraries(t)
	shows, err := s.CreateLibrary("Shows", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	_ = family
	if _, err := s.CreateRequest(Request{
		UserID: 1, LibraryID: shows.ID, Kind: "show", TvdbID: 81189,
		Title: "Breaking Bad", Status: "approved",
	}); err != nil {
		t.Fatal(err)
	}
	n, err := s.ClearRequestsFor(shows.ID, "show", 0, 81189)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("closed %d, want 1 — a TVDB-keyed request was missed", n)
	}
}
