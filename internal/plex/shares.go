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

// Who the server is shared with, and to which of its libraries.
//
// This is the sign-in gate. A person signs in through the PIN flow,
// plex.tv says which account the token belongs to, and that account id
// has to appear here — matched on the id and nothing else, because
// emails and usernames change hands.
//
// The endpoint is plex.tv's v1 API and answers XML. There is no JSON
// equivalent: /api/v2/shared_servers is POST-only (it creates a share)
// and /api/v2/shared_servers/{id} wants one share's numeric id, so
// neither lists anything.

// Share is one person the server is shared with.
type Share struct {
	// ID is the share record, which is not a person and not stable
	// across re-invites. AccountID is the person.
	ID int64
	// AccountID is plex.tv's account id — the only thing a sign-in is
	// ever matched on.
	AccountID int64
	Username  string
	Email     string

	// AllLibraries is a standing grant rather than a description of the
	// current one: sections that do not exist yet are shared too. Both
	// states occur alongside a full Section list, so this cannot be
	// inferred from the sections and has to be read.
	AllLibraries bool
	// Accepted is false for an invitation nobody has taken up. Such a
	// person is listed here but has no access to anything, so they must
	// not be able to sign in either.
	Accepted bool

	// FilterMovies and FilterShows are the label restrictions, stored as
	// "label=a%2Cb%2Cc" — the labels OR together, and one naming a label
	// no title carries shows nothing rather than everything. Empty means
	// unrestricted, which is why an empty string is never written.
	//
	// They are read here and written elsewhere: an update sent to this
	// endpoint carrying them is accepted and ignored, so SetRestrictions
	// goes to the friend record instead.
	FilterMovies string
	FilterShows  string

	Sections []Section
}

// Section is one Plex library as the sharing record describes it.
type Section struct {
	// Key is the section's key on the server itself — "1", "2" — and is
	// what joins this to the server's own /library/sections, where the
	// folder paths live. ID is a plex.tv-side id and joins to nothing.
	Key   string
	ID    int64
	Title string
	// Type is Plex's kind: "movie" or "show".
	Type string
	// Shared is the per-section grant. Every section the server has is
	// listed whether or not it is shared, so this is the flag that
	// decides, not the presence of the element.
	Shared bool
}

// Allows reports whether this share reaches a section, by the server's
// own section key.
func (s Share) Allows(key string) bool {
	if !s.Accepted {
		return false
	}
	if s.AllLibraries {
		return true
	}
	for _, sec := range s.Sections {
		if sec.Key == key {
			return sec.Shared
		}
	}
	return false
}

// SharedSections is the sections this share actually reaches.
func (s Share) SharedSections() []Section {
	if !s.Accepted {
		return nil
	}
	out := make([]Section, 0, len(s.Sections))
	for _, sec := range s.Sections {
		if s.AllLibraries || sec.Shared {
			out = append(out, sec)
		}
	}
	return out
}

type sharesDoc struct {
	XMLName xml.Name    `xml:"MediaContainer"`
	Shares  []shareNode `xml:"SharedServer"`
}

type shareNode struct {
	ID       int64  `xml:"id,attr"`
	UserID   int64  `xml:"userID,attr"`
	Username string `xml:"username,attr"`
	Email    string `xml:"email,attr"`
	// these three are "0"/"1" and unix seconds, as strings, because an
	// absent attribute and a zero have to stay distinguishable
	AllLibraries string        `xml:"allLibraries,attr"`
	AcceptedAt   string        `xml:"acceptedAt,attr"`
	FilterMovies string        `xml:"filterMovies,attr"`
	FilterTV     string        `xml:"filterTelevision,attr"`
	Sections     []sectionNode `xml:"Section"`
}

type sectionNode struct {
	ID     int64  `xml:"id,attr"`
	Key    string `xml:"key,attr"`
	Title  string `xml:"title,attr"`
	Type   string `xml:"type,attr"`
	Shared string `xml:"shared,attr"`
}

// SharedServers lists who the server is shared with. The token must be
// the owner's — this is the owner asking about their own server.
func (c *Client) SharedServers(ctx context.Context, token, machineID string) ([]Share, error) {
	if strings.TrimSpace(machineID) == "" {
		return nil, fmt.Errorf("no Plex server chosen yet")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/api/servers/%s/shared_servers", c.base, url.PathEscape(machineID)), nil)
	if err != nil {
		return nil, err
	}
	// no Accept: application/json here — this endpoint has no JSON form,
	// and asking for one gets XML anyway
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
	var doc sharesDoc
	if err := xml.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("plex.tv sharing list: %w", err)
	}
	out := make([]Share, 0, len(doc.Shares))
	for _, n := range doc.Shares {
		if n.UserID == 0 {
			// nothing to match a sign-in against; a row like this cannot
			// grant anything, so it is dropped rather than half-trusted
			continue
		}
		s := Share{
			ID: n.ID, AccountID: n.UserID, Username: n.Username, Email: n.Email,
			AllLibraries: flag(n.AllLibraries), Accepted: stamped(n.AcceptedAt),
			FilterMovies: n.FilterMovies, FilterShows: n.FilterTV,
		}
		for _, sn := range n.Sections {
			s.Sections = append(s.Sections, Section{
				Key: sn.Key, ID: sn.ID, Title: sn.Title,
				Type: sn.Type, Shared: flag(sn.Shared),
			})
		}
		out = append(out, s)
	}
	return out, nil
}

// flag reads Plex's "0"/"1" attributes.
func flag(v string) bool { return v == "1" || strings.EqualFold(v, "true") }

// stamped reports whether a unix-seconds attribute holds a real time.
// An invitation nobody has accepted carries an empty or zero one.
func stamped(v string) bool {
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	return err == nil && n > 0
}
