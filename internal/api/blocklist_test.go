package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
)

func TestBlocklistListAndBulkRemove(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	for _, title := range []string{"Bad.Release.1080p", "Worse.Release.2160p", "Fine.Actually.720p"} {
		if err := srv.Catalog.AddBlocklist(title, "nzbs", 0, 0, "download failed"); err != nil {
			t.Fatal(err)
		}
	}

	rec, _ := doJSON(t, h, "GET", "/api/v1/blocklist", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Blocklist []struct {
			ID           int64  `json:"id"`
			ReleaseTitle string `json:"releaseTitle"`
		} `json:"blocklist"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Blocklist) != 3 {
		t.Fatalf("blocklist = %+v", out.Blocklist)
	}

	// bulk pardon two of the three — the select-all path
	rec, body := doJSON(t, h, "POST", "/api/v1/blocklist/remove",
		map[string]any{"ids": []int64{out.Blocklist[0].ID, out.Blocklist[1].ID}})
	if rec.Code != http.StatusOK || body["removed"] != float64(2) {
		t.Fatalf("remove: %d %v", rec.Code, body)
	}
	rec, _ = doJSON(t, h, "GET", "/api/v1/blocklist", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Blocklist) != 1 {
		t.Fatalf("after remove: %+v", out.Blocklist)
	}

	// empty ids is a caller mistake
	if rec, _ := doJSON(t, h, "POST", "/api/v1/blocklist/remove",
		map[string]any{"ids": []int64{}}); rec.Code != http.StatusBadRequest {
		t.Fatalf("empty remove: %d", rec.Code)
	}
}

// A grab that turned out to be the wrong file can be banned straight off
// its history row — with the same release/indexer/title scope the grab
// itself recorded.
func TestBlocklistFromGrabbedHistoryRow(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	lib, err := srv.Catalog.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	movieID := seedMovie(t, srv, lib.ID)
	if err := srv.Catalog.AddHistory("grabbed", movieID, 0, 0,
		`{"title":"The.Matrix.1999.1080p.WEB-DL","indexer":"nzbs","nzoId":"nzo_1"}`); err != nil {
		t.Fatal(err)
	}
	entries, err := srv.Catalog.ListHistory(1)
	if err != nil || len(entries) != 1 {
		t.Fatalf("history = %v (%v)", entries, err)
	}

	rec, body := doJSON(t, h, "POST", "/api/v1/history/"+strconv.FormatInt(entries[0].ID, 10)+"/blocklist", nil)
	if rec.Code != http.StatusOK || body["blocked"] != "The.Matrix.1999.1080p.WEB-DL" {
		t.Fatalf("blocklist from history: %d %v", rec.Code, body)
	}
	list, err := srv.Catalog.ListBlocklist()
	if err != nil || len(list) != 1 {
		t.Fatalf("blocklist = %v (%v)", list, err)
	}
	e := list[0]
	if e.ReleaseTitle != "The.Matrix.1999.1080p.WEB-DL" || e.Indexer != "nzbs" || e.MovieID != movieID {
		t.Fatalf("entry = %+v", e)
	}

	// only grabs are bannable this way — an import or scan row has no
	// release to ban
	if err := srv.Catalog.AddHistory("imported", movieID, 0, 0, `{"title":"whatever"}`); err != nil {
		t.Fatal(err)
	}
	entries, err = srv.Catalog.ListHistory(1)
	if err != nil || len(entries) != 1 {
		t.Fatal(err)
	}
	if rec, _ := doJSON(t, h, "POST",
		"/api/v1/history/"+strconv.FormatInt(entries[0].ID, 10)+"/blocklist", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("imported row blocklisted: %d, want 400", rec.Code)
	}
}
