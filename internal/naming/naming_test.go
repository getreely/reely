package naming

import (
	"github.com/getreely/reely/internal/parser"
	"path/filepath"
	"strings"
	"testing"
)

const (
	movieTpl = "{Title} ({Year})/{Title} ({Year}) [{Quality}]"
	showTpl  = "{Title} ({Year})/Season {season:00}/{Title} - S{season:00}E{episode:00} - {Episode Title}"
)

func TestMovie(t *testing.T) {
	got := Movie(movieTpl, MovieFields{Title: "Inception", Year: 2010, Quality: "1080p"})
	want := filepath.Join("Inception (2010)", "Inception (2010) [1080p]")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestMovieMissingBitsLeaveNoHusks(t *testing.T) {
	got := Movie(movieTpl, MovieFields{Title: "Inception", Year: 2010})
	want := filepath.Join("Inception (2010)", "Inception (2010)")
	if got != want {
		t.Fatalf("no quality: got %q, want %q", got, want)
	}
	got = Movie(movieTpl, MovieFields{Title: "Unknown"})
	want = filepath.Join("Unknown", "Unknown")
	if got != want {
		t.Fatalf("no year: got %q, want %q", got, want)
	}
}

func TestEpisode(t *testing.T) {
	got := Episode(showTpl, EpisodeFields{
		Title: "Breaking Bad", Year: 2008, Season: 1, Episode: 3,
		EpisodeTitle: "...And the Bag's in the River", Quality: "1080p",
	})
	want := filepath.Join("Breaking Bad (2008)", "Season 01",
		"Breaking Bad - S01E03 - ...And the Bag's in the River")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestUnsafeCharactersStripped(t *testing.T) {
	got := Movie(movieTpl, MovieFields{Title: `M:I – Dead? "Reckoning" <Part|One>`, Year: 2023, Quality: "2160p"})
	if filepath.Base(got) != "MI – Dead Reckoning PartOne (2023) [2160p]" {
		t.Fatalf("got %q", got)
	}
	// a title that is nothing but unsafe characters must not produce an
	// empty path component
	got = Episode(showTpl, EpisodeFields{Title: "Show", Season: 1, Episode: 1, EpisodeTitle: `???`})
	want := filepath.Join("Show", "Season 01", "Show - S01E01")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestValidateAcceptsTheDefaults(t *testing.T) {
	if err := Validate("movie", `{Title} ({Year})/{Title} ({Year}) [{Quality}]`); err != nil {
		t.Fatalf("default movie template rejected: %v", err)
	}
	if err := Validate("show", `{Title} ({Year})/Season {season:00}/{Title} - S{season:00}E{episode:00} - {Episode Title}`); err != nil {
		t.Fatalf("default show template rejected: %v", err)
	}
}

// The expensive mistakes are the ones that render, but render the SAME
// path for different files — the organize pass then walks a library into
// overwriting itself.
func TestValidateRejectsCollidingTemplates(t *testing.T) {
	for _, tc := range []struct{ name, kind, template string }{
		{"movie without a title", "movie", `Movies/{Year} [{Quality}]`},
		{"show without an episode number", "show", `{Title}/Season {season:00}/{Title} - {Episode Title}`},
		{"show without a season number", "show", `{Title}/E{episode:00} - {Episode Title}`},
		{"show without a title", "show", `Season {season:00}/S{season:00}E{episode:00}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := Validate(tc.kind, tc.template); err == nil {
				t.Fatal("accepted a template that collides different files onto one path")
			}
		})
	}
}

// A mistyped placeholder renders as literal text, so every file gets
// "{title}" in its name. Naming it beats letting it through.
func TestValidateRejectsUnknownTokens(t *testing.T) {
	err := Validate("movie", `{title} ({Year})/{Title}`)
	if err == nil {
		t.Fatal("accepted a lowercase {title}")
	}
	if !strings.Contains(err.Error(), "{title}") {
		t.Fatalf("error should name the offending token, got %q", err)
	}
	// a show-only token in a movie template is the same class of mistake
	if err := Validate("movie", `{Title}/S{season:00}`); err == nil {
		t.Fatal("accepted a show token in a movie template")
	}
}

func TestValidateRejectsEmptyAndUnrenderable(t *testing.T) {
	if err := Validate("movie", "   "); err == nil {
		t.Fatal("accepted an empty template")
	}
	// renders to nothing: every segment is decoration that gets stripped
	if err := Validate("movie", ` - . / . - `); err == nil {
		t.Fatal("accepted a template that renders to an empty path")
	}
}

// The template becomes a path under a library root. Neither an absolute
// template nor one climbing out of the root may survive validation.
func TestValidateKeepsPathsInsideTheRoot(t *testing.T) {
	if s := Sample("movie", `/etc/{Title}`); filepath.IsAbs(s) {
		t.Fatalf("an absolute template rendered to an absolute path: %q", s)
	}
	if s := Sample("movie", `../../{Title}`); strings.Contains(s, "..") {
		t.Fatalf("a climbing template kept its .. segments: %q", s)
	}
}

func TestSampleMatchesTheRealRenderer(t *testing.T) {
	tpl := `{Title} ({Year})/Season {season:00}/{Title} - S{season:00}E{episode:00} - {Episode Title}`
	want := Episode(tpl, EpisodeFields{
		Title: "Severance", Year: 2022, Season: 2, Episode: 7,
		EpisodeTitle: "Chikhai Bardo", Quality: "1080p",
	})
	if got := Sample("show", tpl); got != want {
		t.Fatalf("Sample = %q, renderer = %q — the preview must not drift", got, want)
	}
}

// The round trip is the whole point of putting quality and source in the
// filename: a library scan re-derives both by parsing the name, so
// whatever the template writes must come back out. A label the parser
// can't match is the same as writing nothing — and writing nothing is how
// episodes ended up with no recorded quality and got re-downloaded.
func TestQualityAndSourceSurviveAParse(t *testing.T) {
	const tpl = `{Title} ({Year})/Season {season:00}/{Title} - S{season:00}E{episode:00} - {Episode Title} [{Quality} {Source}]`
	for _, src := range []string{"remux", "bluray", "webdl", "webrip", "hdtv", "dvd"} {
		for _, q := range []string{"480p", "720p", "1080p", "2160p"} {
			t.Run(q+"-"+src, func(t *testing.T) {
				rel := Episode(tpl, EpisodeFields{
					Title: "Severance", Year: 2022, Season: 2, Episode: 7,
					EpisodeTitle: "Chikhai Bardo", Quality: q, Source: src,
				})
				got := parser.Parse(filepath.Base(rel) + ".mkv")
				if got.Quality != q {
					t.Errorf("quality %q came back as %q from %q", q, got.Quality, rel)
				}
				if got.Source != src {
					t.Errorf("source %q came back as %q from %q", src, got.Source, rel)
				}
			})
		}
	}
}

// A bare "WEB" is the trap this guards: the parser matches web-dl and
// webrip, never a plain web, so a label of "WEB" would silently fail to
// round-trip.
func TestSourceLabelIsParsable(t *testing.T) {
	for stored, label := range map[string]string{
		"remux": "REMUX", "bluray": "BluRay", "webdl": "WEB-DL", "webrip": "WEBRip",
		"hdtv": "HDTV", "dvd": "DVD",
	} {
		if SourceLabel(stored) != label {
			t.Fatalf("SourceLabel(%q) = %q, want %q", stored, SourceLabel(stored), label)
		}
		if got := parser.Parse("Show.S01E01.1080p." + label + ".x264.mkv").Source; got != stored {
			t.Fatalf("label %q parsed back as %q, want %q", label, got, stored)
		}
	}
	if SourceLabel("nonsense") != "" {
		t.Fatal("an unrecognized source must render empty, not literal text")
	}
}

// One unknown field must not scar every filename with an empty bracket or
// a stray space.
func TestPartlyEmptyDecorationIsTidied(t *testing.T) {
	const tpl = `{Title} - S{season:00}E{episode:00} [{Quality} {Source}]`
	f := EpisodeFields{Title: "Severance", Season: 2, Episode: 7, Quality: "1080p"}
	if got := Episode(tpl, f); got != "Severance - S02E07 [1080p]" {
		t.Fatalf("unknown source left %q", got)
	}
	f.Quality, f.Source = "", "webdl"
	if got := Episode(tpl, f); got != "Severance - S02E07 [WEB-DL]" {
		t.Fatalf("unknown quality left %q", got)
	}
	f.Quality, f.Source = "", ""
	if got := Episode(tpl, f); got != "Severance - S02E07" {
		t.Fatalf("both unknown left %q", got)
	}
}

// Two films can share a title and a year — "Pinocchio (2022)" is two
// different films — and a scanner with only those to go on has to guess.
// An id in the path is the difference between matching and guessing.
func TestIdTokensRenderTheTagAScannerReads(t *testing.T) {
	got := Movie("{Title} ({Year}) {Tmdb}/{Title} ({Year}) [{Quality}]", MovieFields{
		Title: "Pinocchio", Year: 2022, Quality: "1080p", TmdbID: 532639,
	})
	want := "Pinocchio (2022) {tmdb-532639}/Pinocchio (2022) [1080p]"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// An id that is not known must leave nothing behind: "{tmdb-0}" is a lie
// a scanner would then act on.
func TestAnUnknownIdRendersNothing(t *testing.T) {
	got := Movie("{Title} ({Year}) {Tmdb}{Imdb}", MovieFields{Title: "Untitled", Year: 2030})
	if strings.Contains(got, "tmdb") || strings.Contains(got, "imdb") ||
		strings.Contains(got, "{") {
		t.Fatalf("got %q, want no id tag at all", got)
	}
}

// A series carries its TVDB id too, because that is what a TV scanner
// matches on.
func TestAnEpisodePathCanCarryTheSeriesId(t *testing.T) {
	got := Episode("{Title} {Tvdb}/Season {season:00}/{Title} - S{season:00}E{episode:00}",
		EpisodeFields{Title: "Some Show", Season: 2, Episode: 5, TvdbID: 778411})
	want := "Some Show {tvdb-778411}/Season 02/Some Show - S02E05"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}

// The default templates put the id on the FOLDER, because that is what a
// media server matches a title on.
func TestTheDefaultTemplatesNameTheId(t *testing.T) {
	got := Movie(DefaultMovieTemplate, MovieFields{
		Title: "Pinocchio", Year: 2022, Quality: "1080p", Source: "webdl", TmdbID: 532639,
	})
	want := "Pinocchio (2022) - {tmdb-532639}/Pinocchio (2022) [1080p WEB-DL]"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}

	ep := Episode(DefaultShowTemplate, EpisodeFields{
		Title: "Some Show", Year: 2019, Season: 2, Episode: 5, EpisodeTitle: "The One",
		Quality: "1080p", Source: "webdl", TvdbID: 778411,
	})
	wantEp := "Some Show (2019) - {tvdb-778411}/Season 02/Some Show - S02E05 - The One [1080p WEB-DL]"
	if ep != wantEp {
		t.Fatalf("got  %q\nwant %q", ep, wantEp)
	}
}

// A title reely has no id for must be named exactly as it was before the
// tokens existed — no husk, no trailing separator.
func TestATitleWithNoIdIsNamedAsItAlwaysWas(t *testing.T) {
	got := Movie(DefaultMovieTemplate, MovieFields{
		Title: "Unknown Film", Year: 2030, Quality: "1080p", Source: "webdl",
	})
	want := "Unknown Film (2030)/Unknown Film (2030) [1080p WEB-DL]"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
}
