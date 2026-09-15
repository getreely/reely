package refresh

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/scene"
)

// The whole pipeline against TheXEM's real answers to the exact two URLs
// reely calls, captured from the live service: the havemap index and the
// map for Kitchen Nightmares (US). This is the end-to-end path a refresh
// takes — index, mappings, resolve, store — and the numbers it must
// produce are the ones the show's releases actually carry.
func TestSyncSceneAgainstRealXEM(t *testing.T) {
	havemap, err := os.ReadFile("../scene/testdata/xem_havemap.json")
	if err != nil {
		t.Fatal(err)
	}
	all, err := os.ReadFile("../scene/testdata/xem_80552.json")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/havemap":
			w.Write(havemap) //nolint:errcheck
		case "/all":
			w.Write(all) //nolint:errcheck
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	cat := catalog.New(conn)
	lib, err := cat.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	counts := map[int]int{1: 22, 2: 13, 3: 14, 4: 17, 5: 16, 6: 10, 7: 10, 8: 11, 9: 12}
	var seasons []metadata.SeasonDetail
	for n := 1; n <= 9; n++ {
		var eps []metadata.EpisodeDetail
		for e := 1; e <= counts[n]; e++ {
			eps = append(eps, metadata.EpisodeDetail{TmdbID: n*1000 + e, Season: n, Episode: e})
		}
		seasons = append(seasons, metadata.SeasonDetail{Number: n, Episodes: eps})
	}
	showID, err := cat.UpsertShowTVDB(&metadata.ShowDetail{
		TvdbID: 80552, TmdbID: 11294, Title: "Kitchen Nightmares (US)", Year: 2007, Seasons: seasons,
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}

	x := scene.NewXEM()
	x.SetBaseURL(srv.URL)
	(&Refresher{Catalog: cat, XEM: x}).SyncScene(context.Background(), showID, "tvdb", 80552)

	sh, err := cat.GetShow(showID)
	if err != nil {
		t.Fatal(err)
	}
	mapped := 0
	for _, se := range sh.Seasons {
		for _, e := range se.Episodes {
			if e.SceneSeason > 0 {
				mapped++
			}
		}
	}
	if mapped != 115 {
		t.Errorf("episodes carrying scene numbering = %d, want 115", mapped)
	}
	for _, c := range []struct{ season, episode, wantS, wantE int }{
		{9, 8, 10, 8},   // the release from the report
		{9, 12, 10, 12}, // past XEM's last mapping, extrapolated
		{1, 11, 2, 1},   // the season-1 split
		{7, 1, 8, 1},    // the 2023 revival
	} {
		var got catalog.Episode
		for _, se := range sh.Seasons {
			for _, e := range se.Episodes {
				if e.Season == c.season && e.Episode == c.episode {
					got = e
				}
			}
		}
		if got.SceneSeason != c.wantS || got.SceneEpisode != c.wantE {
			t.Errorf("S%02dE%02d → S%02dE%02d, want S%02dE%02d", c.season, c.episode,
				got.SceneSeason, got.SceneEpisode, c.wantS, c.wantE)
		}
	}
}

// seedShow puts one TVDB-sourced show in a fresh library with scene
// numbering already stored — the state a working install is in.
func seedNumberedShow(t *testing.T) (*catalog.Store, int64) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	cat := catalog.New(conn)
	lib, err := cat.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	id, err := cat.UpsertShowTVDB(&metadata.ShowDetail{
		TvdbID: 80552, TmdbID: 11294, Title: "Kitchen Nightmares (US)", Year: 2007,
		Seasons: []metadata.SeasonDetail{{Number: 9, Episodes: []metadata.EpisodeDetail{
			{TmdbID: 1, Season: 9, Episode: 8, Title: "Golden Girls"},
		}}},
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat.SetSceneNumbering(id, []scene.Numbering{
		{Season: 9, Episode: 8, SceneSeason: 10, SceneEpisode: 8},
	}); err != nil {
		t.Fatal(err)
	}
	return cat, id
}

// An unreachable TheXEM must never be written down as "this show has no
// mapping". The numbering already stored is what every search and import
// for the show depends on, and an outage that quietly erased it would
// leave the show unfindable until the service came back.
func TestSceneNumberingSurvivesAnXEMOutage(t *testing.T) {
	cat, showID := seedNumberedShow(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gateway timeout", http.StatusGatewayTimeout)
	}))
	t.Cleanup(srv.Close)
	x := scene.NewXEM()
	x.SetBaseURL(srv.URL)
	r := &Refresher{Catalog: cat, XEM: x}
	for range 3 {
		r.SyncScene(context.Background(), showID, "tvdb", 80552)
	}
	sh, err := cat.GetShow(showID)
	if err != nil {
		t.Fatal(err)
	}
	if ep := sh.Seasons[0].Episodes[0]; ep.SceneSeason != 10 || ep.SceneEpisode != 8 {
		t.Errorf("an XEM outage erased scene numbering: S%02dE%02d", ep.SceneSeason, ep.SceneEpisode)
	}
}

