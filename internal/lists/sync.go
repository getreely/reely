// Package lists syncs external watched lists — TMDB charts and lists,
// Trakt charts and public lists — into libraries. New items arrive exactly
// like a hand-added title: monitored, instantly hard-linked when a sibling
// collection already has the file, and queued for a paced search when not.
package lists

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/getreely/reely/internal/auth"
	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/grab"
	"github.com/getreely/reely/internal/mdblist"
	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/trakt"
)

type Syncer struct {
	Catalog *catalog.Store
	TMDB    *metadata.TMDB
	// TVDB is optional — list-added shows route through it (like every
	// other add) when a key is configured, so a split revival lands on the
	// one continuing series.
	TVDB     *metadata.TVDB
	Trakt    *trakt.Client
	Mdblist  *mdblist.Client
	Grab     *grab.Service
	Settings grab.SettingsReader // lists_sync_minutes; nil falls back to default
	// Accounts resolves who owns a list, which decides whether its items
	// are added or requested. Nil means every list adds — the shape of
	// an install with no accounts at all.
	Accounts Accounts
}

// Accounts is the sliver of the account store the syncer needs: who owns
// a list, and whether that account adds titles or asks for them.
type Accounts interface {
	UserByID(id int64) *auth.User
}

// Interval is how often the automatic sync runs, from lists_sync_minutes
// (default 30, floored at 15 — quick enough that a phone-added entry shows
// up before the popcorn's done, kind enough to the list providers). The
// legacy lists_sync_hours setting still counts when minutes is unset.
func (s *Syncer) Interval() time.Duration {
	if s.Settings != nil {
		if v := s.Settings.Get("lists_sync_minutes"); v != "" {
			if m, err := strconv.Atoi(v); err == nil && m > 0 {
				return time.Duration(max(m, 15)) * time.Minute
			}
		}
		if v := s.Settings.Get("lists_sync_hours"); v != "" {
			if h, err := strconv.Atoi(v); err == nil && h > 0 {
				return time.Duration(h) * time.Hour
			}
		}
	}
	return 30 * time.Minute
}

// config is the union of every source's settings; each source reads its
// own fields.
type config struct {
	Chart  string `json:"chart,omitempty"`  // tmdb_chart / trakt_chart
	ListID int    `json:"listId,omitempty"` // tmdb_list / mdblist
	User   string `json:"user,omitempty"`   // trakt_list / mdblist
	Slug   string `json:"slug,omitempty"`   // trakt_list / mdblist
}

// item is the source-neutral list entry.
type item struct {
	kind   string
	title  string
	tmdbID int
}

// SyncAll walks every enabled list. Returns how many titles were added.
func (s *Syncer) SyncAll(ctx context.Context) int {
	return s.sync(ctx, func(*catalog.List) bool { return true })
}

// SyncDue walks the enabled lists whose own clocks have run out — each
// list carries its cadence, so one person's 15-minute watchlist and
// another's daily chart coexist. The watcher calls this every minute.
func (s *Syncer) SyncDue(ctx context.Context) int {
	now := time.Now().UTC()
	return s.sync(ctx, func(l *catalog.List) bool { return s.due(l, now) })
}

func (s *Syncer) sync(ctx context.Context, want func(*catalog.List) bool) int {
	all, err := s.Catalog.ListLists()
	if err != nil {
		log.Printf("reely: lists: %v", err)
		return 0
	}
	added := 0
	for i := range all {
		if !all[i].Enabled || !want(&all[i]) {
			continue
		}
		if err := ctx.Err(); err != nil {
			return added
		}
		n, _, err := s.SyncList(ctx, &all[i])
		if err != nil {
			log.Printf("reely: sync list %q: %v", all[i].Name, err)
			continue
		}
		added += n
	}
	return added
}

// due reports whether a list's own interval has elapsed. A list that has
// never synced is always due; an unparseable stamp re-syncs rather than
// silently never running again.
func (s *Syncer) due(l *catalog.List, now time.Time) bool {
	if l.LastSynced == "" {
		return true
	}
	last, err := time.Parse("2006-01-02 15:04:05", l.LastSynced)
	if err != nil {
		return true
	}
	return now.Sub(last) >= s.listInterval(l)
}

// UserCadenceKey names the setting holding one user's sync timer — one
// clock per person, covering every list they run.
func UserCadenceKey(userID int64) string { return fmt.Sprintf("lists_sync_minutes.%d", userID) }

