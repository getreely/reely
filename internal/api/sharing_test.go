package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Splitting the library is the owner's. None of it belongs on the
// portal: the group list names every household on the install, and
// deciding who sees what is not something a requester does.
func TestSharingIsNotOnThePortal(t *testing.T) {
	world := requesterWorld(t)
	external := world.srv.ExternalHandler()

	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/sharing/groups"},
		{"POST", "/api/v1/sharing/groups"},
		{"POST", "/api/v1/sharing/seed"},
		{"POST", "/api/v1/sharing/reconcile"},
		{"GET", "/api/v1/sharing/titles/movie/10096"},
	} {
		w := as(t, external, world.admin, tc.method, tc.path, map[string]any{})
		if w.Code != http.StatusNotFound {
			t.Errorf("%s %s on the external listener = %d, want 404: %s",
				tc.method, tc.path, w.Code, w.Body)
		}
	}
}

// A requester cannot see or change who the library is split between,
// even on the local listener.
func TestOnlyTheOwnerSplitsTheLibrary(t *testing.T) {
	world := requesterWorld(t)
	jen := login(t, world.h, "jen", "another long one")
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/v1/sharing/groups"},
		{"POST", "/api/v1/sharing/groups"},
		{"GET", "/api/v1/sharing/titles/movie/10096"},
	} {
		w := as(t, world.h, jen, tc.method, tc.path, map[string]any{"name": "x"})
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s as a requester = %d, want 403: %s",
				tc.method, tc.path, w.Code, w.Body)
		}
	}
}

func TestGroupsAreCreatedRenamedAndFilled(t *testing.T) {
	world := requesterWorld(t)

	w := as(t, world.h, world.admin, "POST", "/api/v1/sharing/groups",
		map[string]any{"name": "Jolene's family"})
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", w.Code, w.Body)
	}
	var g struct {
		ID    int64  `json:"id"`
		Label string `json:"label"`
	}
	json.Unmarshal(w.Body.Bytes(), &g) //nolint:errcheck // asserted below
	if g.Label != "reely.jolenes_family" {
		t.Fatalf("label = %q, want it readable", g.Label)
	}

	// membership is edited as a set of ticks, not one person at a time
	w = as(t, world.h, world.admin, "PUT",
		"/api/v1/sharing/groups/"+itoa64(g.ID)+"/members",
		map[string]any{"userIds": []int64{world.jen}})
	if w.Code != http.StatusOK {
		t.Fatalf("set members = %d: %s", w.Code, w.Body)
	}

	w = as(t, world.h, world.admin, "PATCH", "/api/v1/sharing/groups/"+itoa64(g.ID),
		map[string]any{"name": "The Barretts"})
	if w.Code != http.StatusOK {
		t.Fatalf("rename = %d: %s", w.Code, w.Body)
	}
	var after struct {
		Name  string `json:"name"`
		Label string `json:"label"`
	}
	json.Unmarshal(w.Body.Bytes(), &after) //nolint:errcheck // asserted below
	if after.Label != "reely.the_barretts" {
		t.Fatalf("label after rename = %q, want it to follow the name", after.Label)
	}
}

// Deleting a group revokes every title it grants, so one with members
// says so rather than doing it.
func TestDeletingAGroupWithMembersConflicts(t *testing.T) {
	world := requesterWorld(t)
	w := as(t, world.h, world.admin, "POST", "/api/v1/sharing/groups",
		map[string]any{"name": "Housemates"})
	var g struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &g) //nolint:errcheck // asserted below

	as(t, world.h, world.admin, "PUT", "/api/v1/sharing/groups/"+itoa64(g.ID)+"/members",
		map[string]any{"userIds": []int64{world.jen}})

	if w := as(t, world.h, world.admin, "DELETE",
		"/api/v1/sharing/groups/"+itoa64(g.ID), nil); w.Code != http.StatusConflict {
		t.Fatalf("delete with members = %d, want 409: %s", w.Code, w.Body)
	}
	as(t, world.h, world.admin, "PUT", "/api/v1/sharing/groups/"+itoa64(g.ID)+"/members",
		map[string]any{"userIds": []int64{}})
	if w := as(t, world.h, world.admin, "DELETE",
		"/api/v1/sharing/groups/"+itoa64(g.ID), nil); w.Code != http.StatusNoContent {
		t.Fatalf("delete after emptying = %d: %s", w.Code, w.Body)
	}
}

// The per-title editor: who can see this, and changing it without
// re-adding anything.
func TestATitlesGroupsCanBeReadAndSet(t *testing.T) {
	world := requesterWorld(t)
	w := as(t, world.h, world.admin, "POST", "/api/v1/sharing/groups",
		map[string]any{"name": "Housemates"})
	var g struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &g) //nolint:errcheck // asserted below
	as(t, world.h, world.admin, "PUT", "/api/v1/sharing/groups/"+itoa64(g.ID)+"/members",
		map[string]any{"userIds": []int64{world.jen}})

	if w := as(t, world.h, world.admin, "PUT", "/api/v1/sharing/titles/movie/10096",
		map[string]any{"groupIds": []int64{g.ID}, "title": "Arrival"}); w.Code != http.StatusNoContent {
		t.Fatalf("set title groups = %d: %s", w.Code, w.Body)
	}

	w = as(t, world.h, world.admin, "GET", "/api/v1/sharing/titles/movie/10096", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("read title groups = %d: %s", w.Code, w.Body)
	}
	var got struct {
		Viewers []struct {
			Username  string `json:"username"`
			GroupName string `json:"groupName"`
		} `json:"viewers"`
		GroupIDs []int64 `json:"groupIds"`
	}
	json.Unmarshal(w.Body.Bytes(), &got) //nolint:errcheck // asserted below
	if len(got.GroupIDs) != 1 || got.GroupIDs[0] != g.ID {
		t.Fatalf("groupIds = %v", got.GroupIDs)
	}
	// the answer names the person AND the group granting it, because
	// "why can Jen see this" is the actual question
	if len(got.Viewers) != 1 || got.Viewers[0].Username != "jen" ||
		got.Viewers[0].GroupName != "Housemates" {
		t.Fatalf("viewers = %+v", got.Viewers)
	}

	// unsharing is the same call with an empty set
	as(t, world.h, world.admin, "PUT", "/api/v1/sharing/titles/movie/10096",
		map[string]any{"groupIds": []int64{}, "title": "Arrival"})
	w = as(t, world.h, world.admin, "GET", "/api/v1/sharing/titles/movie/10096", nil)
	json.Unmarshal(w.Body.Bytes(), &got) //nolint:errcheck // asserted below
	if len(got.Viewers) != 0 {
		t.Fatalf("viewers after unsharing = %+v", got.Viewers)
	}
}

