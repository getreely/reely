package quality

import (
	"strings"
	"testing"

	"github.com/getreely/reely/internal/parser"
)

// standard is the seeded default: 1080p/720p, cutoff 1080p, upgrades on,
// 8–80 MB/min. For a 148-minute movie that's a 1.2–11.6 GB window.
func standard() *Profile {
	return &Profile{
		Name: "Standard 1080p", Qualities: []string{"1080p", "720p"}, Cutoff: "1080p",
		Upgrades: true, HDR: HDRAllow, MinMBPerMin: 8, MaxMBPerMin: 80,
	}
}

func cand(name string, gb float64) Candidate {
	return Candidate{Result: parser.Parse(name), SizeBytes: int64(gb * (1 << 30))}
}

func TestEvaluate(t *testing.T) {
	movie := Want{RuntimeMin: 148}
	episode := Want{RuntimeMin: 45}

	cases := []struct {
		name     string
		c        Candidate
		p        *Profile
		w        Want
		accepted bool
		reason   string // substring of the rejection
	}{
		{"good web release fits", cand("Movie.2024.1080p.WEB-DL.x265.mkv", 5), standard(), movie, true, ""},
		{"the 20 GB remux is rejected on size", cand("Movie.2024.1080p.REMUX.mkv", 20), standard(), movie, false, "too large"},
		{"potato encode rejected on size floor", cand("Movie.2024.1080p.WEB.x264.mkv", 0.7), standard(), movie, false, "too small"},
		{"quality outside profile", cand("Movie.2024.2160p.WEB.mkv", 8), standard(), movie, false, "not in profile"},
		{"no quality in name", cand("Movie.2024.WEB.mkv", 5), standard(), movie, false, "no recognizable quality"},
		{"episode band scales down", cand("Show.S01E03.1080p.WEB.mkv", 2), standard(), episode, true, ""},
		{"episode too large for 45 min", cand("Show.S01E03.1080p.REMUX.mkv", 6), standard(), episode, false, "too large"},
		{"unknown runtime skips the band", cand("Movie.2024.1080p.WEB.mkv", 20), standard(), Want{}, true, ""},
		{"unknown size skips the band", Candidate{Result: parser.Parse("Movie.2024.1080p.WEB.mkv")}, standard(), movie, true, ""},
	}
	for _, tc := range cases {
		d := Evaluate(tc.c, tc.p, tc.w)
		if d.Accepted != tc.accepted {
			t.Errorf("%s: accepted = %v (%s)", tc.name, d.Accepted, d.Reason)
			continue
		}
		if !tc.accepted && !strings.Contains(d.Reason, tc.reason) {
			t.Errorf("%s: reason = %q, want %q in it", tc.name, d.Reason, tc.reason)
		}
	}
}

func TestSeasonPackDividesByEpisodes(t *testing.T) {
	pack := Want{RuntimeMin: 45, Episodes: 10}
	// 25 GB over ten 45-min episodes ≈ 57 MB/min — inside the band
	if d := Evaluate(cand("Show.S01.1080p.WEB-DL.mkv", 25), standard(), pack); !d.Accepted {
		t.Errorf("season pack rejected: %s", d.Reason)
	}
	// the same 25 GB claimed by a single episode is absurd
	if d := Evaluate(cand("Show.S01E01.1080p.WEB-DL.mkv", 25), standard(), Want{RuntimeMin: 45}); d.Accepted {
		t.Error("25 GB single episode accepted")
	}
}

func TestUpgradeGate(t *testing.T) {
	movie := Want{RuntimeMin: 148, CurrentQuality: "720p"}

	if d := Evaluate(cand("Movie.2024.1080p.WEB.mkv", 5), standard(), movie); !d.Accepted {
		t.Errorf("upgrade 720p→1080p rejected: %s", d.Reason)
	}
	if d := Evaluate(cand("Movie.2024.720p.WEB.mkv", 3), standard(), movie); d.Accepted {
		t.Error("same quality accepted as an upgrade")
	}

	noUp := standard()
	noUp.Upgrades = false
	if d := Evaluate(cand("Movie.2024.1080p.WEB.mkv", 5), noUp, movie); d.Accepted {
		t.Error("upgrade accepted with upgrades off")
	}

	met := Want{RuntimeMin: 148, CurrentQuality: "1080p"}
	if d := Evaluate(cand("Movie.2024.1080p.PROPER.WEB.mkv", 5), standard(), met); d.Accepted {
		t.Error("cutoff already met but release accepted")
	}
}

