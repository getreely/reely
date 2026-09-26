package metadata

import (
	"sort"
	"strings"
	"unicode"
)

// RankSearch orders one search's movie and show results together, best
// match first.
//
// The two lists come from different places — TMDB for movies, TVDB or
// TMDB for shows — and each arrives in its own relevance order. They
// used to be stuck end to end and then re-sorted by popularity alone,
// which went wrong twice over. Popularity ignores the title: searching
// "rush hour" put Rush Hour 3 and Rush Hour 2 above the film actually
// called Rush Hour. And TVDB search carries no popularity at all, so on
// an install that sources shows from TVDB every show scored zero and
// sank below every movie, however exactly it matched.
//
// So the title decides first, in tiers:
//
//	exact     the title is the query
//	prefix    the title starts with the query as whole words ("Rush Hour 2")
//	contains  the query appears as whole words ("Making Rush Hour 3")
//	other     whatever the provider matched on — an alias, a translation
//
// Within a tier each result keeps the rank its own provider gave it, so
// the providers' relevance still counts and nothing needs a popularity
// TVDB does not supply. Where a movie and a show share a tier and a
// rank, popularity breaks the tie when both have one, and then movies
// before shows so the order is stable.
func RankSearch(query string, movies, shows []SearchResult) []SearchResult {
	q := normTitle(query)
	type ranked struct {
		r    SearchResult
		tier int
		pos  int
	}
	all := make([]ranked, 0, len(movies)+len(shows))
	for i, r := range movies {
		all = append(all, ranked{r, matchTier(q, r.Title), i})
	}
	for i, r := range shows {
		all = append(all, ranked{r, matchTier(q, r.Title), i})
	}
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.tier != b.tier {
			return a.tier < b.tier
		}
		if a.pos != b.pos {
			return a.pos < b.pos
		}
		if a.r.Popularity > 0 && b.r.Popularity > 0 && a.r.Popularity != b.r.Popularity {
			return a.r.Popularity > b.r.Popularity
		}
		return a.r.Kind == "movie" && b.r.Kind != "movie"
	})
	out := make([]SearchResult, len(all))
	for i, a := range all {
		out[i] = a.r
	}
	return out
}

// matchTier is how well a title answers the query; lower is better.
// Both sides are normalised, so case and punctuation never decide it —
// "Rush Hour: The Series" and "rush hour" are a prefix match.
func matchTier(q, title string) int {
	t := normTitle(title)
	switch {
	case q == "":
		return 3
	case t == q:
		return 0
	case strings.HasPrefix(t, q+" "):
		return 1
	case strings.Contains(" "+t+" ", " "+q+" "):
		return 2
	default:
		return 3
	}
}

// normTitle lowercases a title and reduces everything that is not a
// letter or a digit to single spaces, so matching is on words.
func normTitle(s string) string {
	var b strings.Builder
	space := true // swallows leading separators
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			space = false
			continue
		}
		if !space {
			b.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(b.String())
}
