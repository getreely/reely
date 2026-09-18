package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The activity queue merges both clients, so every row has to say which
// one is working on it — otherwise an install running usenet and
// torrents together shows one undifferentiated list.
//
// The value travels as download.QueueItem's own protocol field, straight
// out of the handler with no remapping, so this pins the whole chain the
// badge reads: the client fills it in, the wire carries it, the name
// stays "protocol".
func TestActivityQueueSaysWhichClientHasEachRow(t *testing.T) {
	qbit := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v2/auth/login":
			_, _ = w.Write([]byte("Ok."))
		case "/api/v2/torrents/info":
			w.Header().Set("content-type", "application/json")
			// qBittorrent filters by category server-side, and reely
			// sweeps one category at a time, so the fake has to filter
			// too — otherwise every torrent comes back once per category
			if err := r.ParseForm(); err != nil {
				t.Errorf("unparseable body: %v", err)
			}
			if r.PostForm.Get("category") != "movies" {
				_, _ = w.Write([]byte(`[]`))
				return
			}
			// still downloading: a done torrent is history's business
			_, _ = w.Write([]byte(`[{
				"hash":"abc123","name":"Inception.2010.1080p.WEB-DL","state":"downloading",
				"category":"movies","size":5368709120,"amount_left":1073741824,
				"progress":0.8,"eta":600
			}]`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer qbit.Close()

	srv := testServer(t)
	if err := srv.Settings.Set("qbit_url", qbit.URL); err != nil {
		t.Fatal(err)
	}

	rec, _ := doJSON(t, srv.Handler(), "GET", "/api/v1/activity", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("activity: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Queue []struct {
			NzoID    string `json:"nzo_id"`
			Protocol string `json:"protocol"`
			Filename string `json:"filename"`
			TimeLeft string `json:"timeleft"`
			Pct      int    `json:"percentage"`
		} `json:"queue"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Queue) != 1 {
		t.Fatalf("queue has %d rows, want 1: %s", len(out.Queue), rec.Body)
	}
	row := out.Queue[0]
	if row.Protocol != "torrent" {
		t.Errorf("protocol = %q, want %q — the badge has nothing to read", row.Protocol, "torrent")
	}
	if row.NzoID != "abc123" {
		t.Errorf("id = %q, want the info hash", row.NzoID)
	}
	// the rest of what a SAB row shows, so a torrent reads the same way
	if row.Filename == "" || row.TimeLeft == "" || row.Pct == 0 {
		t.Errorf("a torrent row is missing progress detail: %+v", row)
	}
}
