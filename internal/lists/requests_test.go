package lists

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/getreely/reely/internal/auth"
	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/grab"
	"github.com/getreely/reely/internal/metadata"
)

// A watched list owned by an account that asks rather than adds files
// requests instead of adding. That is the whole point of routing both
// through one permission: a list is an add on a timer, so it answers to
// the same rule the search box's Add button does.

// syncerWithAccounts is testSyncer with an account store on the same
// database, which is what the "who owns this list" lookup needs.
func syncerWithAccounts(t *testing.T) (*Syncer, *catalog.Store, *auth.Store) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	cat := catalog.New(conn)
	tmdb := metadata.NewTMDB(func() string { return "k" })
	tmdb.SetBaseURL(fakeTMDB(t).URL)
	accounts := auth.New(conn)
	return &Syncer{
		Catalog: cat, TMDB: tmdb, Grab: &grab.Service{Catalog: cat}, Accounts: accounts,
	}, cat, accounts
}

// requesterList builds a library, a requester who owns a list feeding it,
// and returns the list ready to sync.
func requesterList(t *testing.T, cat *catalog.Store, accounts *auth.Store) (*catalog.List, int64) {
	t.Helper()
	lib, err := cat.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	jen, err := accounts.CreateUser("jen", "another long one", "user", []int64{lib.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.SetRequestSettings(jen, false, false, false, nil, nil); err != nil {
		t.Fatal(err)
	}
	id, err := cat.CreateList(&catalog.List{
		Name: "Trending", Source: "tmdb_chart", Config: `{"chart":"trending"}`,
		LibraryID: lib.ID, Enabled: true, CreatedBy: jen,
	})
	if err != nil {
		t.Fatal(err)
	}
	list, err := cat.GetList(id)
	if err != nil {
		t.Fatal(err)
	}
	return list, jen
}

func TestRequesterListFilesRequestsInsteadOfAdding(t *testing.T) {
	s, cat, accounts := syncerWithAccounts(t)
	list, _ := requesterList(t, cat, accounts)

	added, requested, err := s.SyncList(context.Background(), list)
	if err != nil {
		t.Fatal(err)
	}
	if added != 0 {
		t.Errorf("added = %d, want nothing added without a decision", added)
	}
	if requested != 2 {
		t.Fatalf("requested = %d, want 2", requested)
	}
	movies, err := cat.ListMovies(list.LibraryID)
	if err != nil || len(movies) != 0 {
		t.Fatalf("movies = %v (%v), want the library untouched", movies, err)
	}
	queued, err := cat.QueuedRequests()
	if err != nil || len(queued) != 2 {
		t.Fatalf("queue = %d rows (%v), want 2 waiting", len(queued), err)
	}
	// looked up rather than carried: a list entry is a title and an id,
	// and the owner decides from more than that
	if queued[0].Year == 0 {
		t.Errorf("queued request was never filled in from TMDB: %+v", queued[0])
	}

	// syncing again asks for nothing twice
	if _, requested, err = s.SyncList(context.Background(), list); err != nil || requested != 0 {
		t.Errorf("re-sync requested = %d (%v), want 0", requested, err)
	}
}

func TestAutoApprovedRequesterListAddsStraightAway(t *testing.T) {
	s, cat, accounts := syncerWithAccounts(t)
	list, jen := requesterList(t, cat, accounts)
	// trusted with films: their list entries skip the queue entirely
	if err := accounts.SetRequestSettings(jen, false, true, false, nil, nil); err != nil {
		t.Fatal(err)
	}

	added, requested, err := s.SyncList(context.Background(), list)
	if err != nil {
		t.Fatal(err)
	}
	if added != 2 || requested != 2 {
		t.Fatalf("added/requested = %d/%d, want 2/2", added, requested)
	}
	movies, err := cat.ListMovies(list.LibraryID)
	if err != nil || len(movies) != 2 {
		t.Fatalf("movies = %v (%v), want both added", movies, err)
	}
	if queued, err := cat.QueuedRequests(); err != nil || len(queued) != 0 {
		t.Errorf("queue = %d rows (%v), want nothing to decide", len(queued), err)
	}
}

// A title the owner turned down is not offered again by the same list. A
// person may ask again — denial returns a title to askable — but a list
// re-filing it every half hour is nagging, and the answer is on record.
func TestDeniedTitleIsNotReRequestedByAList(t *testing.T) {
	s, cat, accounts := syncerWithAccounts(t)
	list, _ := requesterList(t, cat, accounts)

	if _, _, err := s.SyncList(context.Background(), list); err != nil {
		t.Fatal(err)
	}
	queued, err := cat.QueuedRequests()
	if err != nil || len(queued) == 0 {
		t.Fatalf("queue = %v (%v)", queued, err)
	}
	for _, q := range queued {
		if err := cat.DecideRequest(q.ID, 0, "denied"); err != nil {
			t.Fatal(err)
		}
	}
	_, requested, err := s.SyncList(context.Background(), list)
	if err != nil {
		t.Fatal(err)
	}
	if requested != 0 {
		t.Errorf("re-requested %d denied titles, want 0", requested)
	}
}

// An account that adds is unaffected: its lists behave exactly as they
// always have.
func TestAddingAccountsListStillAdds(t *testing.T) {
	s, cat, accounts := syncerWithAccounts(t)
	list, jen := requesterList(t, cat, accounts)
	if err := accounts.SetRequestSettings(jen, true, false, false, nil, nil); err != nil {
		t.Fatal(err)
	}

	added, requested, err := s.SyncList(context.Background(), list)
	if err != nil {
		t.Fatal(err)
	}
	if added != 2 || requested != 0 {
		t.Fatalf("added/requested = %d/%d, want 2/0", added, requested)
	}
	if queued, err := cat.QueuedRequests(); err != nil || len(queued) != 0 {
		t.Errorf("an adding account's list queued %d requests", len(queued))
	}
}
