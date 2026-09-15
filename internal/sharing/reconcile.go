// Package sharing projects reely's entitlements onto Plex.
//
// The entitlements are the truth and are written the moment a request is
// approved. What Plex holds — a label on each title, a restriction on
// each share — is a projection, brought into line by the pass here.
//
// That separation is what makes the timing tractable. A title reely just
// downloaded may not exist in Plex for minutes: the file is on disk but
// the server has not scanned it, so there is no item to label. Nothing
// waits for that. The entitlement is already real, the pass finds no
// item, and it tries again next time — while a targeted scan asks Plex
// to look at that folder now rather than at its own leisure.
//
// Every pass is idempotent and computes the whole desired state from the
// database, which is also why a renamed group costs nothing: the old
// label is simply not in the set any more and comes off.
package sharing

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/plex"
)

// Config is what the owner linked. Token is the OWNER's — the only
// account that may label titles or rewrite shares.
type Config struct {
	Token     string
	MachineID string
	ServerURL string
}

// WaitingTitle is one title with no item on the media server, and why.
type WaitingTitle struct {
	Title string `json:"title"`
	// OnDisk means reely holds the file. Then this is not a download to
	// wait for — the server has that file under a different identity,
	// and it needs matching over there rather than patience here.
	OnDisk bool `json:"onDisk"`
}

// waitingNamed caps how many waiting titles are named. The count is
// always exact; the list is for recognising what kind of thing is
// waiting, and a thousand names would not help anybody do that.
const waitingNamed = 50

// staleAfter is how long a grant must stay unresolvable before it is
// treated as an orphan rather than as a scan that has not caught up. A
// day is many healthy passes: a transient failure sets the clock and the
// next good pass clears it, so only a permanent one ever runs it out.
const staleAfter = 24 * time.Hour

// Reconciler brings Plex into line with the entitlements.
type Reconciler struct {
	Cat  *catalog.Store
	Plex *plex.Client
	Cfg  Config
}

// Result is what one pass did, for the activity log and the health
// panel.
type Result struct {
	// Pending is titles somebody is entitled to that Plex has not seen
	// yet. Not a fault — the ordinary state of a fresh download.
	Pending int `json:"pending"`
	// Waiting names them, up to a limit. A count alone leaves the owner
	// guessing which titles it means, and the two reasons want opposite
	// responses — so each says which it is.
	Waiting []WaitingTitle `json:"waiting,omitempty"`
	// Mismatched counts the waiting titles reely holds a file for. Those
	// are not downloads to be patient about: the media server has the
	// same film under a different identity, and only a person can say
	// which of them is right.
	Mismatched int `json:"mismatched,omitempty"`
	Labelled   int `json:"labelled"`
	Shares     int `json:"shares"`
	// Drifted names people whose share was changed outside reely since
	// the last write. Reported rather than silently overwritten.
	Drifted []string `json:"drifted,omitempty"`
	// Held is set when restrictions were deliberately not written
	// because labelling did not fully succeed. Nobody's access changed,
	// and the next pass tries again.
	Held bool `json:"held,omitempty"`
	// Pruned counts grants dropped because nothing on the install has
	// that title any more — reely has no row for it and the media server
	// has no item. Seeding from a wrong match is where they come from.
	Pruned int      `json:"pruned,omitempty"`
	Errors []string `json:"errors,omitempty"`
}

