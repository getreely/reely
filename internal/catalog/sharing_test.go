package catalog

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/getreely/reely/internal/db"
)

// sharingStore gives a store and three accounts: a household of two and
// somebody on their own.
func sharingStore(t *testing.T) (*Store, int64, int64, int64) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	s := New(conn)
	ids := make([]int64, 0, 3)
	for _, name := range []string{"jo", "sam", "alex"} {
		res, err := conn.Exec(`INSERT INTO users (username, password_hash, role)
			VALUES (?, 'x', 'user')`, name)
		if err != nil {
			t.Fatal(err)
		}
		id, _ := res.LastInsertId()
		if err := s.EnsurePersonalGroup(id, name); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return s, ids[0], ids[1], ids[2]
}

// The empty case is the dangerous one: Plex reads an empty restriction
// as no restriction and shows that person the entire library. Somebody
// entitled to nothing has to end up naming labels no item carries —
// which is what hides everything, rather than the sentinel specifically.
func TestNobodyEntitledSeesNothingRatherThanEverything(t *testing.T) {
	s, jo, _, _ := sharingStore(t)
	got, err := s.RestrictionFor(jo, "movie")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got) == "label=" || strings.TrimSpace(got) == "" {
		t.Fatalf("restriction %q would show them the whole library", got)
	}
	// it names their own group, which holds nothing — so nothing matches
	own, err := s.DefaultGroup(jo)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, own.Label) {
		t.Fatalf("restriction = %q, want their own group named", got)
	}
	labels, err := s.LabelsFor("movie", 10096, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 0 {
		t.Fatalf("an unentitled title carries %v, so the restriction would match it", labels)
	}
}

// Somebody in no group at all still must not get an empty restriction.
func TestAnAccountInNoGroupGetsTheSentinel(t *testing.T) {
	s, jo, _, _ := sharingStore(t)
	own, err := s.DefaultGroup(jo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`DELETE FROM share_group_members WHERE user_id = ?`, jo); err != nil {
		t.Fatal(err)
	}
	_ = own
	got, err := s.RestrictionFor(jo, "movie")
	if err != nil {
		t.Fatal(err)
	}
	if got != "label="+NoneLabel {
		t.Fatalf("restriction = %q, want the none label", got)
	}
}

