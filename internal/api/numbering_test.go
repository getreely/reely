package api

import (
	"encoding/json"
	"net/http"
	"strconv"
	"testing"

	"github.com/getreely/reely/internal/metadata"
)

// The release-numbering mapping: stored via the endpoint, echoed on the
// detail read, and the TVDB override outliving a metadata refresh — TMDB's
// split entries usually map to no TVDB id at all, so a refresh writing
// "none" must not clobber what the user supplied.
func TestShowNumberingRoundTripAndRefreshSafety(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	lib, err := srv.Catalog.CreateLibrary("Shows", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	id := seedShow(t, srv, lib.ID)

	rec, body := doJSON(t, h, "PUT", "/api/v1/shows/"+strconv.FormatInt(id, 10)+"/numbering",
		map[string]any{"seasonOffset": 7, "tvdbId": 80552})
	if rec.Code != http.StatusOK || body["seasonOffset"] != float64(7) {
		t.Fatalf("set numbering: %d %v", rec.Code, body)
	}

	get := func() (int, int) {
		rec, _ := doJSON(t, h, "GET", "/api/v1/shows/"+strconv.FormatInt(id, 10), nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("detail: %d %s", rec.Code, rec.Body)
		}
		var out struct {
			Show struct {
				SeasonOffset int `json:"seasonOffset"`
				TvdbID       int `json:"tvdbId"`
			} `json:"show"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Show.SeasonOffset, out.Show.TvdbID
	}
	if off, tvdb := get(); off != 7 || tvdb != 80552 {
		t.Fatalf("after set: offset %d tvdb %d", off, tvdb)
	}

	// a metadata refresh rewrites tvdb_id from TMDB (which has none for a
	// split entry) — the override must stand
	if _, err := srv.Catalog.UpsertShow(&metadata.ShowDetail{
		TmdbID: 1396, Title: "Breaking Bad", Year: 2008, TvdbID: 0,
	}, lib.ID); err != nil {
		t.Fatal(err)
	}
	if off, tvdb := get(); off != 7 || tvdb != 80552 {
		t.Fatalf("after refresh: offset %d tvdb %d — the override was clobbered", off, tvdb)
	}

	// clearing restores the defaults
	if rec, _ := doJSON(t, h, "PUT", "/api/v1/shows/"+strconv.FormatInt(id, 10)+"/numbering",
		map[string]any{"seasonOffset": 0, "tvdbId": 0}); rec.Code != http.StatusOK {
		t.Fatal(rec.Body)
	}
	if off, tvdb := get(); off != 0 || tvdb != 0 {
		t.Fatalf("after clear: offset %d tvdb %d", off, tvdb)
	}

	// nonsense is refused
	if rec, _ := doJSON(t, h, "PUT", "/api/v1/shows/"+strconv.FormatInt(id, 10)+"/numbering",
		map[string]any{"seasonOffset": 5000, "tvdbId": 0}); rec.Code != http.StatusBadRequest {
		t.Fatalf("wild offset accepted: %d", rec.Code)
	}
}
