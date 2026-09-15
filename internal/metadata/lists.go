package metadata

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
)

// List sources on TMDB: the official charts and any public list by id.
// They ride the API key already in Settings — the zero-setup list source.

// ListItem is one chart or list entry, reduced to what the sync needs.
type ListItem struct {
	Kind   string // movie | show
	Title  string
	Year   int
	TmdbID int
}

// tmdbChartPaths maps a chart name per kind to its endpoint. Trending uses
// the weekly window — the daily one churns too fast for a library feed.
var tmdbChartPaths = map[string]map[string]string{
	"movie": {
		"trending":  "/trending/movie/week",
		"popular":   "/movie/popular",
		"top_rated": "/movie/top_rated",
		"upcoming":  "/movie/upcoming",
	},
	"show": {
		"trending":   "/trending/tv/week",
		"popular":    "/tv/popular",
		"top_rated":  "/tv/top_rated",
		"on_the_air": "/tv/on_the_air",
	},
}

// Chart fetches one official TMDB chart. limit caps the result (single
// page — twenty items — is the natural chart size anyway).
func (t *TMDB) Chart(ctx context.Context, chart, kind string, limit int) ([]ListItem, error) {
	if kind == "movies" {
		kind = "movie"
	}
	if kind == "shows" {
		kind = "show"
	}
	path := tmdbChartPaths[kind][chart]
	if path == "" {
		return nil, fmt.Errorf("unknown TMDB chart %q for %s", chart, kind)
	}
	var body struct {
		Results []struct {
			Adult        bool   `json:"adult"`
			ID           int    `json:"id"`
			Title        string `json:"title"` // movies
			Name         string `json:"name"`  // shows
			ReleaseDate  string `json:"release_date"`
			FirstAirDate string `json:"first_air_date"`
			MediaType    string `json:"media_type"` // trending only
		} `json:"results"`
	}
	if err := t.get(ctx, path, url.Values{}, &body); err != nil {
		return nil, err
	}
	var out []ListItem
	for _, r := range body.Results {
		if r.Adult {
			continue
		}
		item := ListItem{Kind: kind, TmdbID: r.ID}
		if kind == "movie" {
			item.Title, item.Year = r.Title, yearOf(r.ReleaseDate)
		} else {
			item.Title, item.Year = r.Name, yearOf(r.FirstAirDate)
		}
		if item.Title == "" {
			continue
		}
		out = append(out, item)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

// ListItems fetches a public TMDB list by id (the number in its URL).
func (t *TMDB) ListItems(ctx context.Context, listID int, limit int) ([]ListItem, error) {
	var body struct {
		Items []struct {
			Adult        bool   `json:"adult"`
			ID           int    `json:"id"`
			MediaType    string `json:"media_type"`
			Title        string `json:"title"`
			Name         string `json:"name"`
			ReleaseDate  string `json:"release_date"`
			FirstAirDate string `json:"first_air_date"`
		} `json:"items"`
	}
	if err := t.get(ctx, "/list/"+strconv.Itoa(listID), url.Values{}, &body); err != nil {
		return nil, err
	}
	var out []ListItem
	for _, r := range body.Items {
		// a watched list carries whatever its author put in it
		if r.Adult {
			continue
		}
		var item ListItem
		switch r.MediaType {
		case "movie":
			item = ListItem{Kind: "movie", Title: r.Title, Year: yearOf(r.ReleaseDate), TmdbID: r.ID}
		case "tv":
			item = ListItem{Kind: "show", Title: r.Name, Year: yearOf(r.FirstAirDate), TmdbID: r.ID}
		default:
			continue
		}
		out = append(out, item)
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}
