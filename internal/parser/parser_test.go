package parser

import "testing"

func TestParseEpisodes(t *testing.T) {
	cases := []struct {
		in            string
		title         string
		year, s, e, n int
		quality       string
	}{
		{"Severance - S02E07 - Chikhai Bardo.mkv", "Severance", 0, 2, 7, 7, ""},
		{"Severance (2022) - S01E01 - Good News About Hell.mkv", "Severance", 2022, 1, 1, 1, ""},
		{"severance.s02e07.1080p.web.h264-group.mkv", "severance", 0, 2, 7, 7, "1080p"},
		{"The.Bear.S03E01E02.2160p.WEB-DL.mkv", "The Bear", 0, 3, 1, 2, "2160p"},
		{"Slow Horses 1x05.mp4", "Slow Horses", 0, 1, 5, 5, ""},
		{"Andor.S02E03-E04.REPACK.720p.mkv", "Andor", 0, 2, 3, 4, "720p"},
	}
	for _, c := range cases {
		r := Parse(c.in)
		if r.Kind != "episode" {
			t.Errorf("%s: kind = %q, want episode", c.in, r.Kind)
			continue
		}
		if r.Title != c.title || r.Year != c.year || r.Ep.Season != c.s || r.Ep.Episode != c.e || r.Ep.EpisodeEnd != c.n || r.Quality != c.quality {
			t.Errorf("%s: got %+v", c.in, r)
		}
	}
}

func TestParseMovies(t *testing.T) {
	cases := []struct {
		in      string
		title   string
		year    int
		quality string
		source  string
	}{
		{"Dune Part Two (2024) [2160p].mkv", "Dune Part Two", 2024, "2160p", ""},
		{"Oppenheimer.2023.1080p.BluRay.x264-GROUP.mkv", "Oppenheimer", 2023, "1080p", "bluray"},
		{"Interstellar (2014).mkv", "Interstellar", 2014, "", ""},
		{"1917 (2019).mkv", "1917", 2019, "", ""},
		{"1917.mkv", "1917", 0, "", ""},
		{"2001 A Space Odyssey 1968 720p.mkv", "2001 A Space Odyssey", 1968, "720p", ""},
		{"Sinners.2025.2160p.WEB-DL.DV.HDR10.mkv", "Sinners", 2025, "2160p", "webdl"},
	}
	for _, c := range cases {
		r := Parse(c.in)
		if r.Kind != "movie" {
			t.Errorf("%s: kind = %q, want movie", c.in, r.Kind)
			continue
		}
		if r.Title != c.title || r.Year != c.year || r.Quality != c.quality || r.Source != c.source {
			t.Errorf("%s: got %+v", c.in, r)
		}
	}
}

func TestProperFlag(t *testing.T) {
	if !Parse("Show.S01E01.PROPER.1080p.mkv").Proper {
		t.Error("PROPER not detected")
	}
	if Parse("Movie (2024).mkv").Proper {
		t.Error("false positive PROPER")
	}
}

func TestParseSeasonPacks(t *testing.T) {
	cases := []struct {
		in      string
		title   string
		season  int
		quality string
	}{
		{"Breaking.Bad.S02.1080p.BluRay.x264", "Breaking Bad", 2, "1080p"},
		{"Severance (2022) Season 1 2160p WEB-DL", "Severance", 1, "2160p"},
		{"The.Wire.S05.COMPLETE.720p", "The Wire", 5, "720p"},
	}
	for _, c := range cases {
		r := Parse(c.in)
		if r.Kind != "season" {
			t.Errorf("%s: kind = %q, want season", c.in, r.Kind)
			continue
		}
		if r.Title != c.title || r.Ep.Season != c.season || r.Quality != c.quality {
			t.Errorf("%s: got %+v", c.in, r)
		}
	}
	// an episode marker outranks the season token; a plain movie stays a movie
	if r := Parse("Show.S01E02.720p.mkv"); r.Kind != "episode" {
		t.Errorf("S01E02 parsed as %q", r.Kind)
	}
	if r := Parse("Oppenheimer.2023.1080p.BluRay.mkv"); r.Kind != "movie" {
		t.Errorf("movie parsed as %q", r.Kind)
	}
}

