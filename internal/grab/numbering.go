package grab

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/parser"
)

// Release numbering: releases follow TVDB, and TVDB sometimes disagrees
// with TMDB about where a show begins — a revival TVDB keeps as S8+ that
// TMDB restarts as a new show's S1. A show carrying a SeasonOffset maps
// between the two: release season = catalog season + offset.

// countryTags are the region markers scene names append to distinguish
// same-named shows ("Kitchen Nightmares US"). TMDB titles rarely carry
// them, so a release side may bring one the canonical title lacks.
var countryTags = map[string]bool{"us": true, "uk": true, "gb": true, "au": true, "nz": true, "ca": true}

// titleMatchRelease compares a release's parsed title against a show's
// canonical one: an exact match, or the two differing by one trailing
// country tag or year. Release names append tags TMDB titles lack
// ("Kitchen Nightmares US"), and TVDB titles carry markers releases drop
// — a country ("Kitchen Nightmares (US)" against
// "Kitchen.Nightmares.S07E01") or, for same-named shows, a year
// ("Monster (2022)" against "Monster.S01E01") — all the same show. Two
// DIFFERENT years never fold into each other: "Monster (2022)" is not
// "Monster (2017)". Never prefix matching: only a recognized country or
// year token may differ, and the numbering/episode-title guards still
// apply after.
func titleMatchRelease(release, canonical string) bool {
	if titleMatch(release, canonical) {
		return true
	}
	r, c := normalizeTitle(release), normalizeTitle(canonical)
	if dropCountryTag(r) == c || r == dropCountryTag(c) {
		return true
	}
	ry, cy := yearTag(r), yearTag(c)
	if ry != "" && cy != "" && ry != cy {
		return false
	}
	return (ry != "" || cy != "") && dropYearTag(r) == dropYearTag(c)
}

// titleMatchShow is titleMatchRelease across every name a show answers
// to: the canonical title and each alias. An anthology's seasons release
// under their own subtitles ("Monsters: The Lyle and Erik Menendez
// Story" is a season of "Monster (2022)"), and TVDB's aliases carry
// exactly those names.
func titleMatchShow(release string, sh *catalog.Show) bool {
	if titleMatchRelease(release, sh.Title) {
		return true
	}
	for _, a := range sh.Aliases {
		if titleMatchRelease(release, a) {
			return true
		}
	}
	return false
}

// titleMatchMovie is whole-title equality across every name a movie
// answers to: the canonical title and each alias (TMDB's alternative and
// original titles) — "Leon" and "The Professional" are both Léon: The
// Professional, and Radarr matches through the same list. The year veto
// still applies after, alias match or not.
func titleMatchMovie(release string, m *catalog.Movie) bool {
	if titleMatch(release, m.Title) {
		return true
	}
	for _, a := range m.Aliases {
		if titleMatch(release, a) {
			return true
		}
	}
	return false
}

// yearTag returns a normalized title's trailing year token ("monster
// 2022" → "2022"), or "" when the last token isn't a plausible year or is
// the whole title.
func yearTag(s string) string {
	i := strings.LastIndexByte(s, ' ')
	if i <= 0 {
		return ""
	}
	y := s[i+1:]
	if len(y) != 4 || (y[:2] != "19" && y[:2] != "20") {
		return ""
	}
	for _, ch := range y[2:] {
		if ch < '0' || ch > '9' {
			return ""
		}
	}
	return y
}

// dropYearTag removes one trailing year token from a normalized title; a
// title without one comes back unchanged.
func dropYearTag(s string) string {
	if yearTag(s) == "" {
		return s
	}
	return s[:strings.LastIndexByte(s, ' ')]
}

// dropCountryTag removes one trailing country token from a normalized
// title; a title without one comes back unchanged.
func dropCountryTag(s string) string {
	if i := strings.LastIndexByte(s, ' '); i > 0 && countryTags[s[i+1:]] {
		return s[:i]
	}
	return s
}

// sceneNumbered reports whether a show carries scene numbering at all —
// whether TheXEM had anything to say about it. Almost no show does, and
// the ones that do are the ones this whole path exists for.
func sceneNumbered(sh *catalog.ShowDetails) bool {
	for _, se := range sh.Seasons {
		for _, e := range se.Episodes {
			if e.SceneSeason > 0 {
				return true
			}
		}
	}
	return false
}

