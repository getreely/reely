package sabnzbd

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func testClient(t *testing.T, handler http.HandlerFunc) (*Client, *url.Values) {
	t.Helper()
	var got url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.URL.Query()
		handler(w, r)
	}))
	t.Cleanup(srv.Close)
	c := New(func() string { return srv.URL }, func() string { return "test-key" })
	return c, &got
}

func TestAddURL(t *testing.T) {
	c, got := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": true, "nzo_ids": []string{"SABnzbd_nzo_x1"}})
	})
	nzo, err := c.AddURL(context.Background(), "https://indexer/nzb/123", "Movie.2024.1080p", "movies")
	if err != nil {
		t.Fatal(err)
	}
	if nzo != "SABnzbd_nzo_x1" {
		t.Fatalf("nzo = %q", nzo)
	}
	q := *got
	if q.Get("mode") != "addurl" || q.Get("name") != "https://indexer/nzb/123" ||
		q.Get("nzbname") != "Movie.2024.1080p" || q.Get("cat") != "movies" ||
		q.Get("apikey") != "test-key" || q.Get("output") != "json" {
		t.Fatalf("query = %v", q)
	}
}

func TestAddURLRefused(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": false, "error": "API Key Incorrect"})
	})
	_, err := c.AddURL(context.Background(), "https://indexer/nzb/123", "x", "tvshows")
	if err == nil || !strings.Contains(err.Error(), "API Key Incorrect") {
		t.Fatalf("err = %v", err)
	}
}

func TestUnconfigured(t *testing.T) {
	c := New(func() string { return "" }, func() string { return "" })
	if c.Configured() {
		t.Fatal("empty client claims configured")
	}
	if _, err := c.AddURL(context.Background(), "https://x/y", "n", "tvshows"); err == nil {
		t.Fatal("unconfigured AddURL did not error")
	}
}

// The queue read is what Activity pages over, so it has to report SAB's
// own total — not just what one response happened to carry.
func TestQueueReportsTotalAndWindow(t *testing.T) {
	c, got := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"queue": map[string]any{
			"noofslots": 340,
			"slots": []map[string]any{
				{"nzo_id": "a", "filename": "One.1080p", "status": "Downloading", "cat": "movies",
					"mb": "100", "mbleft": "40", "percentage": "60", "timeleft": "0:01:00"},
			},
		}})
	})
	items, total, err := c.Queue(context.Background(), "movies", 100, 25)
	if err != nil {
		t.Fatal(err)
	}
	if total != 340 {
		t.Fatalf("total = %d, want SAB's noofslots 340", total)
	}
	if len(items) != 1 || items[0].ID != "a" {
		t.Fatalf("items = %+v", items)
	}
	// SAB quotes its numbers; the neutral item carries real ones. This is
	// the seam's whole job — a SABnzbd habit that stops at this package
	// instead of travelling out to whatever reads the queue.
	if items[0].SizeMB != 100 || items[0].LeftMB != 40 || items[0].Percentage != 60 {
		t.Errorf("SAB's quoted numbers did not decode: %+v", items[0])
	}
	if items[0].Name != "One.1080p" || items[0].Status != "Downloading" ||
		items[0].Category != "movies" || items[0].TimeLeft != "0:01:00" {
		t.Errorf("fields lost in conversion: %+v", items[0])
	}
	q := *got
	if q.Get("mode") != "queue" || q.Get("category") != "movies" ||
		q.Get("start") != "100" || q.Get("limit") != "25" {
		t.Fatalf("query = %v", q)
	}
}

// Some SAB builds quote the count. A total we can't parse must not fail
// the whole read — it falls back to what actually arrived.
func TestQueueTotalTolerantOfShape(t *testing.T) {
	for _, tc := range []struct {
		name, raw string
		want      int
	}{
		{"quoted", `"12"`, 12},
		{"bare", `12`, 12},
		{"junk", `"lots"`, 1}, // unparseable → at least what was sent
		{"missing-is-short", `0`, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
				_, _ = fmt.Fprintf(w, `{"queue":{"noofslots":%s,"slots":[
					{"nzo_id":"a","filename":"f","status":"Downloading","cat":"movies",
					 "mb":"1","mbleft":"1","percentage":"0","timeleft":""}]}}`, tc.raw)
			})
			_, total, err := c.Queue(context.Background(), "movies", 0, 50)
			if err != nil {
				t.Fatal(err)
			}
			if total != tc.want {
				t.Fatalf("total = %d, want %d", total, tc.want)
			}
		})
	}
}

// Cancelling has to take the partial data with it, or a cancelled grab
// silently leaks disk in SAB's incomplete folder.
func TestDeleteQueueRemovesFiles(t *testing.T) {
	c, got := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": true})
	})
	if err := c.DeleteQueue(context.Background(), "nzo_9", true); err != nil {
		t.Fatal(err)
	}
	q := *got
	if q.Get("mode") != "queue" || q.Get("name") != "delete" ||
		q.Get("value") != "nzo_9" || q.Get("del_files") != "1" {
		t.Fatalf("query = %v", q)
	}
}

func TestDeleteQueueRefused(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": false})
	})
	if err := c.DeleteQueue(context.Background(), "nzo_9", true); err == nil {
		t.Fatal("a refused cancel must be an error — the caller reports it as cancelled otherwise")
	}
}
