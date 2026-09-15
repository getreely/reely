package plex

import (
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
)

// The server's own library list, and how it lines up with reely's.
//
// This one is asked of the Plex Media Server directly rather than of
// plex.tv: only the server knows where its libraries point on disk, and
// the folder is the one thing a Plex library and a reely library
// genuinely share. reely runs alongside Plex, so it can reach it.
//
// The sharing list says which section keys a person reaches; this says
// what those keys are and where they live. Together they turn "shared
// with Ana" into "Ana's requests may go to reely library 3".

// Library is one Plex library section as the server describes it.
type Library struct {
	// Key is the section key — "1", "2" — and is what the sharing list's
	// Section.key refers to.
	Key   string
	Title string
	// Type is Plex's kind: "movie" or "show".
	Type string
	// Paths is where the library points. A Plex library may point at
	// several folders, so this is a list and matching considers all of
	// them.
	Paths  []string
	Hidden bool
}

type sectionsDoc struct {
	XMLName     xml.Name        `xml:"MediaContainer"`
	Directories []directoryNode `xml:"Directory"`
}

type directoryNode struct {
	Key       string         `xml:"key,attr"`
	Title     string         `xml:"title,attr"`
	Type      string         `xml:"type,attr"`
	Hidden    string         `xml:"hidden,attr"`
	Locations []locationNode `xml:"Location"`
}

type locationNode struct {
	Path string `xml:"path,attr"`
}

// Sections lists the server's libraries. serverURL is the Plex Media
// Server itself — "http://192.168.1.10:32400" — not plex.tv.
func (c *Client) Sections(ctx context.Context, serverURL, token string) ([]Library, error) {
	base := strings.TrimRight(strings.TrimSpace(serverURL), "/")
	if base == "" {
		return nil, fmt.Errorf("no Plex server address configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/library/sections", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("X-Plex-Product", product)
	req.Header.Set("X-Plex-Client-Identifier", c.ClientID)
	req.Header.Set("X-Plex-Token", token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		return nil, statusErr{code: resp.StatusCode, body: strings.TrimSpace(string(snippet))}
	}
	var doc sectionsDoc
	if err := xml.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("plex library list: %w", err)
	}
	out := make([]Library, 0, len(doc.Directories))
	for _, d := range doc.Directories {
		lib := Library{Key: d.Key, Title: d.Title, Type: d.Type, Hidden: flag(d.Hidden)}
		for _, l := range d.Locations {
			if p := strings.TrimSpace(l.Path); p != "" {
				lib.Paths = append(lib.Paths, p)
			}
		}
		out = append(out, lib)
	}
	return out, nil
}

// Target is a reely library to match a Plex section against.
type Target struct {
	ID int64
	// Path is the folder reely manages.
	Path string
	// Kind is reely's own word: "movies" or "shows".
	Kind string
}

// Match pairs Plex sections with reely libraries by folder, keyed by
// Plex section key.
//
// Exact paths first, then a match on a shared trailing segment. The
// second pass is not sloppiness: Plex and reely usually run in separate
// containers, so the same directory is commonly mounted at two different
// paths — /srv/library/films to one and /films to the other — and an
// exact-only match would then find nothing at all on an ordinary setup.
//
// A pairing is only made when it is the single candidate. Anything
// ambiguous is left out for somebody to decide, because a wrong guess
// here sends one person's requests into another person's library.
func Match(sections []Library, targets []Target) map[string]int64 {
	out := map[string]int64{}
	taken := map[int64]bool{}

	// exact first, so a setup that mounts things identically is never
	// second-guessed by the looser pass below
	for _, s := range sections {
		for _, t := range targets {
			if taken[t.ID] || !kindsAgree(s.Type, t.Kind) {
				continue
			}
			if _, done := out[s.Key]; done {
				break
			}
			for _, p := range s.Paths {
				if clean(p) == clean(t.Path) {
					out[s.Key], taken[t.ID] = t.ID, true
					break
				}
			}
		}
	}

	for _, s := range sections {
		if _, done := out[s.Key]; done {
			continue
		}
		var only int64
		matches := 0
		for _, t := range targets {
			if taken[t.ID] || !kindsAgree(s.Type, t.Kind) {
				continue
			}
			for _, p := range s.Paths {
				if sharesTail(clean(p), clean(t.Path)) {
					only = t.ID
					matches++
					break
				}
			}
		}
		if matches == 1 {
			out[s.Key], taken[only] = only, true
		}
	}
	return out
}

// kindsAgree keeps a film section from ever landing on a show library.
// Plex says movie/show, reely says movies/shows.
func kindsAgree(plexType, reelyKind string) bool {
	switch plexType {
	case "movie":
		return reelyKind == "movies"
	case "show":
		return reelyKind == "shows"
	}
	return false
}

func clean(p string) string {
	return strings.TrimRight(path.Clean(strings.TrimSpace(p)), "/")
}

// sharesTail reports whether two paths end in the same segments — the
// same folder reached through different mounts. The last segment alone
// is not enough: nearly every library ends in "movies", and matching on
// that would pair two people's separate film folders.
func sharesTail(a, b string) bool {
	as, bs := strings.Split(a, "/"), strings.Split(b, "/")
	n := 0
	for n < len(as) && n < len(bs) {
		x, y := as[len(as)-1-n], bs[len(bs)-1-n]
		if x != y || x == "" {
			break
		}
		n++
	}
	// two segments, unless one path IS that short — "/movies" against
	// "/srv/library/films" is as much agreement as either can offer
	need := 2
	if len(as) <= 2 || len(bs) <= 2 {
		need = 1
	}
	return n >= need
}

// ServerName is what the server calls itself — the name its owner gave
// it, which is what a person recognises. Asked of the server rather than
// of plex.tv so it needs no re-link on an install that connected before
// this existed.
//
// Best effort by design: an empty answer or an error means the caller
// says something generic instead. A friendly label is not worth failing
// a health check over.
func (c *Client) ServerName(ctx context.Context, serverURL, token string) string {
	base := strings.TrimRight(strings.TrimSpace(serverURL), "/")
	if base == "" {
		return ""
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/", nil)
	if err != nil {
		return ""
	}
	req.Header.Set("X-Plex-Product", product)
	req.Header.Set("X-Plex-Client-Identifier", c.ClientID)
	req.Header.Set("X-Plex-Token", token)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return ""
	}
	var doc struct {
		XMLName      xml.Name `xml:"MediaContainer"`
		FriendlyName string   `xml:"friendlyName,attr"`
	}
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&doc); err != nil {
		return ""
	}
	return strings.TrimSpace(doc.FriendlyName)
}
