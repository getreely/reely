package grab

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/parser"
	"github.com/getreely/reely/internal/prowlarr"
)

// The Kitchen Nightmares problem: TMDB lists the 2007 original and the
// 2023 revival as two shows, both plainly titled "Kitchen Nightmares".
// Releases follow TVDB, which keeps everything one series — the revival's
// first season releases as "Kitchen.Nightmares.US.S08...". The revival
// carries seasonOffset 7 so its own S1 maps to release S8.

func seedSplitShows(t *testing.T, cat *catalog.Store) (original, revival *catalog.ShowDetails) {
	t.Helper()
	lib, err := cat.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	episodes := func(base int) []metadata.EpisodeDetail {
		return []metadata.EpisodeDetail{
			{TmdbID: base + 1, Season: 1, Episode: 1, Title: "", Runtime: 42},
			{TmdbID: base + 2, Season: 1, Episode: 2, Title: "", Runtime: 42},
		}
	}
	origID, err := cat.UpsertShow(&metadata.ShowDetail{
		TmdbID: 11294, Title: "Kitchen Nightmares", Year: 2007,
		Seasons: []metadata.SeasonDetail{{Number: 1, Name: "Season 1", Episodes: episodes(100)}},
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	revID, err := cat.UpsertShow(&metadata.ShowDetail{
		TmdbID: 235884, Title: "Kitchen Nightmares", Year: 2023,
		Seasons: []metadata.SeasonDetail{{Number: 1, Name: "Season 1", Episodes: episodes(200)}},
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.SetShowNumbering(revID, 7, 80552); err != nil {
		t.Fatal(err)
	}
	if original, err = cat.GetShow(origID); err != nil {
		t.Fatal(err)
	}
	if revival, err = cat.GetShow(revID); err != nil {
		t.Fatal(err)
	}
	return original, revival
}

func TestTitleMatchRelease(t *testing.T) {
	cases := []struct {
		release, canonical string
		want               bool
	}{
		{"Kitchen Nightmares US", "Kitchen Nightmares", true},
		{"Kitchen Nightmares", "Kitchen Nightmares", true},
		{"Kitchen Nightmares AU", "Kitchen Nightmares", true},
		// symmetric: TVDB canonical titles carry the tag releases drop
		{"Kitchen Nightmares", "Kitchen Nightmares (US)", true},
		{"Kitchen Nightmares US", "Kitchen Nightmares (US)", true},
		// two DIFFERENT countries never match
		{"Kitchen Nightmares AU", "Kitchen Nightmares (US)", false},
		// only a known country tag qualifies as droppable
		{"Kitchen Nightmares Italia", "Kitchen Nightmares", false},
		{"Kitchen Nightmares Spain", "Kitchen Nightmares", false},
		// never prefix matching
		{"Kitchen Nightmares US", "Kitchen", false},
	}
	for _, c := range cases {
		if got := titleMatchRelease(c.release, c.canonical); got != c.want {
			t.Errorf("titleMatchRelease(%q, %q) = %v, want %v", c.release, c.canonical, got, c.want)
		}
	}
}

// A search for the revival's own S1E1 must ask the indexers for S08E01 —
// text and id query both — and judge answers in scene numbering: the
// original's S01E01 is exactly the wrong file.
func TestEpisodeSearchSpeaksSceneNumbering(t *testing.T) {
	svc, idx, cat := testService(t)
	_, revival := seedSplitShows(t, cat)

	idx.releases = []prowlarr.Release{
		{Title: "Kitchen.Nightmares.US.S08E01.1080p.WEB.h264-GRP", Size: gb(2), Protocol: "usenet", Indexer: "nzbs"},
		{Title: "Kitchen.Nightmares.S01E01.1080p.WEB.x264-GRP", Size: gb(2), Protocol: "usenet", Indexer: "nzbs"},
		{Title: "Kitchen.Nightmares.US.S01E01.720p.WEB.x264-GRP", Size: gb(1), Protocol: "usenet", Indexer: "nzbs"},
	}
	views, err := svc.EpisodeReleases(context.Background(), revival, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if idx.lastQ != "Kitchen Nightmares S08E01" {
		t.Fatalf("text query = %q, want the scene number", idx.lastQ)
	}
	if !strings.Contains(idx.lastIDQuery, "{tvdbid:80552}") || !strings.Contains(idx.lastIDQuery, "{season:8}") {
		t.Fatalf("id query = %q, want the override tvdb id and scene season", idx.lastIDQuery)
	}
	got := map[string]bool{}
	for _, v := range views {
		got[v.Title] = v.Accepted
	}
	if !got["Kitchen.Nightmares.US.S08E01.1080p.WEB.h264-GRP"] {
		t.Fatalf("the scene-numbered release was rejected: %+v", views)
	}
	if got["Kitchen.Nightmares.S01E01.1080p.WEB.x264-GRP"] || got["Kitchen.Nightmares.US.S01E01.720p.WEB.x264-GRP"] {
		t.Fatal("the original's S01E01 was accepted for the revival — that is the wrong file")
	}
}

// A season-pack search maps the same way.
func TestSeasonSearchSpeaksSceneNumbering(t *testing.T) {
	svc, idx, cat := testService(t)
	_, revival := seedSplitShows(t, cat)

	idx.releases = []prowlarr.Release{
		{Title: "Kitchen.Nightmares.US.S08.1080p.WEB-DL.h264", Size: gb(4), Protocol: "usenet", Indexer: "nzbs"},
		{Title: "Kitchen.Nightmares.S01.1080p.WEB-DL.x264", Size: gb(4), Protocol: "usenet", Indexer: "nzbs"},
	}
	views, err := svc.SeasonReleases(context.Background(), revival, 1)
	if err != nil {
		t.Fatal(err)
	}
	if idx.lastQ != "Kitchen Nightmares S08" {
		t.Fatalf("text query = %q", idx.lastQ)
	}
	for _, v := range views {
		if v.Title == "Kitchen.Nightmares.US.S08.1080p.WEB-DL.h264" && !v.Accepted {
			t.Fatalf("scene-numbered pack rejected: %s", v.Reason)
		}
		if v.Title == "Kitchen.Nightmares.S01.1080p.WEB-DL.x264" && v.Accepted {
			t.Fatal("the original's S01 pack was accepted for the revival")
		}
	}
}

// The RSS sweep sees both shows' releases in one feed: the S08 release
// belongs to the revival (filed under its own S1), the S01 release to the
// original — never crossed.
func TestRSSRoutesSceneNumberingToTheRightShow(t *testing.T) {
	svc, idx, dl := rssService(t)
	original, revival := seedSplitShows(t, svc.Catalog)

	idx.releases = []prowlarr.Release{
		{Title: "Kitchen.Nightmares.US.S08E01.1080p.WEB.h264-GRP", Size: gb(2), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/revival", Indexer: "nzbs"},
		{Title: "Kitchen.Nightmares.S01E01.1080p.WEB.x264-GRP", Size: gb(2), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/original", Indexer: "nzbs"},
	}
	if n := svc.RSSSync(context.Background()); n != 2 {
		t.Fatalf("grabbed %d, want both shows' episodes", n)
	}
	if dl.calls != 2 {
		t.Fatalf("downloader reached %d times", dl.calls)
	}

	entries, err := svc.Catalog.ListHistory(10)
	if err != nil {
		t.Fatal(err)
	}
	grabbedFor := map[int64]string{} // show id → release title
	for _, e := range entries {
		if e.Kind != "grabbed" {
			continue
		}
		var detail struct {
			Title string `json:"title"`
		}
		if err := json.Unmarshal([]byte(e.Detail), &detail); err != nil {
			t.Fatal(err)
		}
		grabbedFor[e.ShowID] = detail.Title
	}
	if grabbedFor[revival.ID] != "Kitchen.Nightmares.US.S08E01.1080p.WEB.h264-GRP" {
		t.Fatalf("revival grabbed %q", grabbedFor[revival.ID])
	}
	if grabbedFor[original.ID] != "Kitchen.Nightmares.S01E01.1080p.WEB.x264-GRP" {
		t.Fatalf("original grabbed %q", grabbedFor[original.ID])
	}
}

// A grabbed job's files come back named in scene numbering; they must be
// filed under the revival's own seasons.
func TestFileToCatalogMapsSceneNames(t *testing.T) {
	svc, _, cat := testService(t)
	_, revival := seedSplitShows(t, cat)
	_ = svc

	p := parser.Parse("Kitchen.Nightmares.US.S08E02.1080p.WEB.h264-GRP.mkv")
	if p.Kind != "episode" {
		t.Fatalf("parsed as %q", p.Kind)
	}
	mapped := fileToCatalog(revival, p)
	if mapped.Ep.Season != 1 || mapped.Ep.Episode != 2 {
		t.Fatalf("mapped to S%02dE%02d", mapped.Ep.Season, mapped.Ep.Episode)
	}
	// a file someone already renamed to the show's own numbering is
	// honored as written
	own := parser.Parse("Kitchen Nightmares - S01E02 - whatever.mkv")
	if own.Kind != "episode" {
		t.Fatalf("parsed as %q", own.Kind)
	}
	kept := fileToCatalog(revival, own)
	if kept.Ep.Season != 1 || kept.Ep.Episode != 2 {
		t.Fatalf("own-numbered file mapped to S%02dE%02d", kept.Ep.Season, kept.Ep.Episode)
	}
}
