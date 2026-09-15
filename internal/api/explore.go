package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/getreely/reely/internal/metadata"
)

// Explore: the discovery rows. TMDB's charts move slowly, so one fetch
// serves the whole household for an hour instead of burning six TMDB
// calls per page load.

type trendingCache struct {
	mu            sync.Mutex
	movies, shows []metadata.SearchResult // trending, week window
	popMovies     []metadata.SearchResult
	popShows      []metadata.SearchResult
	topMovies     []metadata.SearchResult
	topShows      []metadata.SearchResult
	providers     []providerRow
	fetched       time.Time
}

// providerRow is one streaming service's row for one kind. A service that
// answers with nothing is dropped rather than shown empty — not every
// provider carries both kinds in every region.
type providerRow struct {
	Key     string                  `json:"key"`
	Name    string                  `json:"name"`
	Kind    string                  `json:"kind"` // movie | show
	Results []metadata.SearchResult `json:"results"`
}

const trendingTTL = time.Hour

func (s *Server) handleExplore(w http.ResponseWriter, r *http.Request) {
	if !s.TMDB.Configured() {
		writeErr(w, http.StatusPreconditionFailed, errors.New("no TMDB API key configured — add one in Settings"))
		return
	}
	c := &s.trending
	c.mu.Lock()
	defer c.mu.Unlock()
	if time.Since(c.fetched) > trendingTTL {
		if err := s.refreshExplore(r.Context(), c); err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
	}
	// filtered on the way out, not on the way in: the cache holds what
	// TMDB said, so flipping a setting takes effect on the next page load
	// instead of waiting out the hour
	f := rowFilter{
		hideAnime:   s.Settings.Get("hide_anime") == "true",
		englishOnly: s.Settings.Get("discover_english_only") == "true",
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"movies": f.apply(c.movies), "shows": f.apply(c.shows),
		"popularMovies": f.apply(c.popMovies),
		"popularShows":  f.apply(c.popShows),
		"topMovies":     f.apply(c.topMovies),
		"topShows":      f.apply(c.topShows),
		"providers":     f.applyProviders(c.providers),
		"imageBase":     metadata.ImageBase,
	})
}

// refreshExplore refills the cache. Every row is fetched independently and
// concurrently: a chart that errors costs its own row and nothing else, so
// one TMDB hiccup can't blank the page. Only a wholesale failure — nothing
// answered at all, which is what a bad key or a dead TMDB looks like — is
// reported as an error, and in that case the previous hour's rows are left
// in place rather than thrown away.
func (s *Server) refreshExplore(ctx context.Context, c *trendingCache) error {
	region := s.Settings.Get("watch_region")

	type job struct {
		label string
		run   func() ([]metadata.SearchResult, error)
		into  *[]metadata.SearchResult // charts land here…
		row   *providerRow             // …service rows here
	}
	jobs := []job{
		{label: "trending movies", run: chartFn(ctx, s.TMDB.Trending, "movie"), into: &c.movies},
		{label: "trending shows", run: chartFn(ctx, s.TMDB.Trending, "tv"), into: &c.shows},
		{label: "popular movies", run: chartFn(ctx, s.TMDB.Popular, "movie"), into: &c.popMovies},
		{label: "popular shows", run: chartFn(ctx, s.TMDB.Popular, "tv"), into: &c.popShows},
		{label: "top rated movies", run: chartFn(ctx, s.TMDB.TopRated, "movie"), into: &c.topMovies},
		{label: "top rated shows", run: chartFn(ctx, s.TMDB.TopRated, "tv"), into: &c.topShows},
	}
	// service rows keep the provider order they are declared in, which is
	// why they get their slots up front rather than appending as they land
	rows := make([]providerRow, 0, len(metadata.StreamingProviders)*2)
	for _, p := range metadata.StreamingProviders {
		for _, k := range []string{"movie", "tv"} {
			kind := "movie"
			if k == "tv" {
				kind = "show"
			}
			rows = append(rows, providerRow{Key: p.Key, Name: p.Name, Kind: kind})
		}
	}
	i := 0
	for _, p := range metadata.StreamingProviders {
		for _, k := range []string{"movie", "tv"} {
			id, kk := p.ID, k
			jobs = append(jobs, job{
				label: p.Name + " " + kk,
				run:   func() ([]metadata.SearchResult, error) { return s.TMDB.OnProvider(ctx, kk, id, region) },
				row:   &rows[i],
			})
			i++
		}
	}

	var wg sync.WaitGroup
	ok := make([]bool, len(jobs))
	for n := range jobs {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			j := jobs[n]
			res, err := j.run()
			if err != nil {
				log.Printf("reely: explore %s: %v", j.label, err)
				return
			}
			ok[n] = true
			if j.into != nil {
				*j.into = res
			} else {
				j.row.Results = res
			}
		}(n)
	}
	wg.Wait()

	any, all := false, true
	for _, v := range ok {
		if v {
			any = true
		} else {
			all = false
		}
	}
	if !any {
		return errors.New("TMDB answered nothing — check the API key in Settings")
	}
	// a service that carries nothing in this region is dropped rather than
	// drawn as an empty row
	c.providers = c.providers[:0]
	for _, p := range rows {
		if len(p.Results) > 0 {
			c.providers = append(c.providers, p)
		}
	}
	c.fetched = time.Now()
	if !all {
		// something came back short; try again in minutes rather than
		// leaving a gap in the page for the rest of the hour
		c.fetched = c.fetched.Add(-trendingTTL + 5*time.Minute)
	}
	return nil
}

