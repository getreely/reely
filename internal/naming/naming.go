// Package naming renders the naming templates from Settings into relative
// library paths. Templates are one line; slashes make folders:
//
//	{Title} ({Year})/{Title} ({Year}) [{Quality}]
//	{Title} ({Year})/Season {season:00}/{Title} - S{season:00}E{episode:00} - {Episode Title}
//
// The result carries no extension — the caller appends the source file's.
package naming

import (
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// MovieFields feeds a movie template.
type MovieFields struct {
	Title   string
	Year    int
	Quality string
	Source  string // stored form: remux / bluray / web / hdtv / dvd
	// TmdbID and ImdbID render the id tags a media server matches on.
	// Two films can share a title and a year — "Pinocchio (2022)" is two
	// different films — and a scanner with only those to go on has to
	// guess. An id in the folder name is the difference between matching
	// and guessing.
	TmdbID int
	ImdbID string
}

// EpisodeFields feeds a show template.
type EpisodeFields struct {
	Title        string // the show's title
	Year         int
	Season       int
	Episode      int
	EpisodeTitle string
	Quality      string
	Source       string // stored form: remux / bluray / web / hdtv / dvd
	// The series' ids, for the same reason a movie carries them.
	TmdbID int
	TvdbID int
	ImdbID string
}

// sourceLabels renders a stored source as a form the PARSER reads back.
// This is not cosmetic: a library scan re-derives quality and source from
// the filename, so a label the parser can't match is the same as writing
// nothing. Plain "WEB" is exactly that trap — the parser matches web-dl
// and webrip, never a bare web.
var sourceLabels = map[string]string{
	"remux":  "REMUX",
	"bluray": "BluRay",
	"webdl":  "WEB-DL",
	"webrip": "WEBRip",
	"hdtv":   "HDTV",
	"dvd":    "DVD",
}

// SourceLabel is the filename form of a stored source, empty when the
// source was never recognized.
func SourceLabel(src string) string { return sourceLabels[src] }

// Movie renders a movie's relative path from its template.
func Movie(template string, f MovieFields) string {
	return render(template, map[string]string{
		"{Title}":   f.Title,
		"{Year}":    yearStr(f.Year),
		"{Quality}": f.Quality,
		"{Source}":  SourceLabel(f.Source),
		"{Tmdb}":    idTag("tmdb", f.TmdbID),
		"{Imdb}":    idTagStr("imdb", f.ImdbID),
	})
}

// idTag renders the "{tmdb-123}" form media servers read, and nothing at
// all when the id is unknown — a tag reading "{tmdb-0}" would be a lie
// the scanner then acts on.
func idTag(kind string, id int) string {
	if id <= 0 {
		return ""
	}
	return fmt.Sprintf("{%s-%d}", kind, id)
}

func idTagStr(kind, id string) string {
	if strings.TrimSpace(id) == "" {
		return ""
	}
	return fmt.Sprintf("{%s-%s}", kind, strings.TrimSpace(id))
}

// Episode renders one episode's relative path from its template.
func Episode(template string, f EpisodeFields) string {
	return render(template, map[string]string{
		"{Title}":         f.Title,
		"{Year}":          yearStr(f.Year),
		"{Quality}":       f.Quality,
		"{Source}":        SourceLabel(f.Source),
		"{season:00}":     fmt.Sprintf("%02d", f.Season),
		"{episode:00}":    fmt.Sprintf("%02d", f.Episode),
		"{Episode Title}": f.EpisodeTitle,
		"{Tmdb}":          idTag("tmdb", f.TmdbID),
		"{Tvdb}":          idTag("tvdb", f.TvdbID),
		"{Imdb}":          idTagStr("imdb", f.ImdbID),
	})
}

var (
	// characters that don't survive on common filesystems
	unsafe = regexp.MustCompile(`[<>:"\\|?*\x00-\x1f]`)
	// decoration around a value that turned out empty: " []", " ()", " -" at
	// the end — a missing quality or episode title shouldn't leave husks
	emptyDecor = regexp.MustCompile(`\s*(\[\s*\]|\(\s*\))`)
	// a bracket whose contents came out PARTLY empty — "[1080p ]" when the
	// source is unknown, "[ WEB-DL]" when the quality is. Tidied rather
	// than left, so one unrecognized field doesn't scar every filename.
	bracketed = regexp.MustCompile(`\[([^\[\]]*)\]|\(([^()]*)\)`)
)

func render(template string, values map[string]string) string {
	parts := strings.Split(template, "/")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		for k, v := range values {
			part = strings.ReplaceAll(part, k, v)
		}
		part = tidyBrackets(part)
		part = emptyDecor.ReplaceAllString(part, "")
		part = unsafe.ReplaceAllString(part, "")
		part = strings.Join(strings.Fields(part), " ") // collapse doubled spaces
		part = strings.Trim(part, " -.")
		if part == "" {
			continue
		}
		out = append(out, part)
	}
	return filepath.Join(out...)
}

func yearStr(y int) string {
	if y == 0 {
		return ""
	}
	return fmt.Sprintf("%d", y)
}

