package scene

import "sort"

// Episode is one episode of a show in the catalog's own numbering.
type Episode struct{ Season, Episode int }

// Numbering is one episode paired with the numbers its releases carry.
type Numbering struct {
	Season, Episode           int
	SceneSeason, SceneEpisode int
}

// Resolve pairs a show's episodes with their scene numbering.
//
// XEM's map is a snapshot of what has been mapped so far, which is never
// quite the whole show: a season still airing is mapped up to the last
// episode somebody entered, and a season TVDB has only just listed is not
// mapped at all. Both cases are the ones reely cares most about — they
// are exactly the episodes still being searched for — so unmapped
// episodes inherit the shift from the mapping before them, the way Sonarr
// extrapolates:
//
//   - inside a season that has mappings, an episode past the last mapped
//     one continues its run (the mapped S9E08 → S10E08 makes S9E09 →
//     S10E09);
//   - a season with no mappings at all, arriving after every mapped
//     season, inherits the last mapping's season shift (with S9 → S10
//     mapped, next year's S10 is S11).
//
// Earlier unmapped seasons are left alone: they are the seasons before
// the two numberings diverged, where the catalog's numbers are already
// the right ones.
//
// Only episodes whose scene numbering actually DIFFERS come back — an
// episode the scene numbers the same way needs no mapping stored, and a
// show that agrees with the scene throughout resolves to nothing at all.
func Resolve(eps []Episode, maps []Mapping) []Numbering {
	if len(eps) == 0 || len(maps) == 0 {
		return nil
	}
	// direct mappings, keyed by the catalog's numbering
	direct := make(map[Episode]Mapping, len(maps))
	// the last mapped episode of each season, the baseline a season's
	// unmapped tail continues from
	lastInSeason := map[int]Mapping{}
	var last Mapping // the last mapping overall, for whole unmapped seasons
	for _, m := range maps {
		if m.Season == 0 || m.Episode == 0 {
			continue
		}
		direct[Episode{m.Season, m.Episode}] = m
		if cur, ok := lastInSeason[m.Season]; !ok || m.Episode > cur.Episode {
			lastInSeason[m.Season] = m
		}
		if m.Season > last.Season || (m.Season == last.Season && m.Episode > last.Episode) {
			last = m
		}
	}
	if last.Season == 0 {
		return nil
	}

	out := make([]Numbering, 0, len(eps))
	for _, e := range eps {
		if e.Season == 0 || e.Episode == 0 {
			continue // specials are never scene-numbered
		}
		season, episode := 0, 0
		switch m, ok := direct[e]; {
		case ok:
			season, episode = m.SceneSeason, m.SceneEpisode
		default:
			if base, mapped := lastInSeason[e.Season]; mapped {
				if e.Episode <= base.Episode {
					// a gap BEFORE the last mapping is a hole in XEM's data,
					// not a run to continue — guessing at it would invent a
					// numbering nobody published
					continue
				}
				season = base.SceneSeason
				episode = base.SceneEpisode + (e.Episode - base.Episode)
			} else if e.Season > last.Season {
				season = e.Season + (last.SceneSeason - last.Season)
				episode = e.Episode
			}
		}
		if season <= 0 || episode <= 0 {
			continue
		}
		if season == e.Season && episode == e.Episode {
			continue // agrees with the catalog; nothing to record
		}
		out = append(out, Numbering{
			Season: e.Season, Episode: e.Episode,
			SceneSeason: season, SceneEpisode: episode,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Season != out[j].Season {
			return out[i].Season < out[j].Season
		}
		return out[i].Episode < out[j].Episode
	})
	return out
}
