// Package parser turns release and file names into structured guesses:
// which title, which year, which episode, what quality. It is the first
// stage of both the importer (files on disk) and the grab loop (indexer
// release names) — everything downstream trusts its output, so it prefers
// returning nothing over returning nonsense.
package parser

import (
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Episode is one parsed episode reference. Multi-episode files carry the
// full span (EpisodeEnd >= Episode).
type Episode struct {
	Season     int
	Episode    int
	EpisodeEnd int
	// Title is the run between the episode marker and the first technical
	// token — usually the episode's own title ("Mayfield.Wasdin"), "" when
	// the name goes straight to quality tags. It exists to catch a release
	// that names a DIFFERENT episode than the one being matched: two shows
	// sharing a title ("Wife Swap" 2004 and 2019) produce identical
	// Title+SxxExx forms, and the episode words are the tiebreaker.
	Title string
}

// Result is a parsed name. Kind is "movie", "episode", or "season" (a
// season pack: S01 with no episode); a name with no episode or season
// marker parses as a movie candidate.
type Result struct {
	Kind    string
	Title   string
	Year    int
	Ep      Episode
	Quality string // 2160p / 1080p / 720p / 480p, "" when absent
	// QualityInferred marks a quality read from scene convention rather
	// than a marker in the name — an SD-era TV/DVD/web release carries no
	// resolution token. A release can be judged on it; a file in hand
	// deserves a probe instead.
	QualityInferred bool
	Source          string // remux / bluray / webdl / webrip / hdtv / dvd, "" when absent
	Proper          bool
	HDR             bool // HDR10(+), Dolby Vision, or a bare HDR marker
	// PreRetail marks cam/telesync/screener releases — theater recordings
	// and leaked discs that stamp a resolution on the name without being
	// that quality. The scorer rejects them outright.
	PreRetail bool
	// Languages lists the audio-language tags the name carries, in order of
	// appearance: "Multi", "Dual Audio", a concrete language ("French"), or
	// "VOSTFR" (original audio, French subs). Empty means no tag — scene
	// convention for a release in its original audio. Only the technical
	// tail after the title is scanned, so "The French Dispatch" never reads
	// as a French dub.
	Languages []string
}

var (
	// S01E02, s1e2, S01E02E03, S01E02-E04, 1x02
	reEpisode = regexp.MustCompile(`(?i)\b(?:s(\d{1,2})[ ._-]?e(\d{1,3})(?:[-_. ]?e?(\d{1,3}))?|(\d{1,2})x(\d{2,3}))\b`)
	// a 19xx/20xx year, wrapped in (), [], dots or spaces
	reYear = regexp.MustCompile(`\b(19\d{2}|20\d{2})\b`)
	// Beyond the four stored rungs, the vocabulary knows every marker that
	// MEANS one of them — Sonarr's resolution groups, token for token.
	// This matters doubly since the SD inference: a name whose marker we
	// can't read looks markerless, and "1440p.WEB-DL" or "1080i.HDTV"
	// would be filed as 480p. Pixel-dimension forms and the 4K phrasings
	// are release-name currency too.
	reQuality = regexp.MustCompile(`(?i)\b(2160p|3840x2160|uhd|4k[-_. ]?(?:hevc|bd|h265)|(?:hevc|bd|h265)[-_. ]?4k|4kto1080p|1080p|1080i|1920x1080|1440p|fhd|720p|1280x720|960p|576p|540p|480p|480i|640x480|848x480|360p)\b`)
	reSource  = regexp.MustCompile(`(?i)\b(remux|blu-?ray|bdrip|brrip|bd-?(?:25|50)|bdiso|bdmux|br[-_. ]?disk|hd[-_. ]?dvd|web-?dl|webrip|hdtv|dvdrip|dvd)\b`)
	reProper  = regexp.MustCompile(`(?i)\b(proper|repack)\b`)
	// HDR10, HDR10+, plain HDR, Dolby Vision in its spellings. "DV" alone is
	// the scene's Dolby Vision tag; word bounds keep it out of DVDRip.
	reHDR = regexp.MustCompile(`(?i)\b(hdr10\+?|hdr|dolby[ ._-]?vision|dovi|dv)\b`)
	// a bare season marker (S01, Season 2) — a pack, not one episode
	reSeason = regexp.MustCompile(`(?i)\b(?:s(\d{1,2})|season[ ._-]?(\d{1,2}))\b`)
	// pre-retail junk: the cam/telesync/telecine/screener family. These
	// names often stamp a resolution ("1080p.TELESYNC") that says nothing
	// about the picture. The .ts container extension is stripped before
	// matching.
	//
	// Split by how much a token proves. The long forms name nothing but a
	// theater recording. The short ones are ordinary words and initials
	// that turn up in real titles — "Body Cam" is a police show — so they
	// are read only where a technical token belongs and only when nothing
	// retail contradicts them.
	reJunkStrong = regexp.MustCompile(`(?i)\b(camrip|hd-?cam|telesync|hd-?ts|telecine|hd-?tc|dvd-?scr(eener)?|screener|workprint)\b`)
	reJunkWeak   = regexp.MustCompile(`(?i)\b(cam|ts|tc)\b`)
	// codec/audio tokens and scene/service/language tags: not parsed into
	// fields, but they mark where an episode-title segment ENDS — they sit
	// between the title words and the quality block in most names.
	reTech = regexp.MustCompile(`(?i)\b(x26[45]|h[ ._-]?26[45]|hevc|av1|xvid|divx|aac(?:[ ._-]?2[ ._-]?0)?|dd[p+]?[ ._-]?[2571](?:[ ._-]?[01])?|ac3|eac3|dts(?:[ ._-]?hd)?|atmos|truehd|10bit|8bit)\b`)
	reTags = regexp.MustCompile(`(?i)\b(internal|limited|extended|unrated|uncensored|remastered|complete|multi|dual[ ._-]?audio|subbed|dubbed|german|french|english|spanish|italian|nordic|dutch|polish|swedish|danish|norwegian|finnish|russian|hindi|korean|japanese|vostfr|amzn|nf|dsnp|hulu|hmax|max|atvp|pcok|pmtp|stan|crav|red)\b`)
	// audio-language tags, parsed into Result.Languages. A subset of reTags:
	// only tokens that say something about the audio — MULTI (several
	// tracks), dual-audio, the concrete languages, TRUEFRENCH (a French
	// dub's "not the Quebec one" form), and VOSTFR (original audio, French
	// subs). Deliberately absent: SUBBED/DUBBED (no language named) and
	// NORDIC (a subs region, not an audio track).
	reLanguage = regexp.MustCompile(`(?i)\b(multi|dual[ ._-]?audio|true[ ._-]?french|vostfr|german|french|english|spanish|italian|dutch|polish|swedish|danish|norwegian|finnish|russian|hindi|korean|japanese|chinese)\b`)
)

// langNames maps each matched token (compacted) onto its display name.
var langNames = map[string]string{
	"multi": "Multi", "dualaudio": "Dual Audio",
	"truefrench": "French", "vostfr": "VOSTFR",
	"german": "German", "french": "French", "english": "English",
	"spanish": "Spanish", "italian": "Italian", "dutch": "Dutch",
	"polish": "Polish", "swedish": "Swedish", "danish": "Danish",
	"norwegian": "Norwegian", "finnish": "Finnish", "russian": "Russian",
	"hindi": "Hindi", "korean": "Korean", "japanese": "Japanese",
	"chinese": "Chinese",
}

// parseLanguages reads the audio-language tags out of a name's technical
// tail — never the title span. Duplicates collapse (TRUEFRENCH and FRENCH
// both say French); appearance order is kept.
func parseLanguages(tail string) []string {
	var out []string
	for _, m := range reLanguage.FindAllString(tail, -1) {
		if name := langNames[compactToken(m)]; name != "" && !slices.Contains(out, name) {
			out = append(out, name)
		}
	}
	return out
}

// videoExt lists the container extensions worth stripping. Only these:
// indexer release names have no extension, and blindly dropping the last
// dot-segment would eat their final token ("...COMPLETE.720p" → 720p gone).
var videoExt = map[string]bool{
	".mkv": true, ".mp4": true, ".m4v": true, ".avi": true, ".mov": true,
	".wmv": true, ".ts": true, ".webm": true, ".mpg": true, ".mpeg": true,
}

// Parse reads a file or release name. A video extension is dropped,
// separators normalized; the title is everything before the episode marker
// (shows) or the year (movies).
func Parse(name string) Result {
	base := filepath.Base(name)
	if videoExt[strings.ToLower(filepath.Ext(base))] {
		base = strings.TrimSuffix(base, filepath.Ext(base))
	}
	var r Result

	if m := reEpisode.FindStringSubmatchIndex(base); m != nil {
		r.Kind = "episode"
		groups := reEpisode.FindStringSubmatch(base)
		if groups[1] != "" { // SxxEyy form
			r.Ep.Season = atoi(groups[1])
			r.Ep.Episode = atoi(groups[2])
			r.Ep.EpisodeEnd = r.Ep.Episode
			if groups[3] != "" {
				r.Ep.EpisodeEnd = atoi(groups[3])
			}
		} else { // 1x02 form
			r.Ep.Season = atoi(groups[4])
			r.Ep.Episode = atoi(groups[5])
			r.Ep.EpisodeEnd = r.Ep.Episode
		}
		title := base[:m[0]]
		// a year right before the marker belongs to the show name:
		// "Severance (2022) - S01E01" — keep it as the disambiguating year
		if y := reYear.FindString(title); y != "" {
			r.Year = atoi(y)
		}
		r.Title = cleanTitle(stripYear(title))
		r.Ep.Title = episodeTitleSegment(base[m[1]:])
		r.Languages = parseLanguages(base[m[1]:])
		r.readTechnical(base[m[1]:], base)
		return r
	}

	// no episode marker — a bare season token makes it a pack
	if m := reSeason.FindStringSubmatchIndex(base); m != nil {
		r.Kind = "season"
		groups := reSeason.FindStringSubmatch(base)
		num := groups[1]
		if num == "" {
			num = groups[2]
		}
		r.Ep.Season = atoi(num)
		title := base[:m[0]]
		if y := reYear.FindString(title); y != "" {
			r.Year = atoi(y)
		}
		r.Title = cleanTitle(stripYear(title))
		r.Languages = parseLanguages(base[m[1]:])
		r.readTechnical(base[m[1]:], base)
		return r
	}

	r.Kind = "movie"
	// cut is where the title span ends — everything after it is the
	// technical tail, the only region language tags are read from
	cut := len(base)
	// the LAST year wins ("2001 A Space Odyssey 1968" → 1968), and the title
	// is everything before it
	if ms := reYear.FindAllStringIndex(base, -1); ms != nil {
		last := ms[len(ms)-1]
		// a year at position 0 is the title ("1917"), not a release year —
		// unless another year follows, which the "last wins" rule handled
		if last[0] > 0 {
			r.Year = atoi(base[last[0]:last[1]])
			cut = last[0]
		}
	}
	// quality/source tokens sometimes precede the year-less title's tail
	if i := reQuality.FindStringIndex(base[:cut]); i != nil {
		cut = i[0]
	}
	r.Title = cleanTitle(base[:cut])
	r.Languages = parseLanguages(base[cut:])
	r.readTechnical(base[cut:], base)
	return r
}

// readTechnical fills in everything a release says about the FILE, read
// from its technical tail — what follows the episode marker, or the year.
// That is where source, resolution, edition and pre-retail tokens live,
// and a title is not evidence about a file: "Body Cam" is not a cam,
// "Dolby Vision" is not an HDR grade, and a show called "Proper" is not a
// re-release of itself. Every flag is read from the same span, so none of
// them can disagree about where the name stops being a name.
//
// A name that identifies no tail at all — no marker, no year, no quality
// token — is read whole, which is how all of this behaved before there
// was a split to draw.
func (r *Result) readTechnical(tail, whole string) {
	if strings.Trim(tail, "._- ") == "" {
		tail = whole
	}
	r.Quality = normalizeQuality(reQuality.FindString(tail))
	r.Source = normalizeSource(reSource.FindString(tail))
	r.Proper = reProper.MatchString(tail)
	r.HDR = reHDR.MatchString(tail)
	r.PreRetail = preRetail(tail)
	// Scene convention: HD releases carry their resolution in the name, so
	// a name WITHOUT a marker is read by what its source token implies —
	// the same defaults Sonarr and Radarr apply (SDTV for markerless TV,
	// 480p for the rip tokens, 720p for a bare BluRay, the disc image and
	// remux tokens as full HD). The flag lets a library scan prefer the
	// file's own pixels over any of it.
	if r.Quality == "" {
		if rung := inferredRung(reSource.FindString(tail)); rung != "" {
			r.Quality = rung
			r.QualityInferred = true
		}
	}
}

// reRetail is what a release names when it is a retail copy. Its own
// pattern rather than reSource: a bare "WEB" belongs here, where reSource
// deliberately leaves it out because it does not say WEB-DL from WEBRip —
// a distinction that matters for quality and not at all for whether the
// thing was filmed in a cinema. DVD stays out: a DVD screener is both.
var reRetail = regexp.MustCompile(`(?i)\b(web-?dl|webrip|web|blu-?ray|bdrip|brrip|remux|hdtv)\b`)

// preRetail reads the cam/telesync markers out of a release's TECHNICAL
// tail — what follows the episode marker, or the year — because that is
// where a source token belongs. Read against the whole name instead, the
// police show "Body Cam" had every one of its releases rejected as a
// theater recording on the strength of its own title.
//
// The short forms are held to the further test that nothing retail
// contradicts them: "Body.Cam.S11E08.480p.Cam.WEB.x264" carries a stray
// "Cam" in its tail and is plainly a web rip.
func preRetail(tail string) bool {
	if reJunkStrong.MatchString(tail) {
		return true
	}
	return reJunkWeak.MatchString(tail) && !reRetail.MatchString(tail)
}

// episodeTitleSegment carves the episode's own title out of what follows
// the SxxExx marker: everything up to the first recognized technical
// token, group tail stripped. "" when the marker runs straight into tags.
func episodeTitleSegment(rest string) string {
	cut := len(rest)
	for _, re := range []*regexp.Regexp{reQuality, reSource, reYear, reProper, reHDR, reJunkStrong, reJunkWeak, reTech, reTags} {
		if i := re.FindStringIndex(rest); i != nil && i[0] < cut {
			cut = i[0]
		}
	}
	rest = rest[:cut]
	// a trailing -GROUP only survives here when no technical token followed
	// the title; same rule as release groups elsewhere — no dots or spaces
	if i := strings.LastIndex(rest, "-"); i >= 0 && i < len(rest)-1 && !strings.ContainsAny(rest[i+1:], " .") {
		rest = rest[:i]
	}
	return cleanTitle(rest)
}

func stripYear(s string) string {
	if ms := reYear.FindAllStringIndex(s, -1); ms != nil {
		last := ms[len(ms)-1]
		if last[0] > 0 {
			return s[:last[0]]
		}
	}
	return s
}

func cleanTitle(s string) string {
	s = strings.NewReplacer(".", " ", "_", " ", "(", " ", ")", " ", "[", " ", "]", " ").Replace(s)
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "-– ")
	// collapse runs of spaces left by the separator swap
	return strings.Join(strings.Fields(s), " ")
}