// listInterval is one list's cadence: its creator's personal timer
// (floored at 15 minutes), or the install default when they never set one.
func (s *Syncer) listInterval(l *catalog.List) time.Duration {
	if s.Settings != nil && l.CreatedBy > 0 {
		if v := s.Settings.Get(UserCadenceKey(l.CreatedBy)); v != "" {
			if m, err := strconv.Atoi(v); err == nil && m > 0 {
				return time.Duration(max(m, 15)) * time.Minute
			}
		}
	}
	return s.Interval()
}

// SyncList runs one list now. Returns how many titles it added.
func (s *Syncer) SyncList(ctx context.Context, l *catalog.List) (added, requested int, err error) {
	if !s.TMDB.Configured() {
		return 0, 0, fmt.Errorf("no TMDB API key configured — lists need it to fetch titles")
	}
	lib, err := s.Catalog.GetLibrary(l.LibraryID)
	if err != nil {
		return 0, 0, fmt.Errorf("list %q has no library", l.Name)
	}
	kind := "movie"
	if lib.Kind == "shows" {
		kind = "show"
	}
	items, err := s.fetch(ctx, l, kind)
	if err != nil {
		return 0, 0, err
	}
	// Whose list this is decides what a new entry does. An account that
	// adds titles gets what lists have always done. A requester gets the
	// same thing they get from the search box: the entry becomes an ask,
	// and their auto-approve settings decide whether it stops in the
	// owner's queue or goes straight through. So a watchlist is one
	// mechanism, not two — the same permission answers both.
	owner := s.owner(l)
	for _, it := range items {
		if err := ctx.Err(); err != nil {
			return added, requested, err
		}
		// a mixed list feeds only the entries matching its library's kind
		if it.kind != kind || it.tmdbID <= 0 {
			continue
		}
		have, err := s.Catalog.HasTitle(kind, it.tmdbID, l.LibraryID)
		if err != nil || have {
			continue
		}
		if owner != nil {
			proceed, filed, err := s.request(ctx, owner, l, kind, it)
			if err != nil {
				log.Printf("reely: list %q: request %q: %v", l.Name, it.title, err)
				continue
			}
			if filed {
				requested++
			}
			if !proceed {
				continue
			}
		}
		got, err := s.add(ctx, l, kind, it)
		if err != nil {
			log.Printf("reely: list %q: add %q: %v", l.Name, it.title, err)
			continue
		}
		s.entitle(l, owner, kind, got)
		added++
	}
	if err := s.Catalog.TouchListSynced(l.ID); err != nil {
		log.Printf("reely: stamp list %q: %v", l.Name, err)
	}
	return added, requested, nil
}

func (s *Syncer) fetch(ctx context.Context, l *catalog.List, kind string) ([]item, error) {
	var cfg config
	if err := json.Unmarshal([]byte(l.Config), &cfg); err != nil {
		return nil, fmt.Errorf("bad list config: %w", err)
	}
	switch l.Source {
	case "tmdb_chart":
		rows, err := s.TMDB.Chart(ctx, cfg.Chart, kind, l.ItemLimit)
		if err != nil {
			return nil, err
		}
		out := make([]item, 0, len(rows))
		for _, r := range rows {
			out = append(out, item{kind: r.Kind, title: r.Title, tmdbID: r.TmdbID})
		}
		return out, nil
	case "tmdb_list":
		rows, err := s.TMDB.ListItems(ctx, cfg.ListID, l.ItemLimit)
		if err != nil {
			return nil, err
		}
		out := make([]item, 0, len(rows))
		for _, r := range rows {
			out = append(out, item{kind: r.Kind, title: r.Title, tmdbID: r.TmdbID})
		}
		return out, nil
	case "trakt_chart":
		rows, err := s.Trakt.Chart(ctx, cfg.Chart, kind, l.ItemLimit)
		if err != nil {
			return nil, err
		}
		return traktItems(rows, l.ItemLimit), nil
	case "trakt_list":
		rows, err := s.Trakt.ListItems(ctx, cfg.User, cfg.Slug)
		if err != nil {
			return nil, err
		}
		return traktItems(rows, l.ItemLimit), nil
	case "mdblist":
		rows, err := s.Mdblist.ListItems(ctx, s.MdblistKeyFor(l.CreatedBy), cfg.ListID, cfg.User, cfg.Slug, l.ItemLimit)
		if err != nil {
			return nil, err
		}
		out := make([]item, 0, len(rows))
		for _, r := range rows {
			out = append(out, item{kind: r.Kind, title: r.Title, tmdbID: r.TmdbID})
		}
		return out, nil
	}
	return nil, fmt.Errorf("unknown list source %q", l.Source)
}