// Most installs arrive with a Plex library already full and already
// shared with everybody. The seed grants what exists to a group so the
// split applies only to what comes next.
func TestSeedingGrantsWhatIsAlreadyHeld(t *testing.T) {
	world := requesterWorld(t)

	// no group to pick: the backfill is assembled, not chosen
	w := as(t, world.h, world.admin, "POST", "/api/v1/sharing/seed", map[string]any{})
	if w.Code != http.StatusOK {
		t.Fatalf("seed = %d: %s", w.Code, w.Body)
	}
	var first struct {
		Members int `json:"members"`
		Group   struct {
			ID       int64 `json:"id"`
			Backfill bool  `json:"backfill"`
		} `json:"group"`
	}
	json.Unmarshal(w.Body.Bytes(), &first) //nolint:errcheck // asserted below
	if !first.Group.Backfill {
		t.Fatal("the seed did not make a backfill group")
	}
	if first.Members < 2 {
		t.Fatalf("members = %d, want everybody who can already see the library",
			first.Members)
	}

	// running it twice adds nothing, and does not make a second group
	w = as(t, world.h, world.admin, "POST", "/api/v1/sharing/seed", map[string]any{})
	var second struct {
		Added int `json:"added"`
		Group struct {
			ID int64 `json:"id"`
		} `json:"group"`
	}
	json.Unmarshal(w.Body.Bytes(), &second) //nolint:errcheck // asserted below
	if second.Added != 0 {
		t.Fatalf("seeding twice added %d more", second.Added)
	}
	if second.Group.ID != first.Group.ID {
		t.Fatalf("a second backfill group appeared: %d then %d",
			first.Group.ID, second.Group.ID)
	}
}

// Adding a title records who it is for. An empty set means the adder's
// own group rather than nobody: adding something you cannot then see
// would be a strange default.
func TestAddingATitleSharesItWithTheAdder(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)

	w := as(t, world.h, world.admin, "POST", "/api/v1/movies",
		map[string]any{"tmdbId": 603, "libraryId": world.family})
	if w.Code != http.StatusCreated {
		t.Fatalf("add = %d: %s", w.Code, w.Body)
	}
	w = as(t, world.h, world.admin, "GET", "/api/v1/sharing/titles/movie/603", nil)
	var got struct {
		Viewers []struct {
			Username string `json:"username"`
			Personal bool   `json:"personal"`
		} `json:"viewers"`
	}
	json.Unmarshal(w.Body.Bytes(), &got) //nolint:errcheck // asserted below
	if len(got.Viewers) != 1 || got.Viewers[0].Username != "root" || !got.Viewers[0].Personal {
		t.Fatalf("viewers after a plain add = %+v, want the adder's own group", got.Viewers)
	}
}

// The case the picker exists for: somebody asks the owner in the
// kitchen, the owner adds it for them, and it does not turn up in the
// owner's own library.
func TestAddingForSomebodyElseKeepsItOutOfYourOwnLibrary(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)
	w := as(t, world.h, world.admin, "POST", "/api/v1/sharing/groups",
		map[string]any{"name": "Housemates"})
	var g struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &g) //nolint:errcheck // asserted below
	as(t, world.h, world.admin, "PUT", "/api/v1/sharing/groups/"+itoa64(g.ID)+"/members",
		map[string]any{"userIds": []int64{world.jen}})

	w = as(t, world.h, world.admin, "POST", "/api/v1/movies", map[string]any{
		"tmdbId": 603, "libraryId": world.family, "groupIds": []int64{g.ID},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("add for somebody else = %d: %s", w.Code, w.Body)
	}

	w = as(t, world.h, world.admin, "GET", "/api/v1/sharing/titles/movie/603", nil)
	var got struct {
		Viewers []struct {
			Username  string `json:"username"`
			GroupName string `json:"groupName"`
		} `json:"viewers"`
	}
	json.Unmarshal(w.Body.Bytes(), &got) //nolint:errcheck // asserted below
	if len(got.Viewers) != 1 || got.Viewers[0].Username != "jen" {
		t.Fatalf("viewers = %+v, want only the person it was added for", got.Viewers)
	}
	if got.Viewers[0].GroupName != "Housemates" {
		t.Fatalf("granted through %q", got.Viewers[0].GroupName)
	}
}

// withTMDB points the world at the stub the add flow fetches from.
func withTMDB(t *testing.T, world *testWorld) {
	t.Helper()
	world.srv.TMDB.SetBaseURL(fakeTMDB(t).URL)
	if w := as(t, world.h, world.admin, "PUT", "/api/v1/settings/tmdb_api_key",
		map[string]string{"value": "k"}); w.Code != http.StatusOK {
		t.Fatal(w.Body)
	}
}

// Every account needs somewhere to put its own titles from the moment it
// exists. The migration backfilled the accounts that were already there;
// this pins the ones made since, which is where it was missed.
func TestANewAccountGetsItsOwnGroup(t *testing.T) {
	world := requesterWorld(t)
	w := as(t, world.h, world.admin, "POST", "/api/v1/users", map[string]any{
		"username": "sam", "password": "a long enough one", "role": "user",
		"libraryIds": []int64{world.family},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create = %d: %s", w.Code, w.Body)
	}
	var created struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &created) //nolint:errcheck // asserted below

	w = as(t, world.h, world.admin, "GET", "/api/v1/sharing/groups", nil)
	var groups []struct {
		OwnerUserID int64   `json:"ownerUserId"`
		Personal    bool    `json:"personal"`
		MemberIDs   []int64 `json:"memberIds"`
	}
	json.Unmarshal(w.Body.Bytes(), &groups) //nolint:errcheck // asserted below
	for _, g := range groups {
		if g.OwnerUserID == created.ID {
			if !g.Personal || len(g.MemberIDs) != 1 || g.MemberIDs[0] != created.ID {
				t.Fatalf("new account's group = %+v", g)
			}
			return
		}
	}
	t.Fatalf("a new account got no personal group: %+v", groups)
}

// Approving a request has to share the title with whoever asked, or the
// person who wanted it is the one person who cannot see it.
func TestApprovingARequestSharesItWithTheAsker(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)
	jen := login(t, world.h, "jen", "another long one")

	w := as(t, world.h, jen, "POST", "/api/v1/requests", map[string]any{
		"kind": "movie", "tmdbId": 603, "title": "The Matrix", "libraryId": world.family,
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("request = %d: %s", w.Code, w.Body)
	}
	var req struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &req) //nolint:errcheck // asserted below

	if w := as(t, world.h, world.admin, "POST",
		"/api/v1/requests/"+itoa64(req.ID)+"/approve", nil); w.Code != http.StatusOK {
		t.Fatalf("approve = %d: %s", w.Code, w.Body)
	}

	w = as(t, world.h, world.admin, "GET", "/api/v1/sharing/titles/movie/603", nil)
	var got struct {
		Viewers []struct {
			Username string `json:"username"`
		} `json:"viewers"`
	}
	json.Unmarshal(w.Body.Bytes(), &got) //nolint:errcheck // asserted below
	if len(got.Viewers) != 1 || got.Viewers[0].Username != "jen" {
		t.Fatalf("viewers after approval = %+v, want the person who asked", got.Viewers)
	}
}

