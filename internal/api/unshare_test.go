package api

import (
	"context"
	"net/http"
	"os"
	"testing"
)

// Taking somebody's Plex share away has to land everywhere it shows.
// It used to land in one place: the account went inactive, and that was
// all. Their personal group carried on entitling every title they had
// ever asked for, so the owner opened one and still saw them under
// "shared with" — and Plex still carried their label on the file.
func TestUnsharingInPlexTakesTheirTagsOffTitles(t *testing.T) {
	shares, err := os.ReadFile("../plex/testdata/shared_servers.xml")
	if err != nil {
		t.Fatal(err)
	}
	world := requesterWorld(t)
	fake := fakePlexTVSharing(t, "500001", &shares)
	for _, kv := range [][2]string{
		{"plex_client_id", "reely-test-install"},
		{"plex_owner_token", "owner-tok"},
		{"plex_machine_id", "srv-1111"},
		{"plex_server_url", fake.URL},
	} {
		if err := world.srv.Settings.Set(kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	world.srv.plexBase = fake.URL
	if _, err := world.srv.Catalog.CreateLibrary("Films", "/srv/library/films", "movies"); err != nil {
		t.Fatal(err)
	}
	if _, err := world.srv.Catalog.CreateLibrary("Series", "/srv/library/series", "shows"); err != nil {
		t.Fatal(err)
	}

	if w := plexCheck(t, world); w.Code != http.StatusOK {
		t.Fatalf("sign-in = %d: %s", w.Code, w.Body)
	}
	ana := world.srv.Auth.UserByPlexID(500001)
	if ana == nil {
		t.Fatal("no account was created")
	}
	own, err := world.srv.Catalog.DefaultGroup(ana.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := world.srv.Catalog.Grant(own.ID, "movie", 550, 0, "Fight Club", ana.ID); err != nil {
		t.Fatal(err)
	}
	viewers, err := world.srv.Catalog.SharedWith("movie", 550, 0)
	if err != nil || len(viewers) != 1 {
		t.Fatalf("shared with %+v (%v), want just them", viewers, err)
	}

	// the owner unshares the server with everybody in Plex
	shares = []byte(`<MediaContainer size="0"></MediaContainer>`)

	out, err := world.srv.syncPlexUsers(context.Background())
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if out.Deactivated != 1 {
		t.Errorf("deactivated %d accounts, want 1", out.Deactivated)
	}
	if out.Untagged != 1 {
		t.Errorf("removed %d tags, want 1", out.Untagged)
	}
	viewers, err = world.srv.Catalog.SharedWith("movie", 550, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(viewers) != 0 {
		t.Errorf("the title is still shared with %+v", viewers)
	}
	labels, err := world.srv.Catalog.LabelsFor("movie", 550, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 0 {
		t.Errorf("Plex would still be told to label this %v", labels)
	}
}
