// Package trakt reads public Trakt data: anyone's shared lists and the
// official charts. All of it needs only a client id (a free API app at
// trakt.tv/oauth/applications) — no OAuth, no user login. Private data
// (personal watchlists) would need the device-code flow; not here yet.
package trakt

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const traktBase = "https://api.trakt.tv"

type Client struct {
	// clientID reads live from settings, so pasting one takes effect
	// without a restart.
	clientID func() string
	base     string
	client   *http.Client
}

func New(clientID func() string) *Client {
	return &Client{clientID: clientID, base: traktBase, client: &http.Client{Timeout: 30 * time.Second}}
}

// SetBaseURL points the client at a test double; production never calls it.
func (c *Client) SetBaseURL(u string) { c.base = u }

// Configured reports whether a client id is present.
func (c *Client) Configured() bool { return c.clientID() != "" }

// Item is one list entry, reduced to what the sync needs. Trakt carries
// TMDB ids for everything, which is what lets the rest of reely take over.
type Item struct {
	Kind   string // movie | show
	Title  string
	Year   int
	TmdbID int
}

// traktIDs / traktTitle are the shared shape inside every Trakt payload.
type traktEntity struct {
	Title string `json:"title"`
	Year  int    `json:"year"`
	IDs   struct {
		Tmdb int `json:"tmdb"`
	} `json:"ids"`
}

// ListItems fetches a public list's entries: /users/{user}/lists/{slug}/items.
func (c *Client) ListItems(ctx context.Context, user, slug string) ([]Item, error) {
	var body []struct {
		Type  string       `json:"type"`
		Movie *traktEntity `json:"movie"`
		Show  *traktEntity `json:"show"`
	}
	path := fmt.Sprintf("/users/%s/lists/%s/items", user, slug)
	if err := c.get(ctx, path, &body); err != nil {
		return nil, err
	}
	var out []Item
	for _, row := range body {
		switch {
		case row.Type == "movie" && row.Movie != nil:
			out = append(out, Item{Kind: "movie", Title: row.Movie.Title, Year: row.Movie.Year, TmdbID: row.Movie.IDs.Tmdb})
		case row.Type == "show" && row.Show != nil:
			out = append(out, Item{Kind: "show", Title: row.Show.Title, Year: row.Show.Year, TmdbID: row.Show.IDs.Tmdb})
		}
	}
	return out, nil
}

// Chart fetches an official chart: trending or popular, movies or shows.
func (c *Client) Chart(ctx context.Context, chart, kind string, limit int) ([]Item, error) {
	if chart != "trending" && chart != "popular" {
		return nil, fmt.Errorf("unknown Trakt chart %q", chart)
	}
	media := "movies"
	itemKind := "movie"
	if kind == "show" || kind == "shows" {
		media = "shows"
		itemKind = "show"
	}
	if limit <= 0 {
		limit = 20
	}
	path := fmt.Sprintf("/%s/%s?limit=%d", media, chart, limit)

	// trending wraps the entity ({watchers, movie:{…}}); popular is bare
	if chart == "trending" {
		var body []struct {
			Movie *traktEntity `json:"movie"`
			Show  *traktEntity `json:"show"`
		}
		if err := c.get(ctx, path, &body); err != nil {
			return nil, err
		}
		var out []Item
		for _, row := range body {
			e := row.Movie
			if e == nil {
				e = row.Show
			}
			if e != nil {
				out = append(out, Item{Kind: itemKind, Title: e.Title, Year: e.Year, TmdbID: e.IDs.Tmdb})
			}
		}
		return out, nil
	}
	var body []traktEntity
	if err := c.get(ctx, path, &body); err != nil {
		return nil, err
	}
	var out []Item
	for _, e := range body {
		out = append(out, Item{Kind: itemKind, Title: e.Title, Year: e.Year, TmdbID: e.IDs.Tmdb})
	}
	return out, nil
}

func (c *Client) get(ctx context.Context, path string, into any) error {
	id := c.clientID()
	if id == "" {
		return errors.New("no Trakt client id configured — add one in Settings")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("trakt-api-version", "2")
	req.Header.Set("trakt-api-key", id)
	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("reaching Trakt: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return errors.New("the Trakt client id was rejected")
	}
	if resp.StatusCode == http.StatusNotFound {
		return errors.New("no such Trakt list — check the user and list name")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("calling Trakt %s: %s", path, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(into); err != nil {
		return fmt.Errorf("reading the Trakt response: %w", err)
	}
	return nil
}
