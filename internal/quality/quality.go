// Package quality owns the grab loop's judgement: quality profiles (what a
// library wants) and the release scorer (does this release fit, and how
// well). It is pure — storage lives in catalog, indexers and download
// clients come later — so every decision here is table-testable.
package quality

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Qualities and sources in ascending rank order. The parser's vocabulary,
// exactly — the scorer never sees a value outside these.
var (
	QualityOrder = []string{"480p", "720p", "1080p", "2160p"}
	// WEBRip sits below WEB-DL deliberately: a WEB-DL is the service's own
	// file, a WEBRip a re-encode of a stream. The *arr apps separate them
	// for the same reason, and collapsing the two meant a WEBRip satisfied
	// a web cutoff exactly as a WEB-DL did, with no upgrade possible
	// between them.
	SourceOrder = []string{"dvd", "hdtv", "webrip", "webdl", "bluray", "remux"}
)

// QualityUnknown is the allow-list entry for releases whose name carries no
// resolution at all — obfuscated usenet posts, mostly. It is deliberately
// NOT in QualityOrder: it is a permission, not a rung on the ladder, so it
// can be allowed but never set as a cutoff, and it always ranks last.
const QualityUnknown = "unknown"

// Rank orders known qualities ascending — 480p=1 … 2160p=4; 0 = unknown.
// Exported for the pieces of the loop that compare files without running a
// full Evaluate (upgrade sweeps, sibling fan-out).
func Rank(q string) int { return slices.Index(QualityOrder, q) + 1 }

// SourceRank orders known sources ascending — dvd=1 … remux=6; 0 = unknown.
func SourceRank(s string) int { return slices.Index(SourceOrder, s) + 1 }

func sourceRank(s string) int { return SourceRank(s) }

// FileBetter reports whether the (quality, source) pair on the left is a
// strict improvement over the one on the right. Resolution dominates;
// source only breaks a resolution tie, and only when BOTH sides know their
// source — a file with an unknown source might be anything, so treating it
// as replaceable would churn hardlinks on guesswork.
func FileBetter(newQ, newSrc, oldQ, oldSrc string) bool {
	nq, oq := Rank(newQ), Rank(oldQ)
	if nq != oq {
		return nq > oq
	}
	if newSrc == "" || oldSrc == "" {
		return false
	}
	return SourceRank(newSrc) > SourceRank(oldSrc)
}

// HDR policies: allow takes either, require rejects SDR, block rejects HDR
// (the right call when the screen it plays on washes HDR out).
const (
	HDRAllow   = "allow"
	HDRRequire = "require"
	HDRBlock   = "block"
)

// Profile is what a library wants grabbed. Size limits are per minute of
// runtime, not flat — 8 GB is bloated for a 45-minute episode and stingy for
// a three-hour movie, so a flat cap is wrong in both directions. Zero means
// no bound on that side.
type Profile struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	Qualities []string `json:"qualities"` // allowed resolutions
	Cutoff    string   `json:"cutoff"`    // stop upgrading once on disk at/above this
	Upgrades  bool     `json:"upgrades"`  // keep hunting above the current file until cutoff
	// Sources narrows where encodes may come from (web, bluray, …); empty
	// allows any. SourceCutoff extends the cutoff within its resolution:
	// with cutoff 1080p and source cutoff bluray, a web 1080p file keeps
	// hunting until a bluray (or remux) 1080p — or anything 2160p — lands.
	// Empty means the resolution alone satisfies the cutoff, as before.
	Sources      []string `json:"sources"`
	SourceCutoff string   `json:"sourceCutoff"`
	// Release terms, judged against the raw release name (case-insensitive
	// substring): Required terms must ALL appear, any Blocked term rejects,
	// and each matched Preferred term adds its score to the ranking —
	// positive prefers, negative deprioritizes without blocking.
	Required  []string        `json:"required"`
	Blocked   []string        `json:"blocked"`
	Preferred []PreferredTerm `json:"preferred"`
	// Formats are TRaSH-style custom formats: scored spec bundles judged
	// against the release; matches add their score to the ranking. They
	// live in their own table (Settings → Custom Formats) and are attached
	// at profile load — never part of the profile's own JSON.
	Formats []Format `json:"-"`
	// MinFormatScore is the floor a release's format score (preferred terms
	// plus custom formats) falls below it — the TRaSH-style floor.
	// must reach. Nil is "no floor". Zero is a REAL floor — "nothing that
	// scores negative" is the most common floor there is, and using zero as
	// the off switch made it inexpressible.
	MinFormatScore *int   `json:"minFormatScore"`
	HDR            string `json:"hdr"` // allow | require | block
	MinMBPerMin    int    `json:"minMbPerMin"`
	MaxMBPerMin    int    `json:"maxMbPerMin"`
}

// PreferredTerm is one scored term: releases whose names carry it rank
// score points higher (or lower, when negative).
type PreferredTerm struct {
	Term  string `json:"term"`
	Score int    `json:"score"`
}

// AllowsUnknownQuality reports whether the profile takes releases whose
// name carries no resolution. Off by default: an unlabeled release could be
// anything, and the size band is the only thing left judging it.
func (p *Profile) AllowsUnknownQuality() bool {
	return slices.Contains(p.Qualities, QualityUnknown)
}