func TestHDRPolicy(t *testing.T) {
	uhd := &Profile{
		Name: "4K HDR", Qualities: []string{"2160p"}, Cutoff: "2160p",
		Upgrades: true, HDR: HDRRequire,
	}
	w := Want{RuntimeMin: 148}
	if d := Evaluate(cand("Movie.2024.2160p.WEB-DL.HDR10.mkv", 15), uhd, w); !d.Accepted {
		t.Errorf("HDR release rejected by require-HDR profile: %s", d.Reason)
	}
	if d := Evaluate(cand("Movie.2024.2160p.WEB-DL.mkv", 15), uhd, w); d.Accepted {
		t.Error("SDR release accepted by require-HDR profile")
	}

	uhd.HDR = HDRBlock
	if d := Evaluate(cand("Movie.2024.2160p.WEB-DL.DV.mkv", 15), uhd, w); d.Accepted {
		t.Error("HDR release accepted by block-HDR profile")
	}
}

func TestScoreOrdersCandidates(t *testing.T) {
	p := &Profile{
		Name: "any", Qualities: []string{"2160p", "1080p", "720p", "480p"}, Cutoff: "2160p",
		Upgrades: true, HDR: HDRAllow,
	}
	w := Want{RuntimeMin: 100}
	ordered := []Candidate{
		cand("Movie.2024.2160p.REMUX.HDR.mkv", 0),
		cand("Movie.2024.2160p.BluRay.mkv", 0),
		cand("Movie.2024.1080p.BluRay.PROPER.mkv", 0),
		cand("Movie.2024.1080p.BluRay.mkv", 0),
		cand("Movie.2024.1080p.WEB-DL.mkv", 0),
		cand("Movie.2024.720p.HDTV.mkv", 0),
	}
	prev := int(^uint(0) >> 1)
	for _, c := range ordered {
		d := Evaluate(c, p, w)
		if !d.Accepted {
			t.Fatalf("%s rejected: %s", c.Title, d.Reason)
		}
		if d.Score >= prev {
			t.Errorf("score ordering broken at %+v: %d >= %d", c.Result, d.Score, prev)
		}
		prev = d.Score
	}
}

func TestProfileValidate(t *testing.T) {
	good := standard()
	if err := good.Validate(); err != nil {
		t.Errorf("valid profile rejected: %v", err)
	}
	bad := []Profile{
		{Name: "", Qualities: []string{"1080p"}, Cutoff: "1080p", HDR: HDRAllow},
		{Name: "x", Qualities: nil, Cutoff: "1080p", HDR: HDRAllow},
		{Name: "x", Qualities: []string{"999p"}, Cutoff: "999p", HDR: HDRAllow},
		{Name: "x", Qualities: []string{"720p"}, Cutoff: "1080p", HDR: HDRAllow}, // cutoff outside qualities
		{Name: "x", Qualities: []string{"1080p"}, Cutoff: "1080p", HDR: "sometimes"},
		{Name: "x", Qualities: []string{"1080p"}, Cutoff: "1080p", HDR: HDRAllow, MinMBPerMin: 90, MaxMBPerMin: 40},
		{Name: "x", Qualities: []string{"1080p"}, Cutoff: "1080p", HDR: HDRAllow, MinMBPerMin: -1},
	}
	for i, p := range bad {
		if err := p.Validate(); err == nil {
			t.Errorf("bad profile %d validated", i)
		}
	}
}