// releaseNumbering is the season and episode a show's releases call this
// episode. Scene numbering answers first when the show has any: an
// episode XEM mapped uses its mapping, and an episode it left alone on a
// mapped show is one the scene numbers the same way — that is what an
// absent mapping means. Only a show XEM never covered falls back to the
// manual season offset.
func releaseNumbering(sh *catalog.ShowDetails, season, episode int) (int, int) {
	if e := findEpisode(sh, season, episode); e != nil {
		if e.SceneSeason > 0 && e.SceneEpisode > 0 {
			return e.SceneSeason, e.SceneEpisode
		}
		if sceneNumbered(sh) {
			return season, episode
		}
	}
	return season + sh.SeasonOffset, episode
}

// releaseSeasons lists the season numbers a catalog season's releases
// carry, the one covering most of the season first. Usually one number.
// Two when TVDB merged two broadcast seasons into one and the scene did
// not — TVDB's Kitchen Nightmares S1 is the scene's S1 and S2 — in which
// case a season pack has to be asked for under both.
func releaseSeasons(sh *catalog.ShowDetails, season int) []int {
	scened := sceneNumbered(sh)
	counts := map[int]int{}
	var order []int
	for _, se := range sh.Seasons {
		if se.Number != season {
			continue
		}
		for _, e := range se.Episodes {
			n := e.SceneSeason
			if n == 0 {
				n = e.Season
				if !scened {
					n += sh.SeasonOffset
				}
			}
			if counts[n] == 0 {
				order = append(order, n)
			}
			counts[n]++
		}
	}
	if len(order) == 0 {
		return []int{season + sh.SeasonOffset}
	}
	sort.SliceStable(order, func(i, j int) bool { return counts[order[i]] > counts[order[j]] })
	return order
}

// catalogEpisode places a scene-numbered release on the show's own
// numbering. An episode carrying no mapping is numbered the same way by
// both sides, so it answers for itself.
func catalogEpisode(sh *catalog.ShowDetails, season, episode int) (int, int, bool) {
	for _, se := range sh.Seasons {
		for _, e := range se.Episodes {
			if e.SceneSeason == season && e.SceneEpisode == episode {
				return e.Season, e.Episode, true
			}
		}
	}
	for _, se := range sh.Seasons {
		for _, e := range se.Episodes {
			if e.SceneSeason == 0 && e.Season == season && e.Episode == episode {
				return e.Season, e.Episode, true
			}
		}
	}
	return 0, 0, false
}

// catalogSeason is catalogEpisode for a whole-season release.
func catalogSeason(sh *catalog.ShowDetails, season int) (int, bool) {
	for _, se := range sh.Seasons {
		for _, e := range se.Episodes {
			if e.SceneSeason == season {
				return e.Season, true
			}
		}
	}
	for _, se := range sh.Seasons {
		for _, e := range se.Episodes {
			if e.SceneSeason == 0 && e.Season == season {
				return e.Season, true
			}
		}
	}
	return 0, false
}

// sceneMap reads a release's numbering through a show's scene mapping.
// A number the mapping cannot place is zeroed rather than taken
// literally: on a show whose seasons are shifted, a release numbered the
// catalog's way belongs to a different season than the one it names, and
// letting it through on the coincidence of the numbers lining up is how
// the wrong season gets grabbed.
func sceneMap(sh *catalog.ShowDetails, p parser.Result) parser.Result {
	if p.Kind == "season" {
		season, ok := catalogSeason(sh, p.Ep.Season)
		if !ok {
			p.Ep.Season = 0
			return p
		}
		p.Ep.Season = season
		return p
	}
	season, episode, ok := catalogEpisode(sh, p.Ep.Season, p.Ep.Episode)
	if !ok {
		p.Ep.Season = 0
		return p
	}
	end := episode
	if p.Ep.EpisodeEnd > p.Ep.Episode {
		if _, e, ok := catalogEpisode(sh, p.Ep.Season, p.Ep.EpisodeEnd); ok {
			end = e
		}
	}
	p.Ep.Season, p.Ep.Episode, p.Ep.EpisodeEnd = season, episode, end
	return p
}

