package grab

import (
	"context"
	"encoding/json"
	"log"
	"sort"
	"strconv"
	"time"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/parser"
	"github.com/getreely/reely/internal/prowlarr"
	"github.com/getreely/reely/internal/quality"
)

// The RSS sync: sweep the indexers' latest releases against everything
// monitored, and grab on sight. There is deliberately NO date gate — an
// early release gets scooped the moment it appears; air and digital dates
// only ever drive scheduled searches, never this matcher. Season packs stay
// a manual gesture for now: the sync grabs movies and episode releases.

// RSSInterval is how often the sync runs, from the rss_poll_seconds
// setting, floored at five minutes to stay friendly to the indexers.
func (s *Service) RSSInterval() time.Duration {
	secs, err := strconv.Atoi(s.setting("rss_poll_seconds", "900"))
	if err != nil || secs <= 0 {
		secs = 900
	}
	if secs < 300 {
		secs = 300
	}
	return time.Duration(secs) * time.Second
}

// RSSSync runs one sweep. Returns how many releases were grabbed.
func (s *Service) RSSSync(ctx context.Context) int {
	if !s.Indexer.Configured() || !s.haveClient() {
		return 0
	}
	return s.rssMovies(ctx) + s.rssEpisodes(ctx)
}

// The sync must never lose its place in the feed. One page of "latest"
// (Prowlarr's default, 100 per indexer) is a window, and on a busy
// release night an indexer can post past it between two polls — a release
// that scrolls by unseen is judged never, not once. So each sync
// remembers, per feed and per indexer, the releases it saw, and the next
// sync pages backwards until every indexer's results reconnect with that
// memory — the same walk-back Sonarr does, with the same 1000-result
// ceiling. The cursor is written through to the DB, so a restart resumes
// the walk-back too; only a fresh install starts with a single blind
// page, and the release-day taper covers whatever that misses.
const (
	rssPageSize   = 100
	rssMaxResults = 1000
)

// rssCursor is where one indexer's feed was left after a sync: the ids of
// the releases seen, and the newest publish date among them. Reconnecting
// means seeing one of those ids again — or, for feeds that churn faster
// than the ceiling, paging back to posts older than that date.
type rssCursor struct {
	newest time.Time
	ids    map[string]bool
}

// The cursor is also written through to the catalog's app_state table —
// one row per feed, overwritten every sync — so a restart resumes the
// walk-back where the last sync left off instead of going blind for a
// page. Sonarr keeps the same thing in its indexer-status rows.
type rssCursorJSON struct {
	Newest string   `json:"newest,omitempty"`
	IDs    []string `json:"ids"`
}

func rssCursorKey(feed string) string { return "rss_cursor_" + feed }

func (s *Service) loadRSSCursor(feed string) map[string]rssCursor {
	if s.Catalog == nil {
		return nil
	}
	raw := s.Catalog.GetState(rssCursorKey(feed))
	if raw == "" {
		return nil
	}
	var stored map[string]rssCursorJSON
	if err := json.Unmarshal([]byte(raw), &stored); err != nil {
		log.Printf("reely: rss %s: corrupt stored cursor, starting fresh: %v", feed, err)
		return nil
	}
	out := make(map[string]rssCursor, len(stored))
	for ix, c := range stored {
		cur := rssCursor{ids: make(map[string]bool, len(c.IDs))}
		for _, id := range c.IDs {
			cur.ids[id] = true
		}
		if t, err := time.Parse(time.RFC3339, c.Newest); err == nil {
			cur.newest = t
		}
		out[ix] = cur
	}
	return out
}

func (s *Service) saveRSSCursor(feed string, next map[string]rssCursor) {
	if s.Catalog == nil {
		return
	}
	stored := make(map[string]rssCursorJSON, len(next))
	for ix, c := range next {
		j := rssCursorJSON{IDs: make([]string, 0, len(c.ids))}
		for id := range c.ids {
			j.IDs = append(j.IDs, id)
		}
		sort.Strings(j.IDs)
		if !c.newest.IsZero() {
			j.Newest = c.newest.Format(time.RFC3339)
		}
		stored[ix] = j
	}
	b, err := json.Marshal(stored)
	if err != nil {
		return
	}
	if err := s.Catalog.SetState(rssCursorKey(feed), string(b)); err != nil {
		log.Printf("reely: rss %s: saving cursor: %v", feed, err)
	}
}

