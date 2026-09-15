// Package trash imports TRaSH Guides custom formats. The guides publish
// every format as JSON in their GitHub repository; one tarball download
// brings the whole collection, which is converted into reely's format
// shape and cached for a day. Formats whose rules reely cannot judge
// faithfully (indexer flags, release types, and the language rules that
// judge the title's ORIGINAL language — metadata a release name cannot
// carry) are left out rather than imported half-working. Language rules
// naming a taggable language convert onto the parser's audio-tag
// vocabulary; English carries the *arr parsers' untagged-counts-as-English
// default, so the guides' "Language: Not English" dub-blockers work.
package trash

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/getreely/reely/internal/quality"
)

const defaultTarball = "https://github.com/TRaSH-Guides/Guides/archive/refs/heads/master.tar.gz"

// ListedFormat is one importable format with its guide-recommended score.
// AppliesTo carries the scope the format was published under — the guides'
// movie (Radarr) or show (Sonarr) collection — so importing one files it
// into the right column without asking.
type ListedFormat struct {
	quality.Format
	TrashID   string `json:"trashId"`
	AppliesTo string `json:"appliesTo"`
}

type Service struct {
	client *http.Client

	mu      sync.Mutex
	radarr  []ListedFormat
	sonarr  []ListedFormat
	fetched time.Time
}

func New() *Service {
	return &Service{client: &http.Client{Timeout: 120 * time.Second}}
}

// Formats returns the importable formats for one kind ("movies" or
// "shows" — the guides' Radarr and Sonarr collections), fetching and
// converting at most once a day.
func (s *Service) Formats(ctx context.Context, kind string) ([]ListedFormat, error) {
	if kind != "movies" && kind != "shows" {
		return nil, fmt.Errorf("kind must be movies or shows")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.fetched) > 24*time.Hour {
		radarr, sonarr, err := s.fetchAll(ctx)
		if err != nil {
			return nil, err
		}
		s.radarr, s.sonarr, s.fetched = radarr, sonarr, time.Now()
	}
	if kind == "movies" {
		return s.radarr, nil
	}
	return s.sonarr, nil
}

// tarballURL honors an override for tests and offline rigs.
func tarballURL() string {
	if u := os.Getenv("REELY_TRASH_TARBALL"); u != "" {
		return u
	}
	return defaultTarball
}

func (s *Service) fetchAll(ctx context.Context) (radarr, sonarr []ListedFormat, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, tarballURL(), nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("fetching the TRaSH guides: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("fetching the TRaSH guides: %s", resp.Status)
	}
	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, nil, err
	}
	// read side: a close error can only surface data errors the tar reader
	// already reported
	defer func() { _ = gz.Close() }()

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, err
		}
		if hdr.Typeflag != tar.TypeReg || !strings.HasSuffix(hdr.Name, ".json") {
			continue
		}
		var kind string
		switch {
		case strings.Contains(hdr.Name, "/docs/json/radarr/cf/"):
			kind = "radarr"
		case strings.Contains(hdr.Name, "/docs/json/sonarr/cf/"):
			kind = "sonarr"
		default:
			continue
		}
		raw, err := io.ReadAll(io.LimitReader(tr, 1<<20))
		if err != nil {
			return nil, nil, err
		}
		f, ok := Convert(raw, kind)
		if !ok {
			continue
		}
		if kind == "radarr" {
			f.AppliesTo = "movies"
			radarr = append(radarr, f)
		} else {
			f.AppliesTo = "shows"
			sonarr = append(sonarr, f)
		}
	}
	sort.Slice(radarr, func(i, j int) bool { return radarr[i].Name < radarr[j].Name })
	sort.Slice(sonarr, func(i, j int) bool { return sonarr[i].Name < sonarr[j].Name })
	if len(radarr) == 0 && len(sonarr) == 0 {
		return nil, nil, fmt.Errorf("no custom formats found in the guides download")
	}
	return radarr, sonarr, nil
}

// cfJSON is the guides' custom-format shape.
type cfJSON struct {
	TrashID     string             `json:"trash_id"`
	Name        string             `json:"name"`
	TrashScores map[string]float64 `json:"trash_scores"`
	Specs       []struct {
		Name           string `json:"name"`
		Implementation string `json:"implementation"`
		Negate         bool   `json:"negate"`
		Required       bool   `json:"required"`
		Fields         struct {
			Value any `json:"value"`
			// exceptLanguage inverts a language rule around the title's
			// ORIGINAL language ("anything but, unless it's the original") —
			// metadata reely's matcher doesn't see, so formats using it are
			// refused rather than silently un-inverted.
			ExceptLanguage bool `json:"exceptLanguage"`
		} `json:"fields"`
	} `json:"specifications"`
}

