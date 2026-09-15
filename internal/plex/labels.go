package plex

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Writing the two things that split a library per person: labels on
// items, and the restriction on a share.
//
// They live in different places and behave differently, both established
// against a live server rather than assumed:
//
//   - A label goes on the Plex Media Server, because only it knows what
//     titles exist. Adding one MERGES — labels already on the item stay —
//     and removal takes a separate "tag-" parameter.
//   - A restriction goes to plex.tv, on the friend record rather than the
//     shared-server record, and REPLACES the whole string.
//
// Neither endpoint reports failure the way an API normally would. Both
// answer 200 to a payload they then ignore — a shared_servers update
// carrying sharing_settings is accepted and does nothing, and so is one
// whose body it cannot parse, which silently defaults the fields left
// out. So nothing here believes a status code: every write is read back,
// and a write that did not land is an error.

// Item is one title as Plex holds it, with whatever external ids the
// server knows. Both are carried because a show sourced from TheTVDB may
// have no TMDB id at all, and matching on TMDB alone would skip it.
type Item struct {
	RatingKey int64
	Title     string
	TmdbID    int
	TvdbID    int
	// ImdbID is the third id, and the one worth carrying: an item Plex
	// matched through an agent that supplied no TMDB id has this and
	// nothing else.
	ImdbID string
	Labels []string
}

type itemsDoc struct {
	XMLName xml.Name   `xml:"MediaContainer"`
	Videos  []itemNode `xml:"Video"`
	Dirs    []itemNode `xml:"Directory"`
}

type itemNode struct {
	RatingKey string `xml:"ratingKey,attr"`
	Title     string `xml:"title,attr"`
	// Index and ParentIndex are how an episode says which one it is:
	// its own number and its season's. Both are absent on a movie.
	Index       int         `xml:"index,attr"`
	ParentIndex int         `xml:"parentIndex,attr"`
	Guids       []guidNode  `xml:"Guid"`
	Labels      []labelNode `xml:"Label"`
	Media       []mediaNode `xml:"Media"`
}

type guidNode struct {
	ID string `xml:"id,attr"`
}

type labelNode struct {
	Tag string `xml:"tag,attr"`
}

// Items lists a section's titles with their external ids and labels.
//
// includeGuids is what makes this worth doing in one sweep rather than
// per title: without it the ids are absent and every item would need its
// own round trip.
func (c *Client) Items(ctx context.Context, serverURL, token, sectionID string) ([]Item, error) {
	q := url.Values{"includeGuids": {"1"}}
	var doc itemsDoc
	if err := c.serverXML(ctx, http.MethodGet, serverURL,
		"/library/sections/"+url.PathEscape(sectionID)+"/all", q, token, &doc); err != nil {
		return nil, fmt.Errorf("plex items: %w", err)
	}
	nodes := append(append([]itemNode{}, doc.Videos...), doc.Dirs...)
	out := make([]Item, 0, len(nodes))
	for _, n := range nodes {
		key, err := strconv.ParseInt(n.RatingKey, 10, 64)
		if err != nil {
			continue // no rating key is nothing we can write to
		}
		it := Item{RatingKey: key, Title: n.Title}
		for _, g := range n.Guids {
			switch {
			case strings.HasPrefix(g.ID, "tmdb://"):
				it.TmdbID, _ = strconv.Atoi(strings.TrimPrefix(g.ID, "tmdb://"))
			case strings.HasPrefix(g.ID, "tvdb://"):
				it.TvdbID, _ = strconv.Atoi(strings.TrimPrefix(g.ID, "tvdb://"))
			case strings.HasPrefix(g.ID, "imdb://"):
				it.ImdbID = strings.TrimPrefix(g.ID, "imdb://")
			}
		}
		for _, l := range n.Labels {
			if tag := strings.TrimSpace(l.Tag); tag != "" {
				it.Labels = append(it.Labels, tag)
			}
		}
		out = append(out, it)
	}
	return out, nil
}

