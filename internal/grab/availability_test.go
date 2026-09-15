package grab

import (
	"context"
	"testing"
	"time"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/prowlarr"
)

// Before a movie is actually out, every release claiming it is mislabeled
// or fake — the window the fake-release farms live in. Automation waits
// for availability; manual grabs see the rejected row with its reason and
// can still take it, and a release whose reported ids agree bypasses the
// gate outright.
func TestMovieAvailabilityGate(t *testing.T) {
	today := time.Now().UTC()
	day := func(offset int) string { return today.AddDate(0, 0, offset).Format("2006-01-02") }
	cases := []struct {
		name  string
		m     catalog.Movie
		avail bool
	}{
		{"digital date passed", catalog.Movie{DigitalRelease: day(-3) + "T00:00:00.000Z"}, true},
		{"digital date today", catalog.Movie{DigitalRelease: day(0) + "T00:00:00.000Z"}, true},
		{"digital date ahead", catalog.Movie{DigitalRelease: day(60) + "T00:00:00.000Z"}, false},
		{"no digital date, fresh theatrical", catalog.Movie{ReleaseDate: day(-40)}, false},
		{"no digital date, old theatrical", catalog.Movie{ReleaseDate: day(-120)}, true},
		{"no dates at all: sparse metadata is not a gate", catalog.Movie{}, true},
		{"announced lifts the gate", catalog.Movie{MinAvailability: "announced", DigitalRelease: day(60) + "T00:00:00.000Z"}, true},
	}
	td := today.Format("2006-01-02")
	for _, c := range cases {
		if got := movieAvailable(&c.m, td); got != c.avail {
			t.Errorf("%s: available = %v, want %v", c.name, got, c.avail)
		}
	}
}

// The gate end to end: for an unreleased movie every release is rejected
// with a reason (still grabbable by hand) — an agreeing id included,
// because id mappings come from the release's own NFO, which a faker
// writes. The RSS sweep takes nothing.
func TestUnreleasedMovieGatesAutomation(t *testing.T) {
	svc, idx, cat := testService(t)
	lib, err := cat.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().UTC().AddDate(0, 0, 45)
	id, err := cat.UpsertMovie(&metadata.MovieDetail{
		TmdbID: 900001, Title: "The Odyssey", Year: future.Year(), Runtime: 150,
		ImdbID:      "tt9000001",
		ReleaseDate: time.Now().UTC().AddDate(0, 0, -30).Format("2006-01-02"),
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	m, err := cat.GetMovie(id)
	if err != nil {
		t.Fatal(err)
	}
	if m.MinAvailability != "released" {
		t.Fatalf("default availability = %q", m.MinAvailability)
	}

	fake := prowlarr.Release{Title: "The.Odyssey.1080p.WEB-DL.DDP5.1", Size: gb(5),
		Protocol: "usenet", Indexer: "nzbs", GUID: "g1", DownloadURL: "https://idx/nzb/fake"}
	// a craftier fake: the real movie's IMDb id, copied into the NFO —
	// an agreeing id must NOT open the gate
	spoofed := prowlarr.Release{Title: "The.Odyssey.1080p.WEB-DL.SPOOF", Size: gb(5),
		Protocol: "usenet", Indexer: "nzbs", GUID: "g2", ImdbID: 9000001,
		DownloadURL: "https://idx/nzb/spoofed"}
	idx.releases = []prowlarr.Release{fake, spoofed}

	views, err := svc.MovieReleases(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range views {
		if v.Accepted {
			t.Errorf("%s accepted before the movie is out", v.Title)
		}
		if v.Reason == "" {
			t.Errorf("%s: the gated row must say why, for the manual override", v.Title)
		}
	}

	// RSS: nothing may be taken before the movie is out
	dl := &stubDownloader{}
	svc.Usenet = dl
	if n := svc.RSSSync(context.Background()); n != 0 {
		t.Fatalf("rss grabbed %d pre-release releases, want 0", n)
	}

	// flip the movie to 'announced' and automation may take it again
	if err := cat.SetMovieMinAvailability(id, "announced"); err != nil {
		t.Fatal(err)
	}
	if n := svc.RSSSync(context.Background()); n != 1 {
		t.Fatalf("announced movie: rss grabbed %d, want 1", n)
	}
}