// ── templates as a setting ─────────────────────────────────────────────
//
// Naming is install-wide and admin-only, and a bad template is expensive:
// the organize pass moves real files to wherever the template says they
// belong, so a mistake renames a library around it. Everything below
// exists to catch that before it is saved rather than after.

// DefaultMovieTemplate and DefaultShowTemplate are the templates a fresh
// install starts from. They live here — beside the renderer — because
// settings and the organize pass both need them, and two copies that
// drift would have Organize move every file to a destination the importer
// disagrees with.
//
// Both carry [{Quality} {Source}], and that is load-bearing rather than
// decorative: a library scan re-derives quality and source by parsing the
// filename, so a name that omits them loses them. An episode whose
// quality is unrecorded used to read as MISSING to the RSS sync and get
// downloaded again on every pass.
//
// Both also carry an id on the FOLDER, which is what a media server
// matches a title on. Without it a scanner reading "Pinocchio (2022)" has
// two films to choose between and picks one; with it there is nothing to
// choose. A movie names its TMDB id and a series its TVDB id, because
// those are the ones each scanner actually reads. An id that is not known
// renders nothing and the trailing separator is tidied away, so a title
// reely has no id for is named exactly as it was before.
const (
	DefaultMovieTemplate = "{Title} ({Year}) - {Tmdb}/{Title} ({Year}) [{Quality} {Source}]"
	DefaultShowTemplate  = "{Title} ({Year}) - {Tvdb}/Season {season:00}/{Title} - S{season:00}E{episode:00} - {Episode Title} [{Quality} {Source}]"
)

// MovieTokens and ShowTokens are the placeholders each kind understands,
// in the order they read best in a reference list.
var (
	MovieTokens = []string{"{Title}", "{Year}", "{Quality}", "{Source}", "{Tmdb}", "{Imdb}"}
	ShowTokens  = []string{"{Title}", "{Year}", "{season:00}", "{episode:00}", "{Episode Title}", "{Quality}", "{Source}", "{Tmdb}", "{Tvdb}", "{Imdb}"}
)

// required tokens are the ones without which different titles — or
// different episodes — render to the same path and overwrite each other.
var (
	movieRequired = []string{"{Title}"}
	showRequired  = []string{"{Title}", "{season:00}", "{episode:00}"}
)

// anything in braces, so a typo can be named rather than silently kept as
// literal text: "{title}" renders as the characters "{title}".
var braced = regexp.MustCompile(`\{[^}]*\}`)

// Tokens lists the placeholders valid for a kind ("movie" or "show").
func Tokens(kind string) []string {
	if kind == "movie" {
		return MovieTokens
	}
	return ShowTokens
}

// Validate reports why a template can't be used, or nil if it can.
func Validate(kind, template string) error {
	template = strings.TrimSpace(template)
	if template == "" {
		return fmt.Errorf("a naming template can't be empty")
	}
	allowed := Tokens(kind)
	for _, found := range braced.FindAllString(template, -1) {
		if !slices.Contains(allowed, found) {
			return fmt.Errorf("%s isn't a placeholder for %ss — try one of %s",
				found, kind, strings.Join(allowed, " "))
		}
	}
	required := movieRequired
	if kind != "movie" {
		required = showRequired
	}
	for _, token := range required {
		if !strings.Contains(template, token) {
			return fmt.Errorf("%s has to appear somewhere, or every %s lands on the same path and overwrites the last",
				token, map[bool]string{true: "movie", false: "episode"}[kind == "movie"])
		}
	}
	// a template of nothing but decoration renders away to nothing
	sample := Sample(kind, template)
	if sample == "" {
		return fmt.Errorf("this template renders to an empty path")
	}
	// render already drops empty and traversal segments; assert it, because
	// this is the value that becomes a path under a library root
	if filepath.IsAbs(sample) {
		return fmt.Errorf("a naming template has to be relative to the library root")
	}
	for _, part := range strings.Split(filepath.ToSlash(sample), "/") {
		if part == ".." {
			return fmt.Errorf("a naming template can't step outside the library root")
		}
	}
	return nil
}

// Sample renders a template against a fixed example, so the preview in
// Settings and the validation above agree by construction.
func Sample(kind, template string) string {
	if kind == "movie" {
		return Movie(template, MovieFields{Title: "Arrival", Year: 2016, Quality: "1080p", Source: "bluray"})
	}
	return Episode(template, EpisodeFields{
		Title: "Severance", Year: 2022, Season: 2, Episode: 7,
		EpisodeTitle: "Chikhai Bardo", Quality: "1080p", Source: "webdl",
	})
}

// tidyBrackets trims whitespace and stray separators from inside [] and (),
// so a template like "[{Quality} {Source}]" reads "[1080p]" when the source
// is unknown instead of "[1080p ]". A bracket left with nothing inside is
// handed on to emptyDecor, which removes it along with its leading space.
func tidyBrackets(part string) string {
	return bracketed.ReplaceAllStringFunc(part, func(m string) string {
		open, close := m[:1], m[len(m)-1:]
		inner := strings.Trim(m[1:len(m)-1], " -_.")
		inner = strings.Join(strings.Fields(inner), " ")
		return open + inner + close
	})
}