// sourcePicky is a profile that restricts sources and keeps upgrading
// within 1080p until a bluray lands.
func sourcePicky() *Profile {
	p := standard()
	p.Sources = []string{"webdl", "bluray", "remux"}
	p.SourceCutoff = "bluray"
	return p
}

func TestEvaluateSources(t *testing.T) {
	movie := Want{RuntimeMin: 148}
	webOnDisk := Want{RuntimeMin: 148, CurrentQuality: "1080p", CurrentSource: "webdl"}
	blurayOnDisk := Want{RuntimeMin: 148, CurrentQuality: "1080p", CurrentSource: "bluray"}
	unlabeledOnDisk := Want{RuntimeMin: 148, CurrentQuality: "1080p"}

	cases := []struct {
		name     string
		c        Candidate
		p        *Profile
		w        Want
		accepted bool
		reason   string
	}{
		{"hdtv is not an allowed source", cand("Movie.2024.1080p.HDTV.mkv", 5), sourcePicky(), movie, false, "not an allowed source"},
		{"no source in name with a restricted list", cand("Movie.2024.1080p.x264.mkv", 5), sourcePicky(), movie, false, "no recognizable source"},
		{"bluray passes the source list", cand("Movie.2024.1080p.BluRay.mkv", 8), sourcePicky(), movie, true, ""},
		{"web file upgrades to bluray at the same resolution", cand("Movie.2024.1080p.BluRay.mkv", 8), sourcePicky(), webOnDisk, true, ""},
		{"web file does not re-grab web", cand("Movie.2024.1080p.WEB-DL.mkv", 5), sourcePicky(), webOnDisk, false, "not an upgrade"},
		{"bluray on disk meets the source cutoff", cand("Movie.2024.1080p.BluRay.mkv", 8), sourcePicky(), blurayOnDisk, false, "already met"},
		{"unlabeled file counts as below the source cutoff", cand("Movie.2024.1080p.BluRay.mkv", 8), sourcePicky(), unlabeledOnDisk, true, ""},
		{"without a source cutoff the resolution alone satisfies", cand("Movie.2024.1080p.BluRay.mkv", 8), standard(), webOnDisk, false, "already met"},
		{"unknown source is fine without a source list", cand("Movie.2024.1080p.x264.mkv", 5), standard(), movie, true, ""},
	}
	for _, tc := range cases {
		d := Evaluate(tc.c, tc.p, tc.w)
		if d.Accepted != tc.accepted {
			t.Errorf("%s: accepted = %v (%s)", tc.name, d.Accepted, d.Reason)
			continue
		}
		if !tc.accepted && !strings.Contains(d.Reason, tc.reason) {
			t.Errorf("%s: reason = %q, want %q in it", tc.name, d.Reason, tc.reason)
		}
	}
}

func TestCutoffMet(t *testing.T) {
	p := sourcePicky()
	cases := []struct {
		q, src string
		met    bool
	}{
		{"2160p", "hdtv", true}, // resolution above the cutoff always satisfies
		{"1080p", "remux", true},
		{"1080p", "bluray", true},
		{"1080p", "webdl", false},
		{"1080p", "", false}, // unknown source keeps hunting
		{"720p", "remux", false},
	}
	for _, tc := range cases {
		if got := p.CutoffMet(tc.q, tc.src); got != tc.met {
			t.Errorf("CutoffMet(%s, %q) = %v, want %v", tc.q, tc.src, got, tc.met)
		}
	}
	plain := standard()
	if !plain.CutoffMet("1080p", "") {
		t.Error("plain profile: unlabeled 1080p should meet a 1080p cutoff")
	}
}

func TestFileBetter(t *testing.T) {
	cases := []struct {
		name           string
		nq, ns, oq, os string
		better         bool
	}{
		{"resolution dominates", "2160p", "hdtv", "1080p", "remux", true},
		{"source breaks the tie when both known", "1080p", "bluray", "1080p", "webdl", true},
		{"equal source is not better", "1080p", "webdl", "1080p", "webdl", false},
		{"unknown old source never loses the tie", "1080p", "bluray", "1080p", "", false},
		{"unknown new source never wins the tie", "1080p", "", "1080p", "webdl", false},
	}
	for _, tc := range cases {
		if got := FileBetter(tc.nq, tc.ns, tc.oq, tc.os); got != tc.better {
			t.Errorf("%s: FileBetter = %v, want %v", tc.name, got, tc.better)
		}
	}
}

