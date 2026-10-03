package metadata

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// A TMDB chart served a page at a time, as TMDB serves them.
func pagedTMDB(t *testing.T, pages map[int][]int, total int, failOn int) (*TMDB, *[]int) {
	t.Helper()
	var asked []int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := 1
		if p := r.URL.Query().Get("page"); p != "" {
			page, _ = strconv.Atoi(p)
		}
		asked = append(asked, page)
		if page == failOn {
			http.Error(w, "busy", http.StatusServiceUnavailable)
			return
		}
		rows := []map[string]any{}
		for _, id := range pages[page] {
			rows = append(rows, map[string]any{"id": id, "title": "Film " + strconv.Itoa(id)})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"results": rows, "total_pages": total})
	}))
	t.Cleanup(srv.Close)
	return &TMDB{key: func() string { return "k" }, base: srv.URL, client: srv.Client()}, &asked
}

func ids(rs []SearchResult) []int {
	out := make([]int, len(rs))
	for i, r := range rs {
		out[i] = r.TmdbID
	}
	return out
}

func TestExploreRowsReadSeveralPagesEachTitleOnce(t *testing.T) {
	tmdb, asked := pagedTMDB(t, map[int][]int{1: {1, 2, 3}, 2: {3, 4, 5}, 3: {6}}, 10, 0)
	got, err := tmdb.Trending(context.Background(), "movie")
	if err != nil {
		t.Fatal(err)
	}
	// TMDB repeats a title across pages when the chart moves between requests.
	if want := []int{1, 2, 3, 4, 5, 6}; !equal(ids(got), want) {
		t.Fatalf("got %v, want %v", ids(got), want)
	}
	if !equal(*asked, []int{1, 2, ExplorePages}) {
		t.Fatalf("asked for pages %v", *asked)
	}
}

func TestAChartShorterThanThePagesStopsAtItsEnd(t *testing.T) {
	tmdb, asked := pagedTMDB(t, map[int][]int{1: {1, 2}}, 1, 0)
	got, err := tmdb.Popular(context.Background(), "tv")
	if err != nil || len(got) != 2 || got[0].Kind != "show" {
		t.Fatalf("got %v, %v", got, err)
	}
	if len(*asked) != 1 {
		t.Fatalf("asked for %v pages of a one-page chart", *asked)
	}
}

func TestALaterPageFailingKeepsWhatCameBefore(t *testing.T) {
	tmdb, _ := pagedTMDB(t, map[int][]int{1: {1, 2}, 2: {3}}, 10, 3)
	got, err := tmdb.OnProvider(context.Background(), "movie", 8, "US")
	if err != nil {
		t.Fatal(err)
	}
	if want := []int{1, 2, 3}; !equal(ids(got), want) {
		t.Fatalf("got %v, want %v", ids(got), want)
	}
}

func TestTheFirstPageFailingIsTheChartFailing(t *testing.T) {
	tmdb, _ := pagedTMDB(t, map[int][]int{}, 10, 1)
	if _, err := tmdb.TopRated(context.Background(), "movie"); err == nil {
		t.Fatal("a chart TMDB refused came back as an empty row")
	}
}

func TestMoreLikeThisStaysOnePage(t *testing.T) {
	tmdb, asked := pagedTMDB(t, map[int][]int{1: {1}, 2: {2}}, 5, 0)
	if _, err := tmdb.Similar(context.Background(), "movie", 603); err != nil {
		t.Fatal(err)
	}
	if len(*asked) != 1 {
		t.Fatalf("a title's own row asked for %v pages", *asked)
	}
}

func equal(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