// A show TheXEM genuinely has no map for is a different matter: its
// answer is an ordinary empty one, and stale numbering must clear.
func TestSceneNumberingClearsWhenXEMHasNoMap(t *testing.T) {
	cat, showID := seedNumberedShow(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/havemap" {
			fmt.Fprint(w, `{"result":"success","data":["80552"],"message":""}`)
			return
		}
		fmt.Fprint(w, `{"result":"failure","message":"no show with the tvdb_id 80552 found","data":""}`)
	}))
	t.Cleanup(srv.Close)
	x := scene.NewXEM()
	x.SetBaseURL(srv.URL)
	(&Refresher{Catalog: cat, XEM: x}).SyncScene(context.Background(), showID, "tvdb", 80552)
	sh, err := cat.GetShow(showID)
	if err != nil {
		t.Fatal(err)
	}
	if ep := sh.Seasons[0].Episodes[0]; ep.SceneSeason != 0 {
		t.Errorf("withdrawn mapping kept: S%02dE%02d", ep.SceneSeason, ep.SceneEpisode)
	}
}

// A refresh somebody clicked must account for itself, including when it
// decides to do nothing: the reason it skipped is the whole diagnosis.
func TestVerboseSyncSceneReportsEverySkip(t *testing.T) {
	cat, showID := seedNumberedShow(t)
	var log bytes.Buffer
	restore := captureLog(&log)
	t.Cleanup(restore)

	for _, c := range []struct {
		name   string
		r      *Refresher
		source string
		tvdbID int
		want   string
	}{
		{"no client", &Refresher{Catalog: cat, Verbose: true}, "tvdb", 80552, "no TheXEM client"},
		{"tmdb-sourced", &Refresher{Catalog: cat, XEM: scene.NewXEM(), Verbose: true}, "tmdb", 80552, "not tvdb"},
		{"no id", &Refresher{Catalog: cat, XEM: scene.NewXEM(), Verbose: true}, "tvdb", 0, "no TVDB id"},
	} {
		log.Reset()
		c.r.SyncScene(context.Background(), showID, c.source, c.tvdbID)
		if got := log.String(); !strings.Contains(got, c.want) {
			t.Errorf("%s: log = %q, want it to mention %q", c.name, got, c.want)
		}
	}

	// and the case that matters here: TheXEM simply does not list the show
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"result":"success","data":["70668","70900"],"message":""}`)
	}))
	t.Cleanup(srv.Close)
	x := scene.NewXEM()
	x.SetBaseURL(srv.URL)
	log.Reset()
	(&Refresher{Catalog: cat, XEM: x, Verbose: true}).SyncScene(context.Background(), showID, "tvdb", 80552)
	if got := log.String(); !strings.Contains(got, "lists 2 mapped series and tvdb 80552 is not one") {
		t.Errorf("unmapped show: log = %q", got)
	}
}

// captureLog redirects the standard logger into buf until the returned
// function is called.
func captureLog(buf *bytes.Buffer) func() {
	out, flags := log.Writer(), log.Flags()
	log.SetOutput(buf)
	log.SetFlags(0)
	return func() { log.SetOutput(out); log.SetFlags(flags) }
}
