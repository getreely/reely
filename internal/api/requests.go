package api

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"

	"github.com/getreely/reely/internal/auth"
	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/metadata"
)

// The request flow. An account that may browse a library but not add to
// it asks instead, and the owner decides. Approving runs the ordinary
// add — nothing here reimplements it.
//
// Two rules shape every handler below. A requester is told a title has
// been asked for and never by whom, so nothing on their side carries a
// username. And a request belongs to one library, so "already asked
// for?" is answered against the libraries this account can actually
// reach and nowhere else.

// requestSettings is what an account may do without asking, as the admin
// UI reads and writes it.
type requestSettings struct {
	MayAdd            bool `json:"mayAdd"`
	AutoApproveMovies bool `json:"autoApproveMovies"`
	AutoApproveShows  bool `json:"autoApproveShows"`
	QuotaMoviesWeek   *int `json:"quotaMoviesWeek"`
	QuotaShowsWeek    *int `json:"quotaShowsWeek"`
}

// visibleLibraries is the set an account may see: every library for an
// admin or an open install, the granted ones otherwise. Requests are
// read and written against exactly this.
func (s *Server) visibleLibraries(r *http.Request) ([]int64, error) {
	libs, err := s.Catalog.ListLibraries()
	if err != nil {
		return nil, err
	}
	a := s.access(r)
	ids := make([]int64, 0, len(libs))
	for _, l := range libs {
		if a.mayLibrary(l.ID) {
			ids = append(ids, l.ID)
		}
	}
	return ids, nil
}

// handleRequests lists the open requests in the libraries this account
// can reach — what the browse and search views mark titles from. It
// names nobody.
func (s *Server) handleRequests(w http.ResponseWriter, r *http.Request) {
	ids, err := s.visibleLibraries(r)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	out, err := s.Catalog.OpenRequests(ids)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// ?mine=1 narrows it to the caller's own asks.
	//
	// The wide listing is the default because it is what stops a second
	// person asking for something already on its way — Explore reads it
	// to say "requested" — and that only works if it carries everybody's.
	// A page called Requests wants the other thing: what I asked for.
	// Same route, because it is the same data seen two ways, and opting
	// in leaves the badge working.
	if r.URL.Query().Get("mine") == "1" {
		if u := s.sessionUser(r); u != nil {
			mine := make([]catalog.Request, 0, len(out))
			for _, req := range out {
				if req.UserID == u.ID {
					mine = append(mine, req)
				}
			}
			out = mine
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"requests": out})
}