// ItemLabels reads back the labels on one title. Every label write is
// verified with this rather than trusted, because the write endpoints
// answer 200 whether or not they did anything.
func (c *Client) ItemLabels(ctx context.Context, serverURL, token string, ratingKey int64) ([]string, error) {
	var doc itemsDoc
	if err := c.serverXML(ctx, http.MethodGet, serverURL,
		"/library/metadata/"+strconv.FormatInt(ratingKey, 10), nil, token, &doc); err != nil {
		return nil, fmt.Errorf("plex labels: %w", err)
	}
	// the metadata endpoint answers with exactly the one item, as a
	// Video for a movie and a Directory for a show
	nodes := append(append([]itemNode{}, doc.Videos...), doc.Dirs...)
	if len(nodes) == 0 {
		return nil, fmt.Errorf("plex labels: no such item %d", ratingKey)
	}
	n := nodes[0]
	out := make([]string, 0, len(n.Labels))
	for _, l := range n.Labels {
		if tag := strings.TrimSpace(l.Tag); tag != "" {
			out = append(out, tag)
		}
	}
	return out, nil
}

// itemType is Plex's numeric kind, which the edit endpoint wants rather
// than the word used everywhere else.
func itemType(kind string) (string, error) {
	switch kind {
	case "movie":
		return "1", nil
	case "show":
		return "2", nil
	}
	return "", fmt.Errorf("plex: kind must be movie or show, got %q", kind)
}

// SetLabels brings one item's labels to want, leaving every label the
// keep function rejects exactly as it found it.
//
// The keep function is the whole reason somebody's hand-made tags
// survive. A label absent from want means two different things — "revoke
// this" and "not mine to touch" — and nothing on the item distinguishes
// them, so ownership has to be decided by the caller and applied here.
// have is what the item already carries, when the caller knows: a
// section listing returns every item's labels in one request, so passing
// them saves a GET per title — which on a library of thousands is the
// difference between a pass costing two requests and thousands. Pass nil
// to have it read.
func (c *Client) SetLabels(ctx context.Context, serverURL, token, kind, sectionID string,
	ratingKey int64, want, have []string, owned func(string) bool) error {

	typ, err := itemType(kind)
	if err != nil {
		return err
	}
	if have == nil {
		var err error
		if have, err = c.ItemLabels(ctx, serverURL, token, ratingKey); err != nil {
			return err
		}
	}

	// Plex title-cases tags on write, so "reely.jolene_family" reads back
	// as "Reely.jolene_family" — every comparison here folds case, or the
	// pass would re-add what it just wrote, forever.
	lower := func(ss []string) map[string]string {
		m := map[string]string{}
		for _, s := range ss {
			m[strings.ToLower(s)] = s
		}
		return m
	}
	hav, wnt := lower(have), lower(want)

	var add, remove []string
	for k, v := range wnt {
		if _, ok := hav[k]; !ok {
			add = append(add, v)
		}
	}
	for k, v := range hav {
		if _, ok := wnt[k]; !ok && owned(v) {
			remove = append(remove, v)
		}
	}
	if len(add) == 0 && len(remove) == 0 {
		return nil
	}

	base := "/library/sections/" + url.PathEscape(sectionID) + "/all"
	if len(add) > 0 {
		q := url.Values{"type": {typ}, "id": {strconv.FormatInt(ratingKey, 10)}}
		for i, l := range add {
			q.Set(fmt.Sprintf("label[%d].tag.tag", i), l)
		}
		// locked stops Plex's agent overwriting the field on its next
		// metadata refresh, which would silently drop every label reely
		// put there
		q.Set("label.locked", "1")
		if err := c.serverXML(ctx, http.MethodPut, serverURL, base, q, token, nil); err != nil {
			return fmt.Errorf("plex add labels: %w", err)
		}
	}
	for _, l := range remove {
		q := url.Values{"type": {typ}, "id": {strconv.FormatInt(ratingKey, 10)}}
		q.Set("label[].tag.tag-", l)
		q.Set("label.locked", "1")
		if err := c.serverXML(ctx, http.MethodPut, serverURL, base, q, token, nil); err != nil {
			return fmt.Errorf("plex remove label: %w", err)
		}
	}

	// 200 is not evidence. Read it back.
	after, err := c.ItemLabels(ctx, serverURL, token, ratingKey)
	if err != nil {
		return err
	}
	got := lower(after)
	for k := range wnt {
		if _, ok := got[k]; !ok {
			return fmt.Errorf("plex: label %q did not stick on %d", wnt[k], ratingKey)
		}
	}
	for _, l := range remove {
		if _, ok := got[strings.ToLower(l)]; ok {
			return fmt.Errorf("plex: label %q would not come off %d", l, ratingKey)
		}
	}
	return nil
}

