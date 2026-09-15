package catalog

import (
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/getreely/reely/internal/db"
)

// twoLibraries gives a store, a shared family library and a separate one
// — the shape the per-library rule is about.
func twoLibraries(t *testing.T) (*Store, int64, int64) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	s := New(conn)
	family, err := s.CreateLibrary("Family", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	jen, err := s.CreateLibrary("Jen", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	// requests reference a real account — the foreign key is the point, so
	// the tests get real rows rather than bare ids
	for i := 1; i <= 9; i++ {
		if _, err := s.db.Exec(`INSERT INTO users (id, username, password_hash, role)
			VALUES (?, ?, 'x', 'user')`, i, fmt.Sprintf("user%d", i)); err != nil {
			t.Fatal(err)
		}
	}
	return s, family.ID, jen.ID
}

func ask(t *testing.T, s *Store, userID, libraryID int64, tmdbID int, title string) (int64, error) {
	t.Helper()
	return s.CreateRequest(Request{
		UserID: userID, LibraryID: libraryID, Kind: "movie",
		TmdbID: tmdbID, Title: title, Year: 2024,
	})
}

// Everyone sharing a library shares its requests: the second person to
// ask is told it is already requested rather than opening a duplicate.
// Somebody whose library is separate still sees it as askable, because
// it does still have to be added there.
func TestRequestsAreScopedToOneLibrary(t *testing.T) {
	s, family, jen := twoLibraries(t)

	if _, err := ask(t, s, 1, family, 693134, "Dune: Part Two"); err != nil {
		t.Fatalf("first ask: %v", err)
	}
	// a housemate on the same library
	if _, err := ask(t, s, 2, family, 693134, "Dune: Part Two"); !errors.Is(err, ErrAlreadyRequested) {
		t.Errorf("second ask on the shared library = %v, want ErrAlreadyRequested", err)
	}
	// somebody on their own library is unaffected
	if _, err := ask(t, s, 3, jen, 693134, "Dune: Part Two"); err != nil {
		t.Errorf("ask on a separate library: %v", err)
	}

	open, err := s.OpenRequests([]int64{family})
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 {
		t.Fatalf("family library has %d open requests, want 1", len(open))
	}
	if open[0].Username != "" {
		t.Error("the requester's listing must not name who asked")
	}

	both, err := s.OpenRequests([]int64{family, jen})
	if err != nil {
		t.Fatal(err)
	}
	if len(both) != 2 {
		t.Errorf("across both libraries = %d, want 2", len(both))
	}
	if none, err := s.OpenRequests(nil); err != nil || none != nil {
		t.Errorf("no libraries = %v, %v; want nothing", none, err)
	}
}

// A denied title goes back to askable. There is no refused state to
// explain — the partial index simply stops covering the row.
func TestDeniedRequestCanBeAskedForAgain(t *testing.T) {
	s, family, _ := twoLibraries(t)

	id, err := ask(t, s, 1, family, 335984, "Blade Runner 2049")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ask(t, s, 2, family, 335984, "Blade Runner 2049"); !errors.Is(err, ErrAlreadyRequested) {
		t.Fatalf("while pending = %v, want ErrAlreadyRequested", err)
	}
	if err := s.DecideRequest(id, 9, "denied"); err != nil {
		t.Fatal(err)
	}
	if _, err := ask(t, s, 2, family, 335984, "Blade Runner 2049"); err != nil {
		t.Errorf("after a denial: %v — a denied title must be askable again", err)
	}

	// and the denied row stays for the history rather than vanishing
	got, err := s.GetRequest(id)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "denied" || got.DecidedAt == "" {
		t.Errorf("denied request = %+v, want status denied with a decided_at", got)
	}
}

// Approving holds the title: it is on its way in, so nobody re-asks.
func TestApprovedRequestStillBlocksAnother(t *testing.T) {
	s, family, _ := twoLibraries(t)
	id, err := ask(t, s, 1, family, 872585, "Oppenheimer")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DecideRequest(id, 9, "approved"); err != nil {
		t.Fatal(err)
	}
	if _, err := ask(t, s, 2, family, 872585, "Oppenheimer"); !errors.Is(err, ErrAlreadyRequested) {
		t.Errorf("after approval = %v, want ErrAlreadyRequested", err)
	}
	// a decision only lands once
	if err := s.DecideRequest(id, 9, "denied"); !errors.Is(err, sql.ErrNoRows) {
		t.Errorf("deciding an already-decided request = %v, want ErrNoRows", err)
	}
}

// The owner's queue names who asked and where it would land — the one
// place a username appears.
func TestQueueNamesTheAskerAndLibrary(t *testing.T) {
	s, family, _ := twoLibraries(t)
	if _, err := ask(t, s, 7, family, 693134, "Dune: Part Two"); err != nil {
		t.Fatal(err)
	}
	q, err := s.QueuedRequests()
	if err != nil {
		t.Fatal(err)
	}
	if len(q) != 1 {
		t.Fatalf("queue = %d, want 1", len(q))
	}
	if q[0].Username != "user7" || q[0].LibraryName != "Family" {
		t.Errorf("queue row = %+v, want user7 / Family", q[0])
	}

	// deciding empties it
	if err := s.DecideRequest(q[0].ID, 1, "approved"); err != nil {
		t.Fatal(err)
	}
	if after, err := s.QueuedRequests(); err != nil || len(after) != 0 {
		t.Errorf("queue after approval = %v, %v; want empty", after, err)
	}
}

// Seasons survive the round trip, and a movie carries none.
func TestRequestedSeasonsRoundTrip(t *testing.T) {
	s, family, _ := twoLibraries(t)
	id, err := s.CreateRequest(Request{
		UserID: 1, LibraryID: family, Kind: "show", TvdbID: 80552,
		TmdbID: 11294, Title: "Kitchen Nightmares (US)", Seasons: []int{8, 9},
	})
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.GetRequest(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Seasons) != 2 || got.Seasons[0] != 8 || got.Seasons[1] != 9 {
		t.Errorf("seasons = %v, want [8 9]", got.Seasons)
	}
	if got.TvdbID != 80552 {
		t.Errorf("tvdbId = %d, want 80552", got.TvdbID)
	}

	movieID, err := ask(t, s, 1, family, 693134, "Dune: Part Two")
	if err != nil {
		t.Fatal(err)
	}
	if m, err := s.GetRequest(movieID); err != nil || m.Seasons != nil {
		t.Errorf("movie seasons = %v, %v; want nil", m.Seasons, err)
	}
}

// The quota counts denials too — otherwise being refused and asking
// again would be free, and the limit would bound nothing.
func TestWeeklyCountIncludesDenied(t *testing.T) {
	s, family, jen := twoLibraries(t)
	a, err := ask(t, s, 5, family, 1, "One")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ask(t, s, 5, jen, 2, "Two"); err != nil {
		t.Fatal(err)
	}
	if err := s.DecideRequest(a, 9, "denied"); err != nil {
		t.Fatal(err)
	}
	n, err := s.RequestsThisWeek(5, "movie")
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Errorf("weekly count = %d, want 2 (the denied one still counts)", n)
	}
	if shows, err := s.RequestsThisWeek(5, "show"); err != nil || shows != 0 {
		t.Errorf("shows counted separately: %d, %v", shows, err)
	}
	if other, err := s.RequestsThisWeek(6, "movie"); err != nil || other != 0 {
		t.Errorf("another account's count = %d, %v", other, err)
	}
}
