package prowlarr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Searches only ever ask for movies (2000) and TV (5000), but indexers
// misfile and a category we asked for is not a category we got. Anything
// carrying an XXX category is dropped whatever else it claims to be.
func TestSearchDropsAdultCategories(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`[
			{"title":"Clean Movie 2020 1080p","categories":[{"id":2040,"name":"Movies/HD"}]},
			{"title":"Flagged A","categories":[{"id":6000,"name":"XXX"}]},
			{"title":"Flagged B","categories":[{"id":6060,"name":"XXX/Other"}]},
			{"title":"Flagged C mislabelled","categories":[{"id":2040,"name":"Movies/HD"},{"id":6010,"name":"XXX/DVD"}]},
			{"title":"Clean Show S01E01","categories":[{"id":5030,"name":"TV/SD"}]},
			{"title":"No categories at all","categories":[]}
		]`))
	}))
	defer srv.Close()

	c := New(func() string { return srv.URL }, func() string { return "k" })
	got, err := c.Search(context.Background(), "q", MovieCats)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"Clean Movie 2020 1080p": true,
		"Clean Show S01E01":      true,
		"No categories at all":   true, // nothing to judge on stays; the category is the signal
	}
	if len(got) != len(want) {
		t.Fatalf("got %d releases, want %d: %+v", len(got), len(want), got)
	}
	for _, r := range got {
		if !want[r.Title] {
			t.Errorf("a release that should have been dropped came back: %q", r.Title)
		}
	}
}