// releaseID identifies a release across syncs. GUIDs are the honest key;
// feeds that omit them fall back to the download URL, then the title.
func releaseID(rel prowlarr.Release) string {
	if rel.GUID != "" {
		return rel.GUID
	}
	if rel.DownloadURL != "" {
		return rel.DownloadURL
	}
	return rel.Indexer + "|" + rel.Title
}

func releaseDate(rel prowlarr.Release) (time.Time, bool) {
	t, err := time.Parse(time.RFC3339, rel.PublishDate)
	return t, err == nil
}

// reconnected reports whether every indexer that has a cursor and answered
// this sync has been walked back to where the last sync left off. An
// indexer absent from the results (down, or dropped from Prowlarr) can't
// hold the paging hostage.
func reconnected(prev map[string]rssCursor, all []prowlarr.Release) bool {
	linked := map[string]bool{}
	present := map[string]bool{}
	for _, rel := range all {
		c, ok := prev[rel.Indexer]
		if !ok {
			continue
		}
		present[rel.Indexer] = true
		if c.ids[releaseID(rel)] {
			linked[rel.Indexer] = true
		} else if t, ok := releaseDate(rel); ok && !c.newest.IsZero() && !t.After(c.newest) {
			linked[rel.Indexer] = true
		}
	}
	for ix := range present {
		if !linked[ix] {
			return false
		}
	}
	return true
}

// fetchRecent pulls the feed for one category root, paging backwards until
// it reconnects with the previous sync (see above). Results are deduped
// across pages, since an indexer that ignores offset repeats itself.
func (s *Service) fetchRecent(ctx context.Context, feed string, cats []int) []prowlarr.Release {
	s.mu.Lock()
	prev, loaded := s.rssSeen[feed]
	s.mu.Unlock()
	if !loaded {
		// first sync since boot: pick up where the previous process left
		// off. The nil result of a fresh install is cached too, so the DB
		// is read once per feed, not once per sync.
		prev = s.loadRSSCursor(feed)
		s.mu.Lock()
		if s.rssSeen == nil {
			s.rssSeen = map[string]map[string]rssCursor{}
		}
		if _, ok := s.rssSeen[feed]; !ok {
			s.rssSeen[feed] = prev
		}
		s.mu.Unlock()
	}

	seen := map[string]bool{}
	var all []prowlarr.Release
	exhausted := false
	for offset := 0; offset < rssMaxResults; offset += rssPageSize {
		page, err := s.Indexer.SearchPage(ctx, "", cats, offset, rssPageSize)
		if err != nil {
			log.Printf("reely: rss %s: %v", feed, err)
			break
		}
		fresh := 0
		for _, rel := range page {
			id := releaseID(rel)
			if seen[id] {
				continue
			}
			seen[id] = true
			fresh++
			all = append(all, rel)
		}
		if len(page) < rssPageSize {
			exhausted = true // a short page: the feed has nothing further back
			break
		}
		if len(prev) == 0 {
			break // first sync since boot: no memory to reconnect with
		}
		if fresh == 0 {
			break // a full page of repeats: the indexer ignored the offset
		}
		if reconnected(prev, all) {
			break
		}
	}
	// warn only when the walk stopped short — a feed read to its end was
	// covered in full, however much it churned
	if len(prev) > 0 && len(all) > 0 && !exhausted && !reconnected(prev, all) {
		log.Printf("reely: rss %s: couldn't page back to the previous sync within %d releases — some may have scrolled past; the release-day taper covers monitored gaps", feed, rssMaxResults)
	}

	if len(all) > 0 {
		// each indexer's entry is rebuilt from what it answered this sync;
		// an indexer that answered nothing (down, or briefly erroring in
		// Prowlarr) keeps its old place instead of losing it, so when it
		// comes back the walk-back still knows where it left off
		fresh := map[string]rssCursor{}
		for _, rel := range all {
			c := fresh[rel.Indexer]
			if c.ids == nil {
				c.ids = map[string]bool{}
			}
			c.ids[releaseID(rel)] = true
			if t, ok := releaseDate(rel); ok && t.After(c.newest) {
				c.newest = t
			}
			fresh[rel.Indexer] = c
		}
		next := make(map[string]rssCursor, len(prev)+len(fresh))
		for ix, c := range prev {
			next[ix] = c
		}
		for ix, c := range fresh {
			next[ix] = c
		}
		s.mu.Lock()
		if s.rssSeen == nil {
			s.rssSeen = map[string]map[string]rssCursor{}
		}
		s.rssSeen[feed] = next
		s.mu.Unlock()
		s.saveRSSCursor(feed, next)
	}
	return all
}

