package grab

import (
	"context"
	"testing"
	"time"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/metadata"
)

// The taper: eager while a release is landing, daily through day three,
// single looks at seven and fourteen — and nothing outside those points.
// The two-day window this replaces orphaned every title whose acceptable
// release landed late; day two of the Reacher case is the regression.
func TestTaperGate(t *testing.T) {
	now := time.Now()
	never := time.Time{}
	cases := []struct {
		name string
		age  int
		last time.Time
		want bool
	}{
		{"release day, never searched", 0, never, true},
		{"release day, searched 3h ago", 0, now.Add(-3 * time.Hour), false},
		{"release day, searched 7h ago", 0, now.Add(-7 * time.Hour), true},
		{"day 1 still eager", 1, now.Add(-7 * time.Hour), true},
		// THE case: an acceptable release appears on day two — the old
		// window had already closed forever
		{"day 2, never searched", 2, never, true},
		{"day 2, searched this morning", 2, now.Add(-8 * time.Hour), false},
		{"day 2, searched yesterday", 2, now.Add(-25 * time.Hour), true},
		{"day 3 daily", 3, never, true},
		{"day 4 is a quiet day", 4, never, false},
		{"day 7 straggler look", 7, never, true},
		{"day 10 quiet", 10, never, false},
		{"day 14 last look", 14, never, true},
		{"day 15 belongs to the wanted sweep", 15, never, false},
		{"future dates are the calendar's business", -1, never, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := taperGate(tc.age, tc.last, now); got != tc.want {
				t.Fatalf("taperGate(age=%d) = %v, want %v", tc.age, got, tc.want)
			}
		})
	}
}

// A restart empties the in-memory ledger; the gate must re-try at most
// once per eligible day rather than stampeding the whole window.
func TestTaperAfterRestart(t *testing.T) {
	s := &Service{}
	now := time.Now()
	target := searchTarget{showID: 9, season: 3, episode: 4}

	// day 2, fresh process: one search allowed…
	if !s.dueForScheduledSearch(target, 2, now) {
		t.Fatal("first look after a restart refused")
	}
	// …and not another until tomorrow
	if s.dueForScheduledSearch(target, 2, now.Add(time.Hour)) {
		t.Fatal("second look within the day allowed")
	}
	if !s.dueForScheduledSearch(target, 3, now.Add(25*time.Hour)) {
		t.Fatal("next taper day refused")
	}
	// quiet days stay quiet even with an empty ledger
	if s.dueForScheduledSearch(searchTarget{showID: 9, season: 3, episode: 5}, 5, now) {
		t.Fatal("a quiet day searched")
	}
}

// Seeds one library of each kind with dated titles around today:
//   - movie "Arrival" went digital today, still missing  → wanted
//   - movie "Dune" went digital today but is on disk     → skipped
//   - movie "Blade Runner" goes digital next week        → not yet
//   - episode S01E01 aired yesterday, still missing      → wanted
//   - episode S01E02 airs tomorrow                       → not yet
func seedReleaseDay(t *testing.T, cat *catalog.Store) (movieID, showID int64) {
	t.Helper()
	today := time.Now().Format("2006-01-02")
	yesterday := time.Now().AddDate(0, 0, -1).Format("2006-01-02")
	tomorrow := time.Now().AddDate(0, 0, 1).Format("2006-01-02")
	nextWeek := time.Now().AddDate(0, 0, 7).Format("2006-01-02")

	lib, err := cat.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	movieID, err = cat.UpsertMovie(&metadata.MovieDetail{
		TmdbID: 329865, Title: "Arrival", Year: 2016, Runtime: 116, DigitalRelease: today,
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	onDisk, err := cat.UpsertMovie(&metadata.MovieDetail{
		TmdbID: 438631, Title: "Dune", Year: 2021, Runtime: 155, DigitalRelease: today,
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.AttachMovieFile(onDisk, "/x/dune.mkv", 1, "1080p", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.UpsertMovie(&metadata.MovieDetail{
		TmdbID: 78, Title: "Blade Runner", Year: 1982, Runtime: 117, DigitalRelease: nextWeek,
	}, lib.ID); err != nil {
		t.Fatal(err)
	}

	tvLib, err := cat.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	showID, err = cat.UpsertShow(&metadata.ShowDetail{
		TmdbID: 1396, Title: "Breaking Bad", Year: 2008,
		Seasons: []metadata.SeasonDetail{{Number: 1, Name: "Season 1", Episodes: []metadata.EpisodeDetail{
			{TmdbID: 1, Season: 1, Episode: 1, Title: "Pilot", Runtime: 58, AirDate: yesterday},
			{TmdbID: 2, Season: 1, Episode: 2, Title: "Cat's in the Bag...", Runtime: 48, AirDate: tomorrow},
		}}},
	}, tvLib.ID)
	if err != nil {
		t.Fatal(err)
	}
	return movieID, showID
}

func TestReleaseDayPassQueuesArrivals(t *testing.T) {
	svc, _, cat := testService(t)
	svc.Usenet = &stubDownloader{}
	movieID, showID := seedReleaseDay(t, cat)

	if n := svc.ReleaseDayPass(context.Background()); n != 2 {
		t.Fatalf("queued = %d, want 2 (the missing movie and yesterday's episode)", n)
	}
	if got := svc.QueueLen(); got != 2 {
		t.Fatalf("queue holds %d, want 2", got)
	}
	wantTargets := map[searchTarget]bool{
		{movieID: movieID}:                      true,
		{showID: showID, season: 1, episode: 1}: true,
	}
	for _, target := range svc.queue {
		if !wantTargets[target] {
			t.Fatalf("unexpected target queued: %+v", target)
		}
	}

	// an immediate second pass finds the same items, but the taper gate
	// holds them back
	if n := svc.ReleaseDayPass(context.Background()); n != 0 {
		t.Fatalf("re-queued within the taper gate: %d", n)
	}

	// once the eager-days gate (6h at age 0-1) expires, both are hunted again
	for k := range svc.scheduled {
		svc.scheduled[k] = time.Now().Add(-7 * time.Hour)
	}
	if n := svc.ReleaseDayPass(context.Background()); n != 2 {
		t.Fatalf("expired targets not re-queued: %d", n)
	}
	// the queue itself still dedupes: the first pass's entries never ran
	if got := svc.QueueLen(); got != 2 {
		t.Fatalf("queue holds %d after re-pass, want 2 (deduped)", got)
	}
}

func TestReleaseDayPassPrunesStaleEntries(t *testing.T) {
	svc, _, cat := testService(t)
	svc.Usenet = &stubDownloader{}
	seedReleaseDay(t, cat)

	// a target last touched sixteen days ago is past the taper window
	stale := searchTarget{movieID: 999}
	svc.scheduled = map[searchTarget]time.Time{stale: time.Now().Add(-16 * 24 * time.Hour)}

	if n := svc.ReleaseDayPass(context.Background()); n != 2 {
		t.Fatalf("queued = %d, want 2", n)
	}
	if _, ok := svc.scheduled[stale]; ok {
		t.Fatal("stale scheduled entry survived the prune")
	}
}