func TestValidateSources(t *testing.T) {
	p := sourcePicky()
	if err := p.Validate(); err != nil {
		t.Fatalf("valid source profile rejected: %v", err)
	}
	p.Sources = []string{"betamax"}
	if err := p.Validate(); err == nil {
		t.Error("unknown source accepted")
	}
	p = sourcePicky()
	p.SourceCutoff = "hdtv" // not in the allowed list
	if err := p.Validate(); err == nil {
		t.Error("source cutoff outside the allowed sources accepted")
	}
	p = standard()
	p.SourceCutoff = "bluray" // empty source list allows any, cutoff fine
	if err := p.Validate(); err != nil {
		t.Errorf("source cutoff with open source list rejected: %v", err)
	}
}

// namedCand parses like cand but keeps the raw name for term matching.
func namedCand(name string, gb float64) Candidate {
	c := cand(name, gb)
	c.Name = name
	return c
}

func TestEvaluateReleaseTerms(t *testing.T) {
	movie := Want{RuntimeMin: 148}
	blocked := standard()
	blocked.Blocked = []string{"XVID", "HEVC"}
	required := standard()
	required.Required = []string{"x265"}
	preferred := standard()
	preferred.Preferred = []PreferredTerm{{Term: "x265", Score: 60}, {Term: "AAC", Score: -20}}

	cases := []struct {
		name     string
		c        Candidate
		p        *Profile
		accepted bool
		reason   string
	}{
		{"blocked term rejects", namedCand("Movie.2024.1080p.WEB.HEVC.mkv", 5), blocked, false, "blocked in profile"},
		{"clean name passes the blocklist", namedCand("Movie.2024.1080p.WEB.x264.mkv", 5), blocked, true, ""},
		{"missing required term rejects", namedCand("Movie.2024.1080p.WEB.x264.mkv", 5), required, false, "missing required term"},
		{"required term present passes", namedCand("Movie.2024.1080p.WEB.x265.mkv", 5), required, true, ""},
		{"case-insensitive match", namedCand("Movie.2024.1080p.WEB.X265.mkv", 5), required, true, ""},
	}
	for _, tc := range cases {
		d := Evaluate(tc.c, tc.p, movie)
		if d.Accepted != tc.accepted {
			t.Errorf("%s: accepted = %v (%s)", tc.name, d.Accepted, d.Reason)
			continue
		}
		if !tc.accepted && !strings.Contains(d.Reason, tc.reason) {
			t.Errorf("%s: reason = %q, want %q in it", tc.name, d.Reason, tc.reason)
		}
	}

	// preferred terms move the score and are reported back
	plain := Evaluate(namedCand("Movie.2024.1080p.WEB.x264.mkv", 5), preferred, movie)
	boosted := Evaluate(namedCand("Movie.2024.1080p.WEB.x265.mkv", 5), preferred, movie)
	demoted := Evaluate(namedCand("Movie.2024.1080p.WEB.x264.AAC.mkv", 5), preferred, movie)
	if !plain.Accepted || !boosted.Accepted || !demoted.Accepted {
		t.Fatalf("term scoring rejected something: %v %v %v", plain, boosted, demoted)
	}
	if boosted.Score != plain.Score+60 {
		t.Errorf("boost: %d vs plain %d, want +60", boosted.Score, plain.Score)
	}
	if demoted.Score != plain.Score-20 {
		t.Errorf("demote: %d vs plain %d, want -20", demoted.Score, plain.Score)
	}
	if len(boosted.Terms) != 1 || boosted.Terms[0] != "x265" {
		t.Errorf("matched terms = %v, want [x265]", boosted.Terms)
	}
}

