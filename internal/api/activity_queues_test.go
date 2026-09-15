package api

import (
	"encoding/json"
	"net/http"
	"testing"
)

type searchesResponse struct {
	Searches []struct {
		Key     string `json:"key"`
		Kind    string `json:"kind"`
		Title   string `json:"title"`
		Season  int    `json:"season"`
		Episode int    `json:"episode"`
	} `json:"searches"`
	SearchesTotal int `json:"searchesTotal"`
}

func getSearches(t *testing.T, h http.Handler, query string) searchesResponse {
	t.Helper()
	rec, _ := doJSON(t, h, "GET", "/api/v1/activity"+query, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("activity: %d %s", rec.Code, rec.Body)
	}
	var out searchesResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// seedTitles puts one movie and one show in real libraries — the rows the
// queued searches point at, and what their titles resolve through.
func seedTitles(t *testing.T, srv *Server) (int64, int64) {
	t.Helper()
	movieLib, err := srv.Catalog.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	showLib, err := srv.Catalog.CreateLibrary("Shows", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	return seedMovie(t, srv, movieLib.ID), seedShow(t, srv, showLib.ID)
}

// The search queue used to be a bare count with nothing behind it. It now
// has to come back as rows carrying the title each one is for.
func TestActivityListsQueuedSearchesWithTitles(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	movieID, showID := seedTitles(t, srv)

	srv.Grab.EnqueueMovie(movieID)
	srv.Grab.EnqueueEpisode(showID, 1, 2)

	out := getSearches(t, h, "")
	if out.SearchesTotal != 2 || len(out.Searches) != 2 {
		t.Fatalf("searches = %+v (total %d)", out.Searches, out.SearchesTotal)
	}
	if out.Searches[0].Title != "The Matrix" || out.Searches[0].Kind != "movie" {
		t.Fatalf("movie row = %+v", out.Searches[0])
	}
	ep := out.Searches[1]
	if ep.Title != "Breaking Bad" || ep.Kind != "episode" || ep.Season != 1 || ep.Episode != 2 {
		t.Fatalf("episode row = %+v", ep)
	}
}

// Filtering runs over the whole queue before paging, so narrowing then
// selecting everything acts on what was narrowed — not on page one.
func TestActivitySearchFilterAndPaging(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	movieID, showID := seedTitles(t, srv)
	srv.Grab.EnqueueMovie(movieID)
	for ep := 1; ep <= 3; ep++ {
		srv.Grab.EnqueueEpisode(showID, 1, ep)
	}

	all := getSearches(t, h, "")
	if all.SearchesTotal != 4 {
		t.Fatalf("total = %d, want 4", all.SearchesTotal)
	}

	filtered := getSearches(t, h, "?searchFilter=breaking")
	if filtered.SearchesTotal != 3 || len(filtered.Searches) != 3 {
		t.Fatalf("filtered = %+v (total %d)", filtered.Searches, filtered.SearchesTotal)
	}

	// the filtered total drives the pager, so a page inside it must hold
	page := getSearches(t, h, "?searchFilter=breaking&searchLimit=2&searchOffset=2")
	if page.SearchesTotal != 3 || len(page.Searches) != 1 {
		t.Fatalf("last page = %+v (total %d)", page.Searches, page.SearchesTotal)
	}
}

func TestCancelQueuedSearches(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	movieID, showID := seedTitles(t, srv)
	srv.Grab.EnqueueMovie(movieID)
	srv.Grab.EnqueueEpisode(showID, 1, 1)
	srv.Grab.EnqueueEpisode(showID, 1, 2)

	before := getSearches(t, h, "")
	rec, body := doJSON(t, h, "POST", "/api/v1/activity/searches/cancel",
		map[string]any{"keys": []string{before.Searches[0].Key}, "all": false})
	if rec.Code != http.StatusOK || body["cancelled"] != float64(1) {
		t.Fatalf("cancel one: %d %v", rec.Code, body)
	}
	if left := getSearches(t, h, ""); left.SearchesTotal != 2 {
		t.Fatalf("after cancelling one, %d left", left.SearchesTotal)
	}

	rec, body = doJSON(t, h, "POST", "/api/v1/activity/searches/cancel",
		map[string]any{"keys": []string{}, "all": true})
	if rec.Code != http.StatusOK || body["cancelled"] != float64(2) {
		t.Fatalf("cancel all: %d %v", rec.Code, body)
	}
	if left := getSearches(t, h, ""); left.SearchesTotal != 0 {
		t.Fatalf("after cancel all, %d left", left.SearchesTotal)
	}
}

// An empty request is a client bug, not "cancel everything" — that
// distinction is the whole reason `all` is explicit.
func TestCancelSearchesRejectsEmptyRequest(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	movieID, _ := seedTitles(t, srv)
	srv.Grab.EnqueueMovie(movieID)

	rec, _ := doJSON(t, h, "POST", "/api/v1/activity/searches/cancel",
		map[string]any{"keys": []string{}, "all": false})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty cancel: %d, want 400", rec.Code)
	}
	if left := getSearches(t, h, ""); left.SearchesTotal != 1 {
		t.Fatalf("an empty cancel dropped %d searches", 1-left.SearchesTotal)
	}
}

func TestCancelDownloadsRejectsEmptyRequest(t *testing.T) {
	srv := testServer(t)
	rec, _ := doJSON(t, srv.Handler(), "POST", "/api/v1/activity/cancel",
		map[string]any{"nzoIds": []string{}})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty cancel: %d, want 400", rec.Code)
	}
}

// "Narrow it, then cancel all" has to mean everything that matched — not
// the page it happened to fit on. This is the whole point of filtering
// server-side rather than in the browser.
func TestCancelAllRespectsTheFilter(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	movieID, showID := seedTitles(t, srv)
	srv.Grab.EnqueueMovie(movieID)
	srv.Grab.EnqueueEpisode(showID, 1, 1)
	srv.Grab.EnqueueEpisode(showID, 1, 2)

	rec, body := doJSON(t, h, "POST", "/api/v1/activity/searches/cancel",
		map[string]any{"keys": []string{}, "all": true, "filter": "breaking"})
	if rec.Code != http.StatusOK || body["cancelled"] != float64(2) {
		t.Fatalf("cancel all matching: %d %v — want the 2 Breaking Bad rows", rec.Code, body)
	}
	left := getSearches(t, h, "")
	if left.SearchesTotal != 1 || left.Searches[0].Title != "The Matrix" {
		t.Fatalf("left = %+v — the unmatched title must survive", left.Searches)
	}
}

// Cancel-all with no filter still means the whole queue.
func TestCancelAllUnfilteredClearsEverything(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	movieID, showID := seedTitles(t, srv)
	srv.Grab.EnqueueMovie(movieID)
	srv.Grab.EnqueueEpisode(showID, 1, 1)

	rec, body := doJSON(t, h, "POST", "/api/v1/activity/searches/cancel",
		map[string]any{"keys": []string{}, "all": true, "filter": ""})
	if rec.Code != http.StatusOK || body["cancelled"] != float64(2) {
		t.Fatalf("cancel all: %d %v", rec.Code, body)
	}
	if left := getSearches(t, h, ""); left.SearchesTotal != 0 {
		t.Fatalf("%d searches survived a full cancel", left.SearchesTotal)
	}
}
