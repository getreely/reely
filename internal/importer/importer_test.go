package importer

import (
	"testing"

	"github.com/getreely/reely/internal/metadata"
)

func TestBestMatch(t *testing.T) {
	hits := []metadata.SearchResult{
		{TmdbID: 1, Title: "Dune", Year: 1984, Popularity: 20},
		{TmdbID: 2, Title: "Dune: Part Two", Year: 2024, Popularity: 90},
		{TmdbID: 3, Title: "Dune", Year: 2021, Popularity: 80},
	}

	// exact title + year
	if m, _ := bestMatch(hits, "Dune: Part Two", 2024); m == nil || m.TmdbID != 2 {
		t.Errorf("exact match failed: %+v", m)
	}
	// exact title, disambiguated by year
	if m, _ := bestMatch(hits, "Dune", 2021); m == nil || m.TmdbID != 3 {
		t.Errorf("year disambiguation failed: %+v", m)
	}
	// title match, no year given → first name-equal hit
	if m, _ := bestMatch(hits, "Dune", 0); m == nil || m.Title != "Dune" {
		t.Errorf("titled match failed: %+v", m)
	}
	// no name match → no match. This case used to assert the opposite, and
	// that fallback is exactly how a folder named "Monster" became Monster
	// High: TMDB orders by relevance, so its top hit for an unrecognized
	// name is confidently wrong rather than absent.
	if m, nearest := bestMatch(hits, "Arrakis", 0); m != nil {
		t.Errorf("guessed %+v for a name nothing matched", m)
	} else if nearest == "" {
		t.Error("a refusal should name what it turned down")
	}
	// a short name must not be answered by a longer one that starts the same
	if m, _ := bestMatch(hits, "Dune", 0); m != nil && m.Title == "Dune: Part Two" {
		t.Error("a longer franchise title stood in for the shorter show")
	}
	// empty results
	if m, _ := bestMatch(nil, "Anything", 0); m != nil {
		t.Errorf("empty should be nil, got %+v", m)
	}
}

func TestMatchKey(t *testing.T) {
	if matchKey("  The Bear ", 2022) != matchKey("the bear", 2022) {
		t.Error("matchKey should normalize case and whitespace")
	}
	if matchKey("Dune", 2021) == matchKey("Dune", 1984) {
		t.Error("matchKey must distinguish years")
	}
}
