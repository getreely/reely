package quality

import "testing"

func TestFromDimensions(t *testing.T) {
	cases := []struct {
		name          string
		width, height int
		want          string
	}{
		{"UHD", 3840, 2160, "2160p"},
		{"DCI 4K scope", 4096, 1716, "2160p"},
		{"full HD", 1920, 1080, "1080p"},
		// the case that makes width dominant: judged on height this is
		// 720p, and the loop would hunt an upgrade that never satisfies it
		{"1080p scope, letterboxed", 1920, 800, "1080p"},
		{"1080p 2.76:1, heavily letterboxed", 1920, 696, "1080p"},
		{"HD ready", 1280, 720, "720p"},
		{"720p scope", 1280, 536, "720p"},
		{"NTSC DVD", 720, 480, "480p"},
		{"PAL widescreen", 1024, 576, "480p"},
		// a phone shooting "1080p" portrait really is 1080x1920, so the
		// height fallback lands on the label a person would expect
		{"portrait 1080p", 1080, 1920, "1080p"},
		{"nothing known", 0, 0, ""},
		{"negative is nonsense, not 480p", -1, 720, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FromDimensions(tc.width, tc.height); got != tc.want {
				t.Fatalf("FromDimensions(%d, %d) = %q, want %q", tc.width, tc.height, got, tc.want)
			}
		})
	}
}

// Whatever comes back has to be a rung the rest of the loop understands,
// or a probed file is no better off than an unprobed one.
func TestFromDimensionsReturnsRankableValues(t *testing.T) {
	for _, size := range [][2]int{{3840, 2160}, {1920, 1080}, {1280, 720}, {720, 480}} {
		q := FromDimensions(size[0], size[1])
		if Rank(q) == 0 {
			t.Fatalf("FromDimensions(%d, %d) = %q, which ranks 0 — the loop treats that as unknown", size[0], size[1], q)
		}
	}
}
