package api

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/getreely/reely/internal/catalog"
)

// A watched list is its creator's. Two people sharing a library is the
// ordinary shape of a household, not a reason either should be handed
// the other's lists — and "handed" is the word, because every list route
// scoped on library access alone: seeing them, disabling them, deleting
// them, and forcing a sync.
//
// The sync is the one that bites hardest. A list syncs as its CREATOR:
// with their mdblist key, filing requests in their name, against their
// weekly quota. So one housemate could spend another's ration of asks by
// pressing a button on a list that was never theirs.
func TestOneUsersListsAreNotAnothersToSeeOrTouch(t *testing.T) {
	world := requesterWorld(t)
	srv, h := world.srv, world.h

	// a second requester in the same library — a household, not a leak
	w := as(t, h, world.admin, "POST", "/api/v1/users", map[string]any{
		"username": "sam", "password": "a long enough one", "role": "user",
		"libraryIds": []int64{world.family},
	})
	if w.Code != http.StatusCreated {
		t.Fatalf("create second user: %d %s", w.Code, w.Body)
	}
	var made struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &made) //nolint:errcheck // asserted below
	if made.ID == 0 {
		t.Fatalf("no id in %s", w.Body)
	}
	if err := srv.Auth.SetRequestSettings(made.ID, false, false, false, nil, nil); err != nil {
		t.Fatal(err)
	}

	jensList, err := srv.Catalog.CreateList(&catalog.List{
		Name: "Jen's watchlist", Source: "tmdb_chart", Config: `{"chart":"trending"}`,
		LibraryID: world.family, Enabled: true, CreatedBy: world.jen,
	})
	if err != nil {
		t.Fatal(err)
	}

	sam := login(t, h, "sam", "a long enough one")
	lists := func(c *http.Cookie) []catalog.List {
		t.Helper()
		w := as(t, h, c, "GET", "/api/v1/lists", nil)
		if w.Code != http.StatusOK {
			t.Fatalf("lists = %d: %s", w.Code, w.Body)
		}
		var out struct {
			Lists []catalog.List `json:"lists"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Lists
	}

	if got := lists(sam); len(got) != 0 {
		t.Errorf("sam sees %d of jen's lists: %+v", len(got), got)
	}
	// and cannot reach it by id either — absent, not merely unlisted
	for _, call := range []struct {
		method, path string
		body         any
	}{
		{"PUT", "/api/v1/lists/" + itoa64(jensList) + "/enabled", map[string]any{"enabled": false}},
		{"POST", "/api/v1/lists/" + itoa64(jensList) + "/sync", nil},
		{"DELETE", "/api/v1/lists/" + itoa64(jensList), nil},
	} {
		if w := as(t, h, sam, call.method, call.path, call.body); w.Code != http.StatusNotFound {
			t.Errorf("%s %s = %d, want 404 — sam acted on jen's list",
				call.method, call.path, w.Code)
		}
	}

	// jen still has her own
	jen := login(t, h, "jen", "another long one")
	if got := lists(jen); len(got) != 1 {
		t.Errorf("jen sees %d of her own lists, want 1", len(got))
	}
	// and the owner sees every list on the install, whoever made it —
	// which is what makes setting a list's audience their job
	if got := lists(world.admin); len(got) != 1 {
		t.Errorf("the owner sees %d lists, want jen's", len(got))
	}
}
