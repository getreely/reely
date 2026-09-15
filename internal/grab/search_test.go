package grab

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/prowlarr"
)

type stubIndexer struct {
	releases    []prowlarr.Release
	lastQ       string
	queries     []string // every text query asked, in order
	lastCats    []int
	pageOffsets []int // every offset SearchPage was asked for
	ignorePages bool  // emulate an indexer that ignores offset entirely
	idReleases  []prowlarr.Release
	lastIDType  string
	lastIDQuery string
}

func (s *stubIndexer) Search(_ context.Context, q string, cats []int) ([]prowlarr.Release, error) {
	s.lastQ, s.lastCats = q, cats
	s.queries = append(s.queries, q)
	return s.releases, nil
}

// SearchIDs serves idReleases when set, recording the id query; a stub
// with none behaves like an indexer whose caps answer nothing by id.
func (s *stubIndexer) SearchIDs(_ context.Context, searchType, q string, cats []int) ([]prowlarr.Release, error) {
	s.lastIDType, s.lastIDQuery = searchType, q
	return s.idReleases, nil
}

// SearchPage slices the stub's feed the way a paging indexer would:
// releases[offset : offset+limit].
func (s *stubIndexer) SearchPage(_ context.Context, q string, cats []int, offset, limit int) ([]prowlarr.Release, error) {
	s.lastQ, s.lastCats = q, cats
	s.queries = append(s.queries, q)
	s.pageOffsets = append(s.pageOffsets, offset)
	if s.ignorePages {
		offset = 0
	}
	if offset >= len(s.releases) {
		return nil, nil
	}
	end := len(s.releases)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return s.releases[offset:end], nil
}
func (s *stubIndexer) Configured() bool { return true }

func gb(n float64) int64 { return int64(n * (1 << 30)) }

func testService(t *testing.T) (*Service, *stubIndexer, *catalog.Store) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	cat := catalog.New(conn)
	idx := &stubIndexer{}
	return &Service{Indexer: idx, Catalog: cat}, idx, cat
}

