package trash

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/getreely/reely/internal/parser"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// Real guide documents convert faithfully; ones leaning on rules reely
// can't judge are refused rather than imported half-working.
func TestConvert(t *testing.T) {
	f, ok := Convert(readFixture(t, "atmos-undefined.json"), "radarr")
	if !ok || f.Name != "ATMOS (undefined)" || f.Score != 3000 {
		t.Fatalf("atmos: ok=%v %+v", ok, f)
	}
	// its lookbehind pattern must actually run under the regex engine
	if !f.Matches("Movie.2024.1080p.WEB-DL.ATMOS.mkv", "web", "1080p", nil) {
		t.Error("atmos should match an ATMOS release")
	}
	if f.Matches("Movie.2024.1080p.WEB-DL.DDP5.1.mkv", "web", "1080p", nil) {
		t.Error("atmos must not match a DDP release")
	}

	if _, ok := Convert(readFixture(t, "v0.json"), "radarr"); !ok {
		t.Error("v0 (single title regex) should convert")
	}
	// resolution + remux modifier + source + group regexes — all mappable
	fr, ok := Convert(readFixture(t, "french-uhd-bluray-tier-02.json"), "radarr")
	if !ok || fr.Score != 1750 {
		t.Fatalf("french tier: ok=%v %+v", ok, fr)
	}
	// 2160p bluray (not remux, not webrip, not SDR) from a tier group
	if !fr.Matches("Movie.2024.2160p.Bluray.HDR.x265-FCK.mkv", "bluray", "2160p", nil) {
		t.Error("french tier should match a 2160p bluray from a tier group")
	}
	if fr.Matches("Movie.2024.1080p.Bluray-FCK.mkv", "bluray", "1080p", nil) {
		t.Error("french tier requires 2160p")
	}

	// concrete-language specs (Japanese/Chinese/Korean here) now convert
	// onto the parser's audio-tag vocabulary
	anime, ok := Convert(readFixture(t, "anime-dual-audio.json"), "radarr")
	if !ok {
		t.Fatal("a concrete-language format should convert")
	}
	// the required dual-audio regex plus at least one language hit — the
	// parsed Dual Audio tag counts for any asked language
	if !anime.Matches("Anime.S01E01.Dual.Audio.1080p.BluRay.x265-GRP", "bluray", "1080p", []string{"Dual Audio"}) {
		t.Error("dual-audio release with the tag should match")
	}
	// same name judged without tags: the optional language group misses
	if anime.Matches("Anime.S01E01.Dual.Audio.1080p.BluRay.x265-GRP", "bluray", "1080p", nil) {
		t.Error("untagged audio must not satisfy the language group")
	}
}

// The guides' "Language: Not English" — a negated English spec — is the
// one-format dub blocker: it hits releases tagged with only foreign
// audio, and passes untagged names (the *arr untagged-counts-as-English
// default), MULTI, and VOSTFR.
func TestConvertNotEnglish(t *testing.T) {
	notEnglish := []byte(`{"trash_id":"x","name":"Language: Not English","trash_scores":{"default":-10000},
		"specifications":[{"name":"Not English Language","implementation":"LanguageSpecification",
		"negate":true,"required":true,"fields":{"value":1}}]}`)
	f, ok := Convert(notEnglish, "radarr")
	if !ok || f.Score != -10000 {
		t.Fatalf("not-english: ok=%v %+v", ok, f)
	}
	langsOf := func(name string) []string { return parser.Parse(name).Languages }
	for name, want := range map[string]bool{
		"Movie.2024.FRENCH.1080p.BluRay.x264-GRP": true,  // foreign audio only
		"Movie.2024.GERMAN.1080p.WEB-DL.x264":     true,  // any dub, one format
		"Movie.2024.1080p.WEB-DL.x264-GRP":        false, // untagged = English
		"Movie.2024.MULTI.1080p.WEB-DL.x264":      false, // several tracks incl. English
		"Movie.2023.VOSTFR.1080p.WEB-DL":          false, // original audio, French subs
	} {
		if got := f.Matches(name, "webdl", "1080p", langsOf(name)); got != want {
			t.Errorf("%s: matches=%v, want %v", name, got, want)
		}
	}
}

