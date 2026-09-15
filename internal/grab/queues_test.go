package grab

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"

	"github.com/getreely/reely/internal/download"
)

func qItem(name string) download.QueueItem {
	return download.QueueItem{ID: "nzo_" + name, Name: name, Status: "Downloading", SizeMB: 100}
}

func queueService(t *testing.T, movies, tv []download.QueueItem) (*Service, *stubDownloader) {
	t.Helper()
	dl := &stubDownloader{queue: map[string][]download.QueueItem{"movies": movies, "tvshows": tv}}
	return &Service{Usenet: dl}, dl
}

// The whole point of the change: more than one page of downloads is
// reachable. This used to cap at 50 with no way through.
func TestQueuePagesPastFifty(t *testing.T) {
	var movies []download.QueueItem
	for i := range 120 {
		movies = append(movies, qItem(fmt.Sprintf("Movie.%03d.1080p", i)))
	}
	svc, _ := queueService(t, movies, nil)

	first, total := svc.Queue(context.Background(), "", 0, 50)
	if total != 120 {
		t.Fatalf("total = %d, want 120", total)
	}
	if len(first) != 50 {
		t.Fatalf("first page = %d items, want 50", len(first))
	}
	if first[0].Name != "Movie.000.1080p" {
		t.Fatalf("first page starts at %q", first[0].Name)
	}
	third, _ := svc.Queue(context.Background(), "", 100, 50)
	if len(third) != 20 {
		t.Fatalf("third page = %d items, want the last 20 — the queue is being capped", len(third))
	}
	if third[0].Name != "Movie.100.1080p" {
		t.Fatalf("third page starts at %q", third[0].Name)
	}
	// past the end is empty, not an error and not a wrap to page one
	past, _ := svc.Queue(context.Background(), "", 500, 50)
	if len(past) != 0 {
		t.Fatalf("past the end returned %d items", len(past))
	}
}

func TestQueueMergesBothCategories(t *testing.T) {
	svc, _ := queueService(t,
		[]download.QueueItem{qItem("A.Movie")},
		[]download.QueueItem{qItem("A.Show.S01E01")})
	items, total := svc.Queue(context.Background(), "", 0, 50)
	if total != 2 || len(items) != 2 {
		t.Fatalf("got %d items, total %d — both categories should be in one queue", len(items), total)
	}
}

// Filtering runs across the whole queue, not the visible page — otherwise
// "narrow it, select all, cancel" acts on the wrong set.
func TestQueueFilterSpansEveryPage(t *testing.T) {
	var movies []download.QueueItem
	for i := range 120 {
		name := fmt.Sprintf("Movie.%03d.720p", i)
		if i%10 == 0 {
			name = fmt.Sprintf("Movie.%03d.2160p", i)
		}
		movies = append(movies, qItem(name))
	}
	svc, _ := queueService(t, movies, nil)

	items, total := svc.Queue(context.Background(), "2160p", 0, 50)
	if total != 12 {
		t.Fatalf("filtered total = %d, want the 12 matches across all 120", total)
	}
	if len(items) != 12 {
		t.Fatalf("filtered page = %d items", len(items))
	}
	// and it is case-insensitive, since release names are not consistent
	if _, n := svc.Queue(context.Background(), "MOVIE.000", 0, 50); n != 1 {
		t.Fatalf("case-insensitive filter matched %d", n)
	}
}

func TestCancelDownloadBinsPartialFiles(t *testing.T) {
	svc, dl := queueService(t, []download.QueueItem{qItem("Doomed.Release")}, nil)
	if err := svc.CancelDownload(context.Background(), "nzo_Doomed.Release"); err != nil {
		t.Fatal(err)
	}
	if len(dl.cancelled) != 1 || dl.cancelled[0] != "nzo_Doomed.Release" {
		t.Fatalf("cancelled = %v", dl.cancelled)
	}
	if !dl.cancelledFiles {
		t.Fatal("cancel must ask SAB to delete the partial data, or it leaks disk")
	}
}

// ── the search queue ───────────────────────────────────────────────────

func TestQueuedSearchesListsInRunOrder(t *testing.T) {
	s := &Service{}
	s.enqueue(searchTarget{movieID: 7}, "search")
	s.enqueue(searchTarget{showID: 3, season: 1, episode: 2}, "search")

	got := s.QueuedSearches()
	if len(got) != 2 {
		t.Fatalf("got %d queued searches", len(got))
	}
	if got[0].Kind != "movie" || got[0].MovieID != 7 || got[0].Key != "m7" {
		t.Fatalf("first = %+v", got[0])
	}
	if got[1].Kind != "episode" || got[1].ShowID != 3 || got[1].Season != 1 ||
		got[1].Episode != 2 || got[1].Key != "s3.1.2" {
		t.Fatalf("second = %+v", got[1])
	}
}

