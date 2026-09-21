package qbittorrent

import (
	"context"
	"strings"
	"testing"
)

// Retiring a finished torrent means moving it to a category reely does
// not sweep — and on a fresh install nothing has ever created that
// category, because nothing but this does.
//
// qBittorrent answers 409 for a category it has never heard of, so
// without the create the move never happens. That failure is silent and
// it does not stay small: the torrent stays in the swept category and
// is imported again on every pass, forever, or files a fresh "failed to
// import" once its title has been deleted from the catalog.
func TestSetCategoryCreatesOneQbitHasNeverHeardOf(t *testing.T) {
	q := &stubQB{torrents: "[]"}
	c := testClient(t, q)
	signIn(t, c)

	if err := c.SetCategory(context.Background(), "abc123", "reely-seeding"); err != nil {
		t.Fatalf("SetCategory: %v", err)
	}
	if n := q.called("/api/v2/torrents/createCategory"); n != 1 {
		t.Errorf("createCategory called %d times, want 1", n)
	}
	if got := q.formFor("/api/v2/torrents/createCategory").Get("category"); got != "reely-seeding" {
		t.Errorf("created category %q, want %q", got, "reely-seeding")
	}
	// and the move actually happened, rather than the create standing in
	// for it
	if n := q.called("/api/v2/torrents/setCategory"); n != 2 {
		t.Errorf("setCategory called %d times, want 2 (refused, then retried)", n)
	}
}

// The create is a repair, not a step. A category that already exists
// costs nothing extra — which matters because retiring happens on every
// completed torrent, forever.
func TestSetCategoryLeavesAnExistingCategoryAlone(t *testing.T) {
	q := &stubQB{torrents: "[]", categories: map[string]bool{"reely-seeding": true}}
	c := testClient(t, q)
	signIn(t, c)

	if err := c.SetCategory(context.Background(), "abc123", "reely-seeding"); err != nil {
		t.Fatalf("SetCategory: %v", err)
	}
	if n := q.called("/api/v2/torrents/createCategory"); n != 0 {
		t.Errorf("createCategory called %d times, want 0", n)
	}
	if n := q.called("/api/v2/torrents/setCategory"); n != 1 {
		t.Errorf("setCategory called %d times, want 1", n)
	}
}

// A refusal that creating the category does not fix surfaces as the
// refusal itself — "no such category" reads better than whatever
// createCategory said about a category that was there all along.
func TestSetCategoryReportsTheRefusalItCouldNotFix(t *testing.T) {
	q := &stubQB{torrents: "[]", missing: map[string]bool{
		"/api/v2/torrents/createCategory": true,
	}}
	c := testClient(t, q)

	err := c.SetCategory(context.Background(), "abc123", "reely-seeding")
	if err == nil {
		t.Fatal("SetCategory succeeded against a qBittorrent that refused both calls")
	}
	if want := "setCategory"; !strings.Contains(err.Error(), want) {
		t.Errorf("error %q does not name %s", err, want)
	}
}

// signIn spends the sign-in dance up front, so a call count below is
// the calls the code under test made rather than those plus a 403 and
// its retry.
func signIn(t *testing.T, c *Client) {
	t.Helper()
	if _, err := c.Version(context.Background()); err != nil {
		t.Fatalf("sign in: %v", err)
	}
}