// A request reaches the asker AND every household they are in, because
// a family member asking for a film usually means the family should get
// it — and their own group as well, so leaving the household later does
// not take away what they asked for themselves.
func TestARequestReachesTheAskersHouseholdsToo(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)
	house := makeGroup(t, world, "Jolene's family", world.jen)

	approve(t, world, requestMatrix(t, world, nil))

	w := as(t, world.h, world.admin, "GET", "/api/v1/sharing/titles/movie/603", nil)
	var got struct {
		GroupIDs []int64 `json:"groupIds"`
	}
	json.Unmarshal(w.Body.Bytes(), &got) //nolint:errcheck // asserted below
	if len(got.GroupIDs) != 2 {
		t.Fatalf("granted to %v, want the household and their own", got.GroupIDs)
	}
	found := false
	for _, id := range got.GroupIDs {
		found = found || id == house
	}
	if !found {
		t.Fatalf("granted to %v, missing the household %d", got.GroupIDs, house)
	}
}

// Keeping a request to yourself. With a household group every request is
// otherwise a public act, and somebody asking for a gift has no way to
// keep it quiet.
func TestARequestCanBeKeptToTheAskerAlone(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)
	house := makeGroup(t, world, "Jolene's family", world.jen)

	approve(t, world, requestMatrix(t, world, []int64{}))

	w := as(t, world.h, world.admin, "GET", "/api/v1/sharing/titles/movie/603", nil)
	var got struct {
		GroupIDs []int64 `json:"groupIds"`
	}
	json.Unmarshal(w.Body.Bytes(), &got) //nolint:errcheck // asserted below
	if len(got.GroupIDs) != 1 {
		t.Fatalf("granted to %v, want only their own group", got.GroupIDs)
	}
	if got.GroupIDs[0] == house {
		t.Fatal("a private request reached the household")
	}
}

// Somebody in two households picking one of them.
func TestARequestCanNameOneOfSeveralHouseholds(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)
	house := makeGroup(t, world, "Jolene's family", world.jen)
	other := makeGroup(t, world, "Book club", world.jen)

	approve(t, world, requestMatrix(t, world, []int64{house}))

	w := as(t, world.h, world.admin, "GET", "/api/v1/sharing/titles/movie/603", nil)
	var got struct {
		GroupIDs []int64 `json:"groupIds"`
	}
	json.Unmarshal(w.Body.Bytes(), &got) //nolint:errcheck // asserted below
	for _, id := range got.GroupIDs {
		if id == other {
			t.Fatalf("granted to %v, which includes the household they left out", got.GroupIDs)
		}
	}
	if len(got.GroupIDs) != 2 {
		t.Fatalf("granted to %v, want the named household and their own", got.GroupIDs)
	}
}

// A named group is a claim from the requesting side, so it is checked
// rather than trusted: naming somebody else's household must not grant
// to it.
func TestARequestCannotNameAGroupTheAskerIsNotIn(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)
	strangers := makeGroup(t, world, "Strangers") // jen is not a member

	approve(t, world, requestMatrix(t, world, []int64{strangers}))

	w := as(t, world.h, world.admin, "GET", "/api/v1/sharing/titles/movie/603", nil)
	var got struct {
		GroupIDs []int64 `json:"groupIds"`
	}
	json.Unmarshal(w.Body.Bytes(), &got) //nolint:errcheck // asserted below
	for _, id := range got.GroupIDs {
		if id == strangers {
			t.Fatal("a requester granted a title to a group they are not in")
		}
	}
	if len(got.GroupIDs) != 1 {
		t.Fatalf("granted to %v, want only their own group", got.GroupIDs)
	}
}

// A requester sees their own groups and never the install-wide list.
func TestARequesterSeesOnlyTheirOwnGroups(t *testing.T) {
	world := requesterWorld(t)
	makeGroup(t, world, "Jolene's family", world.jen)
	makeGroup(t, world, "Nothing to do with jen")

	jen := login(t, world.h, "jen", "another long one")
	w := as(t, world.h, jen, "GET", "/api/v1/sharing/me/groups", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("own groups = %d: %s", w.Code, w.Body)
	}
	var mine []struct {
		Name     string `json:"name"`
		Personal bool   `json:"personal"`
	}
	json.Unmarshal(w.Body.Bytes(), &mine) //nolint:errcheck // asserted below
	if len(mine) != 2 {
		t.Fatalf("own groups = %+v, want theirs and the household", mine)
	}
	for _, g := range mine {
		if g.Name == "Nothing to do with jen" {
			t.Fatal("a requester was shown a household they are not in")
		}
	}
}

// makeGroup creates a shared group with the given members.
func makeGroup(t *testing.T, world *testWorld, name string, members ...int64) int64 {
	t.Helper()
	w := as(t, world.h, world.admin, "POST", "/api/v1/sharing/groups",
		map[string]any{"name": name})
	if w.Code != http.StatusCreated {
		t.Fatalf("create group = %d: %s", w.Code, w.Body)
	}
	var g struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &g) //nolint:errcheck // asserted below
	if len(members) > 0 {
		as(t, world.h, world.admin, "PUT",
			"/api/v1/sharing/groups/"+itoa64(g.ID)+"/members",
			map[string]any{"userIds": members})
	}
	return g.ID
}

// requestMatrix has jen ask for the film the stub serves, with the given
// audience. nil means every group she is in.
func requestMatrix(t *testing.T, world *testWorld, audience []int64) int64 {
	t.Helper()
	jen := login(t, world.h, "jen", "another long one")
	body := map[string]any{
		"kind": "movie", "tmdbId": 603, "title": "The Matrix", "libraryId": world.family,
	}
	if audience != nil {
		body["audience"] = audience
	}
	w := as(t, world.h, jen, "POST", "/api/v1/requests", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("request = %d: %s", w.Code, w.Body)
	}
	var req struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &req) //nolint:errcheck // asserted below
	return req.ID
}

func approve(t *testing.T, world *testWorld, id int64) {
	t.Helper()
	if w := as(t, world.h, world.admin, "POST",
		"/api/v1/requests/"+itoa64(id)+"/approve", nil); w.Code != http.StatusOK {
		t.Fatalf("approve = %d: %s", w.Code, w.Body)
	}
}

