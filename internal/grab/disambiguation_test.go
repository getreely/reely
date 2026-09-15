package grab

import (
	"context"
	"testing"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/prowlarr"
)

// seedWifeSwap plants the reported collision: two shows titled "Wife
// Swap", 2004 and 2019, each with an S02E04 of its own — same marker,
// different episode titles.
func seedWifeSwap(t *testing.T, cat *catalog.Store) (old2004, new2019 *catalog.ShowDetails) {
	t.Helper()
	lib, err := cat.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	seed := func(tmdbID, year int, epTitle string) *catalog.ShowDetails {
		id, err := cat.UpsertShow(&metadata.ShowDetail{
			TmdbID: tmdbID, Title: "Wife Swap", Year: year,
			Seasons: []metadata.SeasonDetail{{Number: 2, Name: "Season 2", Episodes: []metadata.EpisodeDetail{
				{TmdbID: tmdbID*100 + 4, Season: 2, Episode: 4, Title: epTitle, Runtime: 42, AirDate: "2020-03-05"},
			}}},
		}, lib.ID)
		if err != nil {
			t.Fatal(err)
		}
		sh, err := cat.GetShow(id)
		if err != nil {
			t.Fatal(err)
		}
		return sh
	}
	return seed(1001, 2004, "Mayfield vs. Wasdin"), seed(2001, 2019, "Floyd-Ely Vs. Clanton")
}

// Searching the 2019 show must not accept the 2004 show's episode: a
// release whose episode-title words name the other show's S02E04 is
// rejected, a year token settles it outright, and a bare name (no episode
// words, no year) is no evidence and stays accepted.
func TestEpisodeReleasesRejectTheOtherShowsSameNumberedEpisode(t *testing.T) {
	svc, idx, cat := testService(t)
	_, new2019 := seedWifeSwap(t, cat)

	idx.releases = []prowlarr.Release{
		{Title: "Wife.Swap.S02E04.Mayfield.Wasdin.WEBDL.480p.h264.english-S1PH3R",
			Size: gb(0.4), Protocol: "usenet", DownloadURL: "u1", Indexer: "nzbs"},
		{Title: "Wife.Swap.2004.S02E04.720p.WEB-DL", Size: gb(1),
			Protocol: "usenet", DownloadURL: "u2", Indexer: "nzbs"},
		{Title: "Wife.Swap.S02E04.Floyd.Ely.Vs.Clanton.720p.WEB-DL",
			Size: gb(1), Protocol: "usenet", DownloadURL: "u3", Indexer: "nzbs"},
		{Title: "Wife.Swap.S02E04.720p.WEB-DL", Size: gb(1),
			Protocol: "usenet", DownloadURL: "u4", Indexer: "nzbs"},
	}
	views, err := svc.EpisodeReleases(context.Background(), new2019, 2, 4)
	if err != nil {
		t.Fatal(err)
	}
	byURL := map[string]ReleaseView{}
	for _, v := range views {
		byURL[v.DownloadURL] = v
	}
	if v := byURL["u1"]; v.Accepted || v.Reason != `names a different episode (Mayfield Wasdin)` {
		t.Fatalf("other show's episode: accepted=%v reason=%q", v.Accepted, v.Reason)
	}
	if v := byURL["u2"]; v.Accepted || v.Reason != "wrong year (2004)" {
		t.Fatalf("year-tagged 2004 release: accepted=%v reason=%q", v.Accepted, v.Reason)
	}
	if v := byURL["u3"]; !v.Accepted {
		t.Fatalf("this show's own episode rejected: %q", v.Reason)
	}
	if v := byURL["u4"]; !v.Accepted {
		t.Fatalf("bare name is no evidence, must stay accepted: %q", v.Reason)
	}
}

