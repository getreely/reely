// Package qbittorrent talks to qBittorrent's WebUI API (v2).
//
// It implements the same download-client surface SABnzbd does, so the
// import pipeline above it does not know or care which one placed a
// file. What differs is underneath, and two differences shape this
// package.
//
// The first is the session. SAB authenticates every call with an API
// key; qBittorrent hands out a cookie and expects it back, so this
// client logs in, keeps the cookie, and logs in again when it is told
// the cookie has gone stale. qBittorrent also rejects a request whose
// Referer does not match its own address — its CSRF guard — so every
// call carries one.
//
// The second is that a torrent does not end. A usenet job finishes and
// leaves a folder; a torrent finishes downloading and then keeps
// seeding, holding the files it just wrote. Nothing here deletes a
// torrent on reely's behalf for that reason: what the import path does
// with a completed torrent is decided above this package, and the
// answer is not the one SAB gets.
package qbittorrent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Client is one qBittorrent WebUI.
type Client struct {
	// read live from settings, so pointing reely at another qBittorrent
	// takes effect without a restart — the same arrangement SAB has
	url  func() string
	user func() string
	pass func() string

	http *http.Client
	// loginMu keeps a burst of calls from racing to sign in. The first
	// one through logs in and the rest use the cookie it got, rather
	// than each opening its own session.
	loginMu sync.Mutex
}

// New builds a client. addr rather than url: this package leans on
// net/url, and a parameter of that name shadows it.
func New(addr, user, pass func() string) *Client {
	// the cookie jar IS the session — qBittorrent's SID comes back as a
	// Set-Cookie and has to ride every later request
	jar, _ := cookiejar.New(nil)
	return &Client{
		url: addr, user: user, pass: pass,
		http: &http.Client{Timeout: 30 * time.Second, Jar: jar},
	}
}

// Configured reports whether there is a qBittorrent to talk to.
//
// Only the URL is required. A qBittorrent with authentication switched
// off for the local subnet is a normal setup, and refusing to talk to
// one because no password was typed would be reely inventing a rule
// qBittorrent does not have.
func (c *Client) Configured() bool { return c.url() != "" }

// base is the WebUI address without a trailing slash.
func (c *Client) base() string { return strings.TrimRight(c.url(), "/") }

// call posts to one WebUI endpoint, signing in first if the session has
// lapsed. The body is returned as text — most endpoints answer "Ok."
// rather than JSON.
//
// A 403 is qBittorrent saying the cookie is no good, which is ordinary:
// sessions expire, and the server restarts. So one 403 buys a login and
// a single retry; a second means the credentials are actually wrong.
func (c *Client) call(ctx context.Context, path string, form url.Values) (string, error) {
	if !c.Configured() {
		return "", errors.New("no qBittorrent URL configured")
	}
	body, status, err := c.post(ctx, path, form)
	if err != nil {
		return "", err
	}
	if status == http.StatusForbidden {
		if err := c.login(ctx); err != nil {
			return "", err
		}
		if body, status, err = c.post(ctx, path, form); err != nil {
			return "", err
		}
	}
	if status != http.StatusOK {
		return "", fmt.Errorf("qBittorrent answered %d to %s: %s",
			status, path, strings.TrimSpace(firstLine(body)))
	}
	return body, nil
}

// post makes one request, without the login dance.
func (c *Client) post(ctx context.Context, path string, form url.Values) (string, int, error) {
	base := c.base()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		base+path, strings.NewReader(form.Encode()))
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	// qBittorrent refuses a request whose Referer is not its own address.
	// It is a CSRF guard aimed at a browser on the same machine, and a
	// request without it is simply rejected.
	req.Header.Set("Referer", base)
	resp, err := c.http.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("reaching qBittorrent: %w", err)
	}
	defer resp.Body.Close()
	// capped: a WebUI answer is a word or a page of JSON, and an
	// unbounded read here would be reely trusting whatever answered
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return "", 0, err
	}
	return string(raw), resp.StatusCode, nil
}

// login exchanges the username and password for the session cookie.
//
// qBittorrent answers 200 with the word "Fails." for a wrong password
// rather than an error status, so the body is what decides.
func (c *Client) login(ctx context.Context) error {
	c.loginMu.Lock()
	defer c.loginMu.Unlock()
	body, status, err := c.post(ctx, "/api/v2/auth/login", url.Values{
		"username": {c.user()}, "password": {c.pass()},
	})
	if err != nil {
		return err
	}
	if status == http.StatusForbidden {
		return errors.New("qBittorrent refused the sign-in: too many failed attempts, and this address is banned for now")
	}
	if status != http.StatusOK || !strings.HasPrefix(strings.TrimSpace(body), "Ok.") {
		return errors.New("qBittorrent refused the username or password")
	}
	return nil
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// ratio limit sentinels, which are qBittorrent's own.
//
// They matter because 0 is a real answer here and not an empty one: a
// ratio limit of 0 means stop the moment the download finishes, having
// uploaded nothing, which is a thing somebody may genuinely want. So
// "no limit" and "whatever the global setting says" get their own
// values rather than sharing zero's.
const (
	RatioUnlimited = -1 // seed forever
	RatioGlobal    = -2 // defer to qBittorrent's own setting
)