// bestPer keeps the highest-scoring accepted release per target.
type candidateRelease struct {
	release prowlarr.Release
	score   int
}

func (s *Service) rssMovies(ctx context.Context) int {
	releases := s.fetchRecent(ctx, "movies", prowlarr.MovieCats)
	movies, err := s.Catalog.ListMovies(0)
	if err != nil {
		log.Printf("reely: rss movies: %v", err)
		return 0
	}

	blocked := s.blockedReleases()
	today := time.Now().UTC().Format("2006-01-02")
	allowed := s.protocols()
	best := map[int64]candidateRelease{} // movie id → best release
	for _, rel := range releases {
		if !allowed.allows(rel.Protocol) || blocked.Has(rel.Title, rel.Indexer) {
			continue
		}
		p := parser.Parse(rel.Title)
		if p.Kind != "movie" {
			continue
		}
		for i := range movies {
			m := &movies[i]
			if !m.Monitored {
				continue
			}
			// an agreeing reported id IS the movie (Radarr's first step);
			// only without one do the name and year heuristics decide
			if !movieIDAgrees(rel, m.TmdbID, m.ImdbID) {
				if !titleMatchMovie(p.Title, m) {
					continue
				}
				if p.Year > 0 && m.Year > 0 && !yearIsTitle(p.Year, m.Title) && absInt(p.Year-m.Year) > 1 {
					continue
				}
				if movieIDMismatch(rel, m.TmdbID, m.ImdbID) != "" {
					continue // the indexer's own ids name a different movie
				}
			}
			// the gate exempts nothing, agreeing id included: id mappings
			// come from the release's own NFO, which a faker writes
			if !movieAvailable(m, today) {
				continue // before the movie is out, a perfect name is bait
			}
			profile, err := s.resolveProfile(m.ProfileID, m.LibraryID, "movies")
			if err != nil {
				continue
			}
			current, currentSrc := "", ""
			if m.FilePath != "" {
				current, currentSrc = m.Quality, m.Source
			}
			d := quality.Evaluate(
				quality.Candidate{Result: p, Name: rel.Title, SizeBytes: rel.Size},
				profile,
				quality.Want{
					RuntimeMin: m.Runtime, HasFile: m.FilePath != "",
					CurrentQuality: current, CurrentSource: currentSrc,
				})
			if d.Accepted && d.Score > best[m.ID].score {
				best[m.ID] = candidateRelease{release: rel, score: d.Score}
			}
		}
	}

	grabbed := 0
	for movieID, c := range best {
		if pending, err := s.Catalog.HasPendingGrab(movieID, 0); err != nil || pending {
			continue
		}
		m, err := s.Catalog.GetMovie(movieID)
		if err != nil {
			continue
		}
		if err := s.GrabMovie(ctx, m, grabRequestFor(c.release)); err != nil {
			log.Printf("reely: rss grab %q: %v", c.release.Title, err)
			continue
		}
		log.Printf("reely: rss grabbed %q for %s", c.release.Title, m.Title)
		grabbed++
	}
	return grabbed
}

