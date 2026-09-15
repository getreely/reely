package grab

import (
	"context"
	"testing"

	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/prowlarr"
)

// Two 2026 productions both called "The Odyssey" produce releases the
// parser cannot tell apart: same title, same year, plausible quality. The
// ids some indexers attach to a posting are the one signal that can — and
// when every reported id names a different title, the release is rejected
// however perfect its name looks. No id reported changes nothing: this is
// a veto on positive evidence, never a gate on its absence, so an early
// release the indexer hasn't mapped yet still gets through.
func TestMovieSearchVetoesIDMismatchedRelease(t *testing.T) {
	svc, idx, cat := testService(t)
	lib, err := cat.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	id, err := cat.UpsertMovie(&metadata.MovieDetail{
		TmdbID: 1139087, Title: "The Odyssey", Year: 2026, Runtime: 150, ImdbID: "tt1877830",
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	m, err := cat.GetMovie(id)
	if err != nil {
		t.Fatal(err)
	}

	idx.releases = []prowlarr.Release{
		// the imposter: name and year of the wanted movie, ids of another
		{Title: "The.Odyssey.2026.1080p.WEB-DL.DDP5.1-WRONG", Size: gb(5), Protocol: "usenet",
			Indexer: "nzbs", GUID: "g1", TmdbID: 555555},
		{Title: "The.Odyssey.2026.1080p.WEB-DL.DDP5.1-ALSOWRONG", Size: gb(5), Protocol: "usenet",
			Indexer: "nzbs", GUID: "g2", ImdbID: 999},
		// no mapping at all: judged on its name, as before
		{Title: "The.Odyssey.2026.1080p.WEB-DL.DDP5.1-UNMAPPED", Size: gb(5), Protocol: "usenet",
			Indexer: "nzbs", GUID: "g3"},
		// one agreeing id clears the release, even beside a wrong one
		{Title: "The.Odyssey.2026.1080p.WEB-DL.DDP5.1-MAPPED", Size: gb(5), Protocol: "usenet",
			Indexer: "nzbs", GUID: "g4", ImdbID: 1877830, TmdbID: 555555},
	}
	views, err := svc.MovieReleases(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ReleaseView{}
	for _, v := range views {
		got[v.Title] = v
	}
	for _, title := range []string{
		"The.Odyssey.2026.1080p.WEB-DL.DDP5.1-WRONG",
		"The.Odyssey.2026.1080p.WEB-DL.DDP5.1-ALSOWRONG",
	} {
		v, ok := got[title]
		if !ok {
			t.Fatalf("%s missing from results", title)
		}
		if v.Accepted {
			t.Errorf("%s accepted despite the indexer naming a different movie", title)
		}
		if v.Reason == "" {
			t.Errorf("%s rejected without a reason", title)
		}
	}
	for _, title := range []string{
		"The.Odyssey.2026.1080p.WEB-DL.DDP5.1-UNMAPPED",
		"The.Odyssey.2026.1080p.WEB-DL.DDP5.1-MAPPED",
	} {
		if v := got[title]; !v.Accepted {
			t.Errorf("%s rejected: %s", title, v.Reason)
		}
	}
}

// Shows must NOT be vetoed on a mismatched reported id. TV entries merge,
// split and rename ("Monster (2022)" was once "DAHMER — Monster: The
// Jeffrey Dahmer Story"), and indexers map TV releases by name — often to
// an entry since folded into another — so a foreign tvdb id on a
// correctly-named release is common and proves nothing. A show-side veto
// shipped briefly and silently emptied real shows' searches.
func TestEpisodeSearchIgnoresMismatchedReportedTvdbID(t *testing.T) {
	svc, idx, cat := testService(t)
	lib, err := cat.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	id, err := cat.UpsertShow(&metadata.ShowDetail{
		TmdbID: 1396, Title: "Breaking Bad", Year: 2008, TvdbID: 81189,
		Seasons: []metadata.SeasonDetail{{Number: 1, Name: "Season 1", Episodes: []metadata.EpisodeDetail{
			{TmdbID: 1, Season: 1, Episode: 2, Title: "Cat's in the Bag...", Runtime: 48},
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
		{Title: "Breaking.Bad.S01E02.1080p.WEB-DL.OTHER", Size: gb(2), Protocol: "usenet",
			Indexer: "nzbs", GUID: "g1", TvdbID: 70000},
		{Title: "Breaking.Bad.S01E02.1080p.WEB-DL.RIGHT", Size: gb(2), Protocol: "usenet",
			Indexer: "nzbs", GUID: "g2", TvdbID: 81189},
		{Title: "Breaking.Bad.S01E02.1080p.WEB-DL.UNMAPPED", Size: gb(2), Protocol: "usenet",
			Indexer: "nzbs", GUID: "g3"},
	}
	views, err := svc.EpisodeReleases(context.Background(), sh, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if !v.Accepted {
			t.Errorf("%s rejected: %s — reported tv ids must never veto a show", v.Title, v.Reason)
		}
	}
}

// The RSS sweep applies the same veto: the imposter would outscore the
// real release (bluray beats web), so only the veto explains the web
// release winning.
func TestRSSSyncSkipsIDMismatchedRelease(t *testing.T) {
	svc, idx, dl := rssService(t)
	seedMovie(t, svc.Catalog, t.TempDir()) // Inception, tmdb 27205, monitored, no file

	idx.releases = []prowlarr.Release{
		{Title: "Inception.2010.1080p.BluRay.x264", Size: gb(9), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/imposter", Indexer: "nzbs", GUID: "g1", TmdbID: 555555},
		{Title: "Inception.2010.1080p.WEB-DL", Size: gb(5), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/real", Indexer: "nzbs", GUID: "g2", TmdbID: 27205},
	}
	if n := svc.RSSSync(context.Background()); n != 1 {
		t.Fatalf("grabbed = %d, want 1", n)
	}
	if dl.lastURL != "https://idx/nzb/real" {
		t.Fatalf("grabbed %q — the id-mismatched bluray should have been skipped", dl.lastURL)
	}
}

func TestImdbNum(t *testing.T) {
	cases := map[string]int{
		"tt1375666": 1375666, "1375666": 1375666, " tt0000001 ": 1,
		"": 0, "ttabc": 0, "tt-5": 0,
	}
	for in, want := range cases {
		if got := imdbNum(in); got != want {
			t.Errorf("imdbNum(%q) = %d, want %d", in, got, want)
		}
	}
}