// A household is one group with several members, and a title granted to
// it carries ONE label that every member's restriction names.
func TestAHouseholdSharesOneLabel(t *testing.T) {
	s, jo, sam, alex := sharingStore(t)
	house, err := s.CreateGroup("Jolene's family")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []int64{jo, sam} {
		if err := s.AddMember(house.ID, u); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Grant(house.ID, "movie", 10096, 0, "Arrival", 0); err != nil {
		t.Fatal(err)
	}

	labels, err := s.LabelsFor("movie", 10096, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 1 || labels[0] != house.Label {
		t.Fatalf("labels on a household title = %v, want just %q", labels, house.Label)
	}
	for _, u := range []int64{jo, sam} {
		got, err := s.RestrictionFor(u, "movie")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(got, house.Label) {
			t.Fatalf("member restriction %q does not name %q", got, house.Label)
		}
	}
	// and the person outside the household is not swept in
	got, err := s.RestrictionFor(alex, "movie")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, house.Label) {
		t.Fatalf("a non-member's restriction %q names the household label", got)
	}
}

// Adding a fourth person costs one restriction string and no re-tag:
// the labels on the title must not change.
func TestAddingAMemberDoesNotRetagTitles(t *testing.T) {
	s, jo, sam, _ := sharingStore(t)
	house, _ := s.CreateGroup("Jolene's family")
	if err := s.AddMember(house.ID, jo); err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(house.ID, "movie", 10096, 0, "Arrival", 0); err != nil {
		t.Fatal(err)
	}
	before, _ := s.LabelsFor("movie", 10096, 0)

	if err := s.AddMember(house.ID, sam); err != nil {
		t.Fatal(err)
	}
	after, _ := s.LabelsFor("movie", 10096, 0)
	if len(before) != len(after) || before[0] != after[0] {
		t.Fatalf("labels changed when a member joined: %v -> %v", before, after)
	}
	got, _ := s.RestrictionFor(sam, "movie")
	if !strings.Contains(got, house.Label) {
		t.Fatalf("the new member's restriction %q does not name the household", got)
	}
}

// Removing somebody revokes what the group granted and leaves what they
// asked for themselves.
func TestLeavingAHouseholdKeepsYourOwnTitles(t *testing.T) {
	s, jo, _, _ := sharingStore(t)
	own, err := s.DefaultGroup(jo)
	if err != nil {
		t.Fatal(err)
	}
	house, _ := s.CreateGroup("Jolene's family")
	if err := s.AddMember(house.ID, jo); err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(house.ID, "movie", 10096, 0, "Arrival", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(own.ID, "movie", 27205, 0, "Inception", 0); err != nil {
		t.Fatal(err)
	}

	if err := s.RemoveMember(house.ID, jo); err != nil {
		t.Fatal(err)
	}
	got, err := s.RestrictionFor(jo, "movie")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, house.Label) {
		t.Fatalf("restriction %q still names the household they left", got)
	}
	if !strings.Contains(got, own.Label) {
		t.Fatalf("restriction %q dropped their own group", got)
	}
}

// A show that came from TheTVDB may carry no TMDB id at all, and the
// design skipped exactly those before this was pinned.
func TestAShowResolvesOnItsTvdbIdAlone(t *testing.T) {
	s, jo, _, _ := sharingStore(t)
	own, _ := s.DefaultGroup(jo)
	if err := s.Grant(own.ID, "show", 0, 778411, "Some Show", 0); err != nil {
		t.Fatal(err)
	}
	labels, err := s.LabelsFor("show", 0, 778411)
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 1 || labels[0] != own.Label {
		t.Fatalf("labels for a tvdb-only show = %v, want %q", labels, own.Label)
	}
	got, _ := s.RestrictionFor(jo, "show")
	if !strings.Contains(got, own.Label) {
		t.Fatalf("show restriction %q does not name their group", got)
	}
	// The restriction names the same groups for both kinds; what keeps a
	// show entitlement out of the movie library is that no MOVIE carries
	// the label. The labels discriminate, not the restriction.
	if labels, err := s.LabelsFor("movie", 0, 778411); err != nil || len(labels) != 0 {
		t.Fatalf("movie labels for a show-only entitlement = %v (%v)", labels, err)
	}
}

// Plex title-cases tags on write, so a label read back never matches
// what was sent byte for byte.
func TestLabelOwnershipSurvivesPlexCasing(t *testing.T) {
	for _, l := range []string{"reely.jolene_family", "Reely.jolene_family", "REELY.JO"} {
		if !Managed(l) {
			t.Fatalf("%q should be recognised as reely's to manage", l)
		}
	}
	for _, l := range []string{"test", "Test2", "christmas", "4k"} {
		if Managed(l) {
			t.Fatalf("%q is a hand-made label and must never be touched", l)
		}
	}
}

// The restriction string is what Plex parses. A label carrying a comma
// or a space must not be able to widen it.
func TestRestrictionEncodesSeparatorsAndValues(t *testing.T) {
	got := Restriction([]string{"reely.g1", "reely.g2"})
	if got != "label=reely.g1%2Creely.g2" {
		t.Fatalf("restriction = %q", got)
	}
	sneaky := Restriction([]string{"reely.g1,everything else"})
	if strings.Count(sneaky, "%2C") != 1 || strings.Contains(sneaky, ",") {
		t.Fatalf("a comma inside a label leaked into the restriction: %q", sneaky)
	}
}

// Deleting a group revokes every title it grants, so one with members is
// refused rather than cascaded.
func TestAGroupWithMembersIsNotDeletable(t *testing.T) {
	s, jo, _, _ := sharingStore(t)
	house, _ := s.CreateGroup("Jolene's family")
	if err := s.AddMember(house.ID, jo); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteGroup(house.ID); !errors.Is(err, ErrGroupInUse) {
		t.Fatalf("delete with members = %v, want ErrGroupInUse", err)
	}
	if err := s.RemoveMember(house.ID, jo); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteGroup(house.ID); err != nil {
		t.Fatalf("delete after emptying: %v", err)
	}
}

// An account must always have somewhere to put its own titles.
func TestAPersonalGroupCannotBeEmptiedOrDeleted(t *testing.T) {
	s, jo, _, _ := sharingStore(t)
	own, err := s.DefaultGroup(jo)
	if err != nil {
		t.Fatal(err)
	}
	if !own.Personal {
		t.Fatal("a fresh account should default to its personal group")
	}
	if err := s.RemoveMember(own.ID, jo); err == nil {
		t.Fatal("removing an owner from their personal group should be refused")
	}
	if err := s.DeleteGroup(own.ID); err == nil {
		t.Fatal("deleting a personal group should be refused")
	}
}

// Approving the same title twice, or a reconcile re-running, costs
// nothing.
func TestGrantingTwiceIsOneEntitlement(t *testing.T) {
	s, jo, _, _ := sharingStore(t)
	own, _ := s.DefaultGroup(jo)
	for i := 0; i < 3; i++ {
		if err := s.Grant(own.ID, "movie", 10096, 0, "Arrival", 0); err != nil {
			t.Fatalf("grant %d: %v", i, err)
		}
	}
	g, err := s.Group(own.ID)
	if err != nil {
		t.Fatal(err)
	}
	if g.Titles != 1 {
		t.Fatalf("titles after three identical grants = %d, want 1", g.Titles)
	}
}

// The label is readable, so somebody looking at a film in Plex can tell
// what the tag means without coming back to reely.
func TestAGroupLabelReadsLikeItsName(t *testing.T) {
	s, _, _, _ := sharingStore(t)
	house, err := s.CreateGroup("Jolene's family")
	if err != nil {
		t.Fatal(err)
	}
	if house.Label != "reely.jolenes_family" {
		t.Fatalf("label = %q, want it derived from the name", house.Label)
	}
	if !Managed(house.Label) {
		t.Fatalf("%q must still be recognised as reely's", house.Label)
	}
}

// Renaming moves the label with the name. Nothing needs re-tagging by
// hand: the reconcile pass writes the whole reely-owned set for each
// item, so the old label falls off — and the entitlements deciding which
// items those are do not move.
func TestRenamingAGroupMovesItsLabelAndKeepsItsTitles(t *testing.T) {
	s, jo, _, _ := sharingStore(t)
	house, _ := s.CreateGroup("Jolene's family")
	if err := s.AddMember(house.ID, jo); err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(house.ID, "movie", 10096, 0, "Arrival", 0); err != nil {
		t.Fatal(err)
	}

	if err := s.RenameGroup(house.ID, "The Barretts"); err != nil {
		t.Fatal(err)
	}
	after, err := s.Group(house.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Label == house.Label {
		t.Fatalf("label did not move on rename: still %q", after.Label)
	}
	if after.Label != "reely.the_barretts" {
		t.Fatalf("label = %q, want it to follow the new name", after.Label)
	}
	if after.Titles != 1 {
		t.Fatalf("titles after rename = %d, want the entitlement to survive", after.Titles)
	}
	// what gets written to Plex follows immediately, with no re-tag step
	labels, _ := s.LabelsFor("movie", 10096, 0)
	if len(labels) != 1 || labels[0] != after.Label {
		t.Fatalf("labels after rename = %v, want just %q", labels, after.Label)
	}
	got, _ := s.RestrictionFor(jo, "movie")
	if !strings.Contains(got, after.Label) || strings.Contains(got, house.Label) {
		t.Fatalf("restriction %q did not follow the rename", got)
	}
}

// Two groups may legitimately be called the same thing. Their labels may
// not, because the label is what Plex matches on.
func TestGroupsWithTheSameNameGetDistinctLabels(t *testing.T) {
	s, _, _, _ := sharingStore(t)
	a, err := s.CreateGroup("The Smiths")
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.CreateGroup("The Smiths")
	if err != nil {
		t.Fatal(err)
	}
	if a.Label == b.Label {
		t.Fatalf("both groups took the label %q", a.Label)
	}
	if b.Label != "reely.the_smiths_2" {
		t.Fatalf("second label = %q", b.Label)
	}
}

// A group called "None" must not take the label that means "entitled to
// nothing", or its members would hand that person their titles.
func TestAGroupCannotClaimTheNoneLabel(t *testing.T) {
	s, _, _, _ := sharingStore(t)
	g, err := s.CreateGroup("None")
	if err != nil {
		t.Fatal(err)
	}
	if strings.EqualFold(g.Label, NoneLabel) {
		t.Fatalf("a group claimed the sentinel label %q", g.Label)
	}
}

// A name with nothing sluggable in it still has to produce a label.
func TestAnUnsluggableNameStillGetsALabel(t *testing.T) {
	s, _, _, _ := sharingStore(t)
	g, err := s.CreateGroup("!!!")
	if err != nil {
		t.Fatal(err)
	}
	if !Managed(g.Label) || g.Label == LabelPrefix {
		t.Fatalf("label = %q", g.Label)
	}
}

// The seed puts everyone in one group so nobody loses what they can
// already see. That group must not then become where their requests
// land, or the first thing anybody asks for would be shared with the
// whole install.
func TestBelongingToAWideGroupDoesNotWidenYourOwnRequests(t *testing.T) {
	s, jo, sam, _ := sharingStore(t)
	everyone, err := s.CreateGroup("Everyone")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range []int64{jo, sam} {
		if err := s.AddMember(everyone.ID, u); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.GrantHeld(everyone.ID, 0); err != nil {
		t.Fatal(err)
	}

	// where jo's next request goes
	target, err := s.DefaultGroup(jo)
	if err != nil {
		t.Fatal(err)
	}
	if !target.Personal || target.ID == everyone.ID {
		t.Fatalf("requests would land in %q, want jo's own group", target.Name)
	}

	if err := s.Grant(target.ID, "movie", 10096, 0, "Arrival", 0); err != nil {
		t.Fatal(err)
	}
	// sam is in Everyone alongside jo, and must not pick this up
	got, err := s.RestrictionFor(sam, "movie")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(got, target.Label) {
		t.Fatalf("sam's restriction %q names jo's own group", got)
	}
	viewers, err := s.SharedWith("movie", 10096, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(viewers) != 1 || viewers[0].UserID != jo {
		t.Fatalf("viewers = %+v, want only the person who asked", viewers)
	}
}

// The whole feature turns on this. Splitting an install that already has
// a full Plex library starts by granting everything to a group holding
// everyone — so everybody is in that group. If a request then reached
// every group its asker is in, it would reach everybody, and the split
// would never begin.
//
// Seeding does not decide this. Granting the back catalogue to a
// HOUSEHOLD is equally reasonable, and switching that household's
// routing off as a side effect would break the thing it exists for.
func TestABackCatalogueGroupTakesNoNewRequests(t *testing.T) {
	s, jo, sam, _ := sharingStore(t)
	everyone, err := s.BackfillGroup("Existing library")
	if err != nil {
		t.Fatal(err)
	}
	house, err := s.CreateGroup("Jolene's family")
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range []int64{everyone.ID, house.ID} {
		for _, u := range []int64{jo, sam} {
			if err := s.AddMember(g, u); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := s.GrantHeld(everyone.ID, 0); err != nil {
		t.Fatal(err)
	}

	targets, err := s.RequestTargets(jo, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range targets {
		if id == everyone.ID {
			t.Fatal("a new request would land in the back catalogue, so everybody would see it")
		}
	}
	// but the household still gets it, which is the point of a household
	found := false
	for _, id := range targets {
		found = found || id == house.ID
	}
	if !found {
		t.Fatalf("targets = %v, want the household", targets)
	}
	if len(targets) != 2 {
		t.Fatalf("targets = %v, want their own group and the household", targets)
	}
}

// A request never reaches the back catalogue, however the audience was
// built. Everybody is in that group, so a title granted there is shared
// with the whole install — and a request that could do that is the split
// not happening. Naming it is not a way round the rule: it was the way
// round the rule, because the picker named every group somebody was in.
//
// Sharing into it stays possible, as the owner's act, through the
// owner's own routes — those grant directly and never come through here.
func TestARequestNeverReachesTheBackCatalogue(t *testing.T) {
	s, jo, _, _ := sharingStore(t)
	everyone, _ := s.BackfillGroup("Existing library")
	if err := s.AddMember(everyone.ID, jo); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GrantHeld(everyone.ID, 0); err != nil {
		t.Fatal(err)
	}
	house, err := s.CreateGroup("Household")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(house.ID, jo); err != nil {
		t.Fatal(err)
	}

	// named outright, the way the picker used to name it
	targets, err := s.RequestTargets(jo, []int64{everyone.ID, house.ID})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range targets {
		if id == everyone.ID {
			t.Fatalf("targets = %v, want the back catalogue refused", targets)
		}
	}
	// and the rest of the ask is honoured rather than the whole thing
	// being thrown away
	found := false
	for _, id := range targets {
		found = found || id == house.ID
	}
	if !found {
		t.Fatalf("targets = %v, want the household that was also named", targets)
	}

	// same when nothing is named at all
	targets, err = s.RequestTargets(jo, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range targets {
		if id == everyone.ID {
			t.Fatalf("default targets = %v, want the back catalogue absent", targets)
		}
	}
}

// The owner's own route is unaffected: sharing what already existed is
// exactly what that group is for, and an owner grants to it directly
// rather than through a request's targets.
func TestTheOwnerCanStillShareIntoTheBackCatalogue(t *testing.T) {
	s, jo, _, _ := sharingStore(t)
	everyone, _ := s.BackfillGroup("Existing library")
	if err := s.AddMember(everyone.ID, jo); err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(everyone.ID, "movie", 424243, 0, "Already Here", 0); err != nil {
		t.Fatal(err)
	}
	byTitle, err := s.GroupsByTitle("movie")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, gid := range byTitle.For(424243, 0) {
		found = found || gid == everyone.ID
	}
	if !found {
		t.Fatalf("the owner could not share into the back catalogue: %+v", byTitle)
	}
}

// The backfill group is assembled rather than chosen: everybody who can
// already see the library is in it, because they are exactly who would
// otherwise lose it.
func TestTheBackfillGroupHoldsEverybody(t *testing.T) {
	s, jo, sam, alex := sharingStore(t)
	g, err := s.BackfillGroup("Existing library")
	if err != nil {
		t.Fatal(err)
	}
	if !g.Backfill {
		t.Fatal("the backfill group is not marked as one")
	}
	if _, err := s.SyncBackfillMembers(g.ID); err != nil {
		t.Fatal(err)
	}
	ids, err := s.GroupMemberIDs(g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 3 {
		t.Fatalf("members = %v, want all three accounts", ids)
	}
	_ = jo
	_ = sam
	_ = alex

	// and asking for it again is the same group, not a second one
	again, err := s.BackfillGroup("Something else")
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != g.ID {
		t.Fatalf("a second backfill group was made: %d then %d", g.ID, again.ID)
	}
}

// Both gaps this closes were found on a real library, and TMDB alone
// cannot close either.
func TestATitlePlexKnowsOnlyByImdbStillResolves(t *testing.T) {
	s, jo, _, _ := sharingStore(t)
	lib, err := s.CreateLibrary("Films", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	// reely's row: it has both ids
	if _, err := s.db.Exec(`INSERT INTO movies (tmdb_id, title, imdb_id, library_id)
		VALUES (612, 'Some Film', 'tt7000002', ?)`, lib.ID); err != nil {
		t.Fatal(err)
	}
	// Plex matched the file through an agent that gave no TMDB id
	if err := s.CachePlexItems(1, []PlexItem{
		{Kind: "movie", RatingKey: 70001, ImdbID: "tt7000002"},
	}); err != nil {
		t.Fatal(err)
	}

	own, _ := s.DefaultGroup(jo)
	if err := s.Grant(own.ID, "movie", 612, 0, "Some Film", 0); err != nil {
		t.Fatal(err)
	}
	item, err := s.PlexItemFor("movie", 612, 0)
	if err != nil {
		t.Fatal(err)
	}
	if item == nil || item.RatingKey != 70001 {
		t.Fatalf("item = %+v, want the one Plex knows only by its imdb id", item)
	}
}

// Where reely and Plex matched the same file to different TMDB ids, the
// seed makes two entitlements for one film: one that resolves and one
// that waits forever. IMDb is the id they still agree on.
func TestATitleTheTwoMatchedDifferentlyStillResolves(t *testing.T) {
	s, jo, _, _ := sharingStore(t)
	lib, err := s.CreateLibrary("Films", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO movies (tmdb_id, title, imdb_id, library_id)
		VALUES (1002185, 'Another Film', 'tt7000001', ?)`, lib.ID); err != nil {
		t.Fatal(err)
	}
	// Plex settled on a different TMDB id for the same file
	if err := s.CachePlexItems(1, []PlexItem{
		{Kind: "movie", RatingKey: 70002, TmdbID: 880001, ImdbID: "tt7000001"},
	}); err != nil {
		t.Fatal(err)
	}

	own, _ := s.DefaultGroup(jo)
	if err := s.Grant(own.ID, "movie", 1002185, 0, "Another Film", 0); err != nil {
		t.Fatal(err)
	}
	item, err := s.PlexItemFor("movie", 1002185, 0)
	if err != nil {
		t.Fatal(err)
	}
	if item == nil || item.RatingKey != 70002 {
		t.Fatalf("item = %+v, want the film Plex matched to a different tmdb id", item)
	}
}

// A title genuinely absent from Plex still waits: the fallback must not
// invent a match for something with no file.
func TestATitlePlexDoesNotHaveStillWaits(t *testing.T) {
	s, jo, _, _ := sharingStore(t)
	lib, err := s.CreateLibrary("Films", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO movies (tmdb_id, title, imdb_id, library_id)
		VALUES (55555, 'Not Downloaded Yet', 'tt55555555', ?)`, lib.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.CachePlexItems(1, []PlexItem{
		{Kind: "movie", RatingKey: 70002, TmdbID: 880001, ImdbID: "tt7000001"},
	}); err != nil {
		t.Fatal(err)
	}
	own, _ := s.DefaultGroup(jo)
	if err := s.Grant(own.ID, "movie", 55555, 0, "Not Downloaded Yet", 0); err != nil {
		t.Fatal(err)
	}
	item, err := s.PlexItemFor("movie", 55555, 0)
	if err != nil {
		t.Fatal(err)
	}
	if item != nil {
		t.Fatalf("item = %+v, want nothing — Plex does not have it", item)
	}
}

// A title that exists only in Plex still has a name, and seeding must
// keep it. reely read the title on every sweep and threw it away, so
// entitling what the install already held produced rows the waiting
// list could only render as "movie 424242" — reely knew the name and
// had discarded it a line earlier.
func TestSeedingKeepsTheNamePlexHasForAPlexOnlyTitle(t *testing.T) {
	s, jo, _, _ := sharingStore(t)
	if err := s.CachePlexItems(3, []PlexItem{{
		Kind: "movie", RatingKey: 88001, SectionID: 3, TmdbID: 424242,
		Title: "A Film Only Plex Has",
	}}); err != nil {
		t.Fatal(err)
	}
	everyone, err := s.CreateGroup("Everyone")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(everyone.ID, jo); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GrantHeld(everyone.ID, 0); err != nil {
		t.Fatal(err)
	}

	// what seeding walked: the name has to survive this, because the
	// grant it makes is where the name is copied FROM
	held, err := s.HeldTitles()
	if err != nil {
		t.Fatal(err)
	}
	named := false
	for _, h := range held {
		if h.TmdbID == 424242 {
			named = h.Title == "A Film Only Plex Has"
		}
	}
	if !named {
		t.Fatalf("HeldTitles dropped the name Plex has: %+v", held)
	}

	// and the grant it made carries it, rather than an empty string the
	// waiting list would have to render as an id
	var stored string
	if err := s.db.QueryRow(`SELECT title FROM entitlements
		WHERE group_id = ? AND tmdb_id = 424242`, everyone.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != "A Film Only Plex Has" {
		t.Fatalf("entitlements.title = %q, want the name Plex has for it", stored)
	}
}

// The stored title is a copy taken when the grant was made, and an old
// grant may have had nothing to copy. Reading it back must fall through
// to whichever table does know the name rather than showing an id.
func TestAnEntitlementWithNoNameIsNamedFromWhateverKnowsIt(t *testing.T) {
	s, jo, _, _ := sharingStore(t)
	g, err := s.CreateGroup("House")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(g.ID, jo); err != nil {
		t.Fatal(err)
	}
	// granted with no name, the way seeding used to
	if err := s.Grant(g.ID, "movie", 71234, 0, "", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.CachePlexItems(4, []PlexItem{{
		Kind: "movie", RatingKey: 88002, SectionID: 4, TmdbID: 71234,
		Title: "Named By Plex",
	}}); err != nil {
		t.Fatal(err)
	}

	titles, err := s.EntitledTitles()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range titles {
		if e.TmdbID == 71234 {
			if e.Title != "Named By Plex" {
				t.Fatalf("title = %q, want it recovered from the Plex cache", e.Title)
			}
			return
		}
	}
	t.Fatal("the entitlement vanished")
}

// Seeding grants from what the media server holds, so a server holding a
// wrong match produces a grant keyed on the wrong id. Correcting the
// match over there leaves the grant pointing at nothing, and no pass
// will ever resolve it.
func TestAGrantForATitleNothingHoldsIsDropped(t *testing.T) {
	s, jo, _, _ := sharingStore(t)
	g, err := s.CreateGroup("House")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(g.ID, jo); err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(g.ID, "movie", 909090, 0, "", 0); err != nil {
		t.Fatal(err)
	}
	// a sweep that reached the server and found other things
	if err := s.CachePlexItems(1, []PlexItem{{
		Kind: "movie", RatingKey: 5501, SectionID: 1, TmdbID: 111, Title: "Something Else",
	}}); err != nil {
		t.Fatal(err)
	}

	// first pass only starts the clock: one observation is never enough
	if stale, err := s.SweepStale(24 * time.Hour); err != nil {
		t.Fatal(err)
	} else if len(stale) != 0 {
		t.Fatalf("pruned %+v on the first sighting, want the clock started only", stale)
	}
	// a day of healthy passes later, still held by nothing
	stale, err := s.SweepStale(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 1 || stale[0].TmdbID != 909090 {
		t.Fatalf("pruned %+v, want the one grant nothing holds", stale)
	}
	left, err := s.EntitledTitles()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range left {
		if e.TmdbID == 909090 {
			t.Fatal("the stale grant survived the prune")
		}
	}
}

// The dangerous confusion: a title somebody asked for and has not
// downloaded shows in the waiting list exactly like a stale grant does.
// It is not one. A request creates the row reely searches on, and that
// row is the difference — deleting these would revoke titles people are
// waiting for.
func TestATitleSomebodyIsWaitingForIsNotDropped(t *testing.T) {
	s, jo, _, _ := sharingStore(t)
	lib, err := s.CreateLibrary("Films", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	// reely has the title and no file for it — the ordinary state of a
	// request that has not landed
	if _, err := s.db.Exec(`INSERT INTO movies (library_id, tmdb_id, title, year)
		VALUES (?, 777777, 'Asked For, Not Here Yet', 2026)`, lib.ID); err != nil {
		t.Fatal(err)
	}
	g, err := s.CreateGroup("House")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(g.ID, jo); err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(g.ID, "movie", 777777, 0, "Asked For, Not Here Yet", 0); err != nil {
		t.Fatal(err)
	}
	if err := s.CachePlexItems(1, []PlexItem{{
		Kind: "movie", RatingKey: 5502, SectionID: 1, TmdbID: 111, Title: "Something Else",
	}}); err != nil {
		t.Fatal(err)
	}

	stale, err := s.SweepStale(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Fatalf("pruned %+v, want a title somebody is waiting for left alone", stale)
	}
}

// On a cache that failed to fill, every entitlement looks stale. Acting
// on that would delete an install's entire sharing configuration because
// one scan did not answer.
func TestNothingIsPrunedWhenThePlexCacheIsEmpty(t *testing.T) {
	s, jo, _, _ := sharingStore(t)
	g, err := s.CreateGroup("House")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(g.ID, jo); err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(g.ID, "movie", 909090, 0, "", 0); err != nil {
		t.Fatal(err)
	}

	stale, err := s.SweepStale(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Fatalf("pruned %+v on an empty cache, want nothing touched", stale)
	}
}

// The failure this whole clock exists to prevent. A section that fails
// to scan empties its half of the cache, and every grant for it becomes
// indistinguishable from an orphan. If one observation were enough, a
// scan that did not answer would revoke an install's sharing.
func TestASectionThatFailedToScanDoesNotOrphanItsGrants(t *testing.T) {
	s, jo, _, _ := sharingStore(t)
	g, err := s.CreateGroup("House")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(g.ID, jo); err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(g.ID, "movie", 313131, 0, "Held By Plex", 0); err != nil {
		t.Fatal(err)
	}

	// healthy pass: Plex has it
	good := []PlexItem{
		{Kind: "movie", RatingKey: 6001, SectionID: 1, TmdbID: 313131, Title: "Held By Plex"},
		{Kind: "show", RatingKey: 6002, SectionID: 2, TvdbID: 4242, Title: "A Series"},
	}
	if err := s.CachePlexItems(1, good[:1]); err != nil {
		t.Fatal(err)
	}
	if err := s.CachePlexItems(2, good[1:]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SweepStale(0); err != nil {
		t.Fatal(err)
	}

	// the movie section comes back empty — a scan that did not answer.
	// The TV section still has rows, so the cache is not empty overall.
	if err := s.CachePlexItems(1, nil); err != nil {
		t.Fatal(err)
	}
	stale, err := s.SweepStale(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(stale) != 0 {
		t.Fatalf("pruned %+v on a section that failed to scan", stale)
	}

	// and when the section comes back, the clock is cleared rather than
	// left running toward a delete
	if err := s.CachePlexItems(1, good[:1]); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SweepStale(0); err != nil {
		t.Fatal(err)
	}
	var clock sql.NullString
	if err := s.db.QueryRow(`SELECT missing_since FROM entitlements
		WHERE tmdb_id = 313131`).Scan(&clock); err != nil {
		t.Fatal(err)
	}
	if clock.Valid {
		t.Fatalf("missing_since = %q, want it cleared once Plex had it again", clock.String)
	}
}

// The owner asking for a film from the portal, away from home, is still
// the owner. The portal is where they are, not who they are — so the one
// group only they may share into is still theirs to name.
func TestTheOwnerCanNameTheBackCatalogueOnARequest(t *testing.T) {
	s, _, _, _ := sharingStore(t)
	res, err := s.db.Exec(`INSERT INTO users (username, password_hash, role)
		VALUES ('owner', 'x', 'admin')`)
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := res.LastInsertId()
	if err := s.EnsurePersonalGroup(owner, "owner"); err != nil {
		t.Fatal(err)
	}
	everyone, _ := s.BackfillGroup("Existing library")
	if err := s.AddMember(everyone.ID, owner); err != nil {
		t.Fatal(err)
	}

	// named outright: theirs to make
	targets, err := s.RequestTargets(owner, []int64{everyone.ID})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range targets {
		found = found || id == everyone.ID
	}
	if !found {
		t.Fatalf("targets = %v, want the owner's explicit choice honoured", targets)
	}

	// but never just because they belong to it
	targets, err = s.RequestTargets(owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range targets {
		if id == everyone.ID {
			t.Fatalf("default targets = %v, want it absent unless named", targets)
		}
	}
}

// Belonging to the back catalogue and being allowed to share into it are
// different things. An owner is often not in it — it is assembled from
// who could already see the library, and the owner's access was never
// granted that way — and deriving the capability from membership meant
// the one group only they may use was refused to them, for a reason
// nothing on screen could explain.
func TestTheOwnerNeedNotBelongToTheBackCatalogueToShareIntoIt(t *testing.T) {
	s, _, _, _ := sharingStore(t)
	res, err := s.db.Exec(`INSERT INTO users (username, password_hash, role)
		VALUES ('owner', 'x', 'admin')`)
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := res.LastInsertId()
	if err := s.EnsurePersonalGroup(owner, "owner"); err != nil {
		t.Fatal(err)
	}
	// created, and the owner deliberately NOT put in it
	everyone, err := s.BackfillGroup("Existing library")
	if err != nil {
		t.Fatal(err)
	}

	targets, err := s.RequestTargets(owner, []int64{everyone.ID})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, id := range targets {
		found = found || id == everyone.ID
	}
	if !found {
		t.Fatalf("targets = %v, want the owner's choice honoured without membership", targets)
	}

	// still never by default, and still nobody else's to name
	targets, err = s.RequestTargets(owner, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range targets {
		if id == everyone.ID {
			t.Fatalf("default targets = %v, want it absent unless named", targets)
		}
	}
}

// The owner's listing says who each title is shared with, and it reads
// the same entitlements the portal does. A grant naming one id must be
// found for a title carrying both, or a shared show reads as shared with
// nobody — and the fix for that is an owner re-sharing something that was
// already shared.
func TestGroupsByTitleFindsAGrantByEitherId(t *testing.T) {
	s, jo, _, _ := sharingStore(t)
	house, err := s.CreateGroup("Household")
	if err != nil {
		t.Fatal(err)
	}
	night, err := s.CreateGroup("Night shift")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddMember(house.ID, jo); err != nil {
		t.Fatal(err)
	}
	// as a grant seeded from a Plex item matched by a TMDB agent
	if err := s.Grant(house.ID, "show", 213713, 0, "Monster", 0); err != nil {
		t.Fatal(err)
	}
	// and the same show granted again elsewhere by its TVDB id, the way
	// an owner sharing from the library does
	if err := s.Grant(night.ID, "show", 0, 451136, "Monster", 0); err != nil {
		t.Fatal(err)
	}

	byTitle, err := s.GroupsByTitle("show")
	if err != nil {
		t.Fatal(err)
	}
	got := byTitle.For(213713, 451136)
	if len(got) != 2 {
		t.Fatalf("groups = %v, want both grants found for one show", got)
	}

	// a group named by both ids is one group, not two
	if err := s.Grant(house.ID, "show", 999001, 999002, "Both", 0); err != nil {
		t.Fatal(err)
	}
	byTitle, err = s.GroupsByTitle("show")
	if err != nil {
		t.Fatal(err)
	}
	if got := byTitle.For(999001, 999002); len(got) != 1 {
		t.Fatalf("groups = %v, want the one group once", got)
	}

	// and a title nothing names is still shared with nobody
	if got := byTitle.For(4242, 4243); len(got) != 0 {
		t.Fatalf("groups = %v, want none", got)
	}

	// VisibleTitles reads the same grants and must agree
	v, err := s.VisibleTitles(jo, "show")
	if err != nil {
		t.Fatal(err)
	}
	if !v.Has(213713, 451136) {
		t.Error("a show granted by its TMDB id is invisible to a member")
	}
	// the group jo does not belong to entitles nothing
	if v.Has(0, 451136) {
		t.Error("a grant in somebody else's group made a show visible")
	}
}

// An "Everyone" group is not a request default.
//
// A fresh install has no back catalogue, so an owner who wants "the
// people I share Plex with" makes a group and calls it Everyone. Made
// the ordinary way that group IS a request default, and every member is
// in it — so the first person to ask for something shared it with the
// whole house. The flag is what stops that, and it has to hold on the
// server: a client that ticks every group its user belongs to would
// otherwise name it every time.
func TestAnEveryoneGroupIsNeverARequestDefault(t *testing.T) {
	s, jo, sam, _ := sharingStore(t)
	house, err := s.CreateGroup("Everyone")
	if err != nil {
		t.Fatal(err)
	}
	family, err := s.CreateGroup("Family")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{jo, sam} {
		if err := s.AddMember(house.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.AddMember(family.ID, jo); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEveryoneGroup(house.ID, true); err != nil {
		t.Fatal(err)
	}

	has := func(ids []int64, want int64) bool {
		for _, id := range ids {
			if id == want {
				return true
			}
		}
		return false
	}

	// jo asks for something with no audience named
	got, err := s.RequestTargets(jo, nil)
	if err != nil {
		t.Fatal(err)
	}
	if has(got, house.ID) {
		t.Error("a request reached the whole house without anybody choosing it")
	}
	if !has(got, family.ID) {
		t.Error("an ordinary household stopped being a request default")
	}

	// and naming it outright does not help a requester either
	got, err = s.RequestTargets(jo, []int64{house.ID, family.ID})
	if err != nil {
		t.Fatal(err)
	}
	if has(got, house.ID) {
		t.Error("a requester named the everyone group and got it")
	}
}

// One per install, and never a personal group. "Everyone" is singular:
// two of them would be two answers to who the house is.
func TestOnlyOneGroupIsEveryone(t *testing.T) {
	s, jo, _, _ := sharingStore(t)
	first, err := s.CreateGroup("Everyone")
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.CreateGroup("The House")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetEveryoneGroup(first.ID, true); err != nil {
		t.Fatal(err)
	}
	if err := s.SetEveryoneGroup(second.ID, true); err != nil {
		t.Fatal(err)
	}
	id, err := s.EveryoneGroupID()
	if err != nil {
		t.Fatal(err)
	}
	if id != second.ID {
		t.Errorf("everyone group = %d, want the one just named (%d)", id, second.ID)
	}
	again, err := s.Group(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if again.Everyone {
		t.Error("two groups are the everyone group")
	}

	// a personal group is somebody's own corner, which is the opposite
	mine, err := s.GroupRefsOf(jo)
	if err != nil {
		t.Fatal(err)
	}
	for _, g := range mine {
		if !g.Personal {
			continue
		}
		if err := s.SetEveryoneGroup(g.ID, true); err == nil {
			t.Error("a personal group was made the everyone group")
		}
	}
}

// A new account joins the household as it is created — the point of
// difference with the backfill group, whose membership is frozen on
// purpose because a latecomer never had the old library.
func TestANewAccountJoinsTheEveryoneGroup(t *testing.T) {
	s, _, _, _ := sharingStore(t)
	house, err := s.CreateGroup("Everyone")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetEveryoneGroup(house.ID, true); err != nil {
		t.Fatal(err)
	}
	before, err := s.Group(house.ID)
	if err != nil {
		t.Fatal(err)
	}

	res, err := s.db.Exec(`INSERT INTO users (username, password_hash, role)
		VALUES ('newcomer', 'x', 'user')`)
	if err != nil {
		t.Fatal(err)
	}
	id, _ := res.LastInsertId()
	if err := s.JoinEveryone(id); err != nil {
		t.Fatal(err)
	}

	after, err := s.Group(house.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Members != before.Members+1 {
		t.Errorf("members = %d, want one more than %d", after.Members, before.Members)
	}
}
