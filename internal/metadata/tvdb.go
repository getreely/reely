package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TVDB is the optional show-metadata source. Release groups follow TVDB's
// numbering, and TVDB keeps revivals as one continuing series where TMDB
// splits them — so an install that supplies a TVDB API key gets its shows
// from here, while movies (and discovery, cast, and everything
// cross-media) stay on TMDB. The client speaks v4 and produces the same
// ShowDetail the TMDB client does, so everything downstream is
// provider-blind.
//
// Attribution: metadata from TheTVDB (thetvdb.com). The Settings page and
// README carry the required notice.

const tvdbBase = "https://api4.thetvdb.com/v4"

// tvdbTokenTTL is how long a login token is reused. TVDB tokens live for
// about a month; refreshing daily keeps a healthy margin without logging
// in on every call.
const tvdbTokenTTL = 24 * time.Hour

type TVDB struct {
	// key returns the current API key on every call, so a key saved in
	// Settings takes effect without a restart.
	key    func() string
	base   string
	client *http.Client

	mu      sync.Mutex
	token   string
	tokenAt time.Time
	tokenBy string // the key the token was minted for — a changed key re-logs in
}

func NewTVDB(key func() string) *TVDB {
	return &TVDB{key: key, base: tvdbBase, client: &http.Client{Timeout: 15 * time.Second}}
}

// SetBaseURL points the client somewhere else — tests aim it at a local
// fake; production never calls this.
func (t *TVDB) SetBaseURL(u string) { t.base = u }

// Configured reports whether an API key is present — the switch that
// routes shows through TVDB at all.
func (t *TVDB) Configured() bool { return t.key() != "" }

// login trades the API key for a bearer token, caching it.
func (t *TVDB) login(ctx context.Context) (string, error) {
	key := t.key()
	if key == "" {
		return "", fmt.Errorf("no TVDB API key configured")
	}
	t.mu.Lock()
	if t.token != "" && t.tokenBy == key && time.Since(t.tokenAt) < tvdbTokenTTL {
		tok := t.token
		t.mu.Unlock()
		return tok, nil
	}
	t.mu.Unlock()

	body, _ := json.Marshal(map[string]string{"apikey": key})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.base+"/login", strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("TVDB login: %s — check the API key in Settings", resp.Status)
	}
	var out struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	if out.Data.Token == "" {
		return "", fmt.Errorf("TVDB login answered without a token")
	}
	t.mu.Lock()
	t.token, t.tokenAt, t.tokenBy = out.Data.Token, time.Now(), key
	t.mu.Unlock()
	return out.Data.Token, nil
}

// expireToken drops the cached token so the next call logs in again — the
// 401 path, for a token revoked or expired early.
func (t *TVDB) expireToken() {
	t.mu.Lock()
	t.token = ""
	t.mu.Unlock()
}

// get performs one authenticated GET, re-logging in once on a 401.
func (t *TVDB) get(ctx context.Context, path string, query url.Values, v any) error {
	for attempt := 0; ; attempt++ {
		tok, err := t.login(ctx)
		if err != nil {
			return err
		}
		u := t.base + path
		if len(query) > 0 {
			u += "?" + query.Encode()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+tok)
		resp, err := t.client.Do(req)
		if err != nil {
			return err
		}
		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			resp.Body.Close()
			t.expireToken()
			continue
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("TVDB %s: %s", path, resp.Status)
		}
		return json.NewDecoder(resp.Body).Decode(v)
	}
}

// SearchShows searches series by name. Results carry the TVDB id (and
// absolute artwork URLs — TVDB serves full URLs, unlike TMDB's paths).
func (t *TVDB) SearchShows(ctx context.Context, q string) ([]SearchResult, error) {
	var out struct {
		Data []struct {
			TvdbID   string `json:"tvdb_id"`
			Name     string `json:"name"`
			Year     string `json:"year"`
			Overview string `json:"overview"`
			ImageURL string `json:"image_url"`
			Country  string `json:"country"`
		} `json:"data"`
	}
	query := url.Values{"query": {q}, "type": {"series"}, "limit": {"20"}}
	if err := t.get(ctx, "/search", query, &out); err != nil {
		return nil, err
	}
	results := make([]SearchResult, 0, len(out.Data))
	for _, d := range out.Data {
		id, _ := strconv.Atoi(d.TvdbID)
		if id == 0 {
			continue
		}
		year, _ := strconv.Atoi(d.Year)
		results = append(results, SearchResult{
			TvdbID: id, Kind: "show", Title: d.Name, Year: year,
			Overview: d.Overview, Poster: d.ImageURL,
		})
	}
	return results, nil
}

