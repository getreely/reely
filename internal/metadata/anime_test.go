package metadata

import "testing"

// The anime rule has to be narrow: animation alone would take Pixar and
// every western cartoon out of the discovery rows, and Japanese origin
// alone would take out Kurosawa. Only the pair means anime.
func TestIsAnime(t *testing.T) {
	cases := []struct {
		name      string
		genres    []int
		language  string
		countries []string
		want      bool
	}{
		{"japanese animated film", []int{animationGenre, 12}, "ja", nil, true},
		{"japanese animated series by country", []int{animationGenre}, "en", []string{"JP"}, true},
		{"pixar", []int{animationGenre, 10751}, "en", []string{"US"}, false},
		{"western cartoon series", []int{animationGenre, 35}, "en", []string{"US"}, false},
		{"live-action japanese drama", []int{18}, "ja", []string{"JP"}, false},
		{"live-action japanese film", []int{28, 18}, "ja", nil, false},
		{"nothing known", nil, "", nil, false},
		{"korean animation stays", []int{animationGenre}, "ko", []string{"KR"}, false},
	}
	for _, c := range cases {
		if got := isAnime(c.genres, c.language, c.countries); got != c.want {
			t.Errorf("%s: isAnime = %v, want %v", c.name, got, c.want)
		}
	}
}

// The English filter is about what an English-speaking household wants to
// see, not strict nationality: the language carries it, stated US origin
// rescues the odd American title filed under another language, and British
// and Australian titles deliberately stay.
func TestIsEnglish(t *testing.T) {
	cases := []struct {
		name      string
		language  string
		countries []string
		want      bool
	}{
		{"american series", "en", []string{"US"}, true},
		{"british series stays", "en", []string{"GB"}, true},
		{"australian series stays", "en", []string{"AU"}, true},
		{"movie with only a language", "en", nil, true},
		{"us title filed under another language", "es", []string{"US"}, true},
		{"korean drama", "ko", []string{"KR"}, false},
		{"spanish series", "es", []string{"ES"}, false},
		{"japanese film", "ja", []string{"JP"}, false},
		// unknown is kept, not dropped: the filter acts on evidence
		{"nothing known is kept", "", nil, true},
		{"unlabeled but foreign origin", "", []string{"KR"}, false},
	}
	for _, c := range cases {
		if got := isEnglish(c.language, c.countries); got != c.want {
			t.Errorf("%s: isEnglish = %v, want %v", c.name, got, c.want)
		}
	}
}
