package quality

import (
	"path/filepath"
	"testing"

	"github.com/getreely/reely/internal/naming"
	"github.com/getreely/reely/internal/parser"
)

// The seam this locks down: a profile stores the CANONICAL source
// vocabulary (web, bluray, hdtv, dvd, remux), while a filename carries a
// release-style label (WEB-DL, BluRay). The parser normalises one to the
// other, so a profile listing "webdl" matches a file named WEB-DL — and a
// profile listing "web-dl" would match nothing at all, since nothing ever
// produces that string.
func TestProfileSourcesMatchWhatTheTemplateWrites(t *testing.T) {
	p := &Profile{
		Name:      "Standard 1080p",
		Qualities: []string{"1080p", "720p"},
		Cutoff:    "1080p",
		Sources:   SourceOrder, // exactly what a profile stores
	}
	for _, src := range SourceOrder {
		t.Run(src, func(t *testing.T) {
			rel := naming.Episode(naming.DefaultShowTemplate, naming.EpisodeFields{
				Title: "Severance", Year: 2022, Season: 2, Episode: 7,
				EpisodeTitle: "Chikhai Bardo", Quality: "1080p", Source: src,
			})
			got := parser.Parse(filepath.Base(rel) + ".mkv")
			if got.Source != src {
				t.Fatalf("%q wrote %q, which parsed back as %q", src, filepath.Base(rel), got.Source)
			}
			if !p.AllowsSource(got.Source) {
				t.Fatalf("a profile listing %v rejected its own written form %q", SourceOrder, got.Source)
			}
			if !p.CutoffMet(got.Quality, got.Source) {
				t.Fatalf("cutoff unmet after a round trip: q=%q src=%q", got.Quality, got.Source)
			}
		})
	}
}

// The mistake this guards against: "correcting" a profile to hold the
// label instead of the canonical value.
func TestProfileListingLabelFormsMatchesNothing(t *testing.T) {
	p := &Profile{Sources: []string{"web-dl", "WEB-DL"}}
	parsed := parser.Parse("Severance - S02E07 [1080p WEB-DL].mkv")
	if parsed.Source != "webdl" {
		t.Fatalf("parser gave %q, expected the canonical webdl", parsed.Source)
	}
	if p.AllowsSource(parsed.Source) {
		t.Fatal("a profile holding label forms should NOT match — profiles store webdl, not web-dl")
	}
}