// Show fetches one series: identity, status, genres, remote ids, and the
// full episode tree in default (aired) order. Season 0 (specials) is
// skipped, matching the TMDB path. Cast is left empty — people stay a
// TMDB concern, joined through the remote TMDB id (EnrichShowFromTMDB).
func (t *TVDB) Show(ctx context.Context, id int) (*ShowDetail, error) {
	var head struct {
		Data struct {
			Name       string `json:"name"`
			Image      string `json:"image"`
			FirstAired string `json:"firstAired"`
			Year       string `json:"year"`
			Status     struct {
				Name string `json:"name"`
			} `json:"status"`
			Genres []struct {
				Name string `json:"name"`
			} `json:"genres"`
			Aliases []struct {
				Language string `json:"language"`
				Name     string `json:"name"`
			} `json:"aliases"`
			Seasons []struct {
				Number int    `json:"number"`
				Name   string `json:"name"`
				Type   struct {
					Type string `json:"type"`
				} `json:"type"`
			} `json:"seasons"`
			RemoteIDs []struct {
				ID         string `json:"id"`
				SourceName string `json:"sourceName"`
			} `json:"remoteIds"`
			Translations struct {
				OverviewTranslations []struct {
					Language string `json:"language"`
					Overview string `json:"overview"`
				} `json:"overviewTranslations"`
			} `json:"translations"`
			Artworks []tvdbArtwork `json:"artworks"`
		} `json:"data"`
	}
	if err := t.get(ctx, "/series/"+strconv.Itoa(id)+"/extended", url.Values{"meta": {"translations"}}, &head); err != nil {
		return nil, err
	}
	d := &ShowDetail{TvdbID: id, Title: head.Data.Name, Status: head.Data.Status.Name}
	// data.image is whatever TVDB's contributors ranked first, in any
	// language — which is how an English-language show ended up wearing a
	// Spanish cover while the overview beside it was picked as English on
	// purpose. Prefer a poster that says it is English; failing that keep
	// the ranked one, but mark it so the TMDB pass may replace it.
	if art := englishPoster(head.Data.Artworks); art != "" {
		d.Poster = art
	} else {
		d.Poster, d.PosterLocalised = head.Data.Image, head.Data.Image != ""
	}
	d.Year, _ = strconv.Atoi(head.Data.Year)
	if d.Year == 0 && len(head.Data.FirstAired) >= 4 {
		d.Year, _ = strconv.Atoi(head.Data.FirstAired[:4])
	}
	for _, g := range head.Data.Genres {
		d.Genres = append(d.Genres, g.Name)
	}
	// aliases in every language, deduped: release names borrow them freely
	// (an anthology's seasons each release under their own subtitle), and
	// a foreign-script alias that never matches anything costs nothing
	seen := map[string]bool{head.Data.Name: true}
	for _, a := range head.Data.Aliases {
		name := strings.TrimSpace(a.Name)
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		d.Aliases = append(d.Aliases, name)
	}
	for _, r := range head.Data.RemoteIDs {
		switch r.SourceName {
		case "TheMovieDB.com":
			d.TmdbID, _ = strconv.Atoi(r.ID)
		case "IMDB":
			d.ImdbID = r.ID
		}
	}
	for _, o := range head.Data.Translations.OverviewTranslations {
		if o.Language == "eng" {
			d.Overview = o.Overview
			break
		}
	}

	// real season names, where TVDB has them: an anthology names each
	// season after its story ("The Lyle and Erik Menendez Story"), and
	// matching uses those names to place subtitle-named releases
	seasonNames := map[int]string{}
	for _, se := range head.Data.Seasons {
		if se.Type.Type == "official" && strings.TrimSpace(se.Name) != "" {
			seasonNames[se.Number] = strings.TrimSpace(se.Name)
		}
	}

	// the episode tree, paged; grouped into seasons in one pass since the
	// default order arrives season-sorted
	seasons := map[int]*SeasonDetail{}
	var order []int
	for page := 0; ; page++ {
		var out struct {
			Data struct {
				Episodes []struct {
					ID           int    `json:"id"`
					SeasonNumber int    `json:"seasonNumber"`
					Number       int    `json:"number"`
					Name         string `json:"name"`
					Overview     string `json:"overview"`
					Aired        string `json:"aired"`
					Runtime      int    `json:"runtime"`
				} `json:"episodes"`
			} `json:"data"`
			Links struct {
				Next string `json:"next"`
			} `json:"links"`
		}
		q := url.Values{"page": {strconv.Itoa(page)}}
		if err := t.get(ctx, "/series/"+strconv.Itoa(id)+"/episodes/default", q, &out); err != nil {
			return nil, err
		}
		for _, e := range out.Data.Episodes {
			if e.SeasonNumber == 0 {
				continue // specials, same as the TMDB path
			}
			se, ok := seasons[e.SeasonNumber]
			if !ok {
				name := seasonNames[e.SeasonNumber]
				if name == "" {
					name = fmt.Sprintf("Season %d", e.SeasonNumber)
				}
				se = &SeasonDetail{Number: e.SeasonNumber, Name: name}
				seasons[e.SeasonNumber] = se
				order = append(order, e.SeasonNumber)
			}
			se.Episodes = append(se.Episodes, EpisodeDetail{
				TmdbID: e.ID, // the source's own episode id; informational
				Season: e.SeasonNumber, Episode: e.Number,
				Title: e.Name, Overview: e.Overview, AirDate: e.Aired, Runtime: e.Runtime,
			})
		}
		if out.Links.Next == "" {
			break
		}
	}
	for _, n := range order {
		d.Seasons = append(d.Seasons, *seasons[n])
	}
	return d, nil
}