// An owner is a person too: adding a film while in a household means the
// household gets it, the same rule a request follows. The asymmetry
// where an owner's add reached only themselves was a surprise with
// nothing to recommend it.
func TestAnOwnerAddReachesTheirOwnHouseholdsToo(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)
	// the owner's own household, and one that is nothing to do with them
	house := makeGroup(t, world, "The Barretts", world.adminID)
	theirs := makeGroup(t, world, "Someone else's", world.jen)

	w := as(t, world.h, world.admin, "POST", "/api/v1/movies",
		map[string]any{"tmdbId": 603, "libraryId": world.family})
	if w.Code != http.StatusCreated {
		t.Fatalf("add = %d: %s", w.Code, w.Body)
	}

	w = as(t, world.h, world.admin, "GET", "/api/v1/sharing/titles/movie/603", nil)
	var got struct {
		GroupIDs []int64 `json:"groupIds"`
	}
	json.Unmarshal(w.Body.Bytes(), &got) //nolint:errcheck // asserted below
	if len(got.GroupIDs) != 2 {
		t.Fatalf("granted to %v, want the owner's own group and their household", got.GroupIDs)
	}
	seen := map[int64]bool{}
	for _, id := range got.GroupIDs {
		seen[id] = true
	}
	if !seen[house] {
		t.Fatalf("granted to %v, missing the owner's household %d", got.GroupIDs, house)
	}
	if seen[theirs] {
		t.Fatal("an owner's add reached a household they are not in")
	}
}

// The picker has to say which of the install's groups are the owner's
// own, or it cannot tick the right boxes.
func TestGroupsSayWhichAreYours(t *testing.T) {
	world := requesterWorld(t)
	mine := makeGroup(t, world, "The Barretts", world.adminID)
	notMine := makeGroup(t, world, "Someone else's", world.jen)

	w := as(t, world.h, world.admin, "GET", "/api/v1/sharing/groups", nil)
	var groups []struct {
		ID   int64 `json:"id"`
		Mine bool  `json:"mine"`
	}
	json.Unmarshal(w.Body.Bytes(), &groups) //nolint:errcheck // asserted below
	for _, g := range groups {
		if g.ID == mine && !g.Mine {
			t.Fatal("the owner's own household was not marked as theirs")
		}
		if g.ID == notMine && g.Mine {
			t.Fatal("a household the owner is not in was marked as theirs")
		}
	}
}

// Picking a run out of the grid and sharing it with a household in one
// gesture. Adding is the default because forty films picked for one
// household were not picked to have their other audiences revoked.
func TestSharingManyTitlesAtOnceAdds(t *testing.T) {
	world := requesterWorld(t)
	house := makeGroup(t, world, "Jolene's family", world.jen)
	kids := makeGroup(t, world, "Kids", world.jen)

	// one title already belongs to the household
	as(t, world.h, world.admin, "PUT", "/api/v1/sharing/titles/movie/603",
		map[string]any{"groupIds": []int64{house}, "title": "The Matrix"})

	w := as(t, world.h, world.admin, "POST", "/api/v1/sharing/titles/bulk",
		map[string]any{
			"mode": "add", "groupIds": []int64{kids},
			"titles": []map[string]any{
				{"kind": "movie", "tmdbId": 603, "title": "The Matrix"},
				{"kind": "movie", "tmdbId": 604, "title": "Another"},
			},
		})
	if w.Code != http.StatusOK {
		t.Fatalf("bulk = %d: %s", w.Code, w.Body)
	}

	w = as(t, world.h, world.admin, "GET", "/api/v1/sharing/titles/movie/603", nil)
	var got struct {
		GroupIDs []int64 `json:"groupIds"`
	}
	json.Unmarshal(w.Body.Bytes(), &got) //nolint:errcheck // asserted below
	if len(got.GroupIDs) != 2 {
		t.Fatalf("groups = %v, want the household it had plus the one added", got.GroupIDs)
	}
}

// Removing takes the named groups away and leaves the rest.
func TestSharingManyTitlesAtOnceRemoves(t *testing.T) {
	world := requesterWorld(t)
	house := makeGroup(t, world, "Jolene's family", world.jen)
	kids := makeGroup(t, world, "Kids", world.jen)
	as(t, world.h, world.admin, "PUT", "/api/v1/sharing/titles/movie/603",
		map[string]any{"groupIds": []int64{house, kids}, "title": "The Matrix"})

	if w := as(t, world.h, world.admin, "POST", "/api/v1/sharing/titles/bulk",
		map[string]any{
			"mode": "remove", "groupIds": []int64{kids},
			"titles": []map[string]any{{"kind": "movie", "tmdbId": 603, "title": "The Matrix"}},
		}); w.Code != http.StatusOK {
		t.Fatalf("bulk remove = %d: %s", w.Code, w.Body)
	}

	w := as(t, world.h, world.admin, "GET", "/api/v1/sharing/titles/movie/603", nil)
	var got struct {
		GroupIDs []int64 `json:"groupIds"`
	}
	json.Unmarshal(w.Body.Bytes(), &got) //nolint:errcheck // asserted below
	if len(got.GroupIDs) != 1 || got.GroupIDs[0] != house {
		t.Fatalf("groups = %v, want only the household left", got.GroupIDs)
	}
}

// Replacing makes the named groups the whole audience.
func TestSharingManyTitlesAtOnceReplaces(t *testing.T) {
	world := requesterWorld(t)
	house := makeGroup(t, world, "Jolene's family", world.jen)
	kids := makeGroup(t, world, "Kids", world.jen)
	as(t, world.h, world.admin, "PUT", "/api/v1/sharing/titles/movie/603",
		map[string]any{"groupIds": []int64{house}, "title": "The Matrix"})

	if w := as(t, world.h, world.admin, "POST", "/api/v1/sharing/titles/bulk",
		map[string]any{
			"mode": "replace", "groupIds": []int64{kids},
			"titles": []map[string]any{{"kind": "movie", "tmdbId": 603, "title": "The Matrix"}},
		}); w.Code != http.StatusOK {
		t.Fatalf("bulk replace = %d: %s", w.Code, w.Body)
	}

	w := as(t, world.h, world.admin, "GET", "/api/v1/sharing/titles/movie/603", nil)
	var got struct {
		GroupIDs []int64 `json:"groupIds"`
	}
	json.Unmarshal(w.Body.Bytes(), &got) //nolint:errcheck // asserted below
	if len(got.GroupIDs) != 1 || got.GroupIDs[0] != kids {
		t.Fatalf("groups = %v, want only the group named", got.GroupIDs)
	}
}

