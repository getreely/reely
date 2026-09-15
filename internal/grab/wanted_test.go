package grab

import (
	"testing"
	"time"
)

func TestWantedSearchIntervalDefaultsOffAndFloors(t *testing.T) {
	svc, _, _ := testService(t)
	if got := svc.WantedSearchInterval(); got != 0 {
		t.Fatalf("default interval = %v, want off", got)
	}
	svc.Settings = settingsMap{"wanted_search_hours": "3"}
	if got := svc.WantedSearchInterval(); got != 6*time.Hour {
		t.Fatalf("floored interval = %v, want 6h", got)
	}
	svc.Settings = settingsMap{"wanted_search_hours": "24"}
	if got := svc.WantedSearchInterval(); got != 24*time.Hour {
		t.Fatalf("custom interval = %v, want 24h", got)
	}
	svc.Settings = settingsMap{"wanted_search_hours": "junk"}
	if got := svc.WantedSearchInterval(); got != 0 {
		t.Fatalf("junk interval = %v, want off", got)
	}
}

func TestWantedPassSweepsTheBacklog(t *testing.T) {
	svc, _, cat := testService(t)
	svc.Usenet = &stubDownloader{}
	// seedReleaseDay: two missing monitored movies (Arrival today, Blade
	// Runner next week — the sweep has no date gate for movies), one on
	// disk; one aired episode missing, one unaired
	movieID, showID := seedReleaseDay(t, cat)

	if n := svc.WantedPass(); n != 3 {
		t.Fatalf("swept = %d, want 3 (two movies + the aired episode)", n)
	}
	if got := svc.QueueLen(); got != 3 {
		t.Fatalf("queue holds %d, want 3", got)
	}

	// a second sweep re-lists the same targets, but the queue dedupes
	if n := svc.WantedPass(); n != 3 {
		t.Fatalf("re-sweep = %d", n)
	}
	if got := svc.QueueLen(); got != 3 {
		t.Fatalf("queue grew to %d on re-sweep", got)
	}

	// unmonitoring drops a title out of the sweep entirely
	if err := cat.SetMovieMonitored(movieID, false); err != nil {
		t.Fatal(err)
	}
	if err := cat.SetSeasonMonitored(showID, 1, false); err != nil {
		t.Fatal(err)
	}
	svc.queue, svc.queued = nil, nil
	if n := svc.WantedPass(); n != 1 {
		t.Fatalf("after unmonitoring, swept = %d, want 1 (Blade Runner)", n)
	}
}