// The format-score floor: nil is "no floor" (negative scores only
// deprioritize); a set minimum rejects anything summing below it — and
// ZERO is a real minimum. "Nothing that scores negative" is the most
// common floor there is, and zero-as-off-switch made it unsayable.
func TestMinFormatScore(t *testing.T) {
	floor := func(n int) *int { return &n }
	movie := Want{RuntimeMin: 148}
	p := standard()
	p.Preferred = []PreferredTerm{{Term: "AAC", Score: -20}, {Term: "x265", Score: 60}}
	if d := Evaluate(namedCand("Movie.2024.1080p.WEB.AAC.mkv", 5), p, movie); !d.Accepted {
		t.Fatalf("no floor: negative score must not block: %+v", d)
	}
	p.MinFormatScore = floor(50)
	if d := Evaluate(namedCand("Movie.2024.1080p.WEB.AAC.mkv", 5), p, movie); d.Accepted || !strings.Contains(d.Reason, "below the profile minimum") {
		t.Fatalf("floor on: %+v", d)
	}
	if d := Evaluate(namedCand("Movie.2024.1080p.WEB.x265.mkv", 5), p, movie); !d.Accepted || d.FormatScore != 60 {
		t.Fatalf("above the floor: %+v", d)
	}

	// zero rejects the -20 and admits an exact zero — a floor, not an off
	// switch
	p.MinFormatScore = floor(0)
	if d := Evaluate(namedCand("Movie.2024.1080p.WEB.AAC.mkv", 5), p, movie); d.Accepted {
		t.Fatalf("floor 0 admitted a negative score: %+v", d)
	}
	if d := Evaluate(namedCand("Movie.2024.1080p.WEB.mkv", 5), p, movie); !d.Accepted || d.FormatScore != 0 {
		t.Fatalf("floor 0 must admit a score of exactly 0: %+v", d)
	}
}

// A telesync with 1080p stamped on the name is not a 1080p — pre-retail
// releases reject before anything else gets a say.
func TestEvaluateRejectsPreRetail(t *testing.T) {
	d := Evaluate(cand("The.Odyssey.2026.1080p.TELESYNC.x264", 6), standard(), Want{RuntimeMin: 148})
	if d.Accepted || !strings.Contains(d.Reason, "telesync") {
		t.Fatalf("telesync not rejected: %+v", d)
	}
}

// A tier format ORs its release-group list within the group kind and ANDs
// it with its WEB-DL/WEBRip title rules — a web release from an unlisted
// (or missing) group must not ride in on the source rule alone.
func TestFormatKindsANDTogether(t *testing.T) {
	tier := Format{Name: "WEB Tier 01", Score: 1700, Specs: []FormatSpec{
		{Kind: "group", Value: "^(NTb)$"},
		{Kind: "group", Value: "^(FLUX)$"},
		{Kind: "title", Value: `\bWEB[-_. ]?DL\b`},
		{Kind: "title", Value: `\bWEB[-_. ]?Rip\b`},
	}}
	if !tier.Matches("Show.S01E03.1080p.WEB-DL.DDP5.1.H.264-NTb", "webdl", "1080p", nil) {
		t.Error("listed group on a WEB-DL should match")
	}
	if tier.Matches("Show.S01E03.1080p.WEB-DL.DD5.1.H.264", "webdl", "1080p", nil) {
		t.Error("group-less WEB-DL matched the tier on its source rule alone")
	}
	if tier.Matches("Show.S01E03.1080p.BluRay.x264-NTb", "bluray", "1080p", nil) {
		t.Error("a bluray from a tier group is not a WEB tier release")
	}
}