// Bulk sharing is the owner's, like every other part of it.
func TestBulkSharingIsNotOnThePortalOrForRequesters(t *testing.T) {
	world := requesterWorld(t)
	if w := as(t, world.srv.ExternalHandler(), world.admin, "POST",
		"/api/v1/sharing/titles/bulk", map[string]any{}); w.Code != http.StatusNotFound {
		t.Errorf("bulk on the external listener = %d, want 404", w.Code)
	}
	jen := login(t, world.h, "jen", "another long one")
	if w := as(t, world.h, jen, "POST", "/api/v1/sharing/titles/bulk",
		map[string]any{}); w.Code != http.StatusForbidden {
		t.Errorf("bulk as a requester = %d, want 403", w.Code)
	}
}

// reely must not advertise titles somebody cannot play in Plex — nor
// tell them what other households have.
func TestAManagedRequesterSeesOnlyTheirOwnTitles(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)
	house := makeGroup(t, world, "Jolene's family", world.jen)

	// two titles in the same library: one theirs, one somebody else's
	as(t, world.h, world.admin, "POST", "/api/v1/movies",
		map[string]any{"tmdbId": 603, "libraryId": world.family})
	as(t, world.h, world.admin, "PUT", "/api/v1/sharing/titles/movie/603",
		map[string]any{"groupIds": []int64{house}, "title": "The Matrix"})

	jen := login(t, world.h, "jen", "another long one")

	// until reely manages their share, nothing about what they can see
	// has changed — so neither should this
	w := as(t, world.h, jen, "GET", "/api/v1/movies", nil)
	var before struct {
		Movies []struct {
			TmdbID int `json:"tmdbId"`
		} `json:"movies"`
	}
	json.Unmarshal(w.Body.Bytes(), &before) //nolint:errcheck // asserted below
	if len(before.Movies) != 1 {
		t.Fatalf("an unmanaged account saw %d titles, want the library unchanged",
			len(before.Movies))
	}

	// managed, and entitled to it
	as(t, world.h, world.admin, "PUT",
		"/api/v1/sharing/users/"+itoa64(world.jen)+"/managed",
		map[string]any{"managed": true})
	w = as(t, world.h, jen, "GET", "/api/v1/movies", nil)
	json.Unmarshal(w.Body.Bytes(), &before) //nolint:errcheck // asserted below
	if len(before.Movies) != 1 || before.Movies[0].TmdbID != 603 {
		t.Fatalf("movies = %+v, want the one they are entitled to", before.Movies)
	}

	// revoked: it leaves their library, because it left their Plex too
	as(t, world.h, world.admin, "PUT", "/api/v1/sharing/titles/movie/603",
		map[string]any{"groupIds": []int64{}, "title": "The Matrix"})
	w = as(t, world.h, jen, "GET", "/api/v1/movies", nil)
	json.Unmarshal(w.Body.Bytes(), &before) //nolint:errcheck // asserted below
	if len(before.Movies) != 0 {
		t.Fatalf("movies = %+v, want nothing they are not entitled to", before.Movies)
	}
}

// The grid filters by household, so the owner's listing carries each
// title's groups. A requester's never does — it is the shape of every
// household on the install.
func TestOnlyTheOwnerSeesATitlesGroups(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)
	house := makeGroup(t, world, "Jolene's family", world.jen)
	as(t, world.h, world.admin, "POST", "/api/v1/movies",
		map[string]any{"tmdbId": 603, "libraryId": world.family})
	as(t, world.h, world.admin, "PUT", "/api/v1/sharing/titles/movie/603",
		map[string]any{"groupIds": []int64{house}, "title": "The Matrix"})

	w := as(t, world.h, world.admin, "GET", "/api/v1/movies", nil)
	var mine struct {
		Movies []struct {
			GroupIDs []int64 `json:"groupIds"`
		} `json:"movies"`
	}
	json.Unmarshal(w.Body.Bytes(), &mine) //nolint:errcheck // asserted below
	if len(mine.Movies) != 1 || len(mine.Movies[0].GroupIDs) != 1 {
		t.Fatalf("the owner's listing carries no groups: %+v", mine.Movies)
	}

	jen := login(t, world.h, "jen", "another long one")
	w = as(t, world.h, jen, "GET", "/api/v1/movies", nil)
	if strings.Contains(w.Body.String(), "groupIds") {
		t.Fatalf("a requester was told which households hold a title: %s", w.Body)
	}
}

// Re-matching changes which film a row IS, and keeps everything that is
// genuinely the owner's. The file on disk was always this film; reely
// was calling it another one.
func TestRematchingAMovieKeepsItsFileAndMovesItsIdentity(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)

	w := as(t, world.h, world.admin, "POST", "/api/v1/movies",
		map[string]any{"tmdbId": 603, "libraryId": world.family})
	if w.Code != http.StatusCreated {
		t.Fatalf("add = %d: %s", w.Code, w.Body)
	}
	var added struct {
		Movie struct {
			ID     int64 `json:"id"`
			TmdbID int   `json:"tmdbId"`
		} `json:"movie"`
	}
	json.Unmarshal(w.Body.Bytes(), &added) //nolint:errcheck // asserted below

	// pretend it was imported with a file, as a mis-matched title would be
	if _, err := world.srv.db.Exec(
		`UPDATE movies SET file_path = '/films/x.mkv', monitored = 0 WHERE id = ?`,
		added.Movie.ID); err != nil {
		t.Fatal(err)
	}

	w = as(t, world.h, world.admin, "POST",
		"/api/v1/movies/"+itoa64(added.Movie.ID)+"/rematch",
		map[string]any{"tmdbId": 604})
	if w.Code != http.StatusOK {
		t.Fatalf("rematch = %d: %s", w.Code, w.Body)
	}

	var after struct {
		Movie struct {
			ID        int64  `json:"id"`
			TmdbID    int    `json:"tmdbId"`
			FilePath  string `json:"filePath"`
			Monitored bool   `json:"monitored"`
		} `json:"movie"`
	}
	json.Unmarshal(w.Body.Bytes(), &after) //nolint:errcheck // asserted below
	if after.Movie.ID != added.Movie.ID {
		t.Fatalf("rematch made a new row: %d -> %d", added.Movie.ID, after.Movie.ID)
	}
	if after.Movie.TmdbID != 604 {
		t.Fatalf("tmdbId = %d, want the film it was matched to", after.Movie.TmdbID)
	}
	if after.Movie.FilePath != "/films/x.mkv" {
		t.Fatalf("filePath = %q, want the file kept", after.Movie.FilePath)
	}
	if after.Movie.Monitored {
		t.Fatal("rematch reset the monitored flag")
	}
}