// The language values that judge the title's ORIGINAL language, and the
// exceptLanguage inversion around it, need metadata a name cannot carry —
// those formats are refused whole rather than imported half-working.
func TestConvertRefusesMetadataLanguageRules(t *testing.T) {
	original := []byte(`{"trash_id":"x","name":"Language: Original","trash_scores":{"default":10},
		"specifications":[{"name":"Original","implementation":"LanguageSpecification",
		"negate":false,"required":true,"fields":{"value":-2}}]}`)
	if _, ok := Convert(original, "radarr"); ok {
		t.Error("the Original language value must be refused")
	}
	except := []byte(`{"trash_id":"x","name":"Not French","trash_scores":{"default":10},
		"specifications":[{"name":"French","implementation":"LanguageSpecification",
		"negate":false,"required":true,"fields":{"value":2,"exceptLanguage":true}}]}`)
	if _, ok := Convert(except, "radarr"); ok {
		t.Error("exceptLanguage must be refused")
	}
}

// The tarball path: fixtures served as a guides-shaped archive parse into
// per-kind lists through the daily cache.
func TestFormatsFromTarball(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		gz := gzip.NewWriter(w)
		tw := tar.NewWriter(gz)
		add := func(path string, body []byte) {
			if err := tw.WriteHeader(&tar.Header{Name: path, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
				t.Fatal(err)
			}
			if _, err := tw.Write(body); err != nil {
				t.Fatal(err)
			}
		}
		add("Guides-master/docs/json/radarr/cf/atmos-undefined.json", readFixture(t, "atmos-undefined.json"))
		add("Guides-master/docs/json/radarr/cf/anime-dual-audio.json", readFixture(t, "anime-dual-audio.json"))
		add("Guides-master/docs/json/sonarr/cf/v0.json", readFixture(t, "v0.json"))
		// write side: Close flushes — a swallowed error here would hand the
		// service a truncated archive and fail the test confusingly later
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		if err := gz.Close(); err != nil {
			t.Fatal(err)
		}
	}))
	defer srv.Close()
	t.Setenv("REELY_TRASH_TARBALL", srv.URL)

	svc := New()
	movies, err := svc.Formats(context.Background(), "movies")
	// both radarr fixtures convert now, sorted by name
	if err != nil || len(movies) != 2 || movies[0].Name != "ATMOS (undefined)" || movies[1].Name != "Anime Dual Audio" {
		t.Fatalf("movies = %+v (%v)", movies, err)
	}
	shows, err := svc.Formats(context.Background(), "shows")
	if err != nil || len(shows) != 1 || shows[0].Name != "v0" {
		t.Fatalf("shows = %+v (%v)", shows, err)
	}
	if _, err := svc.Formats(context.Background(), "radarr"); err == nil {
		t.Error("app-name kinds are not the API")
	}
}

// The regression behind "how is this hitting SHO/HMAX/HBO/DSCP?": a
// streaming-service format is (service token) AND (web source). The web
// SourceSpecifications must convert to their own source_title group — as
// plain title specs they OR with the token patterns, and every WEBDL
// release matches every service.
func TestConvertServiceFormatRequiresTheToken(t *testing.T) {
	f, ok := Convert(readFixture(t, "hmax.json"), "sonarr")
	if !ok || f.Name != "HMAX" || f.Score != 75 {
		t.Fatalf("hmax: ok=%v %+v", ok, f)
	}
	kinds := map[string]int{}
	for _, s := range f.Specs {
		kinds[s.Kind]++
	}
	if kinds["title"] != 2 || kinds["source_title"] != 2 {
		t.Fatalf("spec kinds = %v, want 2 title + 2 source_title", kinds)
	}

	// a plain WEBDL release with no service token — the reported case
	if f.Matches("Wife.Swap.S01E11.Elliott.Burkhalter.WEBDL.480p.h264.english-S1PH3R", "webdl", "480p", nil) {
		t.Error("a tokenless WEBDL release must not read as HMAX")
	}
	// the token alone isn't enough either: the web-source group must hit
	if f.Matches("Show.S01E01.1080p.HMAX].Bluray.x264-GRP", "bluray", "1080p", nil) {
		t.Error("an HMAX token on a bluray must not read as HMAX web")
	}
	// token + web: the real thing
	if !f.Matches("Show.S01E01.HMAX.WEB-DL.1080p.DDP5.1.H.264-GRP", "webdl", "1080p", nil) {
		t.Error("an HMAX WEB-DL release should match")
	}
}