// Run makes one pass. It never returns early on a single failure: one
// unreachable title must not stop everybody else's access being correct.
func (r *Reconciler) Run(ctx context.Context) (Result, error) {
	var res Result
	if strings.TrimSpace(r.Cfg.Token) == "" || strings.TrimSpace(r.Cfg.ServerURL) == "" {
		return res, fmt.Errorf("sharing: Plex is not linked")
	}

	sections, labels, err := r.refreshCache(ctx)
	if err != nil {
		return res, err
	}

	r.labelTitles(ctx, sections, labels, &res)

	// Labels first, then restrictions — and only if the labels all
	// landed. A restriction written over a half-tagged library hides
	// titles somebody is entitled to: it narrows their access on the
	// strength of a projection that is not finished. Waiting costs them
	// nothing, because until the restriction is written they still see
	// what they saw before.
	//
	// Pending titles are not a reason to wait. A title Plex has not
	// scanned yet has no item to tag, and waiting for one would mean
	// never writing a restriction on an install where anything is mid
	// download.
	if len(res.Errors) > 0 {
		res.Held = true
		return res, nil
	}

	// Only on a pass that reached the server, read its items and
	// reported nothing wrong. A grant nothing holds is normally an
	// orphan from seeding, but a section that failed to scan makes every
	// grant for it look the same — so the condition has to have held for
	// staleAfter, across passes, before anything is dropped.
	if stale, err := r.Cat.SweepStale(staleAfter); err != nil {
		res.Errors = append(res.Errors, err.Error())
	} else {
		res.Pruned = len(stale)
		for _, st := range stale {
			log.Printf("reely: sharing — dropped a stale grant: %s %q (tmdb %d, tvdb %d)",
				st.Kind, st.Title, st.TmdbID, st.TvdbID)
		}
	}

	r.writeShares(ctx, &res)
	return res, nil
}

// RefreshItems reads what Plex holds into the cache without changing
// anything over there. The seed calls it: granting from reely's own
// catalog alone would miss whatever was in Plex before reely existed.
func (r *Reconciler) RefreshItems(ctx context.Context) error {
	_, _, err := r.refreshCache(ctx)
	return err
}

// refreshCache reads every section and stores what Plex holds, returning
// the section id for each kind.
func (r *Reconciler) refreshCache(ctx context.Context) (map[string]string, map[int64][]string, error) {
	libs, err := r.Plex.Sections(ctx, r.Cfg.ServerURL, r.Cfg.Token)
	if err != nil {
		return nil, nil, fmt.Errorf("sharing: read libraries: %w", err)
	}
	kinds := map[string]string{}
	// what each item already carries, from the same listing. Keeping it
	// is what lets a pass over a settled library cost two requests
	// instead of one per title.
	labels := map[int64][]string{}
	for _, lib := range libs {
		kind := lib.Type // plex's own word: movie | show
		if kind != "movie" && kind != "show" {
			continue
		}
		items, err := r.Plex.Items(ctx, r.Cfg.ServerURL, r.Cfg.Token, lib.Key)
		if err != nil {
			return nil, nil, fmt.Errorf("sharing: read section %s: %w", lib.Title, err)
		}
		sectionID, _ := strconv.ParseInt(lib.Key, 10, 64)
		rows := make([]catalog.PlexItem, 0, len(items))
		for _, it := range items {
			rows = append(rows, catalog.PlexItem{
				Kind: kind, RatingKey: it.RatingKey, SectionID: sectionID,
				TmdbID: it.TmdbID, TvdbID: it.TvdbID, ImdbID: it.ImdbID,
				Title: it.Title,
			})
			labels[it.RatingKey] = it.Labels
		}
		if err := r.Cat.CachePlexItems(sectionID, rows); err != nil {
			return nil, nil, err
		}
		if _, seen := kinds[kind]; !seen {
			kinds[kind] = lib.Key
		}
	}
	return kinds, labels, nil
}

