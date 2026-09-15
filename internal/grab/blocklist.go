package grab

import (
	"log"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/download"
	"github.com/getreely/reely/internal/parser"
)

// Blocklist plumbing: a release that was grabbed and then failed goes on
// the blocklist, and the AUTOMATIC grab paths (search queue, RSS sync)
// refuse it from then on. Interactive manual grabs stay an override — a
// deliberate click may know better than one bad propagation day.

// blockedReleases loads the blocklist; an error just means an empty set —
// blocking is a nicety, never a reason to stop grabbing.
func (s *Service) blockedReleases() catalog.Blocked {
	blocked, err := s.Catalog.BlockedReleases()
	if err != nil {
		log.Printf("reely: read blocklist: %v", err)
		return catalog.Blocked{}
	}
	return blocked
}

// blocklistFailed bans a failed download's release name.
func (s *Service) blocklistFailed(item download.HistoryItem) {
	s.blocklistJob(item.ID, item.Name, firstNonEmpty(item.FailMessage, "download failed"))
}

// BlocklistJob bans the release a SAB job carries — the cancel path's
// "and don't grab this again". name is the job's display name, the
// fallback when reely never grabbed the job itself.
func (s *Service) BlocklistJob(id, name, reason string) {
	s.blocklistJob(id, name, reason)
}

// blocklistJob bans a job's release name. The grab record says exactly
// which release and title it was for; parsing the job name is the
// fallback for jobs queued outside reely.
func (s *Service) blocklistJob(id, name, reason string) {
	// the ban is scoped to the indexer this copy came from; another
	// indexer's post of the same name is a different file worth trying
	title, indexer, err := s.Catalog.GrabRelease(id)
	if err != nil {
		log.Printf("reely: blocklist %q: reading its grab record: %v", name, err)
	}
	title = firstNonEmpty(title, name)
	if title == "" {
		log.Printf("reely: blocklist %s: no release name to ban", id)
		return
	}
	movieID, showID, _, err := s.Catalog.GrabTarget(id)
	if err != nil || (movieID == 0 && showID == 0) {
		p := parser.Parse(title)
		if p.Kind == "movie" {
			if m, err := s.findMovie(p.Title, p.Year); err == nil {
				movieID = m.ID
			}
		} else if sh, err := s.findShow(p.Title, p.Year); err == nil {
			showID = sh.ID
		}
	}
	if err := s.Catalog.AddBlocklist(title, indexer, movieID, showID, reason); err != nil {
		log.Printf("reely: blocklist %q: %v", title, err)
	}
}

// retryFailed re-searches the title a failed grab was hunting, so the next
// best release replaces it right away — the failed one is already on the
// blocklist by the time the search runs. Jobs reely didn't grab (nothing in
// the grab record) are left alone; the wanted sweep owns those titles.
func (s *Service) retryFailed(item download.HistoryItem) {
	movieID, showID, episodeID, err := s.Catalog.GrabTarget(item.ID)
	if err != nil || (movieID == 0 && showID == 0) {
		return
	}
	switch {
	case movieID > 0:
		s.EnqueueMovie(movieID)
		log.Printf("reely: %q failed — re-searching for the next best release", item.Name)
	case episodeID > 0:
		season, episode, err := s.Catalog.EpisodeNumbers(episodeID)
		if err != nil {
			return
		}
		s.EnqueueEpisode(showID, season, episode)
		log.Printf("reely: %q failed — re-searching S%02dE%02d", item.Name, season, episode)
	default:
		// a season-pack grab: the season number lives only in the release
		// name — which speaks scene numbering, so a show with an offset maps
		// it back; season 0 re-queues every wanted episode as the fallback
		season := parser.Parse(item.Name).Ep.Season
		if offset, err := s.Catalog.ShowSeasonOffset(showID); err == nil && season > 0 {
			season -= offset
		}
		s.EnqueueShow(showID, season)
		log.Printf("reely: %q failed — re-searching the season", item.Name)
	}
}

// bestAcceptedUnblocked picks the first accepted view not on the
// blocklist — the judge already sorts accepted rows best-first.
func bestAcceptedUnblocked(views []ReleaseView, blocked catalog.Blocked) *ReleaseView {
	for i := range views {
		if views[i].Accepted && !blocked.Has(views[i].Title, views[i].Indexer) {
			return &views[i]
		}
	}
	return nil
}