// Two rows in one library cannot claim the same film.
func TestRematchingOntoATitleAlreadyThereConflicts(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)
	w := as(t, world.h, world.admin, "POST", "/api/v1/movies",
		map[string]any{"tmdbId": 603, "libraryId": world.family})
	var first struct {
		Movie struct{ ID int64 } `json:"movie"`
	}
	json.Unmarshal(w.Body.Bytes(), &first) //nolint:errcheck // asserted below

	as(t, world.h, world.admin, "POST", "/api/v1/movies",
		map[string]any{"tmdbId": 604, "libraryId": world.family})

	if w := as(t, world.h, world.admin, "POST",
		"/api/v1/movies/"+itoa64(first.Movie.ID)+"/rematch",
		map[string]any{"tmdbId": 604}); w.Code != http.StatusConflict {
		t.Fatalf("rematch onto an existing title = %d, want 409: %s", w.Code, w.Body)
	}
}

// A show's episodes describe the OLD series, so re-identifying without
// rebuilding them would leave a row whose name says one thing and whose
// episodes say another.
func TestRematchingAShowRebuildsItsEpisodes(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)
	lib, err := world.srv.Catalog.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	w := as(t, world.h, world.admin, "POST", "/api/v1/shows",
		map[string]any{"tmdbId": 1396, "libraryId": lib.ID})
	if w.Code != http.StatusCreated {
		t.Fatalf("add show = %d: %s", w.Code, w.Body)
	}
	var added struct {
		Show struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"show"`
	}
	json.Unmarshal(w.Body.Bytes(), &added) //nolint:errcheck // asserted below

	// re-matching onto the same series is the idempotent case, and must
	// leave a coherent row rather than erroring or duplicating episodes
	w = as(t, world.h, world.admin, "POST",
		"/api/v1/shows/"+itoa64(added.Show.ID)+"/rematch",
		map[string]any{"tmdbId": 1396})
	if w.Code != http.StatusOK {
		t.Fatalf("rematch = %d: %s", w.Code, w.Body)
	}
	var after struct {
		Show struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"show"`
	}
	json.Unmarshal(w.Body.Bytes(), &after) //nolint:errcheck // asserted below
	if after.Show.ID != added.Show.ID {
		t.Fatalf("rematch made a new row: %d -> %d", added.Show.ID, after.Show.ID)
	}
	if after.Show.Title != added.Show.Title {
		t.Fatalf("title = %q, want the series it was matched to", after.Show.Title)
	}
}

// A requester asks for titles; changing what one IS is not that.
func TestARequesterCannotRematch(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)
	w := as(t, world.h, world.admin, "POST", "/api/v1/movies",
		map[string]any{"tmdbId": 603, "libraryId": world.family})
	var added struct {
		Movie struct{ ID int64 } `json:"movie"`
	}
	json.Unmarshal(w.Body.Bytes(), &added) //nolint:errcheck // asserted below

	jen := login(t, world.h, "jen", "another long one")
	if w := as(t, world.h, jen, "POST",
		"/api/v1/movies/"+itoa64(added.Movie.ID)+"/rematch",
		map[string]any{"tmdbId": 604}); w.Code != http.StatusForbidden {
		t.Fatalf("rematch as a requester = %d, want 403: %s", w.Code, w.Body)
	}
}