// normalizeQuality maps every recognized marker onto its stored rung,
// mirroring Sonarr's grouping: interlaced and pixel-dimension forms join
// their progressive class, 1440p/FHD read as the 1080 class (claiming
// less, not more), and everything SD-class lands on 480p — the rung reads
// as "SD", exactly as Sonarr's SDTV does.
func normalizeQuality(q string) string {
	switch l := strings.ToLower(q); l {
	case "":
		return ""
	case "2160p", "3840x2160", "uhd":
		// bare UHD is the 2160 class — "UHD.BluRay" names carry no 2160p
		// token, and both *arrs map UHD there via their alternative regex
		return "2160p"
	case "4kto1080p":
		return "1080p" // an upscale label; the content class is 1080
	case "1080p", "1080i", "1920x1080", "1440p", "fhd":
		return "1080p"
	case "720p", "1280x720", "960p":
		return "720p"
	case "480p", "480i", "576p", "540p", "640x480", "848x480", "360p":
		return "480p"
	default:
		// the 4K phrasings ("4K UHD", "HEVC-4K") arrive as the matched
		// phrase rather than a fixed token
		if strings.Contains(l, "4k") {
			return "2160p"
		}
		return l
	}
}

// compactToken strips the separators a token may be written with, so one
// switch covers BLU-RAY, Blu.Ray and BluRay alike.
func compactToken(s string) string {
	return strings.ToLower(strings.NewReplacer("-", "", "_", "", ".", "", " ", "").Replace(s))
}

