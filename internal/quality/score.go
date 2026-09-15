package quality

import (
	"fmt"
	"strings"

	"github.com/getreely/reely/internal/parser"
)

// Candidate is one release under judgement: its parsed name plus what the
// indexer reported about it. Name is the raw release name — the release
// terms match against it, not against the parse.
type Candidate struct {
	parser.Result
	Name      string
	SizeBytes int64
}

// Want is the context the candidate is judged against: how long the wanted
// thing runs, how many episodes the release spans (season packs divide their
// size before the band check), and what's already on disk.
type Want struct {
	RuntimeMin int // minutes per movie/episode; 0 = unknown, size band skipped
	Episodes   int // episodes the release covers; 0/1 = single
	// HasFile says whether anything is on disk at all. It is separate from
	// CurrentQuality on purpose: a file whose quality was never recognized
	// also has an empty CurrentQuality, and reading that as "missing" is
	// what made the loop re-grab files it already had.
	HasFile        bool
	CurrentQuality string // quality of the file on disk, "" = unrecognized
	CurrentSource  string // source of the file on disk, "" = unknown
}

// Decision is the verdict on one candidate. Rejected carries the reason —
// it lands in the activity log so "why wasn't this grabbed" has an answer.
// Terms lists the matched preferred terms and custom formats. Score ranks
// candidates (the internal ladder plus the format score); FormatScore is
// the visible part — what the matched terms and formats added up to.
type Decision struct {
	Accepted    bool     `json:"accepted"`
	Score       int      `json:"score"`
	FormatScore int      `json:"formatScore"`
	Reason      string   `json:"reason,omitempty"`
	Terms       []string `json:"terms,omitempty"`
}

func reject(format string, args ...any) Decision {
	return Decision{Reason: fmt.Sprintf(format, args...)}
}

// Evaluate judges a candidate against a profile and what's wanted. Accepted
// candidates carry a score; among acceptable releases the highest score
// wins (quality above source above HDR above PROPER, size only as the gate).
func Evaluate(c Candidate, p *Profile, w Want) Decision {
	if c.PreRetail {
		return reject("cam/telesync release — not retail quality")
	}
	// a release whose name carries no resolution is judged only if the
	// profile deliberately allows unknowns; the size band is then the only
	// thing standing between it and the disk
	cRank := Rank(c.Quality)
	if cRank == 0 {
		if !p.AllowsUnknownQuality() {
			return reject("no recognizable quality in the name — allow Unknown in profile %q to take these", p.Name)
		}
	} else {
		allowed := false
		for _, q := range p.Qualities {
			if q == c.Quality {
				allowed = true
				break
			}
		}
		if !allowed {
			return reject("%s is not in profile %q", c.Quality, p.Name)
		}
	}
	if !p.AllowsSource(c.Source) {
		if c.Source == "" {
			// a markerless name is exactly what the Unknown quality box
			// opts into: a profile that takes unclassifiable releases on
			// the resolution axis takes them on the source axis too, and
			// the size band stays the judge. Without that opt-in, a
			// restricted source list keeps rejecting what it can't read.
			if !p.AllowsUnknownQuality() {
				return reject("no recognizable source in the name and profile %q restricts sources", p.Name)
			}
		} else {
			return reject("%s is not an allowed source in profile %q", c.Source, p.Name)
		}
	}

	if p.HDR == HDRRequire && !c.HDR {
		return reject("profile requires HDR")
	}
	if p.HDR == HDRBlock && c.HDR {
		return reject("profile blocks HDR")
	}

	// release terms judge the raw name — the parser's vocabulary is small
	// on purpose, and codecs, groups, and audio tags live outside it
	for _, term := range p.Blocked {
		if containsTerm(c.Name, term) {
			return reject("contains %q — blocked in profile %q", term, p.Name)
		}
	}
	for _, term := range p.Required {
		if !containsTerm(c.Name, term) {
			return reject("missing required term %q", term)
		}
	}

	// upgrade gate: with a file on disk, only a strict improvement below
	// the cutoff is worth bandwidth. With a source cutoff set, source
	// breaks resolution ties (web 1080p → bluray 1080p is an upgrade);
	// without one, resolution alone decides, as always.
	if w.HasFile || w.CurrentQuality != "" {
		if !p.Upgrades {
			return reject("already on disk and upgrades are off")
		}
		// A file whose quality was never recognized can't be compared
		// against: there is no telling whether a candidate improves on it
		// or is the same thing again. The active upgrade sweep skips these
		// for exactly this reason — and before this check, they fell
		// through the whole gate and were re-grabbed on every pass.
		if Rank(w.CurrentQuality) == 0 {
			return reject("already on disk, and its quality isn't recorded — nothing to compare a candidate against")
		}
		if p.CutoffMet(w.CurrentQuality, w.CurrentSource) {
			if p.SourceCutoff != "" {
				return reject("cutoff %s %s already met", p.SourceCutoff, p.Cutoff)
			}
			return reject("cutoff %s already met", p.Cutoff)
		}
		if !p.IsUpgrade(c.Quality, c.Source, w.CurrentQuality, w.CurrentSource) {
			return reject("%s is not an upgrade over %s",
				sourceLabel(c.Source, c.Quality), sourceLabel(w.CurrentSource, w.CurrentQuality))
		}
	}

	if d, ok := sizeGate(c, p, w); !ok {
		return d
	}

	// a resolution step is worth 1000 and a source step 100, so preferred
	// scores in the tens re-rank within a tier and hundreds jump tiers
	score := cRank * 1000
	score += sourceRank(c.Source) * 100
	if c.HDR {
		score += 50
	}
	if c.Proper {
		score += 10
	}
	var matched []string
	formatScore := 0
	for _, pt := range p.Preferred {
		if containsTerm(c.Name, pt.Term) {
			formatScore += pt.Score
			matched = append(matched, pt.Term)
		}
	}
	for i := range p.Formats {
		if p.Formats[i].Matches(c.Name, c.Source, c.Quality, c.Languages) {
			formatScore += p.Formats[i].Score
			matched = append(matched, p.Formats[i].Name)
		}
	}
	// 0 = no floor: negative scores deprioritize without blocking unless a
	// minimum is deliberately set
	if p.MinFormatScore != nil && formatScore < *p.MinFormatScore {
		return reject("format score %d is below the profile minimum %d", formatScore, *p.MinFormatScore)
	}
	return Decision{Accepted: true, Score: score + formatScore, FormatScore: formatScore, Terms: matched}
}

