package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/getreely/reely/internal/catalog"
)

// Asking for season 1 of a show, and then, once it is here, for more of
// it. Holding a show is not holding every season of it.

func gotSeasons(t *testing.T, list []int, want ...int) bool {
	t.Helper()
	if len(list) != len(want) {
		return false
	}
	for i := range want {
		if list[i] != want[i] {
			return false
		}
	}
	return true
}

func TestAskingForMoreSeasonsOfAShowThatIsHere(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)
	shows, err := world.srv.Catalog.CreateLibrary("Series", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	if err := world.srv.Auth.SetUserLibraries(world.jen, []int64{world.family, shows.ID}); err != nil {
		t.Fatal(err)
	}
	jen := login(t, world.h, "jen", "another long one")
	got := func() map[string]any {
		return map[string]any{"kind": "show", "tmdbId": 1399, "title": "Game of Thrones", "libraryId": shows.ID}
	}

	// Season 1, asked for and approved: the show is here with season 1 on.
	first := got()
	first["seasons"] = []int{1}
	w := as(t, world.h, jen, "POST", "/api/v1/requests", first)
	if w.Code != http.StatusCreated {
		t.Fatalf("asking for season 1 = %d: %s", w.Code, w.Body)
	}
	var made struct {
		ID int64 `json:"id"`
	}
	json.Unmarshal(w.Body.Bytes(), &made) //nolint:errcheck // asserted below
	if w := as(t, world.h, world.admin, "POST", "/api/v1/requests/"+strconv.FormatInt(made.ID, 10)+"/approve", nil); w.Code != http.StatusOK {
		t.Fatalf("approve = %d: %s", w.Code, w.Body)
	}
	showID, err := world.srv.Catalog.HeldShowID(shows.ID, 1399, 0)
	if err != nil || showID == 0 {
		t.Fatalf("the show wasn't added: %d (%v)", showID, err)
	}
	if asked, _ := world.srv.Catalog.SeasonsAsked(showID); !gotSeasons(t, asked, 1) {
		t.Fatalf("seasons asked = %v, want [1]", asked)
	}

	// The preview says which seasons are asked for, so the page offers the rest.
	w = as(t, world.h, jen, "GET", "/api/v1/preview/show/1399", nil)
	var preview struct {
		InLibraries  []int64 `json:"inLibraries"`
		SeasonsAsked []struct {
			LibraryID int64 `json:"libraryId"`
			Seasons   []int `json:"seasons"`
		} `json:"seasonsAsked"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &preview); err != nil || w.Code != http.StatusOK {
		t.Fatalf("preview = %d: %s", w.Code, w.Body)
	}
	if len(preview.SeasonsAsked) != 1 || preview.SeasonsAsked[0].LibraryID != shows.ID || !gotSeasons(t, preview.SeasonsAsked[0].Seasons, 1) {
		t.Fatalf("seasons asked in the preview = %+v, want [1] in %d", preview.SeasonsAsked, shows.ID)
	}

	// Season 2 now: approved on the spot, season 1 left on, one open request covering both.
	more := got()
	more["seasons"] = []int{2}
	w = as(t, world.h, jen, "POST", "/api/v1/requests", more)
	if w.Code != http.StatusCreated {
		t.Fatalf("asking for season 2 of a show that's here = %d: %s", w.Code, w.Body)
	}
	var answer struct {
		ID     int64  `json:"id"`
		Status string `json:"status"`
	}
	json.Unmarshal(w.Body.Bytes(), &answer) //nolint:errcheck // asserted below
	if answer.Status != "approved" || answer.ID != made.ID {
		t.Errorf("answer = %+v, want the first request (%d), approved", answer, made.ID)
	}
	if asked, _ := world.srv.Catalog.SeasonsAsked(showID); !gotSeasons(t, asked, 1, 2) {
		t.Errorf("seasons asked = %v, want [1 2]: season 1 must stay on", asked)
	}
	open, err := world.srv.Catalog.OpenRequestFor(shows.ID, "show", 1399)
	if err != nil || open == nil || !gotSeasons(t, open.Seasons, 1, 2) {
		t.Errorf("the open request = %+v (%v), want seasons [1 2]", open, err)
	}

	// Asking for what's already asked for adds nothing: already requested.
	again := got()
	again["seasons"] = []int{1, 2}
	if w := as(t, world.h, jen, "POST", "/api/v1/requests", again); w.Code != http.StatusConflict {
		t.Errorf("asking again for seasons already asked = %d, want 409: %s", w.Code, w.Body)
	}

	// The rest of it, as a whole-show ask: every season on.
	if w := as(t, world.h, jen, "POST", "/api/v1/requests", got()); w.Code != http.StatusCreated {
		t.Fatalf("asking for the whole show = %d: %s", w.Code, w.Body)
	}
	if asked, _ := world.srv.Catalog.SeasonsAsked(showID); !gotSeasons(t, asked, 1, 2, 3) {
		t.Errorf("seasons asked = %v, want [1 2 3]", asked)
	}
	if open, _ := world.srv.Catalog.OpenRequestFor(shows.ID, "show", 1399); open == nil || open.Seasons != nil {
		t.Errorf("the open request = %+v, want the whole show", open)
	}
}

// A request still waiting on the owner is still the answer to asking again.
func TestMoreSeasonsWhileTheFirstAskWaits(t *testing.T) {
	world := requesterWorld(t)
	withTMDB(t, world)
	shows, err := world.srv.Catalog.CreateLibrary("Series", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	if err := world.srv.Auth.SetUserLibraries(world.jen, []int64{world.family, shows.ID}); err != nil {
		t.Fatal(err)
	}
	jen := login(t, world.h, "jen", "another long one")
	ask := map[string]any{"kind": "show", "tmdbId": 1399, "title": "Game of Thrones", "libraryId": shows.ID, "seasons": []int{1}}
	if w := as(t, world.h, jen, "POST", "/api/v1/requests", ask); w.Code != http.StatusCreated {
		t.Fatalf("first ask = %d: %s", w.Code, w.Body)
	}
	ask["seasons"] = []int{2}
	if w := as(t, world.h, jen, "POST", "/api/v1/requests", ask); w.Code != http.StatusConflict {
		t.Errorf("a second ask while the first waits = %d, want 409: %s", w.Code, w.Body)
	}
}

func TestSeasonLists(t *testing.T) {
	if got := catalog.UnionSeasons([]int{3, 1}, []int{2, 1}); !gotSeasons(t, got, 1, 2, 3) {
		t.Errorf("union = %v", got)
	}
	if catalog.UnionSeasons(nil, []int{2}) != nil || catalog.UnionSeasons([]int{2}, nil) != nil {
		t.Error("the whole show joined with anything is the whole show")
	}
	if got := catalog.SeasonsNotIn([]int{1, 2, 3}, []int{2}); !gotSeasons(t, got, 1, 3) {
		t.Errorf("not in = %v", got)
	}
}