func normalizeSource(s string) string {
	switch compactToken(s) {
	case "remux":
		return "remux"
	case "bluray", "bdrip", "brrip", "bd25", "bd50", "bdiso", "bdmux", "brdisk", "hddvd":
		return "bluray"
	case "webdl":
		return "webdl"
	case "webrip":
		return "webrip"
	case "hdtv":
		return "hdtv"
	case "dvdrip", "dvd":
		return "dvd"
	}
	return ""
}

// inferredRung is the resolution a markerless name of this source token
// implies, following Radarr's own defaults: rip tokens (BDRip, BRRip) are
// the SD-era convention; a bare BluRay or HD-DVD name defaults to 720p; a
// disc image or a remux IS the full-HD article (UHD discs carry a UHD
// token, caught as a real marker before this runs); TV, DVD and web
// without a marker are standard definition. Empty means no convention to
// lean on.
func inferredRung(token string) string {
	switch compactToken(token) {
	case "bdrip", "brrip", "dvdrip", "dvd", "hdtv", "webdl", "webrip":
		return "480p"
	case "bluray", "hddvd":
		return "720p"
	case "bd25", "bd50", "bdiso", "bdmux", "brdisk", "remux":
		return "1080p"
	}
	return ""
}

func atoi(s string) int { n, _ := strconv.Atoi(s); return n }