// containsTerm is the term matcher: case-insensitive substring of the raw
// release name. Predictable beats clever here — "x265" matches x265 and
// X265, and a term with dots matches exactly what was typed.
func containsTerm(name, term string) bool {
	return strings.Contains(strings.ToLower(name), strings.ToLower(strings.TrimSpace(term)))
}

// sizeGate applies the runtime-scaled size band. Unknown runtime or size
// skips the check rather than guessing — the band is a guard, not an oracle.
func sizeGate(c Candidate, p *Profile, w Want) (Decision, bool) {
	if w.RuntimeMin <= 0 || c.SizeBytes <= 0 || (p.MinMBPerMin == 0 && p.MaxMBPerMin == 0) {
		return Decision{}, true
	}
	episodes := max(w.Episodes, 1)
	minutes := w.RuntimeMin * episodes
	sizeMB := c.SizeBytes / (1 << 20)
	if p.MaxMBPerMin > 0 {
		maxMB := int64(minutes) * int64(p.MaxMBPerMin)
		if sizeMB > maxMB {
			return reject("too large: %s for %d min (max %s)", fmtGB(sizeMB), minutes, fmtGB(maxMB)), false
		}
	}
	if p.MinMBPerMin > 0 {
		minMB := int64(minutes) * int64(p.MinMBPerMin)
		if sizeMB < minMB {
			return reject("too small: %s for %d min (min %s) — likely a bad encode", fmtGB(sizeMB), minutes, fmtGB(minMB)), false
		}
	}
	return Decision{}, true
}

// sourceLabel names a file for a reject reason: "web 1080p", or just the
// resolution when the source is unknown — and says so outright when
// neither is known, so the reason never reads with a hole in it.
func sourceLabel(src, q string) string {
	if q == "" {
		q = "unknown quality"
	}
	if src == "" {
		return q
	}
	return src + " " + q
}

func fmtGB(mb int64) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1f GB", float64(mb)/1024)
	}
	return fmt.Sprintf("%d MB", mb)
}
