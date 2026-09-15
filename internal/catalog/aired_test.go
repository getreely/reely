package catalog

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/metadata"
)

// airedFixture builds a show whose season is halfway through airing: two
// episodes out, two still to come, and one with no date at all — the shape
// TMDB actually serves for a season in progress.
func airedFixture(t *testing.T) (*Store, int64, int64) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	cat := New(conn)
	lib, err := cat.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	day := func(n int) string {
		return time.Now().AddDate(0, 0, n).Format("2006-01-02")
	}
	showID, err := cat.UpsertShow(&metadata.ShowDetail{
		TmdbID: 1, Title: "Half Aired", Year: 2026,
		Seasons: []metadata.SeasonDetail{{Number: 1, Name: "Season 1", Episodes: []metadata.EpisodeDetail{
			{TmdbID: 1, Season: 1, Episode: 1, Title: "One", AirDate: day(-14)},
			{TmdbID: 2, Season: 1, Episode: 2, Title: "Two", AirDate: day(-7)},
			{TmdbID: 3, Season: 1, Episode: 3, Title: "Three", AirDate: day(7)},
			{TmdbID: 4, Season: 1, Episode: 4, Title: "Four", AirDate: day(14)},
			{TmdbID: 5, Season: 1, Episode: 5, Title: "Five"}, // announced, undated
		}}},
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	return cat, lib.ID, showID
}

// An episode that hasn't aired isn't missing. The show's aired count — what
// the UI subtracts on-disk from to say "N missing" — must exclude both
// future episodes and undated ones, matching what the backlog search will
// actually go looking for.
func TestShowAiredCountExcludesUnairedEpisodes(t *testing.T) {
	cat, _, showID := airedFixture(t)

	d, err := cat.GetShow(showID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Episodes != 5 {
		t.Fatalf("total episodes = %d, want 5", d.Episodes)
	}
	if d.Aired != 2 {
		t.Fatalf("aired = %d, want 2 (two future and one undated are not missing)", d.Aired)
	}

	// the list path has to agree with the detail path — the library grid and
	// the show page must not disagree about the same show
	shows, err := cat.ListShows(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(shows) != 1 {
		t.Fatalf("shows = %d, want 1", len(shows))
	}
	if shows[0].Aired != d.Aired || shows[0].Episodes != d.Episodes {
		t.Fatalf("list says %d/%d, detail says %d/%d",
			shows[0].Aired, shows[0].Episodes, d.Aired, d.Episodes)
	}

	// and the count has to match what the sweep would queue, or "3 missing"
	// promises searches that never happen
	want, err := cat.Wanted(time.Now().Format("2006-01-02"))
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != d.Aired-d.OnDisk {
		t.Fatalf("wanted %d targets, but the page would claim %d missing",
			len(want), d.Aired-d.OnDisk)
	}
}

// The library-scoped sweep behind the "search missing" button must cover
// only the kind and libraries asked for, and an empty permission set must
// find nothing rather than falling back to the whole install.
func TestWantedInScopesByKindAndLibrary(t *testing.T) {
	cat, tvLib, _ := airedFixture(t)
	movieLib, err := cat.CreateLibrary("Films", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cat.UpsertMovie(&metadata.MovieDetail{
		TmdbID: 27205, Title: "Inception", Year: 2010,
	}, movieLib.ID); err != nil {
		t.Fatal(err)
	}
	today := time.Now().Format("2006-01-02")

	cases := []struct {
		name  string
		kind  string
		libs  []int64
		want  int
		movie bool // expect movie targets in the result
	}{
		{"everything", "", nil, 3, true},
		{"shows only", "shows", nil, 2, false},
		{"movies only", "movies", nil, 1, true},
		{"one library", "shows", []int64{tvLib}, 2, false},
		{"other library", "shows", []int64{movieLib.ID}, 0, false},
		{"no libraries visible", "", []int64{}, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := cat.WantedIn(today, c.kind, c.libs)
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != c.want {
				t.Fatalf("got %d targets, want %d", len(got), c.want)
			}
			for _, g := range got {
				if g.MovieID > 0 && !c.movie {
					t.Fatalf("kind %q returned a movie target", c.kind)
				}
			}
		})
	}
}

// "Missing" may only count what the sweep would actually hunt: monitored,
// aired, and no file. An aired episode somebody un-monitored is a choice,
// not a shortfall — counting it kept shows amber forever and invited a
// search for something deliberately declined.
func TestShowWantedCountHonorsMonitoring(t *testing.T) {
	cat, _, showID := airedFixture(t)

	d, err := cat.GetShow(showID)
	if err != nil {
		t.Fatal(err)
	}
	// both aired episodes start monitored and fileless — both wanted
	if d.Wanted != 2 {
		t.Fatalf("wanted = %d at the start, want 2", d.Wanted)
	}

	// a file lands for episode one — no longer wanted
	ep1 := d.Seasons[0].Episodes[0]
	if err := cat.AttachEpisodeFile(ep1.ID, "/tv/one.mkv", 1, "1080p", "webdl"); err != nil {
		t.Fatal(err)
	}
	// episode two is deliberately un-monitored — declined, not missing
	ep2 := d.Seasons[0].Episodes[1]
	if err := cat.SetEpisodeMonitored(ep2.ID, false); err != nil {
		t.Fatal(err)
	}

	d, err = cat.GetShow(showID)
	if err != nil {
		t.Fatal(err)
	}
	if d.Wanted != 0 {
		t.Fatalf("wanted = %d, want 0 — one has a file, the other was declined", d.Wanted)
	}
	// aired stays factual: the un-monitored episode still aired
	if d.Aired != 2 || d.OnDisk != 1 {
		t.Fatalf("aired = %d onDisk = %d — the factual counts must not bend", d.Aired, d.OnDisk)
	}

	// the card's denominator: monitored-and-aired. One of the two aired
	// episodes was declined, so the fraction should read 1/1 — done —
	// never 1/2, which is what made a satisfied show look short.
	if d.MonitoredAired != 1 {
		t.Fatalf("monitoredAired = %d, want 1", d.MonitoredAired)
	}
	if have := d.MonitoredAired - d.Wanted; have != 1 {
		t.Fatalf("card fraction numerator = %d, want 1", have)
	}

	// and the list view agrees with the detail view
	shows, err := cat.ListShows(0)
	if err != nil || len(shows) != 1 {
		t.Fatalf("shows = %v (%v)", shows, err)
	}
	if shows[0].Wanted != 0 || shows[0].Aired != 2 || shows[0].MonitoredAired != 1 {
		t.Fatalf("list view wanted=%d aired=%d monitoredAired=%d — must match the detail view",
			shows[0].Wanted, shows[0].Aired, shows[0].MonitoredAired)
	}
}