// labelTitles writes each entitled title's labels. A title nobody is
// entitled to is not visited: taking reely's labels off something that
// fell out of every group is the stale sweep's job, and that walks the
// cache rather than the entitlements.
func (r *Reconciler) labelTitles(ctx context.Context, sections map[string]string,
	known map[int64][]string, res *Result) {
	titles, err := r.Cat.EntitledTitles()
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
		return
	}
	for _, t := range titles {
		item, err := r.Cat.PlexItemFor(t.Kind, t.TmdbID, t.TvdbID)
		if err != nil {
			res.Errors = append(res.Errors, err.Error())
			continue
		}
		if item == nil {
			// Plex has not scanned it yet, or does not have it at all —
			// a title with no file yet is the usual reason. Leave the
			// entitlement; the next pass tries again.
			res.Pending++
			if t.OnDisk {
				res.Mismatched++
			}
			if len(res.Waiting) < waitingNamed {
				name := t.Title
				if name == "" {
					name = fmt.Sprintf("%s %d", t.Kind, max(t.TmdbID, t.TvdbID))
				}
				res.Waiting = append(res.Waiting, WaitingTitle{Title: name, OnDisk: t.OnDisk})
			}
			continue
		}
		want, err := r.Cat.LabelsFor(t.Kind, t.TmdbID, t.TvdbID)
		if err != nil {
			res.Errors = append(res.Errors, err.Error())
			continue
		}
		have, seen := known[item.RatingKey]
		if seen && settled(have, want) {
			continue // already right; not worth a request to confirm
		}
		section := strconv.FormatInt(item.SectionID, 10)
		if s, ok := sections[t.Kind]; ok && item.SectionID == 0 {
			section = s
		}
		if err := r.Plex.SetLabels(ctx, r.Cfg.ServerURL, r.Cfg.Token, t.Kind,
			section, item.RatingKey, want, have, catalog.Managed); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("%s: %v", t.Title, err))
			continue
		}
		res.Labelled++
	}
}

// writeShares brings each managed account's restriction into line.
//
// Only accounts explicitly opted in are touched. Turning management on
// narrows what somebody sees, so an install whose shares are
// unrestricted today keeps them that way until each person is moved
// across deliberately.
func (r *Reconciler) writeShares(ctx context.Context, res *Result) {
	users, err := r.Cat.ManagedUsers()
	if err != nil {
		res.Errors = append(res.Errors, err.Error())
		return
	}
	if len(users) == 0 {
		return
	}
	shares, err := r.Plex.SharedServers(ctx, r.Cfg.Token, r.Cfg.MachineID)
	if err != nil {
		res.Errors = append(res.Errors, fmt.Sprintf("read shares: %v", err))
		return
	}
	live := map[int64]plex.Share{}
	for _, sh := range shares {
		live[sh.AccountID] = sh
	}

	// Two managed accounts pointing at one Plex account would each write
	// the whole restriction for that share, so whichever ran last would
	// decide what the person sees — and it would change between passes
	// as the order did. Neither is written: silently applying one of two
	// contradictory answers is worse than applying none and saying so,
	// because nothing about the result would look wrong.
	claimed := map[int64][]int64{}
	for _, uid := range users {
		if account, err := r.Cat.ShareAccount(uid); err == nil && account != 0 {
			claimed[account] = append(claimed[account], uid)
		}
	}

	for _, uid := range users {
		account, err := r.Cat.ShareAccount(uid)
		if err != nil || account == 0 {
			continue
		}
		if len(claimed[account]) > 1 {
			res.Errors = append(res.Errors, fmt.Sprintf(
				"accounts %v all watch on Plex account %d — only one may, "+
					"so none of their shares were written", claimed[account], account))
			continue
		}
		movies, err := r.Cat.RestrictionFor(uid, "movie")
		if err != nil {
			res.Errors = append(res.Errors, err.Error())
			continue
		}
		shows, err := r.Cat.RestrictionFor(uid, "show")
		if err != nil {
			res.Errors = append(res.Errors, err.Error())
			continue
		}

		state, err := r.Cat.ShareStateOf(uid)
		if err != nil {
			res.Errors = append(res.Errors, err.Error())
			continue
		}
		cur, known := live[account]
		if !known {
			res.Errors = append(res.Errors, fmt.Sprintf(
				"account %d is not shared with on this server", account))
			continue
		}
		// A share that no longer matches what reely last wrote was edited
		// by hand. Say so — but still write, because leaving it is how a
		// person quietly keeps access they were meant to lose.
		if state.WrittenAt != "" &&
			(!sameRestriction(cur.FilterMovies, state.Movies) ||
				!sameRestriction(cur.FilterShows, state.Shows)) {
			res.Drifted = append(res.Drifted, cur.Username)
		}
		if sameRestriction(cur.FilterMovies, movies) && sameRestriction(cur.FilterShows, shows) {
			continue // already right
		}
		if err := r.Plex.SetRestrictions(ctx, r.Cfg.Token, account, movies, shows); err != nil {
			res.Errors = append(res.Errors, fmt.Sprintf("share for %s: %v", cur.Username, err))
			continue
		}
		if err := r.Cat.RecordWrite(uid, movies, shows); err != nil {
			res.Errors = append(res.Errors, err.Error())
		}
		res.Shares++
	}

	// A write is only believed after it is read back: these endpoints
	// answer 200 to payloads they ignore.
	if res.Shares > 0 {
		if err := r.verify(ctx, users, res); err != nil {
			res.Errors = append(res.Errors, err.Error())
		}
	}
}

