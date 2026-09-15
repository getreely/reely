package grab

import (
	"testing"

	"github.com/getreely/reely/internal/catalog"
)

func combinedShow(totalPerSeason int) *catalog.ShowDetails {
	sh := &catalog.ShowDetails{}
	for s := 1; s <= 6; s++ {
		se := catalog.SeasonEpisodes{Number: s, Name: "Season " + string(rune('0'+s))}
		for e := 1; e <= totalPerSeason; e++ {
			title := ""
			if s == 3 && e == 1 {
				title = "Spanks, but no Spanks"
			}
			se.Episodes = append(se.Episodes, catalog.Episode{Season: s, Episode: e, Title: title})
		}
		sh.Seasons = append(sh.Seasons, se)
	}
	return sh
}

// The scene's compact numbering — "Show.301." meaning S03E01 — is read
// only with evidence: the episode must exist, title words must agree
// when present, and a bare number that could be an absolute episode
// count is refused rather than guessed.
func TestCombinedEpisodeNumbering(t *testing.T) {
	sh := combinedShow(24) // 144 episodes across 6 seasons

	// episode-title words in the name confirm the mapping
	p, ok := combinedEpisode(sh, "Yes.Dear.301.Spanks.But.No.Spanks.SDTV.avi", false)
	if !ok || p.Ep.Season != 3 || p.Ep.Episode != 1 {
		t.Fatalf("titled 301 → ok=%v S%02dE%02d, want S03E01", ok, p.Ep.Season, p.Ep.Episode)
	}

	// no title words, but 301 exceeds the show's 144 episodes — it cannot
	// be an absolute number, so the compact reading stands
	p, ok = combinedEpisode(sh, "Yes.Dear.301.SDTV.avi", false)
	if !ok || p.Ep.Season != 3 || p.Ep.Episode != 1 {
		t.Fatalf("bare 301 → ok=%v S%02dE%02d, want S03E01", ok, p.Ep.Season, p.Ep.Episode)
	}

	// a year is never a candidate
	if _, ok = combinedEpisode(sh, "Yes.Dear.2002.SDTV.avi", true); ok {
		t.Fatal("a year token was read as an episode")
	}

	// wrong title words veto the mapping
	if _, ok = combinedEpisode(sh, "Yes.Dear.301.Some.Entirely.Different.Episode.avi", true); ok {
		t.Fatal("a conflicting episode title did not veto the compact reading")
	}

	// an episode the show doesn't have is no candidate
	if _, ok = combinedEpisode(sh, "Yes.Dear.999.SDTV.avi", true); ok {
		t.Fatal("a nonexistent episode was invented")
	}

	// ambiguity guard: on a show with hundreds of episodes, a bare "104"
	// with no title words could be absolute episode 104 — refuse to guess
	big := combinedShow(80) // 480 episodes
	if _, ok = combinedEpisode(big, "Long.Show.104.avi", false); ok {
		t.Fatal("guessed compact numbering where absolute numbering is plausible")
	}
	// but title agreement resolves even that
	big.Seasons[0].Episodes[3].Title = "The Fourth Outing"
	p, ok = combinedEpisode(big, "Long.Show.104.The.Fourth.Outing.avi", false)
	if !ok || p.Ep.Season != 1 || p.Ep.Episode != 4 {
		t.Fatalf("titled 104 → ok=%v S%02dE%02d, want S01E04", ok, p.Ep.Season, p.Ep.Episode)
	}
}

// A job whose own name declares seasons vouches for its files: inside a
// "Show.S01-S06" pack, a bare "103" is S01E03 even on a show whose
// episode count makes the absolute reading possible — and a conflicting
// episode title still vetoes, trusted or not (asserted above).
func TestCombinedEpisodeTrustedContext(t *testing.T) {
	big := combinedShow(80) // 480 episodes: ambiguous without context
	p, ok := combinedEpisode(big, "Long.Show.103.dsr.xvid-group.avi", true)
	if !ok || p.Ep.Season != 1 || p.Ep.Episode != 3 {
		t.Fatalf("trusted 103 → ok=%v S%02dE%02d, want S01E03", ok, p.Ep.Season, p.Ep.Episode)
	}
	if _, ok = combinedEpisode(big, "Long.Show.103.dsr.xvid-group.avi", false); ok {
		t.Fatal("untrusted bare 103 on a 480-episode show should be refused")
	}
}
