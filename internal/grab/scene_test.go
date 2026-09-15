package grab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/parser"
	"github.com/getreely/reely/internal/prowlarr"
	"github.com/getreely/reely/internal/scene"
)

// Kitchen Nightmares (US), end to end through TheXEM's real map.
//
// TheTVDB's aired order folds the 2007 and 2008 runs into one 22-episode
// Season 1, so from 2010 onward every release is numbered a season ahead
// of the catalog: the run TVDB calls S9 releases as S10. Before scene
// numbering, reely asked indexers for S09E08 and rejected every
// "Kitchen.Nightmares.US.S10E08" it was offered as "wrong episode",
// leaving the show undownloadable.
func seedKitchenNightmares(t *testing.T, cat *catalog.Store) *catalog.ShowDetails {
	t.Helper()
	lib, err := cat.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	counts := map[int]int{1: 22, 2: 13, 3: 14, 4: 17, 5: 16, 6: 10, 7: 10, 8: 11, 9: 12}
	// episode titles distinct per season, so a name carrying one can be
	// told from the same numbers in a neighbouring season
	word := []string{"", "Alpha", "Bravo", "Charlie", "Delta", "Echo", "Foxtrot", "Golf", "Hotel", "India"}
	var seasons []metadata.SeasonDetail
	for n := 1; n <= 9; n++ {
		var eps []metadata.EpisodeDetail
		for e := 1; e <= counts[n]; e++ {
			title := word[n] + " Kitchen"
			if n == 9 && e == 8 {
				title = "Golden Girls" // the episode from the report
			}
			eps = append(eps, metadata.EpisodeDetail{
				TmdbID: n*1000 + e, Season: n, Episode: e, Runtime: 42,
				Title: title, AirDate: "2026-09-01",
			})
		}
		seasons = append(seasons, metadata.SeasonDetail{Number: n, Episodes: eps})
	}
	id, err := cat.UpsertShowTVDB(&metadata.ShowDetail{
		TvdbID: 80552, TmdbID: 11294, Title: "Kitchen Nightmares (US)", Year: 2007,
		Seasons: seasons,
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}

	// the real XEM map, served from the fixture the scene package keeps
	body, err := os.ReadFile("../scene/testdata/xem_80552.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(body) //nolint:errcheck // test server
	}))
	t.Cleanup(srv.Close)
	x := scene.NewXEM()
	x.SetBaseURL(srv.URL)
	maps, err := x.Mappings(context.Background(), 80552)
	if err != nil {
		t.Fatal(err)
	}
	eps, err := cat.SceneEpisodes(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat.SetSceneNumbering(id, scene.Resolve(eps, maps)); err != nil {
		t.Fatal(err)
	}
	sh, err := cat.GetShow(id)
	if err != nil {
		t.Fatal(err)
	}
	return sh
}

func TestSceneNumberedEpisodeSearch(t *testing.T) {
	svc, idx, cat := testService(t)
	sh := seedKitchenNightmares(t, cat)

	idx.releases = []prowlarr.Release{
		{Title: "Kitchen.Nightmares.US.S10E08.Golden.Girls.1080p.DSNP.WEB-DL.DDP5.1.H.264-RAWR",
			Size: gb(2), Protocol: "usenet", Indexer: "nzbs", GUID: "g1"},
		{Title: "Kitchen Nightmares US S10E08 Golden Girls 1080p DSNP WEB-DL DDP5 1 H 264-RAWR",
			Size: gb(2), Protocol: "usenet", Indexer: "nzbs", GUID: "g2"},
		// the catalog's own numbering: on this show that names S08E08,
		// a different episode, and must not be taken for this one
		{Title: "Kitchen.Nightmares.US.S09E08.1080p.WEB-DL", Size: gb(2),
			Protocol: "usenet", Indexer: "nzbs", GUID: "g3"},
	}
	views, err := svc.EpisodeReleases(context.Background(), sh, 9, 8)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Kitchen Nightmares (US) S10E08"; idx.lastQ != want {
		t.Errorf("query = %q, want %q", idx.lastQ, want)
	}
	if want := "{tvdbid:80552}{season:10}{episode:8}"; idx.lastIDQuery != want {
		t.Errorf("id query = %q, want %q", idx.lastIDQuery, want)
	}
	for _, v := range views {
		if strings.Contains(v.Title, "S09E08") {
			if v.Accepted {
				t.Error("S09E08 accepted for the episode that releases as S10E08")
			}
			continue
		}
		if !v.Accepted {
			t.Errorf("scene-numbered release rejected: %s — %s", v.Title, v.Reason)
		}
	}
}