// The *arr apps number their source enums differently — the guides carry
// both. Values map either onto reely's source vocabulary or, for the
// WEBRip/WEB-DL split reely's parser doesn't make, onto title regexes that
// match the tokens release names actually carry.
type sourceMap struct {
	kind, value string // "source"/"source_title" + its value
}

// Web conditions become source_title, NOT title: kind is the AND-grouping
// key in matching, and a streaming-service format means (service token)
// AND (web source). Filed under "title" the web regex would OR with the
// token specs, making every web release match every service format.
var sourceEnums = map[string]map[float64]sourceMap{
	"radarr": { // MovieSource: … 5 dvd, 7 webdl, 8 webrip, 9 bluray
		5: {"source", "dvd"},
		7: {"source_title", `\bWEB[-_. ]?DL\b`},
		8: {"source_title", `\bWEB[-_. ]?Rip\b`},
		9: {"source", "bluray"},
	},
	"sonarr": { // 1 labeled WEB by the guides, 3 webdl, 4 webrip, 5 dvd, 6 bluray, 7 bluray remux
		// the guides' generic "WEB" covers both rungs, which no single
		// source value can say now that they are separate
		1: {"source_title", `\bWEB[-_. ]?(DL|Rip)\b`},
		3: {"source_title", `\bWEB[-_. ]?DL\b`},
		4: {"source_title", `\bWEB[-_. ]?Rip\b`},
		5: {"source", "dvd"},
		6: {"source", "bluray"},
		7: {"source", "remux"},
	},
}

// The *arr language enum, shared low ids in both apps — only the values
// that map onto reely's audio-tag vocabulary. English carries the same
// untagged-counts-as-English default the *arr parsers apply, which is
// what makes the guides' negated "Language: Not English" formats work.
// Deliberately absent: 0 Unknown, -1 Any, -2 Original — those judge the
// title's ORIGINAL language, metadata a release name cannot carry — plus
// the ids outside the parser's vocabulary. A format using any of them is
// refused whole.
var langEnums = map[float64]string{
	1: "english", 2: "french", 3: "spanish", 4: "german", 5: "italian",
	6: "danish", 7: "dutch", 8: "japanese", 10: "chinese", 11: "russian",
	12: "polish", 14: "swedish", 15: "norwegian", 16: "finnish", 21: "korean",
}

// Convert turns one guides JSON document into a reely format. ok is false
// when the document isn't a custom format or leans on rules reely can't
// judge (indexer flags, release types, the metadata-dependent language
// values) — importing those half-working would misrank quietly. kind
// picks the right source enum: radarr and sonarr number them differently.
func Convert(raw []byte, kind string) (ListedFormat, bool) {
	var cf cfJSON
	if err := json.Unmarshal(raw, &cf); err != nil || cf.Name == "" || len(cf.Specs) == 0 {
		return ListedFormat{}, false
	}
	out := ListedFormat{TrashID: cf.TrashID}
	out.Name = cf.Name
	out.Score = int(cf.TrashScores["default"])
	for _, s := range cf.Specs {
		spec := quality.FormatSpec{Negate: s.Negate, Required: s.Required}
		switch s.Implementation {
		case "ReleaseTitleSpecification", "ReleaseGroupSpecification":
			pattern, ok := s.Fields.Value.(string)
			if !ok || pattern == "" {
				return ListedFormat{}, false
			}
			// group patterns are anchored to the release GROUP, which reely
			// extracts from the name at match time
			if s.Implementation == "ReleaseGroupSpecification" {
				spec.Kind, spec.Value = "group", pattern
			} else {
				spec.Kind, spec.Value = "title", pattern
			}
		case "SourceSpecification", "QualityModifierSpecification":
			num, ok := s.Fields.Value.(float64)
			if s.Implementation == "QualityModifierSpecification" {
				// the only modifier the guides use is 5 = remux
				if !ok || num != 5 {
					return ListedFormat{}, false
				}
				spec.Kind, spec.Value = "source", "remux"
				break
			}
			m, known := sourceEnums[kind][num]
			if !ok || !known {
				return ListedFormat{}, false
			}
			spec.Kind, spec.Value = m.kind, m.value
		case "ResolutionSpecification":
			num, ok := s.Fields.Value.(float64)
			res := fmt.Sprintf("%dp", int(num))
			if !ok || quality.Rank(res) == 0 {
				return ListedFormat{}, false
			}
			spec.Kind, spec.Value = "resolution", res
		case "LanguageSpecification":
			num, ok := s.Fields.Value.(float64)
			lang, known := langEnums[num]
			if !ok || !known || s.Fields.ExceptLanguage {
				return ListedFormat{}, false
			}
			spec.Kind, spec.Value = "language", lang
		default:
			return ListedFormat{}, false
		}
		out.Specs = append(out.Specs, spec)
	}
	if err := quality.ValidateFormat(out.Format); err != nil {
		return ListedFormat{}, false
	}
	return out, true
}
