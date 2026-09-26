package metadata

import (
	"reflect"
	"testing"
)

func titles(rs []SearchResult) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Title
		if r.Kind == "show" {
			out[i] += " (show)"
		}
	}
	return out
}

// The search that prompted this, as TMDB and TVDB answer it. Sorting on
// popularity alone put the sequels above the film actually called Rush
// Hour, and put the TV series — no popularity on a TVDB row, so zero —
// below everything, making-of documentaries included.
func TestRankSearchPutsTheTitleFirst(t *testing.T) {
	movies := []SearchResult{ // TMDB's own relevance order
		{Kind: "movie", Title: "Rush Hour", Year: 1998, Popularity: 18},
		{Kind: "movie", Title: "Rush Hour 2", Year: 2001, Popularity: 21},
		{Kind: "movie", Title: "Rush Hour 3", Year: 2007, Popularity: 25},
		{Kind: "movie", Title: "Rush Hour", Year: 2024, Popularity: 2},
		{Kind: "movie", Title: "A Piece of the Action: Behind the Scenes of Rush Hour", Year: 1999, Popularity: 3},
		{Kind: "movie", Title: "Making Rush Hour 3", Year: 2007, Popularity: 1},
	}
	shows := []SearchResult{ // TVDB: no popularity
		{Kind: "show", Title: "Rush Hour", Year: 2016},
	}

	got := titles(RankSearch("rush hour", movies, shows))
	want := []string{
		"Rush Hour",
		"Rush Hour (show)",
		"Rush Hour",
		"Rush Hour 2",
		"Rush Hour 3",
		"A Piece of the Action: Behind the Scenes of Rush Hour",
		"Making Rush Hour 3",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("order:\n got %q\nwant %q", got, want)
	}
}

// A zero popularity must never sink a show. Only the title and each
// provider's own order decide, so a show that answers the query better
// than every movie comes first.
func TestRankSearchDoesNotSinkShowsWithoutPopularity(t *testing.T) {
	movies := []SearchResult{
		{Kind: "movie", Title: "Severance Pay", Popularity: 90},
	}
	shows := []SearchResult{
		{Kind: "show", Title: "Severance"},
	}
	got := titles(RankSearch("severance", movies, shows))
	if got[0] != "Severance (show)" {
		t.Errorf("order = %q, want the exact match first", got)
	}
}

// Matching is on words, not on case, punctuation, or partial words.
func TestMatchTier(t *testing.T) {
	for _, tc := range []struct {
		q, title string
		want     int
	}{
		{"rush hour", "Rush Hour", 0},
		{"Rush  Hour!", "rush hour", 0},
		{"rush hour", "Rush Hour: The Series", 1},
		{"rush hour", "Rush Hour 2", 1},
		{"rush hour", "Making Rush Hour 3", 2},
		{"rush hour", "Rush Hours", 3}, // not the word
		{"rush hour", "Hora punta", 3}, // matched on an alias
		{"", "Anything", 3},
	} {
		if got := matchTier(normTitle(tc.q), tc.title); got != tc.want {
			t.Errorf("matchTier(%q, %q) = %d, want %d", tc.q, tc.title, got, tc.want)
		}
	}
}
