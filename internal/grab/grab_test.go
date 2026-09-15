package grab

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"

	"github.com/getreely/reely/internal/download"
)

type stubDownloader struct {
	lastURL, lastName, lastCat string
	calls                      int
	history                    map[string][]download.HistoryItem
	queue                      map[string][]download.QueueItem
	deleted                    []string
	cancelled                  []string
	priorities                 []string // id:priority, in call order
	cancelledFiles             bool
	// reportTotal stands in for SAB's noofslots, which describes the whole
	// queue rather than the filtered category — 0 means "same as returned"
	reportTotal int
}

func (d *stubDownloader) AddURL(_ context.Context, nzbURL, nzbName, category string) (string, error) {
	d.lastURL, d.lastName, d.lastCat = nzbURL, nzbName, category
	d.calls++
	return "nzo_test", nil
}

// Queue honours start/limit the way SAB does, so a caller that asks for
// too narrow a window really does come up short here — that is what keeps
// the paging tests honest rather than decorative.
func (d *stubDownloader) Queue(_ context.Context, category string, start, limit int) ([]download.QueueItem, int, error) {
	all := d.queue[category]
	total := len(all)
	if d.reportTotal > 0 {
		total = d.reportTotal
	}
	if start >= len(all) {
		return nil, total, nil
	}
	return all[start:min(start+limit, len(all))], total, nil
}

func (d *stubDownloader) SetPriority(_ context.Context, id string, priority int) error {
	d.priorities = append(d.priorities, id+":"+strconv.Itoa(priority))
	return nil
}

func (d *stubDownloader) DeleteQueue(_ context.Context, id string, delFiles bool) error {
	d.cancelled = append(d.cancelled, id)
	d.cancelledFiles = delFiles
	for cat, items := range d.queue {
		kept := items[:0]
		for _, it := range items {
			if it.ID != id {
				kept = append(kept, it)
			}
		}
		d.queue[cat] = kept
	}
	return nil
}

func (d *stubDownloader) History(_ context.Context, category string, _ int) ([]download.HistoryItem, error) {
	return d.history[category], nil
}

func (d *stubDownloader) DeleteHistory(_ context.Context, id string, _ bool) error {
	d.deleted = append(d.deleted, id)
	for cat, items := range d.history {
		kept := items[:0]
		for _, it := range items {
			if it.ID != id {
				kept = append(kept, it)
			}
		}
		d.history[cat] = kept
	}
	return nil
}
func (d *stubDownloader) Configured() bool { return true }

func TestGrabMovie(t *testing.T) {
	svc, _, cat := testService(t)
	dl := &stubDownloader{}
	svc.Usenet = dl
	m := seedMovie(t, cat, t.TempDir())

	req := GrabRequest{
		Title: "Inception.2010.1080p.BluRay.x264", DownloadURL: "https://indexer/nzb/1",
		Indexer: "nzbs", Size: 9 << 30,
	}
	if err := svc.GrabMovie(context.Background(), m, req); err != nil {
		t.Fatal(err)
	}
	if dl.lastCat != "movies" || dl.lastName != req.Title || dl.lastURL != req.DownloadURL {
		t.Fatalf("downloader got %+v", dl)
	}

	entries, err := cat.ListHistory(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Kind != "grabbed" || entries[0].MovieID != m.ID {
		t.Fatalf("history = %+v", entries)
	}
	var detail map[string]any
	if err := json.Unmarshal([]byte(entries[0].Detail), &detail); err != nil {
		t.Fatal(err)
	}
	if detail["title"] != req.Title || detail["nzoId"] != "nzo_test" {
		t.Fatalf("detail = %v", detail)
	}
}

func TestGrabForShow(t *testing.T) {
	svc, _, cat := testService(t)
	dl := &stubDownloader{}
	svc.Usenet = dl
	sh := seedShow(t, cat, t.TempDir())

	req := GrabRequest{Title: "Breaking.Bad.S01E02.1080p.WEB", DownloadURL: "https://indexer/nzb/2"}
	if err := svc.GrabForShow(context.Background(), sh, 1, 2, req); err != nil {
		t.Fatal(err)
	}
	if dl.lastCat != "tvshows" {
		t.Fatalf("category = %q", dl.lastCat)
	}
	entries, _ := cat.ListHistory(10)
	if len(entries) != 1 || entries[0].ShowID != sh.ID || entries[0].EpisodeID == 0 {
		t.Fatalf("history = %+v", entries)
	}

	// season pack: no episode pin
	pack := GrabRequest{Title: "Breaking.Bad.S01.1080p.WEB", DownloadURL: "https://indexer/nzb/3"}
	if err := svc.GrabForShow(context.Background(), sh, 1, 0, pack); err != nil {
		t.Fatal(err)
	}
	entries, _ = cat.ListHistory(10)
	if entries[0].EpisodeID != 0 || entries[0].ShowID != sh.ID {
		t.Fatalf("pack history = %+v", entries[0])
	}

	// an episode the show doesn't carry
	if err := svc.GrabForShow(context.Background(), sh, 9, 9, req); err == nil {
		t.Fatal("grab for unknown episode did not error")
	}
}

func TestGrabValidation(t *testing.T) {
	svc, _, cat := testService(t)
	dl := &stubDownloader{}
	svc.Usenet = dl
	m := seedMovie(t, cat, t.TempDir())

	bad := []GrabRequest{
		{Title: "", DownloadURL: "https://x/y"},
		{Title: "x", DownloadURL: ""},
		{Title: "x", DownloadURL: "ftp://x/y"},
		{Title: "x", DownloadURL: "not a url"},
	}
	for _, req := range bad {
		if err := svc.GrabMovie(context.Background(), m, req); err == nil {
			t.Errorf("bad request %+v accepted", req)
		}
	}
	if dl.calls != 0 {
		t.Fatalf("downloader reached %d times by invalid requests", dl.calls)
	}
	if err := (&GrabRequest{Title: "x", DownloadURL: "https://x/y"}).Validate(); err != nil {
		t.Fatalf("good request rejected: %v", err)
	}
}
