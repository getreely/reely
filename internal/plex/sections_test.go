package plex

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func sectionsClient(t *testing.T) (*Client, string) {
	t.Helper()
	body, err := os.ReadFile("testdata/sections.xml")
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/library/sections" {
			t.Errorf("asked the server for %s", r.URL.Path)
		}
		if r.Header.Get("X-Plex-Token") == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/xml")
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return New("reely-test-install"), srv.URL
}

func libs(t *testing.T) []Library {
	t.Helper()
	c, addr := sectionsClient(t)
	got, err := c.Sections(context.Background(), addr, "owner-token")
	if err != nil {
		t.Fatal(err)
	}
	return got
}

// Location is a child element and repeats, so a library can point at
// more than one folder. Reading only the first would silently fail to
// match a library through its second mount.
func TestSectionsReadEveryLocation(t *testing.T) {
	got := libs(t)
	if len(got) != 3 {
		t.Fatalf("sections = %d, want 3", len(got))
	}
	if got[0].Key != "1" || got[0].Type != "movie" || len(got[0].Paths) != 1 {
		t.Fatalf("first = %+v", got[0])
	}
	if len(got[2].Paths) != 2 {
		t.Fatalf("a two-folder library read %d paths: %+v", len(got[2].Paths), got[2])
	}
	if !got[2].Hidden {
		t.Error("hidden was not read")
	}
}

// The happy case: Plex and reely see the same paths.
func TestMatchPairsIdenticalPaths(t *testing.T) {
	m := Match(libs(t), []Target{
		{ID: 7, Path: "/srv/library/films", Kind: "movies"},
		{ID: 8, Path: "/srv/library/series/", Kind: "shows"},
	})
	if m["1"] != 7 || m["2"] != 8 {
		t.Fatalf("match = %v, want 1→7 and 2→8", m)
	}
}

// The ordinary Docker case: the same folder mounted at two different
// paths. An exact-only match would find nothing at all here.
func TestMatchPairsThroughDifferentMounts(t *testing.T) {
	m := Match(libs(t), []Target{
		{ID: 7, Path: "/data/library/films", Kind: "movies"},
		{ID: 8, Path: "/data/library/series", Kind: "shows"},
	})
	if m["1"] != 7 || m["2"] != 8 {
		t.Fatalf("match = %v, want both paired through their tails", m)
	}
}

// A film section must never land on a show library, however the paths
// happen to read.
func TestMatchNeverCrossesKinds(t *testing.T) {
	m := Match(libs(t), []Target{{ID: 9, Path: "/srv/library/films", Kind: "shows"}})
	if len(m) != 0 {
		t.Fatalf("match = %v, want a film section left unpaired", m)
	}
}

// Two equally good candidates are left for a person to decide. A wrong
// guess here sends one person's requests into another person's library,
// which is worse than asking.
func TestAmbiguousMatchesAreLeftAlone(t *testing.T) {
	m := Match(libs(t), []Target{
		{ID: 1, Path: "/tank/a/films", Kind: "movies"},
		{ID: 2, Path: "/tank/b/films", Kind: "movies"},
	})
	if _, paired := m["1"]; paired {
		t.Fatalf("match = %v, want the ambiguous section left out", m)
	}
}

// One reely library cannot take two Plex sections.
func TestATargetIsClaimedOnlyOnce(t *testing.T) {
	m := Match(libs(t), []Target{{ID: 5, Path: "/srv/library/films", Kind: "movies"}})
	if m["1"] != 5 {
		t.Fatalf("match = %v, want the exact pairing", m)
	}
	if _, also := m["3"]; also {
		t.Errorf("the documentaries section took a claimed library: %v", m)
	}
}

// A shared trailing segment has to be more than the last one: nearly
// every film library ends in the same word, and pairing on that alone
// would cross two people's separate folders.
func TestOneSharedSegmentIsNotAMatch(t *testing.T) {
	m := Match(
		[]Library{{Key: "1", Type: "movie", Paths: []string{"/srv/ana/films"}}},
		[]Target{{ID: 3, Path: "/tank/ben/films", Kind: "movies"}},
	)
	if len(m) != 0 {
		t.Fatalf("match = %v, want no pairing on one shared segment", m)
	}
}