// chartFn binds one of TMDB's kind-taking chart calls to a kind, so the
// job list above reads as a list rather than as six closures.
func chartFn(ctx context.Context, f func(context.Context, string) ([]metadata.SearchResult, error), kind string) func() ([]metadata.SearchResult, error) {
	return func() ([]metadata.SearchResult, error) { return f(ctx, kind) }
}

// rowFilter is what the household chose to leave out of the discovery
// rows. Both settings are off by default, and neither touches search:
// looking a title up by name is always a deliberate ask.
type rowFilter struct {
	hideAnime   bool
	englishOnly bool
}

// applyProviders filters each service row and drops any left empty.
func (f rowFilter) applyProviders(rows []providerRow) []providerRow {
	out := make([]providerRow, 0, len(rows))
	for _, p := range rows {
		p.Results = f.apply(p.Results)
		if len(p.Results) > 0 {
			out = append(out, p)
		}
	}
	return out
}

func (f rowFilter) apply(rows []metadata.SearchResult) []metadata.SearchResult {
	if !f.hideAnime && !f.englishOnly {
		return rows
	}
	out := make([]metadata.SearchResult, 0, len(rows))
	for _, r := range rows {
		if f.hideAnime && r.Anime {
			continue
		}
		if f.englishOnly && !r.English {
			continue
		}
		out = append(out, r)
	}
	return out
}

// handleSimilar is a title's "more like this" row. It is a live TMDB call
// rather than a cached chart: it is per-title, and the page that asks for
// it is already waiting on TMDB for the title itself.
func (s *Server) handleSimilar(w http.ResponseWriter, r *http.Request) {
	if !s.TMDB.Configured() {
		writeErr(w, http.StatusPreconditionFailed, errors.New("no TMDB API key configured — add one in Settings"))
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	kind := r.PathValue("kind")
	if kind != "movie" && kind != "tv" {
		writeErr(w, http.StatusBadRequest, errors.New("kind must be movie or tv"))
		return
	}
	rows, err := s.TMDB.Similar(r.Context(), kind, int(id))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	f := rowFilter{
		hideAnime:   s.Settings.Get("hide_anime") == "true",
		englishOnly: s.Settings.Get("discover_english_only") == "true",
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"results": f.apply(rows), "imageBase": metadata.ImageBase,
	})
}