// subtitleSeason finds the one season whose stored name — a real
// subtitle, not "Season N" — is spelled out in the release's title. An
// anthology names each season after its story ("The Lyle and Erik
// Menendez Story"), and its releases carry that subtitle while numbering
// themselves S01, as their own little show. The subtitle is the truth:
// it both places such a release on the right season and refuses it for
// any other, however its numbers happen to line up. Needs at least two
// significant words of subtitle and exactly one season matching —
// anything weaker proves nothing.
func subtitleSeason(sh *catalog.ShowDetails, releaseTitle string) (int, bool) {
	words := significantWords(releaseTitle)
	found, count := 0, 0
	for _, se := range sh.Seasons {
		sub := significantWords(se.Name)
		if len(sub) < 2 {
			continue // "Season 2", "Specials", one-word names: too weak
		}
		all := true
		for w := range sub {
			if !words[w] {
				all = false
				break
			}
		}
		if all {
			found = se.Number
			count++
		}
	}
	if count == 1 && found > 0 {
		return found, true
	}
	return 0, false
}

// yearIsTitle reports whether a parsed "year" is actually the title's
// own name — "1923", "1883": a title that IS a year reads as one to the
// parser, and vetoing on it rejected every release of such a show.
func yearIsTitle(year int, title string) bool {
	return year > 0 && normalizeTitle(title) == strconv.Itoa(year)
}

// queryPunct collapses punctuation for indexer text queries — a colon in
// "Monsters: The Lyle and Erik Menendez Story" is noise to a keyword
// search.
var queryPunct = regexp.MustCompile(`[^\pL\pN]+`)

// seasonSubtitleQuery is the free-text form to ask indexers for a
// subtitled season: an alias that spells the season's story out when one
// exists (that's how the releases are named), else the season name
// itself. false for plain "Season N" seasons.
func seasonSubtitleQuery(sh *catalog.ShowDetails, season int) (string, bool) {
	var name string
	for _, se := range sh.Seasons {
		if se.Number == season {
			name = se.Name
		}
	}
	sub := significantWords(name)
	if len(sub) < 2 {
		return "", false
	}
	best := name
	for _, a := range sh.Aliases {
		words := significantWords(a)
		superset := true
		for w := range sub {
			if !words[w] {
				superset = false
				break
			}
		}
		if superset {
			best = a
			break
		}
	}
	return strings.TrimSpace(queryPunct.ReplaceAllString(best, " ")), true
}

// subtitleAdjust rewrites a subtitle-named release's numbering into the
// show's own: its S01 is the named season. A release that names one
// season but numbers a different one contradicts itself — its season is
// zeroed so nothing downstream can match it.
func subtitleAdjust(sh *catalog.ShowDetails, p parser.Result) (parser.Result, bool) {
	if p.Kind != "episode" && p.Kind != "season" {
		return p, false
	}
	n, ok := subtitleSeason(sh, p.Title)
	if !ok {
		return p, false
	}
	switch p.Ep.Season {
	case 1, n:
		p.Ep.Season = n
	default:
		p.Ep.Season = 0
	}
	return p, true
}

// sceneToCatalog maps a parsed release numbering onto the show's own —
// the strict form the matchers use. A season subtitle in the name wins
// outright; otherwise, with an offset set, a release is read in scene
// numbering and nothing else, so the split-off original's S1 can never
// be mistaken for the revival's.
func sceneToCatalog(sh *catalog.ShowDetails, p parser.Result) parser.Result {
	if p.Kind != "episode" && p.Kind != "season" {
		return p
	}
	if q, ok := subtitleAdjust(sh, p); ok {
		return q
	}
	if sceneNumbered(sh) {
		return sceneMap(sh, p)
	}
	if sh.SeasonOffset == 0 {
		return p
	}
	p.Ep.Season -= sh.SeasonOffset
	return p
}

