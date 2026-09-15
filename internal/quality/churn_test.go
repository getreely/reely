package quality

import (
	"testing"

	"github.com/getreely/reely/internal/parser"
)

// Would an existing library get re-grabbed after the split? Simulate the
// states a migrated install actually lands in and see what Evaluate says
// about a same-resolution candidate.
func TestNoChurnAfterMigration(t *testing.T) {
	candidate := Candidate{Result: parser.Parse("Show.S01E01.1080p.WEB-DL.x264.mkv"), SizeBytes: 2 << 30}

	cases := []struct {
		name         string
		fileQuality  string
		fileSource   string
		sourceCutoff string
		wantGrab     bool
	}{
		// the default profile: no source cutoff at all
		{"migrated webrip, no source cutoff", "1080p", "webrip", "", false},
		{"source never recorded, no source cutoff", "1080p", "", "", false},
		{"already webdl, no source cutoff", "1080p", "webdl", "", false},

		// a profile that had SourceCutoff "web", migrated to "webrip"
		{"migrated webrip against migrated cutoff", "1080p", "webrip", "webrip", false},
		{"rescanned webdl against migrated cutoff", "1080p", "webdl", "webrip", false},

		// The one configuration that DOES re-grab, and it is deliberate:
		// with a source cutoff set, a file whose source was never recorded
		// counts as below it, so the loop keeps hunting until a labeled
		// encode lands. It settles after one grab per file — but it is
		// the reason to leave the source cutoff at "any" on a library
		// whose filenames never carried sources.
		{"source never recorded, source cutoff set", "1080p", "", "webrip", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := standard()
			p.Sources = SourceOrder
			p.SourceCutoff = tc.sourceCutoff
			d := Evaluate(candidate, p, Want{
				RuntimeMin: 45, HasFile: true,
				CurrentQuality: tc.fileQuality, CurrentSource: tc.fileSource,
			})
			if d.Accepted != tc.wantGrab {
				t.Fatalf("accepted=%v want %v (reason: %s)", d.Accepted, tc.wantGrab, d.Reason)
			}
		})
	}
}

// Upgrades that are unambiguous keep working.
func TestGenuineUpgradesStillHappen(t *testing.T) {
	p := standard()
	p.Sources = SourceOrder
	p.SourceCutoff = "webdl"

	// a better resolution is unambiguous, whatever the sources say
	better := Evaluate(
		Candidate{Result: parser.Parse("Show.S01E01.1080p.WEB-DL.x264.mkv"), SizeBytes: 2 << 30},
		p, Want{RuntimeMin: 45, HasFile: true, CurrentQuality: "720p", CurrentSource: ""})
	if !better.Accepted {
		t.Fatalf("720p → 1080p was refused: %s", better.Reason)
	}

	// and a source tie still breaks when BOTH sides are known
	tie := Evaluate(
		Candidate{Result: parser.Parse("Show.S01E01.1080p.BluRay.x264.mkv"), SizeBytes: 3 << 30},
		p, Want{RuntimeMin: 45, HasFile: true, CurrentQuality: "1080p", CurrentSource: "webrip"})
	if !tie.Accepted {
		t.Fatalf("webrip → bluray at the same resolution was refused: %s", tie.Reason)
	}
}