func TestHDRFlag(t *testing.T) {
	hdr := []string{
		"Sinners.2025.2160p.WEB-DL.DV.HDR10.mkv",
		"Movie.2024.2160p.HDR.WEBRip.mkv",
		"Movie.2024.2160p.HDR10+.WEB-DL.mkv",
		"Show.S01E01.2160p.Dolby.Vision.mkv",
		"Show.S01E01.2160p.DoVi.WEB.mkv",
	}
	for _, name := range hdr {
		if !Parse(name).HDR {
			t.Errorf("%s: HDR not detected", name)
		}
	}
	sdr := []string{
		"Oppenheimer.2023.1080p.BluRay.x264-GROUP.mkv", // no marker
		"Movie.2023.DVDRip.XviD.mkv",                   // DV inside DVDRip must not count
		"Show.S01E01.HDTV.x264.mkv",                    // HDTV is not HDR
	}
	for _, name := range sdr {
		if Parse(name).HDR {
			t.Errorf("%s: false positive HDR", name)
		}
	}
}

// Pre-retail junk — cam, telesync, telecine, screeners — flags even when
// the name stamps a resolution on itself. Retail tokens that merely look
// close (DTS audio, HDTV) must not trip it.
func TestPreRetailFlag(t *testing.T) {
	junk := []string{
		"The.Odyssey.2026.1080p.TELESYNC.x264",
		"Movie.2025.1080p.HDCAM.mkv",
		"Movie.2025.CAM.x264",
		"Movie.2025.720p.HD-TS.mkv",
		"Movie.2025.1080p.TS.x265",
		"Movie.2025.TELECINE.720p",
		"Movie.2025.DVDSCR.XviD",
		"Movie.2025.1080p.SCREENER",
	}
	for _, name := range junk {
		if !Parse(name).PreRetail {
			t.Errorf("%s: pre-retail not flagged", name)
		}
	}
	clean := []string{
		"Movie.2025.1080p.BluRay.DTS-HD.MA.5.1.x264-GROUP.mkv",
		"Show.S01E01.720p.HDTV.x264",
		"Movie.2025.1080p.WEB-DL.DDP5.1.mkv",
		"recording.ts", // the container extension, not a telesync tag
	}
	for _, name := range clean {
		if Parse(name).PreRetail {
			t.Errorf("%s: falsely flagged pre-retail", name)
		}
	}
}