// fileToCatalog is the lenient form for placing files already vetted (a
// job reely grabbed, a hand-resolved import, an upload): scene numbering
// first, but a name that only resolves literally — a file someone already
// renamed to the show's own numbering — is honored as written.
func fileToCatalog(sh *catalog.ShowDetails, p parser.Result) parser.Result {
	if p.Kind == "episode" || p.Kind == "season" {
		// a season subtitle in the file name places it, exactly as in search
		if q, ok := subtitleAdjust(sh, p); ok {
			if p.Kind == "season" || findEpisode(sh, q.Ep.Season, q.Ep.Episode) != nil {
				return q
			}
		}
	}
	if p.Kind != "episode" && p.Kind != "season" {
		return p
	}
	if sceneNumbered(sh) {
		m := sceneMap(sh, p)
		if p.Kind == "season" {
			if m.Ep.Season > 0 {
				return m
			}
			return p
		}
		mapped := findEpisode(sh, m.Ep.Season, m.Ep.Episode)
		literal := findEpisode(sh, p.Ep.Season, p.Ep.Episode)
		// A file already renamed to the show's own numbering is honoured as
		// written. Under a season offset that needs no deciding — the
		// offset pushes such a name out of the show's range, so only the
		// literal reading resolves at all. XEM maps densely enough that
		// both readings land on a real episode, so the episode title in the
		// name breaks the tie, the same signal that separates two shows
		// with one name.
		if mapped != nil && literal != nil && p.Ep.Title != "" &&
			episodeTitleConflict(p.Ep.Title, mapped.Title) &&
			!episodeTitleConflict(p.Ep.Title, literal.Title) {
			return p
		}
		if mapped != nil {
			return m
		}
		return p
	}
	if sh.SeasonOffset == 0 {
		return p
	}
	mapped := p
	mapped.Ep.Season -= sh.SeasonOffset
	if p.Kind == "season" {
		if hasSeason(sh, mapped.Ep.Season) || !hasSeason(sh, p.Ep.Season) {
			return mapped
		}
		return p
	}
	if findEpisode(sh, mapped.Ep.Season, mapped.Ep.Episode) != nil {
		return mapped
	}
	if findEpisode(sh, p.Ep.Season, p.Ep.Episode) != nil {
		return p
	}
	return mapped
}

// combinedEpisode rescues the scene's compact numbering — "Show.301." is
// S03E01 — for a file whose name parses to no episode at all. Bare
// digits prove nothing on their own (an anime's "301" is absolute
// episode three hundred one), so a candidate must clear evidence gates:
// a year is never a candidate; the mapped episode must exist on the
// show; episode-title words in the name must agree with that episode
// when the name carries any; and a name carrying none is accepted only
// when the number exceeds the show's whole episode count, so it cannot
// be an absolute number read wrong. The candidate is re-parsed with the
// number rewritten as SxxEyy, so quality and episode-title extraction
// run exactly as they would for a normally-named file.
//
// trusted lifts the absolute-number ambiguity guard: a file that arrived
// inside a job whose own name declares seasons ("Show.S01-S06.DVDRIP"),
// or that a person hand-resolved onto this show, carries its context as
// the evidence — "Show.103" in a season pack is S01E03, whatever the
// show's episode count.
func combinedEpisode(sh *catalog.ShowDetails, base string, trusted bool) (parser.Result, bool) {
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	notAlnum := func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) }
	for _, tok := range strings.FieldsFunc(stem, notAlnum) {
		if len(tok) < 3 || len(tok) > 4 {
			continue
		}
		n := 0
		digits := true
		for _, r := range tok {
			if r < '0' || r > '9' {
				digits = false
				break
			}
			n = n*10 + int(r-'0')
		}
		if !digits || (n >= 1900 && n <= 2099) {
			continue // not a number, or a year
		}
		season, episode := n/100, n%100
		if season == 0 || episode == 0 {
			continue
		}
		ep := findEpisode(sh, season, episode)
		if ep == nil {
			continue
		}
		bounded := regexp.MustCompile(`(^|[^0-9A-Za-z])` + tok + `([^0-9A-Za-z]|$)`)
		rewritten := bounded.ReplaceAllString(stem, fmt.Sprintf("${1}S%02dE%02d${2}", season, episode))
		p := parser.Parse(rewritten + ext)
		if p.Kind != "episode" || p.Ep.Season != season || p.Ep.Episode != episode {
			continue
		}
		if episodeTitleConflict(p.Ep.Title, ep.Title) {
			continue
		}
		agree := false
		want := significantWords(ep.Title)
		for w := range significantWords(p.Ep.Title) {
			if want[w] {
				agree = true
				break
			}
		}
		if !trusted && !agree && totalEpisodes(sh) >= n {
			continue // could be an absolute episode number — don't guess
		}
		return p, true
	}
	return parser.Result{}, false
}

func totalEpisodes(sh *catalog.ShowDetails) int {
	n := 0
	for _, se := range sh.Seasons {
		n += len(se.Episodes)
	}
	return n
}

func hasSeason(sh *catalog.ShowDetails, season int) bool {
	for _, se := range sh.Seasons {
		if se.Number == season {
			return true
		}
	}
	return false
}
