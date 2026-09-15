package plex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

// The sharing list decides two things: who may sign in at all, and which
// of the server's libraries each person reaches. Both are read from a
// fixture shaped like plex.tv's answer, whose values are invented but
// whose flag combinations are the ones that actually occur.

func fixtureServer(t *testing.T) *Client {
	t.Helper()
	body, err := os.ReadFile("testdata/shared_servers.xml")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Plex-Token") == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	c := New("reely-test-install")
	c.SetBaseURL(srv.URL)
	return c
}

func shares(t *testing.T) []Share {
	t.Helper()
	got, err := fixtureServer(t).SharedServers(context.Background(), "owner-token", "srv-1111")
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// A row with no account id can grant nothing and cannot be matched at
// sign-in, so it is dropped rather than half-trusted.
func TestSharesWithoutAnAccountIDAreDropped(t *testing.T) {
	got := shares(t)
	if len(got) != 3 {
		t.Fatalf("shares = %d, want the three with an account id", len(got))
	}
	for _, s := range got {
		if s.AccountID == 0 {
			t.Errorf("kept a share with no account: %+v", s)
		}
	}
}

// allLibraries is a standing grant, not a description of the current
// one — and it arrives alongside a full Section list, so it cannot be
// inferred from the sections and has to be read.
func TestAllLibrariesReachesEverythingIncludingSectionsNotListed(t *testing.T) {
	ana := shares(t)[0]
	if !ana.AllLibraries || len(ana.Sections) != 2 {
		t.Fatalf("ana = %+v, want the standing grant with sections still listed", ana)
	}
	for _, key := range []string{"1", "2", "77"} {
		if !ana.Allows(key) {
			t.Errorf("a standing grant did not reach section %q", key)
		}
	}
}

// Every section the server has is listed whether or not it is shared, so
// the flag decides and the element's presence means nothing.
func TestPartialShareIsDecidedByTheSectionFlag(t *testing.T) {
	ben := shares(t)[1]
	if ben.AllLibraries {
		t.Fatalf("ben = %+v, want a partial share", ben)
	}
	if !ben.Allows("1") {
		t.Error("a shared section was refused")
	}
	if ben.Allows("2") {
		t.Error("an unshared section was allowed — the element being present is not the grant")
	}
	if ben.Allows("77") {
		t.Error("a section that isn't in the list was allowed")
	}
	if got := ben.SharedSections(); len(got) != 1 || got[0].Key != "1" {
		t.Errorf("shared sections = %+v, want just the one", got)
	}
}

// An invitation nobody took up is listed but reaches nothing. That
// person must not be able to sign in either — they have no access to the
// server, so they get none to reely.
func TestUnacceptedInviteReachesNothing(t *testing.T) {
	cass := shares(t)[2]
	if cass.Accepted {
		t.Fatalf("cass = %+v, want an unaccepted invite", cass)
	}
	// generous everywhere else, and still nothing
	if !cass.AllLibraries || len(cass.Sections) != 2 {
		t.Fatalf("the fixture stopped covering the case: %+v", cass)
	}
	if cass.Allows("1") || len(cass.SharedSections()) != 0 {
		t.Error("an unaccepted invite granted access")
	}
}

// The section key is what joins to the server's own /library/sections,
// where the folder paths live. The plex.tv-side id joins to nothing.
func TestSectionsCarryTheServerSideKey(t *testing.T) {
	secs := shares(t)[0].Sections
	if secs[0].Key != "1" || secs[1].Key != "2" {
		t.Fatalf("keys = %q, %q, want the server's own section keys", secs[0].Key, secs[1].Key)
	}
	if secs[0].Type != "movie" || secs[1].Type != "show" {
		t.Errorf("types = %q, %q", secs[0].Type, secs[1].Type)
	}
	if secs[0].ID == 0 {
		t.Error("the plex.tv-side id was dropped")
	}
}

// The owner has to have chosen a server first; asking about none is a
// clear refusal rather than a request to a malformed URL.
func TestSharedServersNeedsAMachineID(t *testing.T) {
	if _, err := fixtureServer(t).SharedServers(context.Background(), "t", "  "); err == nil {
		t.Fatal("no machine id was accepted")
	}
}