// Scan asks Plex to look at one folder now instead of at its own
// leisure. Without it a title reely just placed may not exist to label
// for however long the server's next sweep is away.
func (c *Client) Scan(ctx context.Context, serverURL, token, sectionID, dir string) error {
	q := url.Values{}
	if dir != "" {
		q.Set("path", dir)
	}
	if err := c.serverXML(ctx, http.MethodGet, serverURL,
		"/library/sections/"+url.PathEscape(sectionID)+"/refresh", q, token, nil); err != nil {
		return fmt.Errorf("plex scan: %w", err)
	}
	return nil
}

// SetRestrictions writes what one shared account may see, per kind.
//
// This goes to the friend record. The shared-server record is where the
// restriction is READ from, and it accepts a sharing_settings block on
// update that it then ignores — a 200 that changes nothing. So the write
// goes here and is verified by the caller re-reading the sharing list.
//
// An empty string is refused rather than sent. Plex reads it as no
// restriction, which does not show that person nothing — it shows them
// the entire library.
func (c *Client) SetRestrictions(ctx context.Context, token string, accountID int64, movies, shows string) error {
	if strings.TrimSpace(movies) == "" || strings.TrimSpace(shows) == "" {
		return fmt.Errorf("plex: an empty restriction would share everything")
	}
	q := url.Values{"filterMovies": {movies}, "filterTelevision": {shows}}
	path := "/api/friends/" + strconv.FormatInt(accountID, 10) + "?" + q.Encode()
	if err := c.do(ctx, http.MethodPut, path, token, nil); err != nil {
		return fmt.Errorf("plex restrictions: %w", err)
	}
	return nil
}

// serverXML talks to the media server rather than to plex.tv. out may be
// nil for the calls whose body says nothing worth reading.
func (c *Client) serverXML(ctx context.Context, method, serverURL, path string,
	q url.Values, token string, out any) error {

	base := strings.TrimRight(strings.TrimSpace(serverURL), "/")
	if base == "" {
		return fmt.Errorf("no Plex server address configured")
	}
	u := base + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("X-Plex-Product", product)
	req.Header.Set("X-Plex-Client-Identifier", c.ClientID)
	req.Header.Set("X-Plex-Token", token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return statusErr{code: resp.StatusCode, body: strings.TrimSpace(string(snippet))}
	}
	if out == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		return nil
	}
	return xml.NewDecoder(resp.Body).Decode(out)
}

// ── streams ────────────────────────────────────────────────────────────

// Plex probes every file it scans and already knows what is inside it.
// Asking it is cheaper than opening the file again: no ffprobe, no
// container parsing, no second answer to disagree with the first.
type mediaNode struct {
	Parts []partNode `xml:"Part"`
}

type partNode struct {
	File    string       `xml:"file,attr"`
	Streams []streamNode `xml:"Stream"`
}

type streamNode struct {
	// StreamType is Plex's: 1 video, 2 audio, 3 subtitle.
	StreamType   int    `xml:"streamType,attr"`
	Codec        string `xml:"codec,attr"`
	Language     string `xml:"language,attr"`
	LanguageCode string `xml:"languageCode,attr"`
	Title        string `xml:"title,attr"`
	DisplayTitle string `xml:"displayTitle,attr"`
	Channels     int    `xml:"channels,attr"`
	Height       int    `xml:"height,attr"`
	Width        int    `xml:"width,attr"`
	Forced       int    `xml:"forced,attr"`
	Default      int    `xml:"default,attr"`
	// HearingImpaired marks an SDH track — the same language as the
	// plain one beside it, and not what somebody reaching for subtitles
	// usually wants offered first.
	HearingImpaired int `xml:"hearingImpaired,attr"`
	// External marks a subtitle sitting beside the file rather than in
	// it, which is the difference between one reely could serve as text
	// and one that would have to be burned in.
	External int `xml:"external,attr"`
}

// Stream is one track inside a file, as the media server reports it.
type Stream struct {
	// Kind is video, audio or subtitle. Anything else Plex invents later
	// is dropped rather than guessed at.
	Kind     string `json:"kind"`
	Codec    string `json:"codec"`
	Language string `json:"language,omitempty"`
	Title    string `json:"title,omitempty"`
	Channels int    `json:"channels,omitempty"`
	Width    int    `json:"width,omitempty"`
	Height   int    `json:"height,omitempty"`
	Forced   bool   `json:"forced,omitempty"`
	Default  bool   `json:"default,omitempty"`
	SDH      bool   `json:"sdh,omitempty"`
	External bool   `json:"external,omitempty"`
}

