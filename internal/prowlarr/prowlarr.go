// Package prowlarr talks to a Prowlarr instance — the indexer aggregator.
// One search call fans out to every indexer Prowlarr knows and comes back
// as JSON, so reely needs no per-indexer Torznab plumbing.
package prowlarr

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Newznab-style category roots: movies and TV. Sub-categories (2040 HD,
// 5030 SD, …) all live under these.
var (
	MovieCats = []int{2000}
	TVCats    = []int{5000}
)

type Client struct {
	// url and key read live from settings, so pointing reely at a Prowlarr
	// takes effect without a restart.
	url    func() string
	key    func() string
	client *http.Client
}

func New(url, key func() string) *Client {
	// searches fan out to every indexer Prowlarr has, and slow ones answer
	// last — give the aggregate call room
	return &Client{url: url, key: key, client: &http.Client{Timeout: 90 * time.Second}}
}

// Configured reports whether both a URL and an API key are present.
func (c *Client) Configured() bool { return c.url() != "" && c.key() != "" }

// Release is one search result from any indexer.
type Release struct {
	GUID        string `json:"guid"`
	Title       string `json:"title"`
	Size        int64  `json:"size"`
	Indexer     string `json:"indexer"`
	IndexerID   int    `json:"indexerId"`
	Protocol    string `json:"protocol"` // usenet | torrent
	PublishDate string `json:"publishDate"`
	DownloadURL string `json:"downloadUrl"`
	// MagnetURL is how a torrent indexer hands over a release when it
	// publishes no .torrent file to fetch. Prowlarr reports the two
	// separately and plenty of torrent indexers fill in only this one,
	// so a release carrying just a magnet had an empty DownloadURL all
	// the way to the browser — where the Grab button renders only when
	// there is a link, and so was never drawn at all.
	MagnetURL string `json:"magnetUrl"`
	Seeders   int    `json:"seeders"`
	// External ids the indexer attached to the posting, when its backend
	// maps releases to databases. 0 = not reported. IMDb comes back as the
	// bare number ("tt1375666" → 1375666).
	ImdbID     int `json:"imdbId"`
	TmdbID     int `json:"tmdbId"`
	TvdbID     int `json:"tvdbId"`
	Categories []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	} `json:"categories"`
}

// adultCategory is the newznab XXX root. Searches only ever ask for movies
// (2000) and TV (5000), so nothing in this range should come back — but
// indexers misfile, and "we asked nicely" is not a filter. Anything
// carrying an XXX category is dropped whatever else it claims to be.
const adultCategory = 6000

func (r Release) adult() bool {
	for _, c := range r.Categories {
		if c.ID >= adultCategory && c.ID < adultCategory+1000 {
			return true
		}
	}
	return false
}

// Search runs one query across all of Prowlarr's indexers, scoped to the
// given newznab categories.
func (c *Client) Search(ctx context.Context, query string, categories []int) ([]Release, error) {
	return c.search(ctx, "search", query, categories, 0, 0)
}

// SearchIDs runs an id-keyed query: Prowlarr parses {tvdbid:...},
// {imdbid:...}, {season:...}, {episode:...} tokens out of the query for
// tvsearch/movie types and forwards them as real newznab id parameters to
// the indexers whose caps support them — the same keys Sonarr and Radarr
// search by. searchType is "tvsearch" or "movie"; token-only queries leave
// no text fallback, which is deliberate: the text query runs separately.
func (c *Client) SearchIDs(ctx context.Context, searchType, query string, categories []int) ([]Release, error) {
	return c.search(ctx, searchType, query, categories, 0, 0)
}

// SearchPage is Search with explicit paging, for walking back through a
// feed: offset skips results, limit caps the page (0 leaves Prowlarr's
// default in place). Prowlarr forwards both to each indexer's newznab
// query, so an indexer that ignores offset just repeats its first page —
// callers must tolerate that.
func (c *Client) SearchPage(ctx context.Context, query string, categories []int, offset, limit int) ([]Release, error) {
	return c.search(ctx, "search", query, categories, offset, limit)
}

func (c *Client) search(ctx context.Context, searchType, query string, categories []int, offset, limit int) ([]Release, error) {
	base := strings.TrimRight(c.url(), "/")
	key := c.key()
	if base == "" || key == "" {
		return nil, errors.New("no Prowlarr URL or API key configured — add them in Settings")
	}
	q := url.Values{"query": {query}, "type": {searchType}}
	for _, cat := range categories {
		q.Add("categories", strconv.Itoa(cat))
	}
	if offset > 0 {
		q.Set("offset", strconv.Itoa(offset))
	}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/api/v1/search?"+q.Encode(), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Api-Key", key)
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("reaching Prowlarr: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errors.New("the Prowlarr API key was rejected")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("searching Prowlarr: %s", resp.Status)
	}
	var raw []Release
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("searching Prowlarr: %w", err)
	}
	// filtered here rather than at each caller: this is the single door
	// every indexer result comes through — manual search, auto search and
	// the RSS sweep alike — so a new caller inherits the filter
	out := make([]Release, 0, len(raw))
	for _, rel := range raw {
		if rel.adult() {
			continue
		}
		// One link, chosen here at the single door rather than by each
		// caller. A magnet is a complete way to hand over a torrent —
		// GrabRequest.Validate accepts one and qBittorrent reads the
		// hash straight out of it — so it stands in wherever the
		// indexer published no .torrent URL.
		if rel.DownloadURL == "" {
			rel.DownloadURL = rel.MagnetURL
		}
		out = append(out, rel)
	}
	return out, nil
}
