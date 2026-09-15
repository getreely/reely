// Package plex signs people in with their Plex account.
//
// The flow is Plex's PIN OAuth, which suits a self-hosted server: reely
// asks plex.tv for a PIN, sends the person to plex.tv to claim it, and
// polls until it comes back carrying a token. Nothing here ever sees a
// password, and reely never asks for one.
//
// Every call is JSON — plex.tv answers XML by default, and every request
// below sets Accept explicitly rather than parsing two shapes.
package plex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	apiBase  = "https://plex.tv"
	authBase = "https://app.plex.tv/auth#?"
	// product is what a person sees on plex.tv's "sign in to…" screen and
	// in their authorized-devices list afterwards
	product = "reely"
)

// ErrPending is a PIN nobody has claimed yet — the ordinary answer while
// the person is still on plex.tv, not a failure.
var ErrPending = errors.New("waiting for the Plex sign-in to be approved")

// ErrExpired is a PIN that ran out. Thirty minutes, per plex.tv.
var ErrExpired = errors.New("that Plex sign-in expired — start again")

// Client talks to plex.tv on behalf of one install.
//
// ClientID identifies this install to plex.tv and must be stable: a PIN
// is bound to the identifier that created it, and polling with a
// different one is a 404. It is generated once and stored, so a restart
// mid-sign-in doesn't strand the person on a PIN nothing can claim.
type Client struct {
	ClientID string
	HTTP     *http.Client
	base     string // overridden by tests
}

func New(clientID string) *Client {
	return &Client{
		ClientID: clientID,
		HTTP:     &http.Client{Timeout: 20 * time.Second},
		base:     apiBase,
	}
}

// SetBaseURL points the client at a fake plex.tv. Tests only.
func (c *Client) SetBaseURL(u string) { c.base = strings.TrimRight(u, "/") }

// PIN is a sign-in in progress: the person opens URL, and the caller
// polls Token with the ID until it answers.
type PIN struct {
	ID   int64  `json:"id"`
	Code string `json:"code"`
	URL  string `json:"url"`
	// ExpiresIn is seconds from now, so a UI can say how long is left
	// without the caller doing clock arithmetic against plex.tv's.
	ExpiresIn int `json:"expiresIn"`
}

// NewPIN starts a sign-in.
func (c *Client) NewPIN(ctx context.Context) (*PIN, error) {
	var out struct {
		ID        int64  `json:"id"`
		Code      string `json:"code"`
		ExpiresIn int    `json:"expiresIn"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v2/pins?strong=true", "", &out); err != nil {
		return nil, err
	}
	if out.ID == 0 || out.Code == "" {
		return nil, errors.New("plex.tv returned a PIN with nothing in it")
	}
	return &PIN{ID: out.ID, Code: out.Code, ExpiresIn: out.ExpiresIn, URL: c.authURL(out.Code)}, nil
}

// authURL is where the person goes to approve the sign-in. The values
// after the fragment are read by plex.tv's own page, so they are encoded
// as a query string and appended to it.
func (c *Client) authURL(code string) string {
	q := url.Values{}
	q.Set("clientID", c.ClientID)
	q.Set("code", code)
	q.Set("context[device][product]", product)
	return authBase + q.Encode()
}

// Token polls a PIN. ErrPending until the person approves it on plex.tv,
// ErrExpired once it has run out.
func (c *Client) Token(ctx context.Context, pinID int64) (string, error) {
	var out struct {
		AuthToken *string `json:"authToken"`
		ExpiresIn int     `json:"expiresIn"`
	}
	err := c.do(ctx, http.MethodGet, fmt.Sprintf("/api/v2/pins/%d", pinID), "", &out)
	if err != nil {
		// plex.tv drops an expired PIN entirely, and answers 404 for one
		// created under a different client identifier — indistinguishable
		// from here, and the honest answer to both is to start again
		var se statusErr
		if errors.As(err, &se) && se.code == http.StatusNotFound {
			return "", ErrExpired
		}
		return "", err
	}
	if out.AuthToken == nil || *out.AuthToken == "" {
		if out.ExpiresIn <= 0 {
			return "", ErrExpired
		}
		return "", ErrPending
	}
	return *out.AuthToken, nil
}

// Account is who a token belongs to.
//
// ID is the only thing an account is ever matched on. Emails and
// usernames change hands; matching on one would hand somebody's library
// access to whoever registered that address at Plex next.
type Account struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
	Thumb    string `json:"thumb"`
}

// Account resolves the account a token belongs to.
func (c *Client) Account(ctx context.Context, token string) (*Account, error) {
	var a Account
	if err := c.do(ctx, http.MethodGet, "/api/v2/user", token, &a); err != nil {
		return nil, err
	}
	if a.ID == 0 {
		return nil, errors.New("plex.tv returned an account with no id")
	}
	return &a, nil
}

// Server is one Plex Media Server an account can reach.
type Server struct {
	Name string `json:"name"`
	// MachineID is the server's permanent identifier — the one the
	// sharing endpoints are addressed by.
	MachineID string `json:"machineId"`
	Owned     bool   `json:"owned"`
}

// Servers lists the servers a token can reach, owned ones included. The
// owner links their account once, and this is how reely learns which
// machine id to ask about sharing — rather than making somebody find it
// in a URL and paste it.
func (c *Client) Servers(ctx context.Context, token string) ([]Server, error) {
	var out []struct {
		Name             string `json:"name"`
		ClientIdentifier string `json:"clientIdentifier"`
		Provides         string `json:"provides"`
		Owned            bool   `json:"owned"`
	}
	if err := c.do(ctx, http.MethodGet, "/api/v2/resources", token, &out); err != nil {
		return nil, err
	}
	var servers []Server
	for _, d := range out {
		// a device provides several things at once ("server,player"), so
		// this is a membership test rather than an equality one
		for _, p := range strings.Split(d.Provides, ",") {
			if strings.TrimSpace(p) == "server" {
				servers = append(servers, Server{
					Name: d.Name, MachineID: d.ClientIdentifier, Owned: d.Owned})
				break
			}
		}
	}
	return servers, nil
}

// statusErr carries plex.tv's status code so callers can tell a missing
// PIN from a refused token.
type statusErr struct {
	code int
	body string
}

func (e statusErr) Error() string {
	if e.body != "" {
		return fmt.Sprintf("plex.tv: %s: %s", http.StatusText(e.code), e.body)
	}
	return "plex.tv: " + http.StatusText(e.code)
}

// do performs one request. token is the account being acted for, empty
// for the calls that need none; out may be nil for the writes whose
// reply says nothing worth reading — some of plex.tv answers those in
// XML regardless of what the Accept header asked for.
func (c *Client) do(ctx context.Context, method, path, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, nil)
	if err != nil {
		return err
	}
	// plex.tv answers XML unless asked otherwise, and identifies the
	// caller by these three headers — without the client identifier a PIN
	// cannot be polled back
	req.Header.Set("Accept", "application/json")
	req.Header.Set("X-Plex-Product", product)
	req.Header.Set("X-Plex-Client-Identifier", c.ClientID)
	if token != "" {
		req.Header.Set("X-Plex-Token", token)
	}
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
	return json.NewDecoder(resp.Body).Decode(out)
}