// AllowsSource reports whether the profile accepts an encode from this
// source. An empty allow-list accepts anything, unknown included; a
// restricted list demands a recognized source.
func (p *Profile) AllowsSource(src string) bool {
	if len(p.Sources) == 0 {
		return true
	}
	return slices.Contains(p.Sources, src)
}

// CutoffMet reports whether a file at (quality, source) satisfies the
// profile's cutoff. Resolution above the cutoff always satisfies it; at
// the cutoff resolution exactly, a source cutoff (when set) must also be
// met — and a file whose source is unknown counts as below it, so the
// loop keeps hunting until a labeled encode lands.
func (p *Profile) CutoffMet(q, src string) bool {
	have, cut := Rank(q), Rank(p.Cutoff)
	if have != cut {
		return have > cut
	}
	if p.SourceCutoff == "" {
		return true
	}
	return SourceRank(src) >= SourceRank(p.SourceCutoff)
}

// CutoffRank orders profiles by how demanding their cutoff is — resolution
// dominant, source cutoff breaking ties — so sibling dedupe can let the
// most demanding profile carry the search.
func (p *Profile) CutoffRank() int {
	return Rank(p.Cutoff)*10 + SourceRank(p.SourceCutoff)
}

// IsUpgrade reports whether a candidate at (candQ, candSrc) improves on a
// file at (fileQ, fileSrc) under this profile. Without a source cutoff the
// comparison is by resolution alone (exactly the old behavior — no
// re-grabbing a 1080p because its label differs); with one, source breaks
// resolution ties and an unknown file source loses to any known candidate.
func (p *Profile) IsUpgrade(candQ, candSrc, fileQ, fileSrc string) bool {
	cq, fq := Rank(candQ), Rank(fileQ)
	if p.SourceCutoff == "" || cq != fq {
		return cq > fq
	}
	return SourceRank(candSrc) > SourceRank(fileSrc)
}

// Validate rejects a profile that could never grab anything sensible.
func (p *Profile) Validate() error {
	if strings.TrimSpace(p.Name) == "" {
		return errors.New("profile needs a name")
	}
	if len(p.Qualities) == 0 {
		return errors.New("pick at least one quality")
	}
	for _, q := range p.Qualities {
		if q == QualityUnknown {
			continue // a permission, not a resolution — see QualityUnknown
		}
		if Rank(q) == 0 {
			return fmt.Errorf("unknown quality %q", q)
		}
	}
	if !slices.Contains(p.Qualities, p.Cutoff) {
		return errors.New("cutoff must be one of the profile's qualities")
	}
	// "unknown" can be allowed but never aimed at: a cutoff is the point
	// where hunting stops, and an unlabeled release proves nothing about
	// what is on disk.
	if Rank(p.Cutoff) == 0 {
		return errors.New("cutoff must be a resolution, not Unknown")
	}
	for _, src := range p.Sources {
		if SourceRank(src) == 0 {
			return fmt.Errorf("unknown source %q", src)
		}
	}
	if p.SourceCutoff != "" {
		if SourceRank(p.SourceCutoff) == 0 {
			return fmt.Errorf("unknown source cutoff %q", p.SourceCutoff)
		}
		if !p.AllowsSource(p.SourceCutoff) {
			return errors.New("source cutoff must be one of the allowed sources")
		}
	}
	for _, term := range p.Required {
		if strings.TrimSpace(term) == "" {
			return errors.New("required terms cannot be blank")
		}
	}
	for _, term := range p.Blocked {
		if strings.TrimSpace(term) == "" {
			return errors.New("blocked terms cannot be blank")
		}
	}
	for _, pt := range p.Preferred {
		if strings.TrimSpace(pt.Term) == "" {
			return errors.New("preferred terms cannot be blank")
		}
	}
	switch p.HDR {
	case HDRAllow, HDRRequire, HDRBlock:
	default:
		return fmt.Errorf("hdr must be %s, %s, or %s", HDRAllow, HDRRequire, HDRBlock)
	}
	if p.MinMBPerMin < 0 || p.MaxMBPerMin < 0 {
		return errors.New("size limits cannot be negative")
	}
	if p.MaxMBPerMin > 0 && p.MinMBPerMin > p.MaxMBPerMin {
		return errors.New("minimum size exceeds maximum")
	}
	return nil
}

// FromDimensions classifies a video's pixel size onto the quality ladder,
// for files whose NAME says nothing about resolution.
//
// Width dominates, because height doesn't survive letterboxing: a 1080p
// scope film is 1920x800, and judging it on height alone would file it as
// 720p and start hunting an "upgrade" forever. Height is kept as a second
// chance for the rare tall or pillarboxed encode.
//
// Returns "" for a size that means nothing, so a caller can tell "no idea"
// apart from 480p.
func FromDimensions(width, height int) string {
	switch {
	case width <= 0 || height <= 0:
		return ""
	case width >= 3200 || height >= 2000:
		return "2160p"
	case width >= 1800 || height >= 1000:
		return "1080p"
	case width >= 1200 || height >= 700:
		return "720p"
	default:
		return "480p"
	}
}
