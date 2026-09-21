package grab

import (
	"context"
	"errors"
	"testing"

	"github.com/getreely/reely/internal/download"
)

// stuckService is an install with a torrent client and one import
// already on the Activity page as stuck.
func stuckService(t *testing.T, item download.HistoryItem) (*Service, *stubRetirer) {
	t.Helper()
	qb := &stubRetirer{stubDownloader: &stubDownloader{
		history: map[string][]download.HistoryItem{"movies": {item}},
		queue:   map[string][]download.QueueItem{},
	}}
	svc := &Service{Torrent: qb, Settings: stubSettings{}}
	svc.deferRetry(item.ID, item.Name,
		errors.New("no movie in the catalog matches this download"))
	return svc, qb
}

func problemIDs(s *Service) []string {
	out := []string{}
	for _, p := range s.Problems() {
		out = append(out, p.NzoID)
	}
	return out
}

// Deleting a stuck torrent used to bin it in qBittorrent and leave the
// row behind: the delete returned the client's answer and nothing ever
// cleared reely's own problem entry. That row then outlived the job it
// described, and all three of its buttons went looking for a history
// entry that no longer existed — the only way out was restarting reely.
func TestDeletingAStuckTorrentTakesItsRowWithIt(t *testing.T) {
	svc, qb := stuckService(t, download.HistoryItem{
		ID: "abc123", Name: "Some.Movie.2024.1080p", Protocol: download.Torrent,
	})

	if err := svc.DeleteJob(context.Background(), "abc123"); err != nil {
		t.Fatalf("DeleteJob: %v", err)
	}
	if len(qb.cancelled) != 1 || qb.cancelled[0] != "abc123" {
		t.Errorf("qBittorrent was asked to delete %v, want [abc123]", qb.cancelled)
	}
	if got := problemIDs(svc); len(got) != 0 {
		t.Errorf("the row is still on the Activity page: %v", got)
	}
}

// Dismiss is the way out of a row whose job has already been dealt
// with — imported by hand, deleted in qBittorrent, dropped from SAB's
// history. Retry, resolve and delete all need the download client to
// still have it, so when it does not, every one of them fails and the
// row is permanent.
func TestDismissClearsARowTheClientNoLongerHas(t *testing.T) {
	svc, qb := stuckService(t, download.HistoryItem{
		ID: "gone", Name: "Some.Movie.2024.1080p", Protocol: download.Torrent,
	})
	// the job itself is no longer anywhere the client will admit to
	qb.history["movies"] = nil

	if err := svc.DismissJob(context.Background(), "gone"); err != nil {
		t.Fatalf("DismissJob: %v", err)
	}
	if got := problemIDs(svc); len(got) != 0 {
		t.Errorf("the row is still on the Activity page: %v", got)
	}
	// nothing was binned on the way: dismiss is about the row, and the
	// files are the user's
	if len(qb.cancelled) != 0 || len(qb.deleted) != 0 {
		t.Errorf("dismiss destroyed something: cancelled %v deleted %v", qb.cancelled, qb.deleted)
	}
	// and the job does not walk straight back on the next sweep
	if !svc.alreadyImported("gone") {
		t.Error("a dismissed job is not remembered as handled — the next sweep files it again")
	}
}

// A job the client still has is retired on the way out, because the
// usual reason a row is stuck is that retiring it is exactly what
// failed. Without that the torrent stays in the swept category and the
// row reappears on the next pass.
func TestDismissRetiresAJobTheClientStillHas(t *testing.T) {
	svc, qb := stuckService(t, download.HistoryItem{
		ID: "abc123", Name: "Some.Movie.2024.1080p", Protocol: download.Torrent,
	})

	if err := svc.DismissJob(context.Background(), "abc123"); err != nil {
		t.Fatalf("DismissJob: %v", err)
	}
	if qb.category != DefaultSeedingCategory {
		t.Errorf("category = %q, want %q — the torrent was not retired", qb.category, DefaultSeedingCategory)
	}
	if len(qb.cancelled) != 0 {
		t.Errorf("dismiss stopped the seeding: %v", qb.cancelled)
	}
}

// Dismiss is for a row that exists. Asking to dismiss something that is
// not stuck is a mistake worth hearing about rather than a silent OK.
func TestDismissRefusesAJobThatIsNotStuck(t *testing.T) {
	svc, _ := stuckService(t, download.HistoryItem{
		ID: "abc123", Name: "Some.Movie.2024.1080p", Protocol: download.Torrent,
	})

	if err := svc.DismissJob(context.Background(), "other"); err == nil {
		t.Fatal("dismissing a job with no problem row succeeded")
	}
}