// Releases whose name carries no resolution are rejected by default, but a
// profile can deliberately take them — the *arr apps' "Unknown" quality.
// Obfuscated usenet posts are the reason it exists.
func TestUnknownQualityIsOptIn(t *testing.T) {
	movie := Want{RuntimeMin: 148}
	nameless := namedCand("aB3xk9-obfuscated-post", 5)

	if d := Evaluate(nameless, standard(), movie); d.Accepted {
		t.Error("unlabeled release accepted by a profile that never allowed it")
	} else if !strings.Contains(d.Reason, "Unknown") {
		t.Errorf("reason %q does not mention the Unknown setting", d.Reason)
	}

	takesUnknown := standard()
	takesUnknown.Qualities = append(takesUnknown.Qualities, "480p", QualityUnknown)
	d := Evaluate(nameless, takesUnknown, movie)
	if !d.Accepted {
		t.Fatalf("unlabeled release rejected with Unknown allowed: %s", d.Reason)
	}
	// it ranks under every real resolution — an unlabeled file is the last
	// thing worth grabbing, never the pick over a known one
	known := Evaluate(cand("Movie.2024.480p.WEB.mkv", 2), takesUnknown, movie)
	if !known.Accepted || d.Score >= known.Score {
		t.Fatalf("unknown scored %d, 480p scored %d — unknown must rank last", d.Score, known.Score)
	}

	// the size band is the only guard left, so it still applies
	if d := Evaluate(namedCand("aB3xk9-obfuscated-post", 40), takesUnknown, movie); d.Accepted {
		t.Error("a 40 GB unlabeled release passed the size band")
	}

	// and it never displaces a file already on disk
	onDisk := Want{RuntimeMin: 148, CurrentQuality: "720p"}
	if d := Evaluate(nameless, takesUnknown, onDisk); d.Accepted {
		t.Error("unlabeled release accepted as an upgrade over a 720p file")
	}
}

func TestUnknownCannotBeTheCutoff(t *testing.T) {
	p := standard()
	p.Qualities = append(p.Qualities, QualityUnknown)
	if err := p.Validate(); err != nil {
		t.Fatalf("allowing Unknown broke the profile: %v", err)
	}
	p.Cutoff = QualityUnknown
	if err := p.Validate(); err == nil {
		t.Fatal("Unknown accepted as a cutoff")
	}
}

// The bug this guards: a file on disk whose quality was never recognized
// left CurrentQuality empty, the upgrade gate was keyed on that string,
// and so the episode read as MISSING — every matching release passed and
// was grabbed, every pass, forever.
func TestFileWithUnreadableQualityIsNotTreatedAsMissing(t *testing.T) {
	p := standard()
	d := Evaluate(
		cand("Show.S01E01.1080p.WEB.x264.mkv", 2),
		p,
		Want{RuntimeMin: 45, HasFile: true, CurrentQuality: "", CurrentSource: ""},
	)
	if d.Accepted {
		t.Fatalf("a file already on disk was re-grabbed because its quality is unrecorded: %+v", d)
	}
}

// The same Want without a file is genuinely missing and must still grab.
func TestMissingFileStillGrabs(t *testing.T) {
	p := standard()
	d := Evaluate(
		cand("Show.S01E01.1080p.WEB.x264.mkv", 2),
		p,
		Want{RuntimeMin: 45, HasFile: false},
	)
	if !d.Accepted {
		t.Fatalf("a missing episode was not grabbed: %s", d.Reason)
	}
}

// A readable file below cutoff still upgrades — the fix must not turn the
// upgrade path off.
func TestReadableFileBelowCutoffStillUpgrades(t *testing.T) {
	p := standard()
	d := Evaluate(
		cand("Show.S01E01.1080p.WEB.x264.mkv", 2),
		p,
		Want{RuntimeMin: 45, HasFile: true, CurrentQuality: "720p", CurrentSource: "webdl"},
	)
	if !d.Accepted {
		t.Fatalf("1080p over a 720p file was refused: %s", d.Reason)
	}
}