// Everything a release says about the FILE is read from its technical
// tail, so no flag can disagree with another about where the name stops
// being a name. A title is not evidence: "Body Cam" is not a cam,
// "Dolby Vision" is not an HDR grade, and a show called "Proper" is not
// a re-release of itself.
func TestTechnicalFlagsIgnoreTheTitle(t *testing.T) {
	for _, c := range []struct {
		name string
		want Result
	}{
		{"Proper.Manners.S01E02.1080p.WEB.x264-GRP", Result{Title: "Proper Manners"}},
		{"The.Repack.S01E01.720p.HDTV.x264-GRP", Result{Title: "The Repack", Source: "hdtv"}},
		{"Dolby.Vision.S01E01.1080p.WEB-GRP", Result{Title: "Dolby Vision"}},
		{"DV.S01E01.1080p.WEB.x264-GRP", Result{Title: "DV"}},
		{"Body.Cam.S11E08.1080p.WEB.h264-CBFM", Result{Title: "Body Cam"}},
	} {
		p := Parse(c.name)
		if p.Proper || p.HDR || p.PreRetail {
			t.Errorf("%s: title read as a technical tag (proper=%v hdr=%v preRetail=%v)",
				c.name, p.Proper, p.HDR, p.PreRetail)
		}
		if p.Title != c.want.Title {
			t.Errorf("%s: title = %q, want %q", c.name, p.Title, c.want.Title)
		}
	}

	// and the same tags in the tail, where they belong, still read
	for _, c := range []struct {
		name                   string
		proper, hdr, preRetail bool
	}{
		{"Show.S01E01.PROPER.1080p.WEB-GRP", true, false, false},
		{"Show.S01E01.REPACK.720p.HDTV.x264", true, false, false},
		{"Show.S01E01.1080p.WEB.DV.HDR10-GRP", false, true, false},
		{"Movie.2025.1080p.BluRay.HDR10.x265-GRP", false, true, false},
		{"Movie.2025.PROPER.1080p.WEB-DL", true, false, false},
		{"The.Cam.Show.S02E03.HDCAM-GRP", false, false, true},
		{"Dolby.Vision.S01E01.PROPER.1080p.WEB.HDR-GRP", true, true, false},
	} {
		p := Parse(c.name)
		if p.Proper != c.proper || p.HDR != c.hdr || p.PreRetail != c.preRetail {
			t.Errorf("%s: proper=%v hdr=%v preRetail=%v, want %v/%v/%v",
				c.name, p.Proper, p.HDR, p.PreRetail, c.proper, c.hdr, c.preRetail)
		}
	}
}

// The markers are technical tokens and are read where technical tokens
// live — after the episode marker, or after the year. Read against the
// whole name, the police show "Body Cam" had every release it has ever
// had rejected as a theater recording on the strength of its own title.
func TestPreRetailIgnoresTheTitle(t *testing.T) {
	clean := []string{
		"Body.Cam.S11E08.1080p.WEB.h264-CBFM",
		"Body.Cam.S11E08.720p.WEB.H264-JFF",
		"Body.Cam.S11E08.Officer.Down.1080p.WEB.h264-CBFM",
		"Body.Cam.2020.1080p.WEB-DL.x264-GRP", // the film of the same name
		"Body.Cam.S11.1080p.WEB.h264-CBFM",    // and its season packs
		// a stray short form in the tail of something plainly retail: a
		// theater recording is not also a web rip
		"Body.Cam.S11E08.480p.Cam.WEB.x264-RMTeam",
	}
	for _, name := range clean {
		if p := Parse(name); p.PreRetail {
			t.Errorf("%s: falsely flagged pre-retail (title %q)", name, p.Title)
		}
	}
	// a show whose title carries the word is no shield for a release that
	// really is one: the marker is in the tail, and nothing retail is
	junk := []string{
		"The.Cam.Show.S02E03.HDCAM-GRP",
		"Body.Cam.S11E08.CAM.x264-GRP",
		"Body.Cam.2020.TELESYNC.x264-GRP",
	}
	for _, name := range junk {
		if !Parse(name).PreRetail {
			t.Errorf("%s: pre-retail not flagged", name)
		}
	}
}