var streamKinds = map[int]string{1: "video", 2: "audio", 3: "subtitle"}

// ItemStreams lists the tracks in one item's file.
//
// The same metadata endpoint the labels come from — Plex nests
// Media → Part → Stream under the item — so this costs one request and
// no file access at all.
//
// An item with no Stream children is not an error. A server that answers
// without them leaves the caller showing nothing, which is the right
// outcome for a thing that is extra information rather than the point.
func (c *Client) ItemStreams(ctx context.Context, serverURL, token string, ratingKey int64) ([]Stream, error) {
	var doc itemsDoc
	if err := c.serverXML(ctx, http.MethodGet, serverURL,
		"/library/metadata/"+strconv.FormatInt(ratingKey, 10), nil, token, &doc); err != nil {
		return nil, fmt.Errorf("plex streams: %w", err)
	}
	nodes := append(append([]itemNode{}, doc.Videos...), doc.Dirs...)
	if len(nodes) == 0 {
		return nil, fmt.Errorf("plex streams: no such item %d", ratingKey)
	}
	return streamsOf(nodes[0]), nil
}

// EpisodeStreams lists the tracks in one episode's file.
//
// It goes through the show because that is the only rating key reely
// keeps: plex_items caches a section's top level, so a show is one row
// and its episodes are none. allLeaves answers with every episode under
// the show at once, which is how the wanted one is found.
//
// Finding it is not the same as describing it. A Plex LISTING — allLeaves,
// children, a section's /all — carries each item's Media and Part but
// leaves the Stream children out; they come back only from the
// single-item endpoint. So the listing supplies the episode's own rating
// key and that key is asked the same question a movie is asked, by the
// same code. Two round trips, and the second is the one that has been
// checked against a live server.
//
// The inline case is still honoured first. Nothing is lost if a server
// does send tracks in a listing, and this stops the second call being
// made for nothing.
//
// An episode Plex does not hold is not an error — the show is there and
// this one is simply missing from it, which is a normal state for a
// library still filling in.
func (c *Client) EpisodeStreams(ctx context.Context, serverURL, token string,
	showRatingKey int64, season, episode int) ([]Stream, error) {
	var doc itemsDoc
	if err := c.serverXML(ctx, http.MethodGet, serverURL,
		"/library/metadata/"+strconv.FormatInt(showRatingKey, 10)+"/allLeaves",
		nil, token, &doc); err != nil {
		return nil, fmt.Errorf("plex episode streams: %w", err)
	}
	for _, n := range doc.Videos {
		if n.ParentIndex != season || n.Index != episode {
			continue
		}
		if inline := streamsOf(n); len(inline) > 0 {
			return inline, nil
		}
		key, err := strconv.ParseInt(n.RatingKey, 10, 64)
		if err != nil || key <= 0 {
			return nil, fmt.Errorf("plex episode streams: episode S%02dE%02d has no rating key",
				season, episode)
		}
		return c.ItemStreams(ctx, serverURL, token, key)
	}
	return []Stream{}, nil
}

// streamsOf flattens an item's Media → Part → Stream nesting.
//
// Multiple parts are a split file rather than two versions of it, so
// their tracks belong to the same list.
func streamsOf(n itemNode) []Stream {
	out := []Stream{}
	for _, m := range n.Media {
		for _, p := range m.Parts {
			for _, s := range p.Streams {
				kind, ok := streamKinds[s.StreamType]
				if !ok {
					continue
				}
				title := strings.TrimSpace(s.Title)
				if title == "" {
					title = strings.TrimSpace(s.DisplayTitle)
				}
				out = append(out, Stream{
					Kind: kind, Codec: strings.TrimSpace(s.Codec),
					Language: strings.TrimSpace(s.Language), Title: title,
					Channels: s.Channels, Width: s.Width, Height: s.Height,
					Forced: s.Forced == 1, Default: s.Default == 1,
					SDH: s.HearingImpaired == 1, External: s.External == 1,
				})
			}
		}
	}
	return out
}