// tvdbArtwork is one entry of a series' artwork list.
type tvdbArtwork struct {
	Image    string  `json:"image"`
	Language string  `json:"language"`
	Type     int     `json:"type"`
	Score    float64 `json:"score"`
	Width    int     `json:"width"`
	Height   int     `json:"height"`
}

// seriesPosterArtwork is TVDB's artwork type for a series poster.
const seriesPosterArtwork = 2

// englishPoster is the best English poster in an artwork list, or "".
//
// Highest score wins, which is the same ranking TVDB itself applies —
// this only narrows the field to artwork that says it is English first.
//
// The portrait check is not decoration. Everything else here rests on
// seriesPosterArtwork naming what it claims to, and if that id ever
// meant a banner instead, the fallbacks below would quietly be replaced
// by a wide image in a poster's slot. A shape that cannot be a poster is
// refused, so being wrong about the id degrades to keeping the old
// behaviour rather than to a broken page. Artwork that reports no
// dimensions is taken at its word.
func englishPoster(artworks []tvdbArtwork) string {
	best, bestScore := "", -1.0
	for _, a := range artworks {
		if a.Image == "" || a.Type != seriesPosterArtwork || a.Language != "eng" {
			continue
		}
		if a.Width > 0 && a.Height > 0 && a.Height <= a.Width {
			continue
		}
		if a.Score > bestScore {
			best, bestScore = a.Image, a.Score
		}
	}
	return best
}

// EnrichShowFromTMDB fills the pieces TVDB doesn't own — cast (people are
// keyed by TMDB ids everywhere) and any missing artwork or overview —
// from the TMDB record the series' remote id points at. Best-effort: a
// show TMDB doesn't carry simply stays as TVDB described it.
func EnrichShowFromTMDB(ctx context.Context, tmdb *TMDB, d *ShowDetail) {
	if d.TmdbID == 0 || tmdb == nil || !tmdb.Configured() {
		return
	}
	full, err := tmdb.Show(ctx, d.TmdbID)
	if err != nil {
		return
	}
	d.Cast = full.Cast
	if d.Backdrop == "" {
		d.Backdrop = full.Backdrop
	}
	// an empty poster, or one that is only "whatever was ranked first" —
	// TMDB's is the show's primary art in English, which is what the
	// localised one was standing in for
	if (d.Poster == "" || d.PosterLocalised) && full.Poster != "" {
		d.Poster, d.PosterLocalised = full.Poster, false
	}
	if d.Overview == "" {
		d.Overview = full.Overview
	}
	if d.ImdbID == "" {
		d.ImdbID = full.ImdbID
	}
}
