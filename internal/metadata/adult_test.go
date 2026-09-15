package metadata

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// adultServer answers every TMDB endpoint reely reads with one clean title
// and one flagged one, and records the include_adult parameter it was sent.
func adultServer(t *testing.T) (*TMDB, *[]string) {
	t.Helper()
	var seen []string
	pair := func(movieKey, dateKey string) []map[string]any {
		return []map[string]any{
			{"id": 1, movieKey: "Clean Title", dateKey: "2020-01-01", "adult": false},
			{"id": 2, movieKey: "Flagged Title", dateKey: "2020-01-01", "adult": true},
		}
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path+"?include_adult="+r.URL.Query().Get("include_adult"))
		p := r.URL.Path
		w.Header().Set("content-type", "application/json")
		var body any
		switch {
		case p == "/movie/2" || p == "/tv/2":
			body = map[string]any{"adult": true, "title": "Flagged", "name": "Flagged"}
		case p == "/movie/1" || p == "/tv/1":
			body = map[string]any{"adult": false, "title": "Clean", "name": "Clean",
				"seasons": []any{}}
		case p == "/person/9":
			body = map[string]any{"name": "Someone", "combined_credits": map[string]any{
				"cast": []map[string]any{
					{"id": 1, "media_type": "movie", "title": "Clean Title", "adult": false},
					{"id": 2, "media_type": "movie", "title": "Flagged Title", "adult": true},
				}}}
		case p == "/list/7":
			body = map[string]any{"items": []map[string]any{
				{"id": 1, "media_type": "movie", "title": "Clean Title", "adult": false},
				{"id": 2, "media_type": "movie", "title": "Flagged Title", "adult": true},
			}}
		case strings.Contains(p, "/tv") && !strings.Contains(p, "search"):
			body = map[string]any{"results": pair("name", "first_air_date")}
		default:
			body = map[string]any{"results": pair("title", "release_date")}
		}
		json.NewEncoder(w).Encode(body) //nolint:errcheck // test fixture
	}))
	t.Cleanup(srv.Close)
	tm := NewTMDB(func() string { return "k" })
	tm.SetBaseURL(srv.URL)
	return tm, &seen
}

// Nothing flagged adult may reach any list reely renders. Each of these is
// a separate decode site, and a surface added later that forgets the filter
// is exactly the failure this is here to catch.
func TestAdultTitlesNeverReachAnyList(t *testing.T) {
	tm, _ := adultServer(t)
	ctx := context.Background()

	searches := map[string]func() ([]SearchResult, error){
		"movie search":    func() ([]SearchResult, error) { return tm.SearchMovies(ctx, "q", 0) },
		"show search":     func() ([]SearchResult, error) { return tm.SearchShows(ctx, "q", 0) },
		"trending movies": func() ([]SearchResult, error) { return tm.Trending(ctx, "movie") },
		"popular shows":   func() ([]SearchResult, error) { return tm.Popular(ctx, "tv") },
		"top rated":       func() ([]SearchResult, error) { return tm.TopRated(ctx, "movie") },
		"more like this":  func() ([]SearchResult, error) { return tm.Similar(ctx, "movie", 1) },
		"on a provider":   func() ([]SearchResult, error) { return tm.OnProvider(ctx, "movie", 8, "US") },
	}
	for name, run := range searches {
		t.Run(name, func(t *testing.T) {
			got, err := run()
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 {
				t.Fatalf("got %d results, want only the clean one", len(got))
			}
			if strings.Contains(got[0].Title, "Flagged") {
				t.Fatalf("a flagged title reached the results: %q", got[0].Title)
			}
		})
	}

	t.Run("person filmography", func(t *testing.T) {
		p, err := tm.Person(ctx, 9)
		if err != nil {
			t.Fatal(err)
		}
		if len(p.Credits) != 1 || strings.Contains(p.Credits[0].Title, "Flagged") {
			t.Fatalf("credits = %+v, want only the clean one", p.Credits)
		}
	})

	t.Run("watched list", func(t *testing.T) {
		items, err := tm.ListItems(ctx, 7, 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 || strings.Contains(items[0].Title, "Flagged") {
			t.Fatalf("items = %+v, want only the clean one", items)
		}
	})

	t.Run("tmdb chart list source", func(t *testing.T) {
		items, err := tm.Chart(ctx, "popular", "movie", 0)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) != 1 || strings.Contains(items[0].Title, "Flagged") {
			t.Fatalf("items = %+v, want only the clean one", items)
		}
	})
}

// The gate that actually protects the library: a tmdb id is guessable, so
// fetching a flagged title by id must fail rather than hand back a record
// that preview, add and refresh would all happily use.
func TestAdultTitleCannotBeFetchedByID(t *testing.T) {
	tm, _ := adultServer(t)
	ctx := context.Background()

	if _, err := tm.Movie(ctx, 2); !errors.Is(err, ErrAdultContent) {
		t.Fatalf("Movie(flagged) error = %v, want ErrAdultContent", err)
	}
	if _, err := tm.Show(ctx, 2); !errors.Is(err, ErrAdultContent) {
		t.Fatalf("Show(flagged) error = %v, want ErrAdultContent", err)
	}
	// and the gate must not block everything else on the way past
	if _, err := tm.Movie(ctx, 1); err != nil {
		t.Fatalf("Movie(clean) = %v, want it to load", err)
	}
}

// Layer one: every request carries include_adult=false, whether or not the
// endpoint reads it, so no call site can forget.
func TestEveryRequestPinsIncludeAdultFalse(t *testing.T) {
	tm, seen := adultServer(t)
	ctx := context.Background()
	_, _ = tm.SearchMovies(ctx, "q", 0)
	_, _ = tm.Trending(ctx, "movie")
	_, _ = tm.Movie(ctx, 1)
	_, _ = tm.Person(ctx, 9)

	if len(*seen) == 0 {
		t.Fatal("no requests recorded")
	}
	for _, req := range *seen {
		if !strings.HasSuffix(req, "?include_adult=false") {
			t.Errorf("request did not pin include_adult=false: %s", req)
		}
	}
}