// Scene convention: HD names carry their resolution, so a TV/DVD/web
// release without one is standard definition — the same reading Sonarr
// makes when it files these as SDTV. Without it, a whole era of pre-HD
// television parsed as "unknown quality".
func TestSDInferredFromSourcedNamesWithoutAMarker(t *testing.T) {
	cases := []struct {
		name    string
		quality string
		source  string
		infer   bool
	}{
		{"Sister.Wives.S08E11.Tell.All.Part.One.HDTV.x264-NY2", "480p", "hdtv", true},
		{"Old.Movie.1994.DVDRip.x264-GRP.mkv", "480p", "dvd", true},
		{"Show.S01E02.WEB-DL.AAC.H.264.mkv", "480p", "webdl", true},
		{"Show.S01E02.WEBRip.x264.mkv", "480p", "webrip", true},
		// a marker always wins — nothing to infer
		{"Show.S08E11.720p.HDTV.x264-NY2", "720p", "hdtv", false},
		// no source, no marker: still unknown, conventions need a source
		{"Some.Show.S01E01.x264-GRP.mkv", "", "", false},
		// bluray-family defaults are graded, following Radarr's own: a
		// bare BluRay name reads 720p, a rip token reads SD-era 480p, and
		// a disc image or remux IS the full-HD article
		{"Old.Film.1987.BluRay.x264.mkv", "720p", "bluray", true},
		{"Old.Film.1994.BDRip.XviD-GRP.mkv", "480p", "bluray", true},
		{"Old.Film.1994.BRRip.x264.mkv", "480p", "bluray", true},
		{"Film.2008.HD-DVD.x264.mkv", "720p", "bluray", true},
		{"Film.2010.BD25.AVC.mkv", "1080p", "bluray", true},
		{"Film.2010.REMUX.AVC.DTS-HD.mkv", "1080p", "remux", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Parse(tc.name)
			if r.Quality != tc.quality || r.Source != tc.source || r.QualityInferred != tc.infer {
				t.Fatalf("got quality=%q source=%q inferred=%v, want %q/%q/%v",
					r.Quality, r.Source, r.QualityInferred, tc.quality, tc.source, tc.infer)
			}
		})
	}
}

// Markers outside the four stored rungs map to the nearest honest one —
// and 1080i is the load-bearing case: without it, the SD inference reads
// "1080i.HDTV" as a markerless TV name and files actual broadcast HD as
// 480p. 576p/540p are SD-class, and claiming 480p claims less, not more.
func TestOddQualityMarkersMapToHonestRungs(t *testing.T) {
	cases := []struct {
		name    string
		quality string
		infer   bool
	}{
		{"Show.S01E01.1080i.HDTV.DD5.1.H.264-GRP", "1080p", false},
		{"Show.S01E01.576p.x264-GRP.mkv", "480p", false}, // sourceless PAL: a real marker now, not a shrug
		{"Show.S01E01.540p.WEB-DL.x264.mkv", "480p", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Parse(tc.name)
			if r.Quality != tc.quality || r.QualityInferred != tc.infer {
				t.Fatalf("got %q inferred=%v, want %q/%v", r.Quality, r.QualityInferred, tc.quality, tc.infer)
			}
		})
	}
}

// The full marker vocabulary, at parity with Sonarr's resolution groups.
// Every one of these means a rung; a marker the parser can't read makes a
// sourced name look markerless, and the SD inference would file it 480p.
func TestQualityMarkerVocabularyMatchesSonarr(t *testing.T) {
	cases := []struct{ name, quality string }{
		{"Show.S01E01.1440p.WEB-DL.x265-GRP", "1080p"}, // the hazard case
		{"Movie.2020.FHD.WEBRip.x264", "1080p"},
		{"Show.S01E01.1920x1080.HDTV.mkv", "1080p"},
		{"Show.S01E01.960p.WEB-DL.mkv", "720p"},
		{"Show.S01E01.1280x720.HDTV.mkv", "720p"},
		{"Movie.2019.4K.UHD.BluRay.x265", "2160p"},
		{"Movie.2019.HEVC-4k.BluRay", "2160p"},
		{"Movie.2019.3840x2160.WEB-DL", "2160p"},
		{"Movie.2005.4kto1080p.BluRay", "1080p"}, // an upscale label is 1080-class content
		{"Old.Show.S01E01.480i.DVDRip", "480p"},
		{"Old.Movie.1990.640x480.DVDRip", "480p"},
		{"Clip.360p.WEB-DL.mp4", "480p"}, // SD-class catch-all, as Sonarr's SDTV is
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := Parse(tc.name)
			if r.Quality != tc.quality || r.QualityInferred {
				t.Fatalf("got %q inferred=%v, want %q as a real marker", r.Quality, r.QualityInferred, tc.quality)
			}
		})
	}
}