// MdblistUserKey names the sealed setting holding one user's personal
// mdblist api key.
func MdblistUserKey(userID int64) string { return fmt.Sprintf("mdblist_key.%d", userID) }

// MdblistKeyFor resolves the api key an mdblist list syncs with: strictly
// its creator's personal key — no falling back to anyone else's. Lists
// from the open (pre-account) install use the shared key, because there
// is only one person then.
func (s *Syncer) MdblistKeyFor(createdBy int64) string {
	if s.Settings == nil {
		return ""
	}
	if createdBy > 0 {
		return s.Settings.Get(MdblistUserKey(createdBy))
	}
	return s.Settings.Get("mdblist_api_key")
}

func traktItems(rows []trakt.Item, limit int) []item {
	out := make([]item, 0, len(rows))
	for _, r := range rows {
		out = append(out, item{kind: r.Kind, title: r.Title, tmdbID: r.TmdbID})
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out
}

// add stores one new title the way the add endpoint would: full TMDB
// record, monitored, sibling files hard-linked in, a search queued for
// whatever is still missing.
// owner returns the account whose entries have to be requested rather
// than added, or nil when this list simply adds — no accounts on the
// install, no recorded owner, an admin, or an account trusted to add.
func (s *Syncer) owner(l *catalog.List) *auth.User {
	if s.Accounts == nil || l.CreatedBy == 0 {
		return nil
	}
	u := s.Accounts.UserByID(l.CreatedBy)
	if u == nil || u.IsAdmin() || u.MayAdd {
		return nil
	}
	return u
}

// request files a list entry as an ask. proceed says whether the add
// should go ahead now — false is the ordinary outcome for a pending
// request, whose add happens when the owner approves it — and filed says
// whether a new request was recorded, so a sync can report what it did.
//
// Every rule the search box's Request button obeys applies here, because
// it is the same ask arriving by a different route: a title the install
// already holds needs no decision, an auto-approved kind skips the queue,
// a weekly limit still counts, and asking twice is one request.
func (s *Syncer) request(ctx context.Context, owner *auth.User, l *catalog.List, kind string, it item) (proceed, filed bool, err error) {
	// A title the owner already turned down is not asked for again. A
	// person may re-ask — that is what denial returning a title to
	// askable means — but a list re-filing it every half hour is nagging,
	// and the answer is already on record.
	denied, err := s.Catalog.EverDenied(l.LibraryID, kind, it.tmdbID)
	if err != nil || denied {
		return false, false, err
	}
	held, err := s.Catalog.HeldAnywhere(kind, it.tmdbID, 0)
	if err != nil {
		return false, false, err
	}
	auto := held ||
		(kind == "movie" && owner.AutoApproveMovies) ||
		(kind == "show" && owner.AutoApproveShows)
	if !auto {
		over, err := s.overQuota(owner, kind)
		if err != nil {
			return false, false, err
		}
		if over {
			// silently: a list is not a person waiting on an answer, and it
			// will offer the same entry again once the week rolls over
			return false, false, nil
		}
	}
	status := "pending"
	if auto {
		status = "approved"
	}
	title, year, poster := s.describe(ctx, kind, it)
	_, err = s.Catalog.CreateRequest(catalog.Request{
		UserID: owner.ID, LibraryID: l.LibraryID, Kind: kind, TmdbID: it.tmdbID,
		Title: title, Year: year, Poster: poster, Status: status,
	})
	if errors.Is(err, catalog.ErrAlreadyRequested) {
		// already waiting on a decision, or already approved and being
		// added; either way this sync has nothing left to do with it
		return false, false, nil
	}
	if err != nil {
		return false, false, err
	}
	return auto, true, nil
}

// describe fills in what a queued request has to render — the owner
// decides from a poster and a year, and a list entry carries neither.
// Best effort: a request that reads as a bare title still decides.
func (s *Syncer) describe(ctx context.Context, kind string, it item) (title string, year int, poster string) {
	title = it.title
	if kind == "movie" {
		d, err := s.TMDB.Movie(ctx, it.tmdbID)
		if err != nil {
			return title, 0, ""
		}
		return d.Title, d.Year, d.Poster
	}
	d, err := s.TMDB.Show(ctx, it.tmdbID)
	if err != nil {
		return title, 0, ""
	}
	return d.Title, d.Year, d.Poster
}

func (s *Syncer) overQuota(owner *auth.User, kind string) (bool, error) {
	limit := owner.QuotaMoviesWeek
	if kind == "show" {
		limit = owner.QuotaShowsWeek
	}
	if limit == nil {
		return false, nil
	}
	used, err := s.Catalog.RequestsThisWeek(owner.ID, kind)
	if err != nil {
		return false, err
	}
	return used >= *limit, nil
}

// entitle shares what a list just added with whoever the list is for.
//
// A list entitled nobody until now, so every sync filled a library with
// titles no shared account could watch until somebody tagged them by
// hand — which for a chart refreshing twice a day is not a thing anybody
// keeps up with.
//
// The audience on the list wins where there is one. Only an admin can
// set it, so it is an owner's decision about any group on the install
// and is applied as given. Where there is none, a list owned by a
// requester falls back to their own households — the same targets
// approving their request by hand would have picked, which until now was
// the only path that entitled anything at all: an entry the syncer
// auto-approved was added and shared with nobody, while the identical
// entry approved by a click was shared. That was a bug wearing a
// coincidence's clothes.
//
// A failure is logged, never returned. The title is in the library; the
// sync should not stop because the sharing bookkeeping stumbled, and the
// next pass writes what this one missed.
func (s *Syncer) entitle(l *catalog.List, owner *auth.User, kind string, got stored) {
	if got.tmdbID <= 0 && got.tvdbID <= 0 {
		return
	}
	targets := l.GroupIDs
	if len(targets) == 0 {
		if owner == nil {
			return // an owner's own list with no audience: as it was
		}
		var err error
		if targets, err = s.Catalog.RequestTargets(owner.ID, nil); err != nil {
			log.Printf("reely: list %q: no group for %q: %v", l.Name, got.title, err)
			return
		}
	}
	by := int64(0)
	if owner != nil {
		by = owner.ID
	}
	for _, gid := range targets {
		if err := s.Catalog.Grant(gid, kind, got.tmdbID, got.tvdbID, got.title, by); err != nil {
			log.Printf("reely: list %q: grant %q: %v", l.Name, got.title, err)
			return
		}
	}
}

// stored is what an add put in the library, by the ids a grant is keyed
// on. A show routed through TheTVDB has both; a movie has one.
type stored struct {
	tmdbID, tvdbID int
	title          string
}

func (s *Syncer) add(ctx context.Context, l *catalog.List, kind string, it item) (stored, error) {
	if kind == "movie" {
		detail, err := s.TMDB.Movie(ctx, it.tmdbID)
		if err != nil {
			return stored{}, err
		}
		id, err := s.Catalog.UpsertMovie(detail, l.LibraryID)
		if err != nil {
			return stored{}, err
		}
		note, _ := json.Marshal(map[string]string{"title": detail.Title, "list": l.Name})
		if err := s.Catalog.AddHistory("added", id, 0, 0, string(note)); err != nil {
			return stored{}, err
		}
		if !s.Grab.LinkMovieFromSiblings(id) {
			s.Grab.EnqueueMovie(id)
		}
		return stored{tmdbID: detail.TmdbID, title: detail.Title}, nil
	}
	detail, err := s.TMDB.Show(ctx, it.tmdbID)
	if err != nil {
		return stored{}, err
	}
	var id int64
	if s.TVDB != nil && s.TVDB.Configured() && detail.TvdbID > 0 {
		// same steering as the add endpoint: the TMDB record's external id
		// points at the TVDB series, which becomes the show's source
		tv, err := s.TVDB.Show(ctx, detail.TvdbID)
		if err != nil {
			return stored{}, err
		}
		metadata.EnrichShowFromTMDB(ctx, s.TMDB, tv)
		id, err = s.Catalog.UpsertShowTVDB(tv, l.LibraryID)
		if err != nil {
			return stored{}, err
		}
		detail = tv
	} else if id, err = s.Catalog.UpsertShow(detail, l.LibraryID); err != nil {
		return stored{}, err
	}
	note, _ := json.Marshal(map[string]string{"title": detail.Title, "list": l.Name})
	if err := s.Catalog.AddHistory("added", 0, id, 0, string(note)); err != nil {
		return stored{}, err
	}
	s.Grab.LinkShowFromSiblings(id)
	s.Grab.EnqueueShow(id, 0)
	// both ids where the record carries both: a grant may be looked up by
	// either, and which one a later lookup uses is not this code's to know
	return stored{tmdbID: detail.TmdbID, tvdbID: detail.TvdbID, title: detail.Title}, nil
}
