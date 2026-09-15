package grab

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/download"
	"github.com/getreely/reely/internal/prowlarr"
)

// inFlightWorld is testService with the connection kept, so a history row
// can be backdated past the settle window — the only way to reach the
// branch that asks the download client whether a grab is still real.
func inFlightWorld(t *testing.T) (*Service, *stubIndexer, *stubDownloader, *sql.DB) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	idx := &stubIndexer{}
	dl := &stubDownloader{}
	return &Service{Indexer: idx, Catalog: catalog.New(conn), Usenet: dl}, idx, dl, conn
}

func backdateHistory(t *testing.T, conn *sql.DB, movieID int64) {
	t.Helper()
	if _, err := conn.Exec(
		`UPDATE history SET created_at = datetime('now','-2 hours') WHERE movie_id = ?`,
		movieID); err != nil {
		t.Fatal(err)
	}
}

// Cancelling a download deletes it from the client and marks it imported
// in memory, but writes no history — so 'grabbed' stayed the title's last
// word and every search for the next three days refused with "already in
// flight" while no such download existed anywhere. Switching protocols
// made it look permanent: the new client was never going to finish a job
// the old one no longer had.
func TestACancelledGrabStopsBlockingSearch(t *testing.T) {
	svc, idx, _, conn := inFlightWorld(t)
	m := seedMovie(t, svc.Catalog, t.TempDir())
	idx.releases = []prowlarr.Release{
		{Title: "Inception.2010.1080p.WEB-DL", Size: gb(5), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/1", Indexer: "nzbs"},
	}
	if err := svc.Catalog.AddHistory("grabbed", m.ID, 0, 0, `{"nzoId":"nzo_cancelled"}`); err != nil {
		t.Fatal(err)
	}
	backdateHistory(t, conn, m.ID)
	// the client's queue is empty: the job was cancelled

	out := svc.SearchNow(context.Background(), m.ID, 0, 0, 0)
	if out.Grabbed == "" {
		t.Fatalf("a cancelled grab still blocks the search: %q", out.Reason)
	}
}

// The same row, with the job actually still in the client's queue, is a
// real download and must go on blocking — otherwise every sweep fetches
// a second copy of whatever is already coming.
func TestAGrabStillQueuedKeepsBlocking(t *testing.T) {
	svc, idx, dl, conn := inFlightWorld(t)
	m := seedMovie(t, svc.Catalog, t.TempDir())
	idx.releases = []prowlarr.Release{
		{Title: "Inception.2010.1080p.WEB-DL", Size: gb(5), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/1", Indexer: "nzbs"},
	}
	if err := svc.Catalog.AddHistory("grabbed", m.ID, 0, 0, `{"nzoId":"nzo_live"}`); err != nil {
		t.Fatal(err)
	}
	backdateHistory(t, conn, m.ID)
	dl.queue = map[string][]download.QueueItem{
		"movies": {{ID: "nzo_live", Name: "Inception.2010.1080p.WEB-DL", Status: "Downloading"}},
	}

	out := svc.SearchNow(context.Background(), m.ID, 0, 0, 0)
	if out.Grabbed != "" || out.Reason == "" {
		t.Fatalf("a live download did not block: grabbed %q, reason %q", out.Grabbed, out.Reason)
	}
}

// A grab recorded seconds ago is believed without asking anyone. The
// client has only just been handed the job and may not list it yet;
// doubting it that early is how the same release gets fetched twice.
func TestAFreshGrabBlocksWithoutConsultingTheClient(t *testing.T) {
	svc, idx, _, _ := inFlightWorld(t)
	m := seedMovie(t, svc.Catalog, t.TempDir())
	idx.releases = []prowlarr.Release{
		{Title: "Inception.2010.1080p.WEB-DL", Size: gb(5), Protocol: "usenet",
			DownloadURL: "https://idx/nzb/1", Indexer: "nzbs"},
	}
	if err := svc.Catalog.AddHistory("grabbed", m.ID, 0, 0, `{"nzoId":"nzo_fresh"}`); err != nil {
		t.Fatal(err)
	}
	// not backdated, and the queue is empty

	out := svc.SearchNow(context.Background(), m.ID, 0, 0, 0)
	if out.Grabbed != "" || out.Reason == "" {
		t.Fatalf("a just-sent grab did not block: grabbed %q, reason %q", out.Grabbed, out.Reason)
	}
}
