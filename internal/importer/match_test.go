package importer

import (
	"testing"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/metadata"
)

// A scan must never invent a show. Taking the provider's top hit
// unconditionally turned a folder called "Monster" into Monster High —
// and because every later scan repeated the match, deleting the wrong
// show didn't get rid of it. Refusing to guess is what stops that loop.
func TestBestMatchRefusesToGuess(t *testing.T) {
	hit := func(title string, year int) metadata.SearchResult {
		return metadata.SearchResult{Title: title, Year: year, TmdbID: len(title)}
	}
	cases := []struct {
		name  string
		hits  []metadata.SearchResult
		title string
		year  int
		want  string // "" = must not match
	}{
		{
			name:  "a longer franchise never stands in for a short name",
			hits:  []metadata.SearchResult{hit("Monster High", 2010), hit("Monster Hunter", 2016)},
			title: "Monster",
			want:  "",
		}, {
			name:  "the real show still wins from further down the list",
			hits:  []metadata.SearchResult{hit("Monster High", 2010), hit("Monster", 2023)},
			title: "Monster",
			want:  "Monster",
		}, {
			name:  "an unrelated title sharing a word is refused",
			hits:  []metadata.SearchResult{hit("Little Monsters", 2019)},
			title: "Monster",
			want:  "",
		}, {
			name:  "decoration in the folder name still resolves",
			hits:  []metadata.SearchResult{hit("Daredevil", 2015)},
			title: "Marvel's Daredevil",
			want:  "Daredevil",
		}, {
			name:  "punctuation and casing don't decide a match",
			hits:  []metadata.SearchResult{hit("It's Always Sunny in Philadelphia", 2005)},
			title: "Its Always Sunny In Philadelphia",
			want:  "It's Always Sunny in Philadelphia",
		}, {
			name:  "the year picks between same-named shows",
			hits:  []metadata.SearchResult{hit("The Office", 2001), hit("The Office", 2005)},
			title: "The Office",
			year:  2005,
			want:  "The Office",
		}, {
			name:  "a wrong year vetoes a contained match",
			hits:  []metadata.SearchResult{hit("Daredevil", 2015)},
			title: "Marvel's Daredevil",
			year:  1975,
			want:  "",
		}, {
			// TVDB names same-named shows with a country suffix; the fold
			// must run before the direction rule, or every such title would
			// read as "a longer name that merely starts the same"
			name:  "a TVDB country suffix folds away",
			hits:  []metadata.SearchResult{hit("Kitchen Nightmares (US)", 2007)},
			title: "Kitchen Nightmares",
			want:  "Kitchen Nightmares (US)",
		}, {
			// a revival's folder carries the revival's year; with one
			// continuing series to point at, the exact name still wins
			name:  "an exact name with a disagreeing year is the fallback, not a refusal",
			hits:  []metadata.SearchResult{hit("Kitchen Nightmares (US)", 2007)},
			title: "Kitchen Nightmares",
			year:  2023,
			want:  "Kitchen Nightmares (US)",
		}, {
			// scene-style decoration on the file side resolves the same way
			name:  "a release-style country tag on the query still matches",
			hits:  []metadata.SearchResult{hit("Kitchen Nightmares (US)", 2007)},
			title: "Kitchen Nightmares US",
			want:  "Kitchen Nightmares (US)",
		}, {
			// TVDB titles the 2013 sitcom with a year suffix and the 1949
			// original without; a year-less file must not read that as the
			// original being the only exact match
			name:  "a TVDB year suffix folds away, leaving an ambiguity",
			hits:  []metadata.SearchResult{hit("The Goldbergs", 1949), hit("The Goldbergs (2013)", 2013)},
			title: "The Goldbergs",
			want:  "",
		}, {
			name:  "a year picks the suffixed show",
			hits:  []metadata.SearchResult{hit("The Goldbergs", 1949), hit("The Goldbergs (2013)", 2013)},
			title: "The Goldbergs",
			year:  2013,
			want:  "The Goldbergs (2013)",
		}, {
			name:  "the suffix carries the year when the row has none",
			hits:  []metadata.SearchResult{hit("The Goldbergs", 1949), hit("The Goldbergs (2013)", 0)},
			title: "The Goldbergs",
			year:  2013,
			want:  "The Goldbergs (2013)",
		}, {
			name:  "same name, no year, provider order does not decide",
			hits:  []metadata.SearchResult{hit("The Studio", 2020), hit("The Studio", 2025)},
			title: "The Studio",
			want:  "",
		}, {
			name:  "nothing at all is not a match",
			hits:  nil,
			title: "Monster",
			want:  "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, nearest := bestMatch(c.hits, c.title, c.year)
			if c.want == "" {
				if got != nil {
					t.Fatalf("matched %q — a wrong show is worse than none", got.Title)
				}
				if len(c.hits) > 0 && nearest == "" {
					t.Error("a refusal should name what it turned down")
				}
				return
			}
			if got == nil {
				t.Fatalf("no match, want %q", c.want)
			}
			if got.Title != c.want {
				t.Fatalf("matched %q, want %q", got.Title, c.want)
			}
		})
	}
}

// The year is a tiebreak, not a requirement: the databases leave it empty
// often enough that demanding it would strand real matches.
func TestBestMatchYearIsATiebreakNotARequirement(t *testing.T) {
	hits := []metadata.SearchResult{{Title: "Severance", Year: 0, TmdbID: 1}}
	got, _ := bestMatch(hits, "Severance", 2022)
	if got == nil {
		t.Fatal("an exact name with an unknown year should still match")
	}
}

// The shows a library already holds answer a year-less name before any
// provider is asked: the library knows which "The Goldbergs" the user has.
func TestOwnedShow(t *testing.T) {
	owned := []catalog.ShowName{
		{ID: 1, Title: "The Goldbergs (2013)", Year: 2013},
		{ID: 2, Title: "Severance", Year: 2022},
		{ID: 3, Title: "The Office (US)", Year: 2005},
		{ID: 4, Title: "The Office (UK)", Year: 2001},
	}
	cases := []struct {
		title string
		year  int
		want  int64
	}{
		{"The Goldbergs", 0, 1},
		{"The Goldbergs", 2013, 1},
		{"The Goldbergs", 1949, 0}, // a different year is a different show
		{"severance", 0, 2},
		{"The Office", 0, 0}, // two held under one name: not this function's call
		{"The Office", 2001, 4},
		{"Monster", 0, 0},
	}
	for _, c := range cases {
		if got := ownedShow(owned, c.title, c.year); got != c.want {
			t.Errorf("ownedShow(%q, %d) = %d, want %d", c.title, c.year, got, c.want)
		}
	}
}