// handleCreateRequest records an ask, or performs it outright when the
// account is trusted with that kind.
func (s *Server) handleCreateRequest(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind      string `json:"kind"`
		TmdbID    int    `json:"tmdbId"`
		TvdbID    int    `json:"tvdbId"`
		Title     string `json:"title"`
		Year      int    `json:"year"`
		Poster    string `json:"poster"`
		Seasons   []int  `json:"seasons"`
		LibraryID int64  `json:"libraryId"` // 0 = wherever their requests land
		// Audience is which of the asker's own groups this should reach
		// once approved. Absent means all of them; an empty list keeps it
		// to the asker. Anything naming a group they are not in is
		// dropped when the grant is made rather than trusted here.
		Audience []int64 `json:"audience"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Kind != "movie" && req.Kind != "show" {
		writeErr(w, http.StatusBadRequest, errors.New("kind must be movie or show"))
		return
	}
	if req.Title == "" || (req.TmdbID == 0 && req.TvdbID == 0) {
		writeErr(w, http.StatusBadRequest, errors.New("a request needs a title and an id"))
		return
	}
	user := s.sessionUser(r)
	if user == nil {
		// an open install has no accounts to attribute a request to, and
		// nobody to approve it either — there is nothing to request from
		writeErr(w, http.StatusPreconditionFailed,
			errors.New("requests need an account; add one in Settings"))
		return
	}

	// Where it lands: what they asked for, else their default. Naming a
	// library they cannot reach is a 404, the same answer as one that
	// does not exist — a requester learns nothing about the rest.
	//
	// An admin names one, because they hold every library and no answer
	// is more obviously right than another. Deliberate rather than a
	// consequence of admins having no stored grants, which is what would
	// otherwise be quietly deciding it.
	//
	// Unless there is only one it could be. A choice with one option is
	// not a choice, and refusing it would be reely asking a question it
	// already knows the answer to — the same rule a requester gets from
	// DefaultLibrary, which falls back to the only library they hold.
	libraryID := req.LibraryID
	if libraryID == 0 {
		var err error
		if user.IsAdmin() {
			libraryID, err = s.onlyLibraryFor(req.Kind)
		} else {
			libraryID, err = s.Auth.DefaultLibrary(user.ID)
		}
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	if libraryID == 0 {
		writeErr(w, http.StatusBadRequest,
			errors.New("pick which library this should go to"))
		return
	}
	if !s.access(r).mayLibrary(libraryID) {
		writeErr(w, http.StatusNotFound, errors.New("not found"))
		return
	}

	// A title the install already holds is approved on the spot, whatever
	// this account's settings say: the file is here, and adding it to
	// another library hardlinks rather than downloads, so there is no
	// decision left to make. It doesn't spend a weekly ask either — a
	// quota bounds how often somebody sends the owner off to fetch
	// something, and this fetches nothing.
	//
	// A series is the loose end: holding a show is not holding every
	// season of it, so an approved series request can still search for
	// the seasons that are missing. That is the same thing approving it
	// by hand would have done, so it is the rule working rather than an
	// exception to it.
	held, err := s.Catalog.HeldAnywhere(req.Kind, req.TmdbID, req.TvdbID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if !held {
		if over, err := s.overQuota(user, req.Kind); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		} else if over {
			writeErr(w, http.StatusTooManyRequests,
				errors.New("that's this week's limit — ask again next week, or ask the owner to raise it"))
			return
		}
	}

	// An account that may add titles without asking does not need
	// approval for asking — there is nobody above it to give any. An
	// admin asking would be putting a request in their own queue and
	// then approving it, which is a step with one possible outcome.
	//
	// This is what the portal makes visible: out there nobody adds,
	// because the add routes are not served, so an owner or a trusted
	// account asks like everybody else. Their permission has not
	// changed, only the door they came through.
	auto := held || user.IsAdmin() || user.MayAdd ||
		(req.Kind == "movie" && user.AutoApproveMovies) ||
		(req.Kind == "show" && user.AutoApproveShows)
	status := "pending"
	if auto {
		// recorded as approved rather than skipped entirely, so an
		// auto-approved ask still shows who asked and for what
		status = "approved"
	}
	id, err := s.Catalog.CreateRequest(catalog.Request{
		UserID: user.ID, LibraryID: libraryID, Kind: req.Kind,
		TmdbID: req.TmdbID, TvdbID: req.TvdbID, Title: req.Title,
		Year: req.Year, Poster: req.Poster, Seasons: req.Seasons, Status: status,
		Audience: req.Audience,
	})
	switch {
	case errors.Is(err, catalog.ErrAlreadyRequested):
		writeErr(w, http.StatusConflict, err)
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if auto {
		// an auto-approved request that recorded an approval and added
		// nothing would be the worst of both
		// carries the asker, the title and the audience: fulfilling
		// shares the result with them, and a row missing those would add
		// the title and entitle nobody
		stored := catalog.Request{
			ID: id, UserID: user.ID, Kind: req.Kind, TmdbID: req.TmdbID,
			TvdbID: req.TvdbID, Title: req.Title, LibraryID: libraryID,
			Seasons: req.Seasons, Audience: req.Audience,
		}
		if err := s.fulfil(r.Context(), stored); err != nil {
			// the row stays approved: the ask is on record, and the owner
			// sees the failure in Activity rather than the request queue
			log.Printf("reely: auto-approved request %d (%s): %v", id, req.Title, err)
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "status": status})
}

// overQuota reports whether an account has used up its week. No quota is
// no limit, which is what every account starts with.
// onlyLibraryFor is the single library a title of this kind could go to,
// or 0 when there is a choice to make. A movie cannot go to a series
// library, so "only one" means only one of the right kind.
func (s *Server) onlyLibraryFor(kind string) (int64, error) {
	want := "movies"
	if kind == "show" {
		want = "shows"
	}
	libs, err := s.Catalog.ListLibraries()
	if err != nil {
		return 0, err
	}
	var found int64
	for _, l := range libs {
		if l.Kind != want {
			continue
		}
		if found != 0 {
			return 0, nil // more than one: theirs to pick
		}
		found = l.ID
	}
	return found, nil
}

func (s *Server) overQuota(user *auth.User, kind string) (bool, error) {
	// A quota bounds how often somebody sends the owner off to fetch
	// something. There is nobody above an admin to be sent, and their
	// asks are approved on the spot — so a limit would refuse a request
	// that needed no permission, which reads as a fault rather than a
	// rule. A stored limit outlives a promotion, so this is asked of the
	// role rather than of whether one happens to be set.
	if user.IsAdmin() {
		return false, nil
	}
	limit := user.QuotaMoviesWeek
	if kind == "show" {
		limit = user.QuotaShowsWeek
	}
	if limit == nil {
		return false, nil
	}
	used, err := s.Catalog.RequestsThisWeek(user.ID, kind)
	if err != nil {
		return false, err
	}
	return used >= *limit, nil
}

// fulfil performs an approved request: the same fetch and store a direct
// add does, into the library the request named. Approving is a decision,
// not a second way of adding something, so this goes through storeMovie
// and storeShow rather than repeating them.
func (s *Server) fulfil(ctx context.Context, req catalog.Request) error {
	if req.Kind == "movie" {
		detail, err := s.TMDB.Movie(ctx, req.TmdbID)
		if err != nil {
			return err
		}
		if _, err := s.storeMovie(detail, req.LibraryID); err != nil {
			return err
		}
		s.entitleRequester(req)
		return nil
	}
	detail, source, err := s.fetchShowToAdd(ctx, &addRequest{
		TmdbID: req.TmdbID, TvdbID: req.TvdbID, LibraryID: req.LibraryID,
		MonitoredSeasons: req.Seasons,
	})
	if err != nil {
		return err
	}
	if _, err := s.storeShow(ctx, detail, source, req.LibraryID, req.Seasons); err != nil {
		return err
	}
	s.entitleRequester(req)
	return nil
}

// entitleRequester shares a fulfilled request with whoever asked for it.
//
// Their own group always, plus whichever of their households the request
// named — every one of them by default, because a family member asking
// for a film usually means the family should get it. Granting their own
// as well is what makes leaving a household later not take away the
// things they asked for themselves.
//
// A failure is logged rather than returned: the title has been added,
// and refusing the approval because the sharing bookkeeping stumbled
// would be the wrong half to fail. The owner can set the audience on the
// title itself, and the next reconcile picks it up.
func (s *Server) entitleRequester(req catalog.Request) {
	targets, err := s.Catalog.RequestTargets(req.UserID, req.Audience)
	if err != nil {
		log.Printf("reely: sharing — no group for request %d: %v", req.ID, err)
		return
	}
	for _, gid := range targets {
		if err := s.Catalog.Grant(gid, req.Kind, req.TmdbID, req.TvdbID,
			req.Title, req.UserID); err != nil {
			log.Printf("reely: sharing — grant %s: %v", req.Title, err)
			return
		}
	}
	s.reconcileSoon()
}

// handleRequestQueue is the owner's list of what is waiting, with the
// asker and the destination named — the one place a username appears.
func (s *Server) handleRequestQueue(w http.ResponseWriter, r *http.Request) {
	out, err := s.Catalog.QueuedRequests()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// the queue draws posters, and a stored poster is either a TMDB path
	// needing this base or an absolute TVDB URL that ignores it
	writeJSON(w, http.StatusOK, map[string]any{
		"requests": out, "imageBase": metadata.ImageBase})
}

// handleDecideRequest approves or denies. Denying leaves the row for the
// history and drops it out of the open index, so the title can be asked
// for again.
func (s *Server) handleDecideRequest(status string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		admin := s.sessionUser(r)
		var adminID int64
		if admin != nil {
			adminID = admin.ID
		}
		req, err := s.Catalog.GetRequest(id)
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, errors.New("no such request"))
			return
		} else if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		// The title is added BEFORE the decision is recorded. A failed add
		// then leaves the request pending and visible rather than marked
		// approved with nothing behind it — the owner can approve again,
		// and the add is an upsert either way.
		if status == "approved" {
			if err := s.fulfil(r.Context(), req); err != nil {
				tmdbErr(w, err)
				return
			}
		}
		err = s.Catalog.DecideRequest(id, adminID, status)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			writeErr(w, http.StatusNotFound, errors.New("no such pending request"))
			return
		case err != nil:
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": status})
	}
}

// handleSetDefaultLibrary records where the caller's OWN requests land.
// The only thing an account may write about itself, and narrow by
// design: it names a library they already hold and changes nothing else.
func (s *Server) handleSetDefaultLibrary(w http.ResponseWriter, r *http.Request) {
	var req struct {
		LibraryID int64 `json:"libraryId"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	user := s.sessionUser(r)
	if user == nil {
		writeErr(w, http.StatusPreconditionFailed, errors.New("no account to set a default for"))
		return
	}
	err := s.Auth.SetDefaultLibrary(user.ID, req.LibraryID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		// they don't have that library; same answer as one that isn't there
		writeErr(w, http.StatusNotFound, errors.New("not found"))
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"libraryId": req.LibraryID})
}

// handleSetRequestSettings is the admin's control over one account:
// whether it adds directly, what skips the queue, and how often it may
// ask.
func (s *Server) handleSetRequestSettings(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req requestSettings
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	err := s.Auth.SetRequestSettings(id, req.MayAdd, req.AutoApproveMovies,
		req.AutoApproveShows, req.QuotaMoviesWeek, req.QuotaShowsWeek)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeErr(w, http.StatusNotFound, errors.New("no such user"))
		return
	case err != nil:
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}
