// Package refresh keeps stored TMDB metadata current. A continuing show
// only learns about next month's episodes if somebody re-asks TMDB — this
// is that somebody. Movies re-fetch too: digital release dates firm up
// over time, and the release-day pass hunts by them.
package refresh

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/scene"
)

// passLimit caps one pass — with hourly passes that's plenty of headroom
// for any home library while staying polite to TMDB after downtime.
const passLimit = 100

type Refresher struct {
	Catalog *catalog.Store
	TMDB    *metadata.TMDB
	// TVDB is optional — the source for shows whose rows say so. Nil or
	// unconfigured, those shows simply wait; their data doesn't go stale
	// dangerously, and the next configured pass catches them up.
	TVDB *metadata.TVDB
	// XEM is optional and needs no key: it supplies the numbering a show's
	// RELEASES carry where the scene disagrees with TheTVDB. Nil, or
	// unreachable, and shows keep their own numbering — which is right for
	// almost all of them and was reely's only behaviour before.
	XEM *scene.XEM
	// Verbose makes SyncScene say what it decided even when it decided to
	// do nothing. The hourly pass leaves it off — it would be a line per
	// show, every pass — but a refresh somebody clicked is one show, and
	// answering "why didn't this apply?" should not need a code reading.
	Verbose bool
}

// Pass refreshes the stalest due titles. Upserting stamps refreshed_at;
// a failed fetch stamps it explicitly so one broken title rotates to the
// back of the queue instead of starving the rest. Returns how many titles
// were refreshed.
func (r *Refresher) Pass(ctx context.Context) int {
	if !r.TMDB.Configured() {
		return 0
	}
	due, err := r.Catalog.DueForRefresh(passLimit)
	if err != nil {
		log.Printf("reely: refresh: %v", err)
		return 0
	}
	refreshed := 0
	for _, t := range due {
		if err := ctx.Err(); err != nil {
			return refreshed
		}
		if err := r.one(ctx, t); err != nil {
			log.Printf("reely: refresh %s %d: %v", t.Kind, t.ID, err)
			if err := r.Catalog.TouchRefreshed(t.Kind, t.ID); err != nil {
				log.Printf("reely: refresh stamp %s %d: %v", t.Kind, t.ID, err)
			}
			continue
		}
		refreshed++
	}
	return refreshed
}

func (r *Refresher) one(ctx context.Context, t catalog.RefreshTarget) error {
	// Everything here is keyed on the row id captured with the target —
	// never the (tmdb, library) upsert. A title deleted while this pass
	// was busy fetching TMDB must stay deleted; the upsert would insert
	// it back, monitored and fileless, and the search loop would set off
	// downloading a show somebody just removed.
	if t.Kind == "movie" {
		d, err := r.TMDB.Movie(ctx, t.TmdbID)
		if err != nil {
			return err
		}
		_, err = r.Catalog.RefreshMovie(t.ID, d)
		return err
	}
	d, err := r.FetchShow(ctx, t.Source, t.TmdbID, t.TvdbID)
	if err != nil {
		return err
	}
	// RefreshShow adds newly-listed episodes monitored; existing rows keep
	// their file and monitor state — this is how the calendar learns about
	// a continuing show's next season
	if _, err := r.Catalog.RefreshShow(t.ID, d); err != nil {
		return err
	}
	// and the new episodes need their scene numbering before anything
	// searches for them
	r.SyncScene(ctx, t.ID, t.Source, t.TvdbID)
	return nil
}

// SyncScene refreshes one show's scene numbering from TheXEM.
//
// Only TVDB-sourced shows: XEM's map is expressed in TheTVDB's numbering,
// so it can only be laid over a tree that came from TheTVDB. A show still
// on TMDB has its own season shape — a revival TMDB restarts at S1 — and
// XEM's rows would land on the wrong episodes entirely. Those shows keep
// the manual season offset, which is exactly what it is for.
//
// Failure is never fatal and never noisy at the call site: XEM is a
// nice-to-have third party, and a show without a mapping (the vast
// majority) is indistinguishable from one XEM has never been asked about.
func (r *Refresher) SyncScene(ctx context.Context, showID int64, source string, tvdbID int) {
	say := func(format string, args ...any) {
		if r.Verbose {
			log.Printf("reely: scene numbering: "+format, args...)
		}
	}
	switch {
	case r.XEM == nil:
		say("show %d: no TheXEM client configured", showID)
		return
	case source != "tvdb":
		say("show %d: source is %q, not tvdb — TheXEM's map only fits a TheTVDB tree", showID, source)
		return
	case tvdbID <= 0:
		say("show %d: no TVDB id on the row", showID)
		return
	}
	mapped, err := r.XEM.Mapped(ctx)
	switch {
	case errors.Is(err, scene.ErrUnavailable):
		say("show %d: TheXEM unreachable a moment ago, not retrying yet", showID)
		return
	case err != nil:
		log.Printf("reely: TheXEM unreachable, scene numbering unchanged: %v", err)
		return
	case !mapped[tvdbID]:
		say("show %d: TheXEM lists %d mapped series and tvdb %d is not one", showID, len(mapped), tvdbID)
		return
	}
	maps, err := r.XEM.Mappings(ctx, tvdbID)
	switch {
	case errors.Is(err, scene.ErrUnavailable):
		say("show %d: TheXEM unreachable a moment ago, not retrying yet", showID)
		return
	case err != nil:
		log.Printf("reely: TheXEM for show %d (tvdb %d): %v", showID, tvdbID, err)
		return
	}
	eps, err := r.Catalog.SceneEpisodes(showID)
	if err != nil {
		log.Printf("reely: scene numbering for show %d: %v", showID, err)
		return
	}
	n, err := r.Catalog.SetSceneNumbering(showID, scene.Resolve(eps, maps))
	if err != nil {
		log.Printf("reely: scene numbering for show %d: %v", showID, err)
		return
	}
	if n > 0 {
		log.Printf("reely: scene numbering: show %d (tvdb %d) — %d episodes release under other numbers",
			showID, tvdbID, n)
		return
	}
	say("show %d: TheXEM returned %d mappings, none of which renumber anything", showID, len(maps))
}

// FetchShow fetches one show's full record from its source: TheTVDB for
// 'tvdb' rows (when a client is configured), TMDB otherwise. TVDB records
// are enriched with TMDB cast and artwork through the remote id — people
// stay keyed by TMDB ids everywhere.
func (r *Refresher) FetchShow(ctx context.Context, source string, tmdbID, tvdbID int) (*metadata.ShowDetail, error) {
	if source == "tvdb" && tvdbID > 0 {
		if r.TVDB == nil || !r.TVDB.Configured() {
			return nil, fmt.Errorf("this show's metadata comes from TVDB, but no TVDB API key is configured")
		}
		d, err := r.TVDB.Show(ctx, tvdbID)
		if err != nil {
			return nil, err
		}
		metadata.EnrichShowFromTMDB(ctx, r.TMDB, d)
		return d, nil
	}
	if tmdbID <= 0 {
		return nil, fmt.Errorf("show has no usable metadata id")
	}
	return r.TMDB.Show(ctx, tmdbID)
}

// One refreshes a single title on demand — the refresh button's path.
// Same fetch and same id-keyed update as the scheduled pass, so a title
// deleted mid-fetch stays deleted.
func (r *Refresher) One(ctx context.Context, kind string, id int64) error {
	t, err := r.Catalog.RefreshTargetFor(kind, id)
	if err != nil {
		return err
	}
	return r.one(ctx, t)
}
