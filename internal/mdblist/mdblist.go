// Package mdblist reads lists from mdblist.com — the aggregator that
// mirrors IMDb charts, Trakt lists, and user-built lists as clean JSON.
// A free api key (mdblist.com → Preferences → API Access) is all it
// needs, which makes it the no-VIP road to IMDb Top 250 territory.
package mdblist

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const (
	apiBase  = "https://api.mdblist.com" // id-addressed lists
	siteBase = "https://mdblist.com"     // user/slug-addressed lists
)

// Client is stateless beyond its hosts: the api key arrives per call,
// because every reely user may bring their own.
type Client struct {
	apiBase  string
	siteBase string
	client   *http.Client
}

func New() *Client {
	return &Client{apiBase: apiBase, siteBase: siteBase, client: &http.Client{Timeout: 30 * time.Second}}
}

// SetBaseURL points both hosts at a test double; production never calls it.
func (c *Client) SetBaseURL(u string) { c.apiBase, c.siteBase = u, u }

// Item is one list entry, reduced to what the sync needs. mdblist's item
// "id" is the TMDB id.
type Item struct {
	Kind   string // movie | show
	Title  string
	Year   int
	TmdbID int
}

// rawItem is mdblist's item shape (flat array on the id API, and inside
// the movies/shows arrays some list endpoints return instead).
type rawItem struct {
	ID          int    `json:"id"` // TMDB id
	Title       string `json:"title"`
	Mediatype   string `json:"mediatype"`
	ReleaseYear int    `json:"release_year"`
}

// ListItems fetches one list with the given api key. Address the list
// either by numeric id (the new API) or by user + slug (the classic list
// URLs) — exactly one applies.
func (c *Client) ListItems(ctx context.Context, key string, listID int, user, slug string, limit int) ([]Item, error) {
	if key == "" {
		return nil, errors.New("no mdblist api key — add yours in Settings, or ask the admin to set the install-wide one")
	}
	var url string
	if listID > 0 {
		url = fmt.Sprintf("%s/lists/%d/items?apikey=%s", c.apiBase, listID, key)
	} else if user != "" && slug != "" {
		url = fmt.Sprintf("%s/api/lists/%s/%s/items?apikey=%s", c.siteBase, user, slug, key)
	} else {
		return nil, errors.New("an mdblist list needs an id or a user and list name")
	}
	if limit > 0 {
		url += fmt.Sprintf("&limit=%d", limit)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reaching mdblist: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, errors.New("the mdblist api key was rejected")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("calling mdblist: %s", resp.Status)
	}

	// two response shapes exist in the wild: a flat item array, and an
	// object with movies/shows arrays — plus {"error": …} on bad keys,
	// which arrives with status 200 on the classic endpoint
	var raw json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("reading the mdblist response: %w", err)
	}
	var flat []rawItem
	if err := json.Unmarshal(raw, &flat); err == nil {
		return convert(flat, limit), nil
	}
	var wrapped struct {
		Movies []rawItem `json:"movies"`
		Shows  []rawItem `json:"shows"`
		Error  string    `json:"error"`
	}
	if err := json.Unmarshal(raw, &wrapped); err != nil {
		return nil, fmt.Errorf("reading the mdblist response: %w", err)
	}
	if wrapped.Error != "" {
		return nil, fmt.Errorf("mdblist: %s", wrapped.Error)
	}
	return convert(append(wrapped.Movies, wrapped.Shows...), limit), nil
}

func convert(rows []rawItem, limit int) []Item {
	var out []Item
	for _, r := range rows {
		kind := ""
		switch r.Mediatype {
		case "movie":
			kind = "movie"
		case "show", "tv":
			kind = "show"
		default:
			continue
		}
		out = append(out, Item{Kind: kind, Title: r.Title, Year: r.ReleaseYear, TmdbID: r.ID})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}
