package lists

import (
	"context"
	"testing"

	"github.com/getreely/reely/internal/catalog"
)

// A list shares what it adds.
//
// Until it could, every sync filled a library with titles no shared
// account could watch until somebody tagged them by hand — which for a
// chart refreshing twice a day nobody keeps up with.

func groupOf(t *testing.T, cat *catalog.Store, name string) *catalog.Group {
	t.Helper()
	g, err := cat.CreateGroup(name)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// granted counts the entitlements a group holds, by the ids a grant is
// keyed on rather than by reading the table raw.
func granted(t *testing.T, cat *catalog.Store, groupID int64) int {
	t.Helper()
	g, err := cat.Group(groupID)
	if err != nil {
		t.Fatal(err)
	}
	return g.Titles
}

// The audience on the list is what an owner set, so it is applied as
// given — to every title the list adds from then on.
func TestAnOwnersListSharesWhatItAdds(t *testing.T) {
	s, cat := testSyncer(t)
	lib, err := cat.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	house := groupOf(t, cat, "Household")
	id, err := cat.CreateList(&catalog.List{
		Name: "Trending", Source: "tmdb_chart", Config: `{"chart":"trending"}`,
		LibraryID: lib.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.SetListGroups(id, []int64{house.ID}); err != nil {
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
	if added == 0 {
		t.Fatal("the list added nothing, so there is nothing to share")
	}
	if got := granted(t, cat, house.ID); got != added {
		t.Errorf("granted %d of %d added titles", got, added)
	}
}

// A list with no audience keeps doing exactly what it did: adding, and
// entitling nobody. Upgrading must not widen access on its own.
func TestAListWithNoAudienceSharesWithNobody(t *testing.T) {
	s, cat := testSyncer(t)
	lib, err := cat.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	house := groupOf(t, cat, "Household")
	id, err := cat.CreateList(&catalog.List{
		Name: "Trending", Source: "tmdb_chart", Config: `{"chart":"trending"}`,
		LibraryID: lib.ID, Enabled: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	list, err := cat.GetList(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(list.GroupIDs) != 0 {
		t.Fatalf("a new list came with an audience: %v", list.GroupIDs)
	}
	if _, _, err := s.SyncList(context.Background(), list); err != nil {
		t.Fatal(err)
	}
	if got := granted(t, cat, house.ID); got != 0 {
		t.Errorf("granted %d titles nobody asked it to share", got)
	}
}

// A requester's list with no audience shares with their own households —
// the same targets approving their request by hand picks. Before this,
// an entry the syncer auto-approved was added and shared with nobody
// while the identical entry approved by a click was shared.
func TestARequestersAutoApprovedListEntrySharesLikeAnApprovalWould(t *testing.T) {
	s, cat, accounts := syncerWithAccounts(t)
	list, jen := requesterList(t, cat, accounts)
	house := groupOf(t, cat, "Household")
	if err := cat.AddMember(house.ID, jen); err != nil {
		t.Fatal(err)
	}
	// auto-approve, so the syncer adds rather than queueing
	if err := accounts.SetRequestSettings(jen, false, true, true, nil, nil); err != nil {
		t.Fatal(err)
	}

	added, _, err := s.SyncList(context.Background(), list)
	if err != nil {
		t.Fatal(err)
	}
	if added == 0 {
		t.Fatal("nothing was added, so nothing could be shared")
	}
	if got := granted(t, cat, house.ID); got != added {
		t.Errorf("granted %d of %d — a list entry shared less than the same ask would", got, added)
	}
}