// The RSS sync makes the same call: a release naming the 2004 show's
// episode lands on the 2004 show, never the 2019 one.
func TestRSSSyncRoutesSharedTitleToTheRightShow(t *testing.T) {
	svc, idx, cat := testService(t)
	svc.Usenet = &stubDownloader{}
	old2004, new2019 := seedWifeSwap(t, cat)

	idx.releases = []prowlarr.Release{
		{Title: "Wife.Swap.S02E04.Mayfield.Wasdin.720p.WEBDL.h264.english-S1PH3R",
			Size: gb(1), Protocol: "usenet", DownloadURL: "https://idx/nzb/mw", Indexer: "nzbs"},
	}
	if n := svc.RSSSync(context.Background()); n != 1 {
		t.Fatalf("grabbed = %d, want 1 (the 2004 show's episode)", n)
	}
	ep04 := findEpisode(old2004, 2, 4)
	ep19 := findEpisode(new2019, 2, 4)
	if pending, err := cat.HasPendingGrab(0, ep04.ID); err != nil || !pending {
		t.Fatalf("2004 show's episode should hold the grab (pending=%v err=%v)", pending, err)
	}
	if pending, err := cat.HasPendingGrab(0, ep19.ID); err != nil || pending {
		t.Fatalf("2019 show must not have grabbed the 2004 episode (pending=%v err=%v)", pending, err)
	}
}

// A lone unrecognized token after the marker is mangled scene junk, not
// an episode title — "Yes.Dear.S03E09.HBTV" must not read as naming a
// different episode than "Jimmy Saves the Day".
func TestLoneJunkTokenIsNotAnEpisodeTitle(t *testing.T) {
	if episodeTitleConflict("HBTV", "Jimmy Saves the Day") {
		t.Fatal("one junk word rejected a correctly-numbered release")
	}
	if !episodeTitleConflict("Mayfield Wasdin", "Floyd-Ely Vs. Clanton") {
		t.Fatal("two real title words failed to disambiguate")
	}
	// punctuation and apostrophes normalize away on both sides
	if episodeTitleConflict("Sammys.Independence.Day", "Sammy's Independence Day") {
		t.Fatal("apostrophe difference read as a different episode")
	}
}

// With a TVDB id stored, an episode search asks by id alongside the text
// query — the way Sonarr asks — and results only the id query found are
// judged with the rest, deduped by identity.
func TestEpisodeSearchAsksByTvdbIDToo(t *testing.T) {
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
	if sh.TvdbID != 81189 {
		t.Fatalf("tvdb id not stored: %d", sh.TvdbID)
	}

	shared := prowlarr.Release{Title: "Breaking.Bad.S01E02.1080p.WEB-DL",
		Size: gb(2), Protocol: "usenet", GUID: "g1", DownloadURL: "u1"}
	idx.releases = []prowlarr.Release{shared}
	idx.idReleases = []prowlarr.Release{shared, // duplicate collapses
		{Title: "Breaking.Bad.S01E02.720p.HDTV.x264", Size: gb(1),
			Protocol: "usenet", GUID: "g2", DownloadURL: "u2"}}

	views, err := svc.EpisodeReleases(context.Background(), sh, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if idx.lastIDType != "tvsearch" || idx.lastIDQuery != "{tvdbid:81189}{season:1}{episode:2}" {
		t.Fatalf("id query = %s %q", idx.lastIDType, idx.lastIDQuery)
	}
	if len(views) != 2 {
		t.Fatalf("views = %d, want 2 (shared release deduped, id-only release kept)", len(views))
	}
}

// A movie with an IMDb id asks by it; one without asks by text alone.
func TestMovieSearchAsksByImdbID(t *testing.T) {
	svc, idx, cat := testService(t)
	lib, err := cat.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	id, err := cat.UpsertMovie(&metadata.MovieDetail{
		TmdbID: 27205, Title: "Inception", Year: 2010, Runtime: 148, ImdbID: "tt1375666",
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	m, err := cat.GetMovie(id)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.MovieReleases(context.Background(), m); err != nil {
		t.Fatal(err)
	}
	if idx.lastIDType != "movie" || idx.lastIDQuery != "{imdbid:tt1375666}{tmdbid:27205}" {
		t.Fatalf("id query = %s %q", idx.lastIDType, idx.lastIDQuery)
	}
}
