package quality

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/dlclark/regexp2"
)

// Custom formats: the TRaSH Guides / Sonarr / Radarr shape, scoped to what
// reely can judge faithfully — the release name (regex), the parsed
// source, and the resolution. Matching follows the *arr apps: specs group
// by kind, optional specs OR within their kind, the kinds AND together,
// and a required spec must pass individually. Without the per-kind split a
// tier format's release-group list would be satisfied by its own WEB-DL
// source rule, making every web release "tier" quality. Each spec can
// negate. Matching formats add their score to the ranking.

type FormatSpec struct {
	// Kind is also the AND-grouping key (see Matches). source_title is a
	// source condition expressed as a name regex — the guides' generic
	// WEB conditions, which reely's source vocabulary can't hold — and it
	// MUST group apart from title: a streaming-service format is
	// (service token) AND (web source), and collapsing both into "title"
	// ORs them instead, making every web release match every service.
	Kind     string `json:"kind"`  // title | group | source | source_title | resolution | language
	Value    string `json:"value"` // regex for title/group/source_title; a SourceOrder name; 720p/1080p/2160p; a language name
	Negate   bool   `json:"negate"`
	Required bool   `json:"required"`
}

type Format struct {
	Name  string       `json:"name"`
	Score int          `json:"score"`
	Specs []FormatSpec `json:"specs"`
}

// Matches judges one candidate against the format. langs is the parsed
// audio-language tag list from the release name.
func (f *Format) Matches(name, source, resolution string, langs []string) bool {
	anyOptional := map[string]bool{}
	optionalHit := map[string]bool{}
	for _, spec := range f.Specs {
		hit := spec.hit(name, source, resolution, langs)
		if spec.Negate {
			hit = !hit
		}
		if spec.Required {
			if !hit {
				return false
			}
			continue
		}
		anyOptional[spec.Kind] = true
		if hit {
			optionalHit[spec.Kind] = true
		}
	}
	for kind := range anyOptional {
		if !optionalHit[kind] {
			return false
		}
	}
	return true
}

func (s *FormatSpec) hit(name, source, resolution string, langs []string) bool {
	switch s.Kind {
	case "title", "source_title":
		return titleRegexMatch(s.Value, name)
	case "group":
		g := releaseGroup(name)
		return g != "" && titleRegexMatch(s.Value, g)
	case "source":
		return source == s.Value
	case "resolution":
		return resolution == s.Value
	case "language":
		return langHit(s.Value, langs)
	}
	return false
}

// formatLanguages is what a language spec may name — the parser's
// audio-tag vocabulary, lowercased. English is the literal ENGLISH tag,
// which foreign releases use to mark an English dub; English-original
// releases don't tag themselves, so demanding it matches almost nothing.
var formatLanguages = []string{
	"english", "french", "german", "spanish", "italian", "dutch", "polish",
	"swedish", "danish", "norwegian", "finnish", "russian", "hindi",
	"korean", "japanese", "chinese",
}

// langHit judges a language spec against the parsed audio tags: the asked
// language's own tag hits, and so do MULTI and dual-audio — several
// tracks, plausibly including it.
//
// English gets the same default the *arr parsers apply: an untagged name
// (or VOSTFR — original audio under foreign subs) counts as English,
// because English releases don't tag themselves. That default is what
// makes a negated english spec — the guides' "Language: Not English" —
// hit exactly the releases tagged with only foreign audio, which is the
// one-format way to keep dubs out. For every other language an untagged
// name never hits: which language it is would take metadata the matcher
// doesn't see.
func langHit(want string, langs []string) bool {
	if want == "english" && len(langs) == 0 {
		return true
	}
	for _, l := range langs {
		if l == "Multi" || l == "Dual Audio" || strings.EqualFold(l, want) {
			return true
		}
		if want == "english" && l == "VOSTFR" {
			return true
		}
	}
	return false
}

// releaseGroup pulls the group token off a release name: the run after the
// last dash, extension stripped. A candidate with spaces or dots in it
// isn't a group ("Blade Runner 2049-..." must not read as a group "2049").
func releaseGroup(name string) string {
	// strip a media extension
	if i := strings.LastIndex(name, "."); i > 0 && len(name)-i <= 5 {
		name = name[:i]
	}
	i := strings.LastIndex(name, "-")
	if i < 0 || i == len(name)-1 {
		return ""
	}
	g := name[i+1:]
	if strings.ContainsAny(g, " .") {
		return ""
	}
	return g
}

// titleRegexMatch runs a C#-flavored regex (TRaSH patterns lean on
// lookarounds Go's RE2 lacks) against the release name, case-insensitive,
// with a hard timeout so a pathological pattern can't stall the loop.
// Compiled patterns are cached — the RSS pass judges hundreds of names.
var regexCache sync.Map // pattern → *regexp2.Regexp

func titleRegexMatch(pattern, name string) bool {
	v, ok := regexCache.Load(pattern)
	if !ok {
		re, err := regexp2.Compile(pattern, regexp2.IgnoreCase)
		if err != nil {
			return false // validated at import; an unloadable pattern just never matches
		}
		re.MatchTimeout = 100 * time.Millisecond
		v, _ = regexCache.LoadOrStore(pattern, re)
	}
	m, err := v.(*regexp2.Regexp).MatchString(name)
	return err == nil && m
}

// ValidateFormat rejects a format reely couldn't evaluate.
func ValidateFormat(f Format) error {
	if strings.TrimSpace(f.Name) == "" {
		return errors.New("format needs a name")
	}
	if len(f.Specs) == 0 {
		return fmt.Errorf("format %q has no specs", f.Name)
	}
	for _, s := range f.Specs {
		switch s.Kind {
		case "title", "group", "source_title":
			if _, err := regexp2.Compile(s.Value, regexp2.IgnoreCase); err != nil {
				return fmt.Errorf("format %q: bad pattern %q: %v", f.Name, s.Value, err)
			}
		case "source":
			if SourceRank(s.Value) == 0 {
				return fmt.Errorf("format %q: unknown source %q", f.Name, s.Value)
			}
		case "resolution":
			if Rank(s.Value) == 0 {
				return fmt.Errorf("format %q: unknown resolution %q", f.Name, s.Value)
			}
		case "language":
			if !slices.Contains(formatLanguages, s.Value) {
				return fmt.Errorf("format %q: unknown language %q", f.Name, s.Value)
			}
		default:
			return fmt.Errorf("format %q: unknown spec kind %q", f.Name, s.Kind)
		}
	}
	return nil
}