func (r *Reconciler) verify(ctx context.Context, users []int64, res *Result) error {
	shares, err := r.Plex.SharedServers(ctx, r.Cfg.Token, r.Cfg.MachineID)
	if err != nil {
		return fmt.Errorf("verify shares: %w", err)
	}
	live := map[int64]plex.Share{}
	for _, sh := range shares {
		live[sh.AccountID] = sh
	}
	for _, uid := range users {
		account, err := r.Cat.ShareAccount(uid)
		if err != nil || account == 0 {
			continue
		}
		want, err := r.Cat.ShareStateOf(uid)
		if err != nil || want.WrittenAt == "" {
			continue
		}
		got, ok := live[account]
		if !ok {
			continue
		}
		if !sameRestriction(got.FilterMovies, want.Movies) ||
			!sameRestriction(got.FilterShows, want.Shows) {
			res.Errors = append(res.Errors, fmt.Sprintf(
				"share for %s did not take: Plex still reports %q",
				got.Username, got.FilterMovies))
		}
	}
	return nil
}

// settled reports whether an item already carries exactly the labels
// reely wants, ignoring any it does not own — those are somebody's own
// tags and are none of its business either way.
//
// Case is folded because Plex title-cases what it stores, so a label
// read back never matches what was sent byte for byte; comparing
// literally would make every pass rewrite every item forever.
func settled(have, want []string) bool {
	mine := map[string]bool{}
	for _, l := range have {
		if catalog.Managed(l) {
			mine[strings.ToLower(l)] = true
		}
	}
	if len(mine) != len(want) {
		return false
	}
	for _, l := range want {
		if !mine[strings.ToLower(l)] {
			return false
		}
	}
	return true
}

// sameRestriction compares two restriction strings by the labels they
// name rather than byte for byte: Plex stores what it was given, and two
// strings naming the same labels in a different order restrict
// identically.
func sameRestriction(a, b string) bool {
	return strings.EqualFold(strings.Join(labels(a), ","), strings.Join(labels(b), ","))
}

func labels(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	s = strings.TrimPrefix(s, "label=")
	parts := strings.Split(strings.ReplaceAll(s, "%2C", ","), ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			out = append(out, p)
		}
	}
	sort.Strings(out)
	return out
}

// Log writes one pass's outcome where the owner can see it.
func (res Result) Log() {
	log.Printf("reely: sharing — %d titles labelled, %d shares written, %d waiting on Plex",
		res.Labelled, res.Shares, res.Pending)
	if res.Mismatched > 0 {
		log.Printf("reely: sharing — %d of those are on disk here: Plex holds the same "+
			"file under a different title, so they need matching over there", res.Mismatched)
	}
	if res.Held {
		log.Printf("reely: sharing — shares left alone this pass: labelling did not finish, " +
			"so nobody's access was narrowed on a half-tagged library")
	}
	for _, d := range res.Drifted {
		log.Printf("reely: sharing — %s's Plex share was changed outside reely", d)
	}
	for _, e := range res.Errors {
		log.Printf("reely: sharing — %s", e)
	}
}