func TestCancelSearchesDropsOnlyThePicked(t *testing.T) {
	s := &Service{}
	s.enqueue(searchTarget{movieID: 1}, "search")
	s.enqueue(searchTarget{movieID: 2}, "search")
	s.enqueue(searchTarget{showID: 9, season: 2, episode: 5}, "search")

	if n := s.CancelSearches([]string{"m1", "s9.2.5"}); n != 2 {
		t.Fatalf("cancelled %d, want 2", n)
	}
	left := s.QueuedSearches()
	if len(left) != 1 || left[0].Key != "m2" {
		t.Fatalf("left = %+v", left)
	}
	if s.QueueLen() != 1 {
		t.Fatalf("QueueLen = %d, want 1", s.QueueLen())
	}
	// a key that already ran is not an error, just nothing to drop
	if n := s.CancelSearches([]string{"m1"}); n != 0 {
		t.Fatalf("re-cancelling an absent key removed %d", n)
	}
}

// Cancelling has to clear the dedupe set too, or the title can never be
// queued again for the rest of the process's life.
func TestCancelSearchesAllowsRequeue(t *testing.T) {
	s := &Service{}
	s.enqueue(searchTarget{movieID: 4}, "search")
	s.CancelSearches([]string{"m4"})
	s.enqueue(searchTarget{movieID: 4}, "search")
	if s.QueueLen() != 1 {
		t.Fatalf("QueueLen = %d — a cancelled search must be enqueueable again", s.QueueLen())
	}
}

func TestClearSearchQueue(t *testing.T) {
	s := &Service{}
	s.enqueue(searchTarget{movieID: 1}, "search")
	s.enqueue(searchTarget{movieID: 2}, "search")
	if n := s.ClearSearchQueue(); n != 2 {
		t.Fatalf("cleared %d, want 2", n)
	}
	if s.QueueLen() != 0 {
		t.Fatalf("QueueLen = %d after clear", s.QueueLen())
	}
	s.enqueue(searchTarget{movieID: 1}, "search")
	if s.QueueLen() != 1 {
		t.Fatal("clearing must reset the dedupe set as well")
	}
}

// The pager bug: the total came from summing SAB's noofslots across
// categories, but that field describes the QUEUE rather than the category
// filtered out of it. With two categories the same jobs were counted
// twice, and the pager offered roughly double the pages that existed —
// the back half resolving to nothing.
func TestQueueTotalCountsJobsNotCategories(t *testing.T) {
	var movies, tv []download.QueueItem
	for i := range 300 {
		movies = append(movies, qItem(fmt.Sprintf("Movie.%03d", i)))
	}
	for i := range 200 {
		tv = append(tv, qItem(fmt.Sprintf("Show.S01E%03d", i)))
	}
	svc, dl := queueService(t, movies, tv)
	// SAB answers both category queries with the whole queue's slot count,
	// which is exactly what made the old sum wrong
	dl.reportTotal = 500

	items, total := svc.Queue(context.Background(), "", 0, 50)
	if total != 500 {
		t.Fatalf("total = %d, want the 500 real jobs — not the doubled count", total)
	}
	if len(items) != 50 {
		t.Fatalf("first page = %d items", len(items))
	}
	// the last page the pager offers must actually hold something
	last, _ := svc.Queue(context.Background(), "", 450, 50)
	if len(last) != 50 {
		t.Fatalf("last page = %d items — the pager is offering pages that don't exist", len(last))
	}
	past, _ := svc.Queue(context.Background(), "", 500, 50)
	if len(past) != 0 {
		t.Fatalf("past the end returned %d items", len(past))
	}
}

// The spurious warning seen live: "queue longer than 1000 jobs — showing
// the first 428", on a queue of exactly 428. The truncation flag compared
// SAB's noofslots — which describes the WHOLE queue — against one
// CATEGORY's results, so any two-category install read as truncated on
// every poll. Truncation means one thing: a category answered with the
// full scan cap.
func TestQueueTruncationWarningNotFooledByNoofslots(t *testing.T) {
	var movies, tv []download.QueueItem
	for i := range 200 {
		movies = append(movies, qItem(fmt.Sprintf("Movie.%03d", i)))
	}
	for i := range 228 {
		tv = append(tv, qItem(fmt.Sprintf("Show.S01E%03d", i)))
	}
	svc, dl := queueService(t, movies, tv)
	dl.reportTotal = 428 // SAB answers every category query with the queue-wide count

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	items, total := svc.Queue(context.Background(), "", 0, 50)
	if total != 428 || len(items) != 50 {
		t.Fatalf("total = %d, page = %d", total, len(items))
	}
	if strings.Contains(buf.String(), "jobs — showing the first") {
		t.Fatalf("a 428-job queue logged a truncation warning: %q", buf.String())
	}
}

// And the warning still fires when a category genuinely hits the cap.
func TestQueueTruncationWarningFiresAtTheCap(t *testing.T) {
	var movies []download.QueueItem
	for i := range queueScanMax {
		movies = append(movies, qItem(fmt.Sprintf("Movie.%04d", i)))
	}
	svc, _ := queueService(t, movies, nil)

	var buf bytes.Buffer
	log.SetOutput(&buf)
	defer log.SetOutput(os.Stderr)

	if _, total := svc.Queue(context.Background(), "", 0, 50); total != queueScanMax {
		t.Fatalf("total = %d", total)
	}
	if !strings.Contains(buf.String(), "showing the first") {
		t.Fatal("a category at the scan cap must say so — silent truncation reads as complete")
	}
}