// The 2023 revival is TVDB's S7 and the scene's S8 — the same shift, one
// era earlier, and the case reely's own numbering test already described
// back when the show still lived on TMDB.
func TestSceneNumberedRevivalSeason(t *testing.T) {
	svc, idx, cat := testService(t)
	sh := seedKitchenNightmares(t, cat)
	idx.releases = []prowlarr.Release{
		{Title: "Kitchen.Nightmares.US.S08E01.1080p.WEB-DL", Size: gb(2),
			Protocol: "usenet", Indexer: "nzbs", GUID: "g1"},
	}
	views, err := svc.EpisodeReleases(context.Background(), sh, 7, 1)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Kitchen Nightmares (US) S08E01"; idx.lastQ != want {
		t.Errorf("query = %q, want %q", idx.lastQ, want)
	}
	if len(views) != 1 || !views[0].Accepted {
		t.Fatalf("revival release not accepted: %+v", views)
	}
}

// TVDB's merged Season 1 is two seasons to the scene, so a pack of it has
// to be asked for under both numbers — and a pack answering to either is
// this season's.
func TestSceneNumberedSplitSeasonPack(t *testing.T) {
	svc, idx, cat := testService(t)
	sh := seedKitchenNightmares(t, cat)
	idx.releases = []prowlarr.Release{
		{Title: "Kitchen.Nightmares.US.S01.1080p.WEB-DL", Size: gb(20),
			Protocol: "usenet", Indexer: "nzbs", GUID: "g1"},
		{Title: "Kitchen.Nightmares.US.S02.1080p.WEB-DL", Size: gb(20),
			Protocol: "usenet", Indexer: "nzbs", GUID: "g2"},
		// S03 is the catalog's season 2, not this one
		{Title: "Kitchen.Nightmares.US.S03.1080p.WEB-DL", Size: gb(20),
			Protocol: "usenet", Indexer: "nzbs", GUID: "g3"},
	}
	views, err := svc.SeasonReleases(context.Background(), sh, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Kitchen Nightmares (US) S01", "Kitchen Nightmares (US) S02"} {
		if !slices.Contains(idx.queries, want) {
			t.Errorf("query %q never asked; asked %q", want, idx.queries)
		}
	}
	for _, v := range views {
		if strings.Contains(v.Title, ".S03.") {
			if v.Accepted {
				t.Error("season 2's pack accepted for season 1")
			}
			continue
		}
		if !v.Accepted {
			t.Errorf("%s rejected: %s", v.Title, v.Reason)
		}
	}
}

// The seasons before the two numberings diverge are left alone: TVDB's
// S1E01 is the scene's S01E01, and asking for it any other way would
// break the part of the show that was never broken.
func TestSceneNumberingLeavesAgreeingEpisodesAlone(t *testing.T) {
	svc, idx, cat := testService(t)
	sh := seedKitchenNightmares(t, cat)
	idx.releases = []prowlarr.Release{
		{Title: "Kitchen.Nightmares.US.S01E01.1080p.WEB-DL", Size: gb(2),
			Protocol: "usenet", Indexer: "nzbs", GUID: "g1"},
	}
	views, err := svc.EpisodeReleases(context.Background(), sh, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if want := "Kitchen Nightmares (US) S01E01"; idx.lastQ != want {
		t.Errorf("query = %q, want %q", idx.lastQ, want)
	}
	if len(views) != 1 || !views[0].Accepted {
		t.Fatalf("agreeing release not accepted: %+v", views)
	}
}

// Files land through the lenient mapper: a download named the scene's way
// places onto the right episode, and one somebody already renamed to the
// show's own numbering is still honoured as written.
func TestSceneNumberedFilePlacement(t *testing.T) {
	_, _, cat := testService(t)
	sh := seedKitchenNightmares(t, cat)
	for _, c := range []struct {
		name            string
		season, episode int
	}{
		{"Kitchen.Nightmares.US.S10E08.Golden.Girls.1080p.WEB-DL.mkv", 9, 8},
		{"Kitchen.Nightmares.US.S02E01.1080p.WEB-DL.mkv", 1, 11},
		// reely's own renaming: the numbers alone read as the scene's
		// S09E08 (the catalog's S08E08), and the episode title is what
		// keeps the file on the episode it actually is
		{"Kitchen Nightmares (US) - S09E08 - Golden Girls.mkv", 9, 8},
	} {
		p := fileToCatalog(sh, parser.Parse(c.name))
		if p.Ep.Season != c.season || p.Ep.Episode != c.episode {
			t.Errorf("%s → S%02dE%02d, want S%02dE%02d",
				c.name, p.Ep.Season, p.Ep.Episode, c.season, c.episode)
		}
	}
}