// Bare UHD is a REAL 2160 marker, not grist for the bluray inference —
// "UHD.BluRay" names carry no 2160p token, and reading them as a
// markerless BluRay would file actual 4K as 720p.
func TestBareUHDIsARealMarker(t *testing.T) {
	r := Parse("Movie.2019.UHD.BluRay.x265-GRP.mkv")
	if r.Quality != "2160p" || r.QualityInferred || r.Source != "bluray" {
		t.Fatalf("got %q inferred=%v source=%q, want a real 2160p bluray", r.Quality, r.QualityInferred, r.Source)
	}
}

// The run after the episode marker is the episode's own title, cut at the
// first technical token — the disambiguating signal when two shows share
// a name.
func TestEpisodeTitleSegment(t *testing.T) {
	cases := []struct{ name, want string }{
		{"Wife.Swap.S02E04.Mayfield.Wasdin.WEBDL.480p.h264.english-S1PH3R", "Mayfield Wasdin"},
		{"Wife.Swap.2019.S02E04.Floyd.Ely.Vs.Clanton.1080p.AMZN.WEB-DL.DDP2.0.H.264-NTb", "Floyd Ely Vs Clanton"},
		{"Show.S01E01.720p.WEB-DL.H264-GRP", ""},
		{"Show.S01E01.iNTERNAL.720p.WEB-DL-GRP", ""},
		{"Show.S01E01.The.Title-GRP", "The Title"},
		{"Show.S01E01.Pilot.HDTV.x264-KILLERS", "Pilot"},
	}
	for _, c := range cases {
		if got := Parse(c.name).Ep.Title; got != c.want {
			t.Errorf("%s: Ep.Title = %q, want %q", c.name, got, c.want)
		}
	}
}

// A year before the episode marker is the show's year, and survives into
// the result for matching.
func TestEpisodeYearExtraction(t *testing.T) {
	r := Parse("Wife.Swap.2019.S02E04.720p.WEB-DL")
	if r.Year != 2019 || r.Title != "Wife Swap" {
		t.Fatalf("year=%d title=%q", r.Year, r.Title)
	}
}

// Language tags are read only from the technical tail after the title —
// "The French Dispatch" is a title, not a dub — and normalize onto display
// names, with MULTI and dual-audio as their own tags.
func TestParseLanguages(t *testing.T) {
	cases := []struct {
		name string
		want []string
	}{
		{"Movie.2024.MULTI.1080p.WEB-DL.x264-GRP", []string{"Multi"}},
		{"Movie.2024.FRENCH.1080p.BluRay.x264-GRP", []string{"French"}},
		{"Movie.2024.TRUEFRENCH.1080p.BluRay.x264-GRP", []string{"French"}},
		{"Movie.2024.MULTI.VF2.1080p.WEB-DL", []string{"Multi"}},
		{"Dark.S01E01.GERMAN.1080p.WEB-DL.x264", []string{"German"}},
		{"Show.S01.MULTI.1080p.WEB-DL", []string{"Multi"}},
		{"Anime.S01E01.Dual.Audio.1080p.BluRay", []string{"Dual Audio"}},
		{"Movie.2023.VOSTFR.1080p.WEB-DL", []string{"VOSTFR"}},
		{"Movie.2024.FRENCH.MULTI.1080p.WEB-DL", []string{"French", "Multi"}},
		// title spans never read as language tags
		{"The.French.Dispatch.2021.1080p.BluRay.x264-GRP", nil},
		{"Russian.Doll.S01E01.1080p.WEB-DL", nil},
		// no tag at all — the original-audio convention
		{"Movie.2024.1080p.WEB-DL.x265-GRP", nil},
	}
	for _, c := range cases {
		got := Parse(c.name).Languages
		if len(got) != len(c.want) {
			t.Errorf("%s: languages = %v, want %v", c.name, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("%s: languages = %v, want %v", c.name, got, c.want)
				break
			}
		}
	}
}
