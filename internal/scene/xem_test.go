package scene

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

// The Kitchen Nightmares (US) shape, which is why this package exists.
// TVDB folds the 2007 and 2008 runs into one 22-episode Season 1, so the
// scene runs a season ahead from the 2010 season onward: the run TVDB
// calls S9 releases as S10. testdata/xem_80552.json is TheXEM's real
// answer for tvdb id 80552, captured verbatim.
func fakeXEM(t *testing.T) *XEM {
	t.Helper()
	body, err := os.ReadFile("testdata/xem_80552.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/havemap":
			fmt.Fprint(w, `{"result":"success","data":["80552","78804"],"message":""}`)
		case r.URL.Path == "/all" && r.URL.Query().Get("id") == "80552":
			w.Write(body) //nolint:errcheck // test server
		case r.URL.Path == "/all":
			// XEM's ordinary answer for a show nobody has mapped
			fmt.Fprint(w, `{"result":"failure","message":"no show with the tvdb_id 12345 found","data":""}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	x := NewXEM()
	x.SetBaseURL(srv.URL)
	return x
}

func TestXEMMappings(t *testing.T) {
	maps, err := fakeXEM(t).Mappings(context.Background(), 80552)
	if err != nil {
		t.Fatal(err)
	}
	if len(maps) != 123 {
		t.Fatalf("got %d mappings, want 123", len(maps))
	}
	// the two ends of the real map, and the release from the report
	for _, want := range []Mapping{
		{Season: 1, Episode: 1, SceneSeason: 1, SceneEpisode: 1},
		{Season: 1, Episode: 11, SceneSeason: 2, SceneEpisode: 1},
		{Season: 9, Episode: 8, SceneSeason: 10, SceneEpisode: 8},
		{Season: 9, Episode: 10, SceneSeason: 10, SceneEpisode: 10},
	} {
		found := false
		for _, m := range maps {
			if m == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("mapping %+v missing", want)
		}
	}
}

// A show XEM has never heard of is the normal case, not an error.
func TestXEMUnmappedShowIsNotAnError(t *testing.T) {
	maps, err := fakeXEM(t).Mappings(context.Background(), 12345)
	if err != nil {
		t.Fatalf("unmapped show errored: %v", err)
	}
	if len(maps) != 0 {
		t.Errorf("got %d mappings for an unmapped show", len(maps))
	}
}

func TestXEMMappedList(t *testing.T) {
	x := fakeXEM(t)
	have, err := x.Mapped(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !have[80552] || have[11111] {
		t.Errorf("mapped list = %v", have)
	}
	if have2, err := x.Mapped(context.Background()); err != nil || !have2[80552] {
		t.Errorf("cached mapped list = %v, %v", have2, err)
	}
}

// kitchenNightmaresEpisodes is the show's episode tree as TheTVDB serves
// it in aired order — the numbering reely stores. Note S9: twelve
// episodes, of which XEM has mapped ten. The last two aired after XEM's
// snapshot, and they are exactly the ones still being searched for.
func kitchenNightmaresEpisodes() []Episode {
	counts := map[int]int{1: 22, 2: 13, 3: 14, 4: 17, 5: 16, 6: 10, 7: 10, 8: 11, 9: 12}
	eps := []Episode{{0, 1}} // a special, which is never scene-numbered
	for season := 1; season <= 9; season++ {
		for e := 1; e <= counts[season]; e++ {
			eps = append(eps, Episode{season, e})
		}
	}
	return eps
}

func TestResolveKitchenNightmares(t *testing.T) {
	maps, err := fakeXEM(t).Mappings(context.Background(), 80552)
	if err != nil {
		t.Fatal(err)
	}
	got := map[Episode]Numbering{}
	for _, n := range Resolve(kitchenNightmaresEpisodes(), maps) {
		got[Episode{n.Season, n.Episode}] = n
	}
	cases := []struct {
		ep              Episode
		season, episode int
		mapped          bool
	}{
		{Episode{0, 1}, 0, 0, false},   // specials stay out
		{Episode{1, 1}, 0, 0, false},   // agrees with the catalog: nothing stored
		{Episode{1, 10}, 0, 0, false},  // still the scene's S1
		{Episode{1, 11}, 2, 1, true},   // where TVDB's merged S1 splits
		{Episode{1, 22}, 2, 12, true},  // the end of that split
		{Episode{2, 1}, 3, 1, true},    // the whole-season shift proper
		{Episode{7, 1}, 8, 1, true},    // the 2023 revival: releases as S08
		{Episode{9, 8}, 10, 8, true},   // the release from the screenshot
		{Episode{9, 10}, 10, 10, true}, // XEM's last mapped episode
		{Episode{9, 11}, 10, 11, true}, // past it — extrapolated
		{Episode{9, 12}, 10, 12, true}, // and the season finale
	}
	for _, c := range cases {
		n, ok := got[c.ep]
		if ok != c.mapped {
			t.Errorf("S%02dE%02d mapped = %v, want %v (%+v)",
				c.ep.Season, c.ep.Episode, ok, c.mapped, n)
			continue
		}
		if ok && (n.SceneSeason != c.season || n.SceneEpisode != c.episode) {
			t.Errorf("S%02dE%02d → S%02dE%02d, want S%02dE%02d", c.ep.Season, c.ep.Episode,
				n.SceneSeason, n.SceneEpisode, c.season, c.episode)
		}
	}
}

// Next year's season arrives on TVDB long before anybody maps it. With
// every mapped season shifted by one, S10 must be searched for as S11 —
// otherwise a brand-new season is unfindable until XEM catches up.
func TestResolveExtrapolatesAWhollyUnmappedSeason(t *testing.T) {
	maps, err := fakeXEM(t).Mappings(context.Background(), 80552)
	if err != nil {
		t.Fatal(err)
	}
	eps := append(kitchenNightmaresEpisodes(), Episode{10, 1}, Episode{10, 2})
	got := map[Episode]Numbering{}
	for _, n := range Resolve(eps, maps) {
		got[Episode{n.Season, n.Episode}] = n
	}
	for _, c := range []struct {
		ep              Episode
		season, episode int
	}{
		{Episode{10, 1}, 11, 1},
		{Episode{10, 2}, 11, 2},
	} {
		n, ok := got[c.ep]
		if !ok || n.SceneSeason != c.season || n.SceneEpisode != c.episode {
			t.Errorf("S%02dE%02d → %+v, want S%02dE%02d",
				c.ep.Season, c.ep.Episode, n, c.season, c.episode)
		}
	}
}

// A show whose numbering the scene agrees with must come back with
// nothing at all — no rows, and nothing for the search paths to consult.
func TestResolveIgnoresAnAgreeingShow(t *testing.T) {
	eps := []Episode{{1, 1}, {1, 2}, {2, 1}}
	maps := []Mapping{
		{Season: 1, Episode: 1, SceneSeason: 1, SceneEpisode: 1},
		{Season: 1, Episode: 2, SceneSeason: 1, SceneEpisode: 2},
		{Season: 2, Episode: 1, SceneSeason: 2, SceneEpisode: 1},
	}
	if got := Resolve(eps, maps); len(got) != 0 {
		t.Errorf("agreeing show resolved to %+v", got)
	}
}

// XEM being down must cost one request, not one per show: after a failure
// every caller is turned away without asking again, so a refresh pass over
// a whole library neither stalls nor floods the log.
func TestXEMOutageIsAskedAboutOnce(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits++
		http.Error(w, "bad gateway", http.StatusBadGateway)
	}))
	t.Cleanup(srv.Close)
	x := NewXEM()
	x.SetBaseURL(srv.URL)

	if _, err := x.Mapped(context.Background()); err == nil {
		t.Fatal("first call should report the outage")
	}
	// every later caller is turned away with ErrUnavailable, never with an
	// empty answer — an empty answer reads as "this show has no mapping"
	// and would be written over numbering that is still correct
	for i := range 5 {
		maps, err := x.Mappings(context.Background(), 80552)
		if !errors.Is(err, ErrUnavailable) {
			t.Fatalf("call %d during the outage window = %v, %v", i+2, maps, err)
		}
	}
	if hits != 1 {
		t.Errorf("asked XEM %d times during one outage window, want 1", hits)
	}
}

// TheXEM sits behind Cloudflare, which refuses Go's default client string
// with a 403 while the same request from a browser succeeds — the reason
// scene numbering silently did nothing on a real install. Every request
// must name reely.
func TestXEMSendsAnIdentifyingUserAgent(t *testing.T) {
	var got []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.Header.Get("User-Agent"))
		fmt.Fprint(w, `{"result":"success","data":[],"message":""}`)
	}))
	t.Cleanup(srv.Close)

	x := NewXEM()
	x.SetBaseURL(srv.URL)
	if _, err := x.Mapped(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := x.Mappings(context.Background(), 80552); err != nil {
		t.Fatal(err)
	}
	x.SetUserAgent("reely/v1.2.3 (+https://github.com/getreely/reely)")
	x.failedAt = time.Time{} // the empty answers above are fine; no outage
	if _, err := x.Mappings(context.Background(), 80553); err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("saw %d requests, want 3", len(got))
	}
	for i, ua := range got[:2] {
		if !strings.Contains(ua, "reely") || strings.Contains(ua, "Go-http-client") {
			t.Errorf("request %d User-Agent = %q", i, ua)
		}
	}
	if got[2] != "reely/v1.2.3 (+https://github.com/getreely/reely)" {
		t.Errorf("stamped User-Agent = %q", got[2])
	}
}

// A refusal must say why. "403 Forbidden" alone sent this hunt in the
// wrong direction once; the body names the gateway's actual reason.
func TestXEMErrorCarriesTheRefusalReason(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		fmt.Fprint(w, "error code: 1010")
	}))
	t.Cleanup(srv.Close)
	x := NewXEM()
	x.SetBaseURL(srv.URL)
	_, err := x.Mappings(context.Background(), 80552)
	if err == nil || !strings.Contains(err.Error(), "error code: 1010") {
		t.Errorf("error = %v, want it to carry the body", err)
	}
}
