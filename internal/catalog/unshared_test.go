package catalog

import (
	"testing"
)

// Losing a Plex share is supposed to be the end of it, and it was not.
// The account went inactive and its sessions went, but its personal
// group carried on entitling everything they had ever asked for — so
// the owner opened a title and still saw their name under "shared
// with", and Plex still carried their label on the file.
func TestUnsharingSomebodyTakesTheirOwnTagsOffTitles(t *testing.T) {
	s, jo, sam, _ := sharingStore(t)
	joGroup, err := s.DefaultGroup(jo)
	if err != nil {
		t.Fatal(err)
	}
	house, err := s.CreateGroup("House")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{jo, sam} {
		if err := s.AddMember(house.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, g := range []int64{joGroup.ID, house.ID} {
		if err := s.Grant(g, "movie", 550, 0, "Fight Club", jo); err != nil {
			t.Fatal(err)
		}
	}

	n, err := s.ClearPersonalEntitlements([]int64{jo})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("cleared %d entitlements, want 1", n)
	}

	labels, err := s.LabelsFor("movie", 550, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range labels {
		if l == joGroup.Label {
			t.Errorf("the departed account's own label is still on the title: %v", labels)
		}
	}
	// the household's tag is not theirs to take. Removing somebody from
	// Plex says nothing about who else the title is shared with, and
	// Sam is still here.
	if len(labels) != 1 || labels[0] != house.Label {
		t.Errorf("labels = %v, want just the household's %q", labels, house.Label)
	}
}

// "Shared with" answers who can see a title. Somebody the owner stopped
// sharing Plex with cannot — their sessions are gone and they cannot
// sign in — so listing them there is simply wrong, and it is the list
// the owner reads to check that unsharing worked.
func TestSharedWithLeavesOutClosedAccounts(t *testing.T) {
	s, jo, sam, _ := sharingStore(t)
	house, err := s.CreateGroup("House")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{jo, sam} {
		if err := s.AddMember(house.ID, id); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Grant(house.ID, "movie", 550, 0, "Fight Club", jo); err != nil {
		t.Fatal(err)
	}

	before, err := s.SharedWith("movie", 550, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 2 {
		t.Fatalf("shared with %d people, want 2", len(before))
	}

	if _, err := s.db.Exec(`UPDATE users SET active = 0 WHERE id = ?`, jo); err != nil {
		t.Fatal(err)
	}
	after, err := s.SharedWith("movie", 550, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != 1 || after[0].UserID != sam {
		t.Fatalf("shared with %+v, want only the account that is still open", after)
	}
}

// An empty list is not "clear everything" — it is a sync where nobody
// left, which is most of them.
func TestClearingNobodysTagsClearsNothing(t *testing.T) {
	s, jo, _, _ := sharingStore(t)
	own, err := s.DefaultGroup(jo)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Grant(own.ID, "movie", 550, 0, "Fight Club", jo); err != nil {
		t.Fatal(err)
	}
	if n, err := s.ClearPersonalEntitlements(nil); err != nil || n != 0 {
		t.Fatalf("cleared %d (%v), want 0", n, err)
	}
	labels, err := s.LabelsFor("movie", 550, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 1 {
		t.Errorf("labels = %v, want the one that was there", labels)
	}
}
