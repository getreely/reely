package grab

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/download"
	"github.com/getreely/reely/internal/prowlarr"
)

// The case this was written for, end to end. SAB fails a grab on missing
// articles; reely records it, blocklists that copy at that indexer, and
// searches again on its own — finding the same release at a second
// indexer, which is a different upload and worth a try. Pressing search
// then met "a download for this is already in flight", which was true
// and read as false: nothing said the download was reely's replacement,
// what it was, or where it had gone.
func TestARefusedSearchNamesTheRetryInItsWay(t *testing.T) {
	svc, idx, dl := rssService(t)
	dl.history = map[string][]download.HistoryItem{}
	m := seedMovie(t, svc.Catalog, t.TempDir())
	const release = "Inception.2010.1080p.WEB-DL"
	idx.releases = []prowlarr.Release{
		{Title: release, Size: gb(5), Protocol: "usenet",
			DownloadURL: "https://first/nzb/1", Indexer: "NZBgeek"},
		{Title: release, Size: gb(5), Protocol: "usenet",
			DownloadURL: "https://second/nzb/1", Indexer: "DrunkenSlug"},
	}

	// the first grab, from the first indexer
	if out := svc.SearchNow(context.Background(), m.ID, 0, 0, 0); out.Grabbed != release {
		t.Fatalf("first search: grabbed %q, reason %q", out.Grabbed, out.Reason)
	}
	// SAB gives up on it
	dl.history["movies"] = []download.HistoryItem{{
		ID: "nzo_test", Protocol: download.Usenet, Name: release,
		Status: download.StatusFailed, FailMessage: "Aborted, cannot be completed - missing articles",
	}}
	svc.ImportCompleted(context.Background())
	// and reely's own retry takes the other indexer's copy
	svc.ProcessQueue(context.Background(), 5)
	if dl.lastURL != "https://second/nzb/1" {
		t.Fatalf("the retry grabbed %q, want the second indexer's copy", dl.lastURL)
	}

	out := svc.SearchNow(context.Background(), m.ID, 0, 0, 0)
	if out.Grabbed != "" {
		t.Fatalf("grabbed %q over a download in flight", out.Grabbed)
	}
	for _, want := range []string{
		release, "DrunkenSlug", "SABnzbd", "reely grabbed it", "after the last one failed",
	} {
		if !strings.Contains(out.Reason, want) {
			t.Errorf("reason %q does not say %q", out.Reason, want)
		}
	}
}

func TestInFlightReasonWording(t *testing.T) {
	now := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	grab := func(mut func(*catalog.PendingGrabDetail)) *catalog.PendingGrabDetail {
		g := &catalog.PendingGrabDetail{
			At: now.Add(-2 * time.Minute), Title: "Show.S01E02.1080p.WEB-DL",
			Indexer: "NZBgeek", Protocol: download.Usenet, Via: "search",
		}
		if mut != nil {
			mut(g)
		}
		return g
	}
	for _, tc := range []struct {
		name string
		g    *catalog.PendingGrabDetail
		st   flightState
		want string
	}{
		{"reely's retry, just sent", grab(func(g *catalog.PendingGrabDetail) { g.AfterFailure = true }), flightFresh,
			"already downloading Show.S01E02.1080p.WEB-DL from NZBgeek via SABnzbd. reely grabbed it 2 minutes ago, after the last one failed."},
		{"confirmed in the queue", grab(func(g *catalog.PendingGrabDetail) { g.At = now.Add(-40 * time.Minute) }), flightQueued,
			"already downloading Show.S01E02.1080p.WEB-DL from NZBgeek via SABnzbd, still in its queue. reely grabbed it 40 minutes ago."},
		{"a torrent nobody could check on", grab(func(g *catalog.PendingGrabDetail) {
			g.Protocol, g.Via, g.At = download.Torrent, "manual", now.Add(-3*time.Hour)
		}), flightUnchecked,
			"already downloading Show.S01E02.1080p.WEB-DL from NZBgeek via qBittorrent, which couldn't be reached to check it's still there. It was grabbed by hand 3 hours ago."},
		{"a grab recorded before the client was", grab(func(g *catalog.PendingGrabDetail) { g.Protocol, g.Via = "", "rss" }), flightFresh,
			"already downloading Show.S01E02.1080p.WEB-DL from NZBgeek via the download client. reely grabbed it from RSS 2 minutes ago."},
		{"nothing to name", nil, flightUnchecked, "a download for this is already in flight"},
		{"no release on the row", grab(func(g *catalog.PendingGrabDetail) { g.Title = "" }), flightFresh,
			"a download for this is already in flight"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := inFlightReason(tc.g, tc.st, "a download for this is already in flight", now); got != tc.want {
				t.Errorf("\n got %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestAgo(t *testing.T) {
	for d, want := range map[time.Duration]string{
		10 * time.Second: "just now",
		time.Minute:      "1 minute ago",
		59 * time.Minute: "59 minutes ago",
		time.Hour:        "1 hour ago",
		26 * time.Hour:   "1 day ago",
		72 * time.Hour:   "3 days ago",
	} {
		if got := ago(d); got != want {
			t.Errorf("ago(%s) = %q, want %q", d, got, want)
		}
	}
}
