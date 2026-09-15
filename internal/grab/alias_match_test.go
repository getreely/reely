package grab

import (
	"context"
	"strings"
	"testing"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/parser"
	"github.com/getreely/reely/internal/prowlarr"
)

// TVDB disambiguates same-named shows with a year in the canonical title
// itself — "Monster (2022)" — and releases never carry it. The title
// match folds a trailing year, but never across two DIFFERENT years.
func TestTitleMatchReleaseFoldsYearSuffix(t *testing.T) {
	cases := []struct {
		release, canonical string
		want               bool
	}{
		{"Monster", "Monster (2022)", true},
		{"Monster 2022", "Monster (2022)", true},
		{"Monster (2022)", "Monster 2022", true},
		{"Monster (2022)", "Monster (2017)", false}, // two years never fold
		{"Monster High", "Monster (2022)", false},
		{"Severance", "Monster (2022)", false},
		{"Kitchen Nightmares", "Kitchen Nightmares (US)", true}, // country fold intact
		{"1923", "1923", true},                                  // a bare year IS some shows' whole title
	}
	for _, c := range cases {
		if got := titleMatchRelease(c.release, c.canonical); got != c.want {
			t.Errorf("titleMatchRelease(%q, %q) = %v, want %v", c.release, c.canonical, got, c.want)
		}
	}
}

func TestQueryTitleDropsParenYear(t *testing.T) {
	if q := queryTitle("Monster (2022)"); q != "Monster 2022" {
		t.Errorf("queryTitle = %q, want %q", q, "Monster 2022")
	}
	if q := queryTitle("Kitchen Nightmares (US)"); q != "Kitchen Nightmares (US)" {
		t.Errorf("a country suffix is not a year: got %q", q)
	}
}