func (s *Service) rssEpisodes(ctx context.Context) int {
	releases := s.fetchRecent(ctx, "tv", prowlarr.TVCats)
	shows, err := s.Catalog.ListShows(0)
	if err != nil {
		log.Printf("reely: rss tv: %v", err)
		return 0
	}
	// full show trees load lazily, once per show that actually matches
	details := map[int64]*catalog.ShowDetails{}

	type target struct {
		showID  int64
		season  int
		episode int
	}
	blocked := s.blockedReleases()
	allowed := s.protocols()
	best := map[target]candidateRelease{}
	for _, rel := range releases {
		if !allowed.allows(rel.Protocol) || blocked.Has(rel.Title, rel.Indexer) {
			continue
		}
		p := parser.Parse(rel.Title)
		if p.Kind != "episode" {
			continue // season packs stay manual for now
		}
		for i := range shows {
			row := &shows[i]
			if !row.Monitored || !titleMatchShow(p.Title, row) {
				continue
			}
			sh := details[row.ID]
			if sh == nil {
				if sh, err = s.Catalog.GetShow(row.ID); err != nil {
					continue
				}
				details[row.ID] = sh
			}
			// two shows can share a title outright ("Wife Swap" 2004 and
			// 2019) — a year token in the name settles it, and a release
			// naming a different episode than this show's is the other one.
			// A title that IS a year ("1923") is not a year token.
			if p.Year > 0 && sh.Year > 0 && !yearIsTitle(p.Year, sh.Title) && absInt(p.Year-sh.Year) > 1 {
				continue
			}
			// releases speak scene numbering; a show with a season offset
			// reads them strictly through it, so the split-off original's
			// S1 can never land on the revival
			pp := sceneToCatalog(sh, p)
			// the release is judged against the first episode it covers
			// that's actually wanted — grabbing fills the rest of its span
			ep := s.firstWantedInSpan(sh, pp)
			if ep == nil {
				continue
			}
			if episodeTitleConflict(pp.Ep.Title, ep.Title) {
				continue
			}
			profile, err := s.resolveProfile(sh.ProfileID, sh.LibraryID, "shows")
			if err != nil {
				continue
			}
			current, currentSrc := "", ""
			if ep.FilePath != "" {
				current, currentSrc = ep.Quality, ep.Source
			}
			d := quality.Evaluate(
				quality.Candidate{Result: pp, Name: rel.Title, SizeBytes: rel.Size},
				profile,
				quality.Want{
					RuntimeMin: ep.Runtime, HasFile: ep.FilePath != "",
					CurrentQuality: current, CurrentSource: currentSrc,
					Episodes: max(pp.Ep.EpisodeEnd-pp.Ep.Episode+1, 1),
				})
			key := target{showID: sh.ID, season: ep.Season, episode: ep.Episode}
			if d.Accepted && d.Score > best[key].score {
				best[key] = candidateRelease{release: rel, score: d.Score}
			}
		}
	}

	grabbed := 0
	for key, c := range best {
		sh := details[key.showID]
		ep := findEpisode(sh, key.season, key.episode)
		if ep == nil {
			continue
		}
		if pending, err := s.Catalog.HasPendingGrab(0, ep.ID); err != nil || pending {
			continue
		}
		if err := s.GrabForShow(ctx, sh, key.season, key.episode, grabRequestFor(c.release)); err != nil {
			log.Printf("reely: rss grab %q: %v", c.release.Title, err)
			continue
		}
		log.Printf("reely: rss grabbed %q for %s S%02dE%02d", c.release.Title, sh.Title, key.season, key.episode)
		grabbed++
	}
	return grabbed
}

// firstWantedInSpan picks the first episode a release covers that is
// monitored and worth bandwidth (missing, or upgradeable — the profile's
// Evaluate settles the second half).
func (s *Service) firstWantedInSpan(sh *catalog.ShowDetails, p parser.Result) *catalog.Episode {
	for n := p.Ep.Episode; n <= p.Ep.EpisodeEnd; n++ {
		ep := findEpisode(sh, p.Ep.Season, n)
		if ep != nil && ep.Monitored {
			return ep
		}
	}
	return nil
}

func grabRequestFor(rel prowlarr.Release) GrabRequest {
	return GrabRequest{
		Title: rel.Title, DownloadURL: rel.DownloadURL,
		Indexer: rel.Indexer, Size: rel.Size, Origin: "rss",
	}
}