// An SD-era release ("HDTV, no resolution token") judges as 480p, exactly
// as Sonarr files it under SDTV. The profile decides from there: a
// 1080p/720p profile turns it away by name — "not in profile" is an
// answer someone can act on, where "no recognizable quality" was a shrug.
func TestSDInferenceJudgesLikeSonarr(t *testing.T) {
	episode := Want{RuntimeMin: 45}
	release := "Sister.Wives.S08E11.Tell.All.Part.One.HDTV.x264-NY2.mkv"

	hd := standard() // 1080p/720p only
	if d := Evaluate(cand(release, 0.5), hd, episode); d.Accepted || !strings.Contains(d.Reason, "not in profile") {
		t.Fatalf("HD-only profile: %+v — want a clean not-in-profile, not an unknown-quality shrug", d)
	}

	// an SD-friendly profile also loosens the size floor — a real SD
	// episode is a few hundred MB, under the default 8 MB/min band
	sd := standard()
	sd.Qualities = []string{"1080p", "720p", "480p"}
	sd.MinMBPerMin = 2
	if d := Evaluate(cand(release, 0.3), sd, episode); !d.Accepted {
		t.Fatalf("a profile allowing 480p refused the SD release: %s", d.Reason)
	}
}

// The Unknown quality box is the opt-in for markerless names on BOTH
// axes: a profile that takes unclassifiable releases on resolution takes
// a name with no source token past a restricted source list too — the
// reported case, Yes.Dear.S03E09.HBTV under a profile with Unknown and
// 480p checked and sources restricted. A RECOGNIZED but disallowed
// source stays rejected regardless.
func TestUnknownQualityOptInCoversMarkerlessSources(t *testing.T) {
	withUnknown := sourcePicky()
	withUnknown.Qualities = append(withUnknown.Qualities, QualityUnknown)

	// no quality, no source: fully unclassifiable, Unknown covers it
	d := Evaluate(Candidate{
		Result: parser.Parse("Yes.Dear.S03E09.HBTV"), Name: "Yes.Dear.S03E09.HBTV",
		SizeBytes: 180 << 20,
	}, withUnknown, Want{RuntimeMin: 21})
	if !d.Accepted {
		t.Fatalf("markerless release rejected despite Unknown: %s", d.Reason)
	}

	// known quality, no source: same opt-in applies on the source axis
	if d := Evaluate(cand("Movie.2024.1080p.x264.mkv", 5), withUnknown, Want{RuntimeMin: 148}); !d.Accepted {
		t.Fatalf("1080p with no source rejected despite Unknown: %s", d.Reason)
	}

	// a recognized source that's not on the list is a real verdict
	if d := Evaluate(cand("Movie.2024.1080p.HDTV.mkv", 5), withUnknown, Want{RuntimeMin: 148}); d.Accepted {
		t.Fatal("hdtv accepted despite the restricted source list")
	}

	// and without the Unknown opt-in, the strict behavior stands
	if d := Evaluate(cand("Movie.2024.1080p.x264.mkv", 5), sourcePicky(), Want{RuntimeMin: 148}); d.Accepted {
		t.Fatal("markerless source accepted without the Unknown opt-in")
	}
}

// A language-kind format judges the parsed audio tags: its own tag or a
// MULTI/dual-audio release hits, untagged audio never does.
func TestLanguageFormatScoring(t *testing.T) {
	p := standard()
	p.Formats = []Format{{
		Name: "French audio", Score: -100,
		Specs: []FormatSpec{{Kind: "language", Value: "french"}},
	}}
	movie := Want{RuntimeMin: 148}

	if d := Evaluate(cand("Movie.2024.MULTI.1080p.WEB-DL.mkv", 5), p, movie); d.FormatScore != -100 {
		t.Errorf("MULTI release format score = %d, want -100", d.FormatScore)
	}
	if d := Evaluate(cand("Movie.2024.FRENCH.1080p.WEB-DL.mkv", 5), p, movie); d.FormatScore != -100 {
		t.Errorf("FRENCH release format score = %d, want -100", d.FormatScore)
	}
	if d := Evaluate(cand("Movie.2024.1080p.WEB-DL.mkv", 5), p, movie); d.FormatScore != 0 {
		t.Errorf("untagged release format score = %d, want 0", d.FormatScore)
	}

	if err := ValidateFormat(Format{Name: "bad", Specs: []FormatSpec{{Kind: "language", Value: "klingon"}}}); err == nil {
		t.Error("unknown language in a format accepted")
	}
}