func seedMovie(t *testing.T, cat *catalog.Store, libPath string) *catalog.MovieDetails {
	t.Helper()
	lib, err := cat.CreateLibrary("Movies", libPath, "movies")
	if err != nil {
		t.Fatal(err)
	}
	id, err := cat.UpsertMovie(&metadata.MovieDetail{
		TmdbID: 27205, Title: "Inception", Year: 2010, Runtime: 148,
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	m, err := cat.GetMovie(id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMovieReleases(t *testing.T) {
	svc, idx, cat := testService(t)
	m := seedMovie(t, cat, t.TempDir())
	idx.releases = []prowlarr.Release{
		{Title: "Inception.2010.1080p.BluRay.x264-GROUP", Size: gb(9), Protocol: "usenet", Indexer: "nzbs"},
		{Title: "Inception.2010.1080p.REMUX", Size: gb(25), Protocol: "usenet", Indexer: "nzbs"},
		{Title: "Inception.2010.720p.WEB", Size: gb(3), Protocol: "torrent", Indexer: "rarbg"},
		{Title: "Interstellar.2014.1080p.BluRay", Size: gb(9), Protocol: "usenet", Indexer: "nzbs"},
		{Title: "Inception.2010.1080p.WEB-DL", Size: gb(5), Protocol: "usenet", Indexer: "nzbs"},
		{Title: "Inception Soundtrack FLAC", Size: gb(1), Protocol: "usenet", Indexer: "nzbs"},
	}
	views, err := svc.MovieReleases(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if len(views) != 6 {
		t.Fatalf("views = %d", len(views))
	}
	// accepted first, best first: bluray 9GB above web-dl 5GB
	if !views[0].Accepted || views[0].Source != "bluray" {
		t.Fatalf("top = %+v", views[0])
	}
	if !views[1].Accepted || views[1].Source != "webdl" {
		t.Fatalf("second = %+v", views[1])
	}
	byTitle := map[string]ReleaseView{}
	for _, v := range views {
		byTitle[v.Title] = v
	}
	if v := byTitle["Inception.2010.1080p.REMUX"]; v.Accepted || v.Reason == "" {
		t.Fatalf("25GB remux not rejected: %+v", v)
	}
	// a torrent is judged on its merits now, not turned away for being a
	// torrent — this install has no client at all, which is a question
	// for the grab rather than for the search
	if v := byTitle["Inception.2010.720p.WEB"]; !v.Accepted {
		t.Fatalf("torrent should be judged like anything else: %+v", v)
	}
	if v := byTitle["Interstellar.2014.1080p.BluRay"]; v.Accepted {
		t.Fatalf("wrong title accepted: %+v", v)
	}
	if v := byTitle["Inception Soundtrack FLAC"]; v.Accepted {
		t.Fatalf("junk accepted: %+v", v)
	}
	if idx.lastQ != "Inception 2010" || idx.lastCats[0] != 2000 {
		t.Fatalf("query = %q cats %v", idx.lastQ, idx.lastCats)
	}
}

func seedShow(t *testing.T, cat *catalog.Store, libPath string) *catalog.ShowDetails {
	t.Helper()
	lib, err := cat.CreateLibrary("TV", libPath, "shows")
	if err != nil {
		t.Fatal(err)
	}
	id, err := cat.UpsertShow(&metadata.ShowDetail{
		TmdbID: 1396, Title: "Breaking Bad", Year: 2008,
		Seasons: []metadata.SeasonDetail{{Number: 1, Name: "Season 1", Episodes: []metadata.EpisodeDetail{
			{TmdbID: 1, Season: 1, Episode: 1, Title: "Pilot", Runtime: 58},
			{TmdbID: 2, Season: 1, Episode: 2, Title: "Cat's in the Bag...", Runtime: 48},
			{TmdbID: 3, Season: 1, Episode: 3, Title: "...And the Bag's in the River", Runtime: 48},
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

// Sibling shows whose names extend each other must never cross-match, and
// TMDB's accents must meet the scene's ASCII.
func TestTitleMatch(t *testing.T) {
	cases := []struct {
		catalog, release string
		want             bool
	}{
		{"90 Day Fiancé", "90 Day Fiance", true},
		{"90 Day Fiancé", "90 Day Fiance The Other Way", false},
		{"90 Day Fiancé: The Other Way", "90 Day Fiance The Other Way", true},
		{"90 Day Fiancé: The Other Way", "90 Day Fiance", false},
		{"The Office", "Office", true}, // leading article drops
		{"Amélie", "Amelie", true},
		{"S.W.A.T.", "S W A T", true},
		{"Breaking Bad", "Breaking", false},
	}
	for _, c := range cases {
		if got := titleMatch(c.catalog, c.release); got != c.want {
			t.Errorf("titleMatch(%q, %q) = %v, want %v", c.catalog, c.release, got, c.want)
		}
	}
}

func TestEpisodeReleases(t *testing.T) {
	svc, idx, cat := testService(t)
	sh := seedShow(t, cat, t.TempDir())
	idx.releases = []prowlarr.Release{
		{Title: "Breaking.Bad.S01E02.1080p.WEB-DL", Size: gb(2), Protocol: "usenet"},
		{Title: "Breaking.Bad.S01E01-E03.1080p.WEB", Size: gb(6), Protocol: "usenet"}, // span covers e02
		{Title: "Breaking.Bad.S01E03.1080p.WEB", Size: gb(2), Protocol: "usenet"},
		{Title: "Breaking.Bad.S01.1080p.WEB-DL", Size: gb(8), Protocol: "usenet"}, // pack, not this episode
	}
	views, err := svc.EpisodeReleases(context.Background(), sh, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	if idx.lastQ != "Breaking Bad S01E02" || idx.lastCats[0] != 5000 {
		t.Fatalf("query = %q cats %v", idx.lastQ, idx.lastCats)
	}
	byTitle := map[string]ReleaseView{}
	for _, v := range views {
		byTitle[v.Title] = v
	}
	if v := byTitle["Breaking.Bad.S01E02.1080p.WEB-DL"]; !v.Accepted {
		t.Fatalf("direct hit rejected: %+v", v)
	}
	if v := byTitle["Breaking.Bad.S01E01-E03.1080p.WEB"]; !v.Accepted {
		t.Fatalf("covering span rejected: %+v", v)
	}
	if v := byTitle["Breaking.Bad.S01E03.1080p.WEB"]; v.Accepted {
		t.Fatalf("wrong episode accepted: %+v", v)
	}
	if v := byTitle["Breaking.Bad.S01.1080p.WEB-DL"]; v.Accepted {
		t.Fatalf("season pack accepted for episode search: %+v", v)
	}
}

func TestSeasonReleases(t *testing.T) {
	svc, idx, cat := testService(t)
	sh := seedShow(t, cat, t.TempDir())
	idx.releases = []prowlarr.Release{
		{Title: "Breaking.Bad.S01.1080p.WEB-DL", Size: gb(7), Protocol: "usenet"},
		// 3 episodes × 48 min median × 80 MB/min max ≈ 11.3 GB — 40 GB busts it
		{Title: "Breaking.Bad.S01.1080p.REMUX", Size: gb(40), Protocol: "usenet"},
		{Title: "Breaking.Bad.S02.1080p.WEB-DL", Size: gb(7), Protocol: "usenet"},
		{Title: "Breaking.Bad.S01E01.1080p.WEB", Size: gb(2), Protocol: "usenet"},
	}
	views, err := svc.SeasonReleases(context.Background(), sh, 1)
	if err != nil {
		t.Fatal(err)
	}
	if idx.lastQ != "Breaking Bad S01" {
		t.Fatalf("query = %q", idx.lastQ)
	}
	byTitle := map[string]ReleaseView{}
	for _, v := range views {
		byTitle[v.Title] = v
	}
	if v := byTitle["Breaking.Bad.S01.1080p.WEB-DL"]; !v.Accepted {
		t.Fatalf("good pack rejected: %+v", v)
	}
	if v := byTitle["Breaking.Bad.S01.1080p.REMUX"]; v.Accepted {
		t.Fatalf("oversize pack accepted: %+v", v)
	}
	if v := byTitle["Breaking.Bad.S02.1080p.WEB-DL"]; v.Accepted {
		t.Fatalf("wrong season accepted: %+v", v)
	}
	if v := byTitle["Breaking.Bad.S01E01.1080p.WEB"]; v.Accepted {
		t.Fatalf("single episode accepted for season search: %+v", v)
	}
}

// Release names drop punctuation that catalog titles keep — the matcher
// must see through apostrophes and ampersands, or "Its Always Sunny"
// never finds "It's Always Sunny in Philadelphia".
func TestTitleMatchPunctuation(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"Its Always Sunny in Philadelphia", "It's Always Sunny in Philadelphia", true},
		{"It’s Always Sunny in Philadelphia", "Its Always Sunny in Philadelphia", true},
		{"Law and Order", "Law & Order", true},
		{"Greys Anatomy", "Grey's Anatomy", true},
		{"The Handmaids Tale", "The Handmaid's Tale", true},
		{"Different Show Entirely", "It's Always Sunny in Philadelphia", false},
	}
	for _, tc := range cases {
		if got := titleMatch(tc.a, tc.b); got != tc.want {
			t.Errorf("titleMatch(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}
