package grab

import (
	"log"
	"os"

	"github.com/getreely/reely/internal/quality"
)

// Quality upgrades: a title whose file sits below its profile's cutoff is
// still wanted. The RSS sync catches better releases passively (Evaluate's
// upgrade gate); the wanted sweep hunts them actively; and when the better
// file lands, the replaced one is deleted — but only once no catalog row
// references it, because a multi-episode file is one path backing several
// rows.

// removeReplaced deletes a file that an import or link just superseded.
// No-op when nothing was replaced, when the new file landed on the same
// path (same-quality re-import), or while any row still points at it.
func (s *Service) removeReplaced(oldPath, newPath string) {
	if oldPath == "" || oldPath == newPath {
		return
	}
	refs, err := s.Catalog.FileReferences(oldPath)
	if err != nil || refs > 0 {
		return
	}
	if err := os.Remove(oldPath); err != nil && !os.IsNotExist(err) {
		log.Printf("reely: remove replaced %s: %v", oldPath, err)
	}
}

// upgradeTargets lists everything on disk below its profile's cutoff —
// the cutoff-unmet half of the wanted sweep. One search per TMDB id: among
// sibling rows the most demanding profile carries the search, and the
// import's fan-out lifts the other copies. Files whose quality was never
// recognized are skipped — there is no telling whether a grab would be an
// upgrade or a waste.
func (s *Service) upgradeTargets() []searchTarget {
	var out []searchTarget

	if movies, err := s.Catalog.ListMovies(0); err == nil {
		byTmdb := map[int]searchTarget{}
		bestCut := map[int]int{}
		for i := range movies {
			m := &movies[i]
			if !m.Monitored || m.FilePath == "" {
				continue
			}
			if quality.Rank(m.Quality) == 0 {
				continue
			}
			// cutoff math only — the formats attach at search time
			p := s.baseProfile(m.ProfileID, m.LibraryID)
			if p == nil || !p.Upgrades {
				continue
			}
			if p.CutoffMet(m.Quality, m.Source) {
				continue
			}
			if cut := p.CutoffRank(); cut > bestCut[m.TmdbID] {
				bestCut[m.TmdbID] = cut
				byTmdb[m.TmdbID] = searchTarget{movieID: m.ID}
			}
		}
		for _, t := range byTmdb {
			out = append(out, t)
		}
	}

	shows, err := s.Catalog.ListShows(0)
	if err != nil {
		return out
	}
	type pick struct {
		id      int64
		cut     int
		profile *quality.Profile
	}
	picks := map[int]pick{}
	for i := range shows {
		row := &shows[i]
		if !row.Monitored {
			continue
		}
		p := s.baseProfile(row.ProfileID, row.LibraryID)
		if p == nil || !p.Upgrades {
			continue
		}
		cut := p.CutoffRank()
		if cur, ok := picks[row.TmdbID]; !ok || cut > cur.cut {
			picks[row.TmdbID] = pick{id: row.ID, cut: cut, profile: p}
		}
	}
	for _, pk := range picks {
		sh, err := s.Catalog.GetShow(pk.id)
		if err != nil {
			continue
		}
		for _, se := range sh.Seasons {
			for _, ep := range se.Episodes {
				if !ep.Monitored || ep.FilePath == "" {
					continue
				}
				if quality.Rank(ep.Quality) == 0 || pk.profile.CutoffMet(ep.Quality, ep.Source) {
					continue
				}
				out = append(out, searchTarget{showID: pk.id, season: ep.Season, episode: ep.Episode})
			}
		}
	}
	return out
}