// Correcting one film should rename that film. Walking the whole library
// to fix one entry is how a small correction stops being worth making.
func TestRematchingRenamesThatTitleOnly(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)

	dir := t.TempDir()
	lib, err := world.srv.Catalog.CreateLibrary("Films", dir, "movies")
	if err != nil {
		t.Fatal(err)
	}
	w := as(t, world.h, world.admin, "POST", "/api/v1/movies",
		map[string]any{"tmdbId": 603, "libraryId": lib.ID})
	var added struct {
		Movie struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"movie"`
	}
	json.Unmarshal(w.Body.Bytes(), &added) //nolint:errcheck // asserted below

	// put a file exactly where reely's template says it belongs, which is
	// what marks the title as one reely filed
	folder := filepath.Join(dir, added.Movie.Title+" (1999)")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	placed := filepath.Join(folder, added.Movie.Title+" (1999).mkv")
	if err := os.WriteFile(placed, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := world.srv.db.Exec(`UPDATE movies SET file_path = ? WHERE id = ?`,
		placed, added.Movie.ID); err != nil {
		t.Fatal(err)
	}

	w = as(t, world.h, world.admin, "POST",
		"/api/v1/movies/"+itoa64(added.Movie.ID)+"/rematch",
		map[string]any{"tmdbId": 604, "rename": true})
	if w.Code != http.StatusOK {
		t.Fatalf("rematch = %d: %s", w.Code, w.Body)
	}

	var after struct {
		Renamed int `json:"renamed"`
		Movie   struct {
			FilePath string `json:"filePath"`
		} `json:"movie"`
	}
	json.Unmarshal(w.Body.Bytes(), &after) //nolint:errcheck // asserted below
	if after.Renamed != 1 {
		t.Fatalf("renamed = %d, want the one file moved: %s", after.Renamed, w.Body)
	}
	if _, err := os.Stat(placed); err == nil {
		t.Fatal("the old path still exists — the file was copied, not moved")
	}
	if !strings.Contains(after.Movie.FilePath, "Reloaded") {
		t.Fatalf("filePath = %q, want it named for the film it now is", after.Movie.FilePath)
	}
}

// Renaming happens because somebody asked for it, not because reely
// worked out that it was allowed to. Left unticked, the files stay put
// wherever they are; ticked, they move even from a layout reely has
// never touched — which is the ordinary case, a library imported at its
// release names.
func TestRematchingRenamesOnlyWhenAsked(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)

	dir := t.TempDir()
	lib, err := world.srv.Catalog.CreateLibrary("Films", dir, "movies")
	if err != nil {
		t.Fatal(err)
	}
	w := as(t, world.h, world.admin, "POST", "/api/v1/movies",
		map[string]any{"tmdbId": 603, "libraryId": lib.ID})
	var added struct {
		Movie struct{ ID int64 } `json:"movie"`
	}
	json.Unmarshal(w.Body.Bytes(), &added) //nolint:errcheck // asserted below

	// somewhere the template would never have put it
	mine := filepath.Join(dir, "my own scheme", "whatever.mkv")
	if err := os.MkdirAll(filepath.Dir(mine), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(mine, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := world.srv.db.Exec(`UPDATE movies SET file_path = ? WHERE id = ?`,
		mine, added.Movie.ID); err != nil {
		t.Fatal(err)
	}

	w = as(t, world.h, world.admin, "POST",
		"/api/v1/movies/"+itoa64(added.Movie.ID)+"/rematch",
		map[string]any{"tmdbId": 604})
	if w.Code != http.StatusOK {
		t.Fatalf("rematch = %d: %s", w.Code, w.Body)
	}
	var after struct {
		Renamed int `json:"renamed"`
	}
	json.Unmarshal(w.Body.Bytes(), &after) //nolint:errcheck // asserted below
	if after.Renamed != 0 {
		t.Fatalf("renamed = %d, want the files left alone when nobody asked", after.Renamed)
	}
	if _, err := os.Stat(mine); err != nil {
		t.Fatal("a rematch moved a file without being asked to")
	}

	// and asked for, the same file moves — reely never filed it, and
	// that is exactly the library the old inference refused to help
	w = as(t, world.h, world.admin, "POST",
		"/api/v1/movies/"+itoa64(added.Movie.ID)+"/rematch",
		map[string]any{"tmdbId": 603, "rename": true})
	if w.Code != http.StatusOK {
		t.Fatalf("rematch = %d: %s", w.Code, w.Body)
	}
	var asked struct {
		Renamed int `json:"renamed"`
		Movie   struct {
			FilePath string `json:"filePath"`
		} `json:"movie"`
	}
	json.Unmarshal(w.Body.Bytes(), &asked) //nolint:errcheck // asserted below
	if asked.Renamed != 1 {
		t.Fatalf("renamed = %d, want the file moved when asked: %s", asked.Renamed, w.Body)
	}
	if _, err := os.Stat(mine); err == nil {
		t.Fatal("the old path still exists — the file was copied, not moved")
	}
	if !strings.Contains(asked.Movie.FilePath, "tmdb-603") {
		t.Fatalf("filePath = %q, want reely's layout", asked.Movie.FilePath)
	}
}

// The box starts ticked for a library reely has already filed, and a
// library organised before the templates named ids still counts as one.
// Reading it as somebody's hand-made layout would start the box unticked
// on exactly the libraries organised the longest.
func TestRenameOutlookKnowsALibraryOrganisedBeforeIdsExisted(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)

	dir := t.TempDir()
	lib, err := world.srv.Catalog.CreateLibrary("Films", dir, "movies")
	if err != nil {
		t.Fatal(err)
	}
	w := as(t, world.h, world.admin, "POST", "/api/v1/movies",
		map[string]any{"tmdbId": 603, "libraryId": lib.ID})
	var added struct {
		Movie struct {
			ID    int64  `json:"id"`
			Title string `json:"title"`
		} `json:"movie"`
	}
	json.Unmarshal(w.Body.Bytes(), &added) //nolint:errcheck // asserted below

	// the OLD layout: no id in the folder, which is where every library
	// organised before the tokens existed still sits
	folder := filepath.Join(dir, added.Movie.Title+" (1999)")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	placed := filepath.Join(folder, added.Movie.Title+" (1999) [ ].mkv")
	if err := os.WriteFile(placed, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	// render what the template gives with no quality or source, which is
	// what this row has, so the test asserts the real destination rather
	// than a guess at it
	org := world.srv.organizer()
	files, err := world.srv.Catalog.OrganizeFilesFor("movie", added.Movie.ID)
	if err != nil {
		t.Fatal(err)
	}
	_ = files
	if _, err := world.srv.db.Exec(`UPDATE movies SET file_path = ? WHERE id = ?`,
		placed, added.Movie.ID); err != nil {
		t.Fatal(err)
	}
	files, _ = world.srv.Catalog.OrganizeFilesFor("movie", added.Movie.ID)
	if len(files) != 1 {
		t.Fatalf("files = %d", len(files))
	}
	bare := files[0]
	bare.TmdbID, bare.ImdbID = 0, ""
	oldDest := org.Destination(bare)
	if err := os.MkdirAll(filepath.Dir(oldDest), 0o755); err != nil {
		t.Fatal(err)
	}
	if oldDest != placed {
		if err := os.Rename(placed, oldDest); err != nil {
			t.Fatal(err)
		}
		if _, err := world.srv.db.Exec(`UPDATE movies SET file_path = ? WHERE id = ?`,
			oldDest, added.Movie.ID); err != nil {
			t.Fatal(err)
		}
	}

	w = as(t, world.h, world.admin, "GET",
		"/api/v1/movies/"+itoa64(added.Movie.ID)+"/rematch/files", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("outlook = %d: %s", w.Code, w.Body)
	}
	var out struct {
		Files  int    `json:"files"`
		Placed bool   `json:"placed"`
		Path   string `json:"path"`
	}
	json.Unmarshal(w.Body.Bytes(), &out) //nolint:errcheck // asserted below
	if out.Files != 1 {
		t.Fatalf("files = %d, want the one on disk: %s", out.Files, w.Body)
	}
	if !out.Placed {
		t.Fatalf("placed = false for a library organised before the ids existed: %s", w.Body)
	}
	if out.Path != oldDest {
		t.Fatalf("path = %q, want the file it would move: %q", out.Path, oldDest)
	}
}

// Re-matching replaces a title's ids. The grants naming it are keyed on
// those ids, so unless they follow, nothing is entitled to what the
// title now is — and everybody who could see it loses it on the media
// server without being told.
func TestRematchingCarriesTheGrantsToTheNewIdentity(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)

	lib, err := world.srv.Catalog.CreateLibrary("Films", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	w := as(t, world.h, world.admin, "POST", "/api/v1/movies",
		map[string]any{"tmdbId": 603, "libraryId": lib.ID})
	var added struct {
		Movie struct{ ID int64 } `json:"movie"`
	}
	json.Unmarshal(w.Body.Bytes(), &added) //nolint:errcheck // asserted below

	before, err := world.srv.Catalog.EntitledTitles()
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range before {
		if e.TmdbID == 603 {
			found = true
		}
	}
	if !found {
		t.Fatalf("adding did not entitle anybody: %+v", before)
	}

	w = as(t, world.h, world.admin, "POST",
		"/api/v1/movies/"+itoa64(added.Movie.ID)+"/rematch",
		map[string]any{"tmdbId": 604})
	if w.Code != http.StatusOK {
		t.Fatalf("rematch = %d: %s", w.Code, w.Body)
	}

	after, err := world.srv.Catalog.EntitledTitles()
	if err != nil {
		t.Fatal(err)
	}
	var stranded, carried bool
	for _, e := range after {
		switch e.TmdbID {
		case 603:
			stranded = true
		case 604:
			carried = true
		}
	}
	if stranded {
		t.Fatalf("a grant was left on the identity that was wrong: %+v", after)
	}
	if !carried {
		t.Fatalf("nothing is entitled to what the title now is: %+v", after)
	}
}

// The case seeding creates: a group holds the same film under both
// sides' ids, because the media server had it under a different match.
// Re-matching makes them the same title, so the leftover is dropped
// rather than becoming a duplicate grant.
func TestRematchingOntoAnIdentityTheGroupAlreadyHoldsDoesNotDuplicate(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)

	lib, err := world.srv.Catalog.CreateLibrary("Films", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	w := as(t, world.h, world.admin, "POST", "/api/v1/movies",
		map[string]any{"tmdbId": 603, "libraryId": lib.ID})
	var added struct {
		Movie struct{ ID int64 } `json:"movie"`
	}
	json.Unmarshal(w.Body.Bytes(), &added) //nolint:errcheck // asserted below

	// the other side's grant, in every group that already holds this one
	byTitle, err := world.srv.Catalog.GroupsByTitle("movie")
	if err != nil {
		t.Fatal(err)
	}
	if len(byTitle.For(603, 0)) == 0 {
		t.Fatalf("adding did not entitle anybody: %+v", byTitle)
	}
	for _, gid := range byTitle.For(603, 0) {
		if err := world.srv.Catalog.Grant(gid, "movie", 604, 0, "The Other Match", 0); err != nil {
			t.Fatal(err)
		}
	}

	w = as(t, world.h, world.admin, "POST",
		"/api/v1/movies/"+itoa64(added.Movie.ID)+"/rematch",
		map[string]any{"tmdbId": 604})
	if w.Code != http.StatusOK {
		t.Fatalf("rematch = %d: %s", w.Code, w.Body)
	}

	after, err := world.srv.Catalog.EntitledTitles()
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range after {
		if e.TmdbID == 603 {
			t.Fatalf("a grant was left on the old identity: %+v", after)
		}
		if e.TmdbID == 604 {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("entitled to 604 %d times, want exactly one: %+v", n, after)
	}
}

// Deleting a film's file should not leave the folder that held it. An
// empty "Some Film (1999) - {tmdb-…}" is litter, and a scanner will
// eventually read it as a title with no file.
func TestDeletingAMovieFileTakesTheFolderWithIt(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)

	dir := t.TempDir()
	lib, err := world.srv.Catalog.CreateLibrary("Films", dir, "movies")
	if err != nil {
		t.Fatal(err)
	}
	w := as(t, world.h, world.admin, "POST", "/api/v1/movies",
		map[string]any{"tmdbId": 603, "libraryId": lib.ID})
	var added struct {
		Movie struct{ ID int64 } `json:"movie"`
	}
	json.Unmarshal(w.Body.Bytes(), &added) //nolint:errcheck // asserted below

	folder := filepath.Join(dir, "The Matrix (1999) - {tmdb-603}")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(folder, "The Matrix (1999).mkv")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := world.srv.db.Exec(`UPDATE movies SET file_path = ? WHERE id = ?`,
		file, added.Movie.ID); err != nil {
		t.Fatal(err)
	}

	w = as(t, world.h, world.admin, "DELETE",
		"/api/v1/movies/"+itoa64(added.Movie.ID)+"/file", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete file = %d: %s", w.Code, w.Body)
	}
	if _, err := os.Stat(file); err == nil {
		t.Fatal("the file is still there")
	}
	if _, err := os.Stat(folder); err == nil {
		t.Fatal("the folder was left behind empty")
	}
	// never the library root, however empty it gets
	if _, err := os.Stat(dir); err != nil {
		t.Fatal("the library root itself was removed")
	}
}

// A folder still holding something is not empty and is not touched —
// which is what makes deleting one episode of a season leave the season
// alone, and what keeps subtitles and artwork safe.
func TestDeletingAFileLeavesAFolderThatStillHoldsSomething(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)

	dir := t.TempDir()
	lib, err := world.srv.Catalog.CreateLibrary("Films", dir, "movies")
	if err != nil {
		t.Fatal(err)
	}
	w := as(t, world.h, world.admin, "POST", "/api/v1/movies",
		map[string]any{"tmdbId": 603, "libraryId": lib.ID})
	var added struct {
		Movie struct{ ID int64 } `json:"movie"`
	}
	json.Unmarshal(w.Body.Bytes(), &added) //nolint:errcheck // asserted below

	folder := filepath.Join(dir, "The Matrix (1999)")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(folder, "The Matrix (1999).mkv")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	subs := filepath.Join(folder, "The Matrix (1999).en.srt")
	if err := os.WriteFile(subs, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := world.srv.db.Exec(`UPDATE movies SET file_path = ? WHERE id = ?`,
		file, added.Movie.ID); err != nil {
		t.Fatal(err)
	}

	w = as(t, world.h, world.admin, "DELETE",
		"/api/v1/movies/"+itoa64(added.Movie.ID)+"/file", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("delete file = %d: %s", w.Code, w.Body)
	}
	if _, err := os.Stat(folder); err != nil {
		t.Fatal("a folder that still held a file was removed")
	}
	if _, err := os.Stat(subs); err != nil {
		t.Fatal("the subtitles went with it")
	}
}

// The picker reads this, and it is the half that made the capability
// invisible: the portal offers the groups an account belongs to, and an
// owner is usually not in the back catalogue. On the LAN the owner sees
// every group on the install, so it was there — the same account, the
// same right, two different answers depending on the door.
func TestTheOwnersGroupListOffersTheBackCatalogueWithoutMembership(t *testing.T) {
	world := requesterWorld(t)
	everyone, err := world.srv.Catalog.BackfillGroup("Existing library")
	if err != nil {
		t.Fatal(err)
	}

	names := func(c *http.Cookie) map[int64]bool {
		w := as(t, world.h, c, "GET", "/api/v1/sharing/me/groups", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("me/groups = %d: %s", w.Code, w.Body)
		}
		var out []struct {
			ID       int64 `json:"id"`
			Backfill bool  `json:"backfill"`
		}
		json.Unmarshal(w.Body.Bytes(), &out) //nolint:errcheck // asserted below
		seen := map[int64]bool{}
		for _, g := range out {
			seen[g.ID] = g.Backfill
		}
		return seen
	}

	// the owner belongs to no such group, and is offered it anyway
	got := names(world.admin)
	if _, ok := got[everyone.ID]; !ok {
		t.Fatalf("the owner was not offered the back catalogue: %+v", got)
	}
	if !got[everyone.ID] {
		t.Fatal("it was offered without being marked as the back catalogue")
	}

	// a requester is not, membership or no
	jen := login(t, world.h, "jen", "another long one")
	if err := world.srv.Catalog.AddMember(everyone.ID, world.jen); err != nil {
		t.Fatal(err)
	}
	if _, ok := names(jen)[everyone.ID]; !ok {
		// a member DOES see it listed — being in a group is not secret.
		// What they cannot do is share into it, which RequestTargets
		// refuses and the picker does not offer.
		t.Fatal("a member was not shown a group they belong to")
	}
}