// The anthology case end to end: the show is "Monster (2022)" and its
// seasons release under their own subtitles, which TVDB carries as
// aliases. Plain-titled, year-suffix-folded, and alias-titled releases
// all match; an unrelated monster does not.
func TestEpisodeSearchMatchesYearSuffixedTitleAndAliases(t *testing.T) {
	svc, idx, cat := testService(t)
	lib, err := cat.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	id, err := cat.UpsertShow(&metadata.ShowDetail{
		TmdbID: 158415, Title: "Monster (2022)", Year: 2022, TvdbID: 389492,
		Aliases: []string{
			"DAHMER - Monster: The Jeffrey Dahmer Story",
			"Monsters: The Lyle and Erik Menendez Story",
		},
		Seasons: []metadata.SeasonDetail{{Number: 1, Name: "Season 1", Episodes: []metadata.EpisodeDetail{
			{TmdbID: 1, Season: 1, Episode: 2, Title: "Please Don't Go", Runtime: 48},
		}}},
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := cat.GetShow(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(sh.Aliases) != 2 {
		t.Fatalf("aliases not stored: %v", sh.Aliases)
	}

	idx.releases = []prowlarr.Release{
		{Title: "Monster.S01E02.1080p.WEB-DL", Size: gb(2), Protocol: "usenet",
			Indexer: "nzbs", GUID: "g1"},
		{Title: "Monster.2022.S01E02.1080p.WEB-DL", Size: gb(2), Protocol: "usenet",
			Indexer: "nzbs", GUID: "g2"},
		{Title: "Dahmer.Monster.The.Jeffrey.Dahmer.Story.S01E02.1080p.WEB-DL", Size: gb(2),
			Protocol: "usenet", Indexer: "nzbs", GUID: "g3"},
		{Title: "Monster.High.S01E02.1080p.WEB-DL", Size: gb(2), Protocol: "usenet",
			Indexer: "nzbs", GUID: "g4"},
	}
	views, err := svc.EpisodeReleases(context.Background(), sh, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	// the text query lost the parenthesized year
	if idx.lastQ != "Monster 2022 S01E02" {
		t.Errorf("text query = %q, want %q", idx.lastQ, "Monster 2022 S01E02")
	}
	for _, v := range views {
		if v.Title == "Monster.High.S01E02.1080p.WEB-DL" {
			if v.Accepted {
				t.Error("Monster High accepted for Monster (2022)")
			}
			continue
		}
		if !v.Accepted {
			t.Errorf("%s rejected: %s", v.Title, v.Reason)
		}
	}
}

// A show whose title IS a year — "1923" — reads as a year token to the
// parser, and the year veto rejected every one of its releases against
// the show's actual year (2022). The title's own digits are never a
// year token.
func TestYearTitledShowMatches(t *testing.T) {
	svc, idx, cat := testService(t)
	lib, err := cat.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	id, err := cat.UpsertShow(&metadata.ShowDetail{
		TmdbID: 157744, Title: "1923", Year: 2022,
		Seasons: []metadata.SeasonDetail{{Number: 2, Name: "Season 2", Episodes: []metadata.EpisodeDetail{
			{TmdbID: 1, Season: 2, Episode: 1, Title: "The Killing Season", Runtime: 60},
		}}},
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := cat.GetShow(id)
	if err != nil {
		t.Fatal(err)
	}
	idx.releases = []prowlarr.Release{
		{Title: "1923.S02E01.The.Killing.Season.1080p.WEB-DL", Size: gb(3), Protocol: "usenet",
			Indexer: "nzbs", GUID: "g1"},
		{Title: "1923.2022.S02E01.1080p.WEB-DL", Size: gb(3), Protocol: "usenet",
			Indexer: "nzbs", GUID: "g2"},
	}
	views, err := svc.EpisodeReleases(context.Background(), sh, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if !v.Accepted {
			t.Errorf("%s rejected: %s", v.Title, v.Reason)
		}
	}
}

// An anthology names each season after its story, and each story's
// releases number themselves S01 as their own little show. The season
// subtitle in the name is the truth: it places a release on the right
// season and refuses it for any other, however the numbers line up.
func TestSeasonSubtitlePlacesAnthologyReleases(t *testing.T) {
	svc, idx, cat := testService(t)
	lib, err := cat.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	id, err := cat.UpsertShow(&metadata.ShowDetail{
		TmdbID: 158415, Title: "Monster (2022)", Year: 2022, TvdbID: 389492,
		Aliases: []string{
			"DAHMER - Monster: The Jeffrey Dahmer Story",
			"Monsters: The Lyle and Erik Menendez Story",
		},
		Seasons: []metadata.SeasonDetail{
			{Number: 1, Name: "Dahmer: The Jeffrey Dahmer Story", Episodes: []metadata.EpisodeDetail{
				{TmdbID: 1, Season: 1, Episode: 1, Title: "Episode One", Runtime: 48},
			}},
			{Number: 2, Name: "The Lyle and Erik Menendez Story", Episodes: []metadata.EpisodeDetail{
				{TmdbID: 2, Season: 2, Episode: 1, Title: "Blame It on the Rain", Runtime: 48},
			}},
		},
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	sh, err := cat.GetShow(id)
	if err != nil {
		t.Fatal(err)
	}

	menendez := prowlarr.Release{Title: "Monsters.The.Lyle.And.Erik.Menendez.Story.S01E01.1080p.WEB-DL",
		Size: gb(2), Protocol: "usenet", Indexer: "nzbs", GUID: "g1"}
	dahmer := prowlarr.Release{Title: "Dahmer.Monster.The.Jeffrey.Dahmer.Story.S01E01.1080p.WEB-DL",
		Size: gb(2), Protocol: "usenet", Indexer: "nzbs", GUID: "g2"}
	plain := prowlarr.Release{Title: "Monster.2022.S02E01.1080p.WEB-DL",
		Size: gb(2), Protocol: "usenet", Indexer: "nzbs", GUID: "g3"}
	idx.releases = []prowlarr.Release{menendez, dahmer, plain}

	// searching anthology S2E1: the Menendez story's own S01E01 is that
	// episode; the Dahmer story's S01E01 must never be, though its
	// numbers agree with the release form
	views, err := svc.EpisodeReleases(context.Background(), sh, 2, 1)
	if err != nil {
		t.Fatal(err)
	}
	// the subtitle form was asked for as its own text query
	if !strings.Contains(idx.lastQ, "Menendez") || !strings.Contains(idx.lastQ, "S01E01") {
		t.Errorf("no subtitle-form query fired; last text query = %q", idx.lastQ)
	}
	for _, v := range views {
		switch v.Title {
		case menendez.Title, plain.Title:
			if !v.Accepted {
				t.Errorf("%s rejected for S2E1: %s", v.Title, v.Reason)
			}
		case dahmer.Title:
			if v.Accepted {
				t.Error("the Dahmer story's S01E01 accepted as the Menendez story's")
			}
		}
	}

	// and the reverse: searching S1E1 must refuse the Menendez release
	views, err = svc.EpisodeReleases(context.Background(), sh, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if v.Title == menendez.Title && v.Accepted {
			t.Error("the Menendez story's S01E01 accepted as the Dahmer story's")
		}
		if v.Title == dahmer.Title && !v.Accepted {
			t.Errorf("the Dahmer story's own S01E01 rejected: %s", v.Reason)
		}
	}
}

// A grabbed file named by its story's subtitle imports onto the named
// season, not the season its own numbering claims.
func TestFileToCatalogFollowsSeasonSubtitle(t *testing.T) {
	sh := &catalog.ShowDetails{
		Seasons: []catalog.SeasonEpisodes{
			{Number: 1, Name: "Dahmer: The Jeffrey Dahmer Story", Episodes: []catalog.Episode{{Season: 1, Episode: 1}}},
			{Number: 2, Name: "The Lyle and Erik Menendez Story", Episodes: []catalog.Episode{{Season: 2, Episode: 1}}},
		},
	}
	p := parser.Parse("Monsters.The.Lyle.and.Erik.Menendez.Story.S01E01.1080p.WEB-DL")
	if got := fileToCatalog(sh, p); got.Ep.Season != 2 || got.Ep.Episode != 1 {
		t.Fatalf("mapped to S%02dE%02d, want S02E01", got.Ep.Season, got.Ep.Episode)
	}
	// a plain release keeps its own numbering
	q := parser.Parse("Monster.2022.S02E01.1080p.WEB-DL")
	if got := fileToCatalog(sh, q); got.Ep.Season != 2 {
		t.Fatalf("plain release remapped to S%02d", got.Ep.Season)
	}
}

// Movies match through TMDB's alternative titles the way Radarr does: a
// foreign or shortened release name is the same movie. The year still
// vetoes, and an agreeing reported id outranks the name entirely.
func TestMovieSearchMatchesAliasesAndAgreeingIds(t *testing.T) {
	svc, idx, cat := testService(t)
	lib, err := cat.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	id, err := cat.UpsertMovie(&metadata.MovieDetail{
		TmdbID: 101, Title: "Léon: The Professional", Year: 1994, Runtime: 110,
		ImdbID:  "tt0110413",
		Aliases: []string{"Léon", "The Professional"},
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	m, err := cat.GetMovie(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Aliases) != 2 {
		t.Fatalf("aliases not stored: %v", m.Aliases)
	}

	idx.releases = []prowlarr.Release{
		{Title: "Leon.1994.1080p.BluRay.x264", Size: gb(8), Protocol: "usenet",
			Indexer: "nzbs", GUID: "g1"},
		{Title: "The.Professional.1994.1080p.WEB-DL", Size: gb(4), Protocol: "usenet",
			Indexer: "nzbs", GUID: "g2"},
		// the id says it's this movie even though no stored name matches
		{Title: "Der.Profi.2.1994.1080p.BluRay.x264", Size: gb(8), Protocol: "usenet",
			Indexer: "nzbs", GUID: "g3", ImdbID: 110413},
		// same wrong name without an id: rejected on the name
		{Title: "Der.Profi.2.1994.720p.WEB-DL", Size: gb(3), Protocol: "usenet",
			Indexer: "nzbs", GUID: "g4"},
	}
	views, err := svc.MovieReleases(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if v.Title == "Der.Profi.2.1994.720p.WEB-DL" {
			if v.Accepted {
				t.Error("an unknown name with no id was accepted")
			}
			continue
		}
		if !v.Accepted {
			t.Errorf("%s rejected: %s", v.Title, v.Reason)
		}
	}
}

// An import grabbed for "Monster (2022)" may arrive named by an alias —
// that's the same show, not a mislabel.
func TestMislabelVetoKnowsAliases(t *testing.T) {
	aliases := []string{"DAHMER - Monster: The Jeffrey Dahmer Story"}
	p := parser.Parse("Dahmer.Monster.The.Jeffrey.Dahmer.Story.S01E02.1080p.WEB-DL")
	if reason := mislabelVeto("Monster (2022)", aliases, p); reason != "" {
		t.Errorf("alias-named import vetoed: %s", reason)
	}
	wrong := parser.Parse("Monster.High.S01E02.1080p.WEB-DL")
	if reason := mislabelVeto("Monster (2022)", aliases, wrong); reason == "" {
		t.Error("a genuinely different title passed the veto")
	}
}
