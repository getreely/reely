package api

import (
	"context"
	"errors"
	"log"
	"net/http"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/refresh"
)

// Adding a title from TMDB search: fetch its full record, store it
// monitored in the chosen library, and kick off an active search.
// Anyone with the library may add into it.

type addRequest struct {
	TmdbID int `json:"tmdbId"`
	// TvdbID is how TVDB-sourced search results ask (shows only, when a
	// TVDB key is configured). Exactly one of the two ids is enough.
	TvdbID    int   `json:"tvdbId"`
	LibraryID int64 `json:"libraryId"`
	// MonitoredSeasons picks which seasons start monitored (shows only).
	// Nil means all of them — the add-everything default.
	MonitoredSeasons []int `json:"monitoredSeasons"`
	// GroupIDs is who the title is for. Nil means the adder's own group,
	// which is the common case; naming somebody else's is how an owner
	// adds a title for the person who asked them in the kitchen without
	// it turning up in their own library.
	GroupIDs []int64 `json:"groupIds"`
}

func (s *Server) decodeAdd(w http.ResponseWriter, r *http.Request, kind string) (*addRequest, bool) {
	var req addRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return nil, false
	}
	if req.TmdbID <= 0 && (kind != "shows" || req.TvdbID <= 0) {
		writeErr(w, http.StatusBadRequest, errors.New("missing tmdbId"))
		return nil, false
	}
	lib, err := s.Catalog.GetLibrary(req.LibraryID)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("no such library"))
		return nil, false
	}
	if lib.Kind != kind {
		writeErr(w, http.StatusBadRequest, errors.New("wrong library kind for this title"))
		return nil, false
	}
	if !s.access(r).mayLibrary(req.LibraryID) {
		writeErr(w, http.StatusForbidden, errors.New("no access to this library"))
		return nil, false
	}
	// shows can arrive purely through TVDB when a key is configured;
	// everything else still needs the TMDB backbone
	if !s.TMDB.Configured() && (kind != "shows" || !s.tvdbEnabled()) {
		writeErr(w, http.StatusPreconditionFailed, errors.New("no TMDB API key configured — add one in Settings"))
		return nil, false
	}
	return &req, true
}

func (s *Server) handleAddMovie(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decodeAdd(w, r, "movies")
	if !ok {
		return
	}
	detail, err := s.TMDB.Movie(r.Context(), req.TmdbID)
	if err != nil {
		tmdbErr(w, err)
		return
	}
	id, err := s.storeMovie(detail, req.LibraryID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	m, err := s.Catalog.GetMovie(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.entitle(r, "movie", req.GroupIDs, req.TmdbID, 0, m.Title)
	writeJSON(w, http.StatusCreated, map[string]any{"movie": m, "imageBase": metadata.ImageBase})
}

// entitle records who an added title is for.
//
// Nothing named means the adder and every household they are in — the
// same rule a request follows, because an owner is a person too and
// adding a film while in a household usually means the household should
// get it. Naming a set instead takes it literally, including a set that
// leaves the adder out: adding a title for whoever asked you for it is
// the case the picker exists for.
//
// A failure here is logged rather than returned. The title is added
// either way, and refusing the add because the sharing bookkeeping
// stumbled would be the wrong half to fail.
func (s *Server) entitle(r *http.Request, kind string, groupIDs []int64, tmdbID, tvdbID int, title string) {
	a := s.access(r)
	if a.user == nil {
		return // no accounts on this install; nothing to share between
	}
	if len(groupIDs) == 0 {
		mine, err := s.Catalog.RequestTargets(a.user.ID, nil)
		if err != nil {
			log.Printf("reely: sharing — groups for %s: %v", a.user.Username, err)
			return
		}
		groupIDs = mine
	}
	for _, gid := range groupIDs {
		if err := s.Catalog.Grant(gid, kind, tmdbID, tvdbID, title, a.user.ID); err != nil {
			log.Printf("reely: sharing — grant %s to group %d: %v", title, gid, err)
			return
		}
	}
	s.reconcileSoon()
}

// storeMovie files a fetched movie into a library and starts looking for
// it. Split out so approving a request takes the same path a direct add
// does — an approved request must not be a second, subtly different way
// of adding something.
func (s *Server) storeMovie(detail *metadata.MovieDetail, libraryID int64) (int64, error) {
	id, err := s.Catalog.UpsertMovie(detail, libraryID)
	if err != nil {
		return 0, err
	}
	// another library already holding this movie fills it instantly via
	// hardlink — no download, no search
	linked := s.Grab.LinkMovieFromSiblings(id)
	m, err := s.Catalog.GetMovie(id)
	if err != nil {
		return 0, err
	}
	// the add IS the ask — go looking right away (unless it's already here)
	if !linked && m.FilePath == "" {
		s.Grab.EnqueueMovie(id)
	}
	return id, nil
}

// tvdbEnabled reports whether shows route through TheTVDB — a key in
// Settings is the whole switch.
func (s *Server) tvdbEnabled() bool { return s.TVDB != nil && s.TVDB.Configured() }

// fetchShowToAdd resolves an add request to a full show record from the
// right source. With TVDB enabled, even a TMDB-id add (Explore, lists,
// preview) is steered onto the TVDB series it maps to, so a split entry
// lands on the one continuing series instead of duplicating it.
func (s *Server) fetchShowToAdd(ctx context.Context, req *addRequest) (*metadata.ShowDetail, string, error) {
	tvdbID := req.TvdbID
	if s.tvdbEnabled() && tvdbID == 0 && req.TmdbID > 0 {
		if d, err := s.TMDB.Show(ctx, req.TmdbID); err == nil && d.TvdbID > 0 {
			tvdbID = d.TvdbID
		}
	}
	if s.tvdbEnabled() && tvdbID > 0 {
		d, err := s.TVDB.Show(ctx, tvdbID)
		if err != nil {
			return nil, "", err
		}
		metadata.EnrichShowFromTMDB(ctx, s.TMDB, d)
		return d, "tvdb", nil
	}
	if req.TmdbID <= 0 {
		return nil, "", errors.New("this show needs a TVDB API key in Settings — it has no TMDB mapping")
	}
	d, err := s.TMDB.Show(ctx, req.TmdbID)
	return d, "tmdb", err
}

func (s *Server) handleAddShow(w http.ResponseWriter, r *http.Request) {
	req, ok := s.decodeAdd(w, r, "shows")
	if !ok {
		return
	}
	detail, source, err := s.fetchShowToAdd(r.Context(), req)
	if err != nil {
		tmdbErr(w, err)
		return
	}
	id, err := s.storeShow(r.Context(), detail, source, req.LibraryID, req.MonitoredSeasons)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	sh, err := s.Catalog.GetShow(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.entitle(r, "show", req.GroupIDs, sh.TmdbID, sh.TvdbID, sh.Title)
	writeJSON(w, http.StatusCreated, map[string]any{"show": sh, "imageBase": metadata.ImageBase})
}

// storeShow files a fetched show into a library, applies the season
// selection, and starts looking. Split out for the same reason as
// storeMovie: approving a request must go through this and not a second
// implementation of it. seasons nil means every season stays monitored.
func (s *Server) storeShow(ctx context.Context, detail *metadata.ShowDetail, source string, libraryID int64, seasons []int) (int64, error) {
	var id int64
	var err error
	if source == "tvdb" {
		id, err = s.Catalog.UpsertShowTVDB(detail, libraryID)
	} else {
		id, err = s.Catalog.UpsertShow(detail, libraryID)
	}
	if err != nil {
		return 0, err
	}
	// scene numbering before anything is searched for: on a show TheXEM
	// maps, the first search for it must already speak the numbering its
	// releases carry
	if source == "tvdb" {
		(&refresh.Refresher{Catalog: s.Catalog, TMDB: s.TMDB, TVDB: s.TVDB, XEM: s.XEM, Verbose: true}).
			SyncScene(ctx, id, "tvdb", detail.TvdbID)
	}
	// season selection from the preview page applies before anything gets
	// enqueued, so an unpicked season never triggers a search
	if seasons != nil {
		want := map[int]bool{}
		for _, n := range seasons {
			want[n] = true
		}
		for _, se := range detail.Seasons {
			if err := s.Catalog.SetSeasonMonitored(id, se.Number, want[se.Number]); err != nil {
				log.Printf("reely: season %d monitor on add: %v", se.Number, err)
			}
		}
	}
	// sibling libraries' episode files hardlink in first, then the search
	// queue covers only what's still missing (EnqueueShow skips on-disk)
	s.Grab.LinkShowFromSiblings(id)
	s.Grab.EnqueueShow(id, 0)
	return id, nil
}

// handleRematchShow changes which series a row is, and rebuilds its
// episode tree to match.
//
// A show costs more than a film: the seasons and episodes underneath it
// describe the OLD series, so re-identifying without rebuilding them
// would leave a row whose name says one thing and whose episodes say
// another. RefreshShow already does that rebuild — this is the same
// operation pointed at a series the owner chose rather than the one on
// record.
func (s *Server) handleRematchShow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		TmdbID int    `json:"tmdbId"`
		TvdbID int    `json:"tvdbId"`
		Source string `json:"source"`
		Rename bool   `json:"rename"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if body.TmdbID <= 0 && body.TvdbID <= 0 {
		writeErr(w, http.StatusBadRequest, errors.New("pick a series to match to"))
		return
	}
	sh, err := s.Catalog.GetShow(id)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	if !s.access(r).mayLibrary(sh.LibraryID) {
		writeErr(w, http.StatusForbidden, errors.New("no access to this library"))
		return
	}
	// two rows in one library must not claim the same series
	if body.TvdbID > 0 {
		if other, err := s.Catalog.ShowIDByTvdb(sh.LibraryID, body.TvdbID); err == nil &&
			other != 0 && other != id {
			writeErr(w, http.StatusConflict, catalog.ErrTitleTaken)
			return
		}
	}
	if body.TmdbID > 0 {
		if other, err := s.Catalog.ShowIDByTmdb(sh.LibraryID, body.TmdbID); err == nil &&
			other != 0 && other != id {
			writeErr(w, http.StatusConflict, catalog.ErrTitleTaken)
			return
		}
	}

	detail, source, err := s.fetchShowToAdd(r.Context(), &addRequest{
		TmdbID: body.TmdbID, TvdbID: body.TvdbID, LibraryID: sh.LibraryID,
	})
	if err != nil {
		tmdbErr(w, err)
		return
	}
	if _, err := s.Catalog.RefreshShow(id, detail); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// RefreshShow carries the TVDB id; the source and the TMDB id are the
	// other half of the identity and move with it
	if err := s.Catalog.MarkShowSource(id, source, detail.TmdbID); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.moveEntitlements("show", sh.TmdbID, sh.TvdbID, detail.TmdbID, detail.TvdbID)
	renamed := 0
	if body.Rename {
		renamed = s.renameAfterRematch("episode", id)
	}
	s.reconcileSoon()
	out, err := s.Catalog.GetShow(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"show": out, "imageBase": metadata.ImageBase, "renamed": renamed,
	})
}

// handleRematchMovie changes which film a row is.
//
// The file on disk was always this film; reely was calling it another
// one. So the identity and the metadata that follows from it are
// replaced, and everything that is genuinely yours — the file, the
// library, the profile, the monitored flag, the history — stays. A
// delete and re-add would lose all of that in service of a mistake that
// was only ever about a number.
func (s *Server) handleRematchMovie(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var body struct {
		TmdbID int  `json:"tmdbId"`
		Rename bool `json:"rename"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if body.TmdbID <= 0 {
		writeErr(w, http.StatusBadRequest, errors.New("pick a title to match to"))
		return
	}
	m, err := s.Catalog.GetMovie(id)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	if !s.access(r).mayLibrary(m.LibraryID) {
		writeErr(w, http.StatusForbidden, errors.New("no access to this library"))
		return
	}
	// the same fetch a fresh add does, so a re-matched film is
	// indistinguishable from one added correctly in the first place
	detail, err := s.TMDB.Movie(r.Context(), body.TmdbID)
	if err != nil {
		tmdbErr(w, err)
		return
	}
	if err := s.Catalog.RematchMovie(id, detail); err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, catalog.ErrTitleTaken) {
			status = http.StatusConflict
		}
		writeErr(w, status, err)
		return
	}
	// The grants naming this title are keyed on the id that just moved.
	// Without following it, everybody entitled to the film quietly loses
	// it: nothing is entitled to what it now is.
	s.moveEntitlements("movie", m.TmdbID, 0, detail.TmdbID, 0)
	renamed := 0
	if body.Rename {
		renamed = s.renameAfterRematch("movie", id)
	}
	// the labels in Plex are keyed on the id that just changed
	s.reconcileSoon()
	out, err := s.Catalog.GetMovie(id)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"movie": out, "imageBase": metadata.ImageBase, "renamed": renamed,
	})
}

// renameOutlook is what "also rename the files" would act on, so the
// re-match dialog can offer the choice with the files named rather than
// asking a person to agree to something they cannot see.
type renameOutlook struct {
	Files int `json:"files"`
	// Placed is true when every file already sits where the templates
	// say it belongs — the mark of a library reely lays out, and the
	// only thing it decides now is whether the box starts ticked.
	Placed bool   `json:"placed"`
	Path   string `json:"path"` // one current path, to show what moves
}

// handleRematchFiles describes a title's files ahead of a re-match.
//
// reely used to infer whether it was allowed to rename: a file where the
// templates would have put it was one reely filed, anything else was
// somebody's own layout. That inference is wrong for the ordinary case —
// a library imported at its release names has never been organised, and
// is nobody's deliberate arrangement either. reely cannot tell the two
// apart, so it stops guessing and asks.
func (s *Server) handleRematchFiles(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		if !s.mayRematch(w, r, kind, id) {
			return
		}
		writeJSON(w, http.StatusOK, s.outlookFor(kind, id))
	}
}

// mayRematch answers the same library question the re-match handlers ask,
// and writes the refusal itself so a caller can simply stop.
func (s *Server) mayRematch(w http.ResponseWriter, r *http.Request, kind string, id int64) bool {
	var libraryID int64
	if kind == "movie" {
		m, err := s.Catalog.GetMovie(id)
		if err != nil {
			notFoundOr500(w, err)
			return false
		}
		libraryID = m.LibraryID
	} else {
		sh, err := s.Catalog.GetShow(id)
		if err != nil {
			notFoundOr500(w, err)
			return false
		}
		libraryID = sh.LibraryID
	}
	if !s.access(r).mayLibrary(libraryID) {
		writeErr(w, http.StatusForbidden, errors.New("no access to this library"))
		return false
	}
	return true
}

func (s *Server) outlookFor(kind string, id int64) renameOutlook {
	files, err := s.Catalog.OrganizeFilesFor(kind, id)
	if err != nil || len(files) == 0 {
		return renameOutlook{}
	}
	org := s.organizer()
	out := renameOutlook{Files: len(files), Placed: true, Path: files[0].Path}
	for _, f := range files {
		if !org.Placed(f) {
			out.Placed = false
			break
		}
	}
	return out
}

// moveEntitlements carries a title's grants across a change of identity.
//
// Logged rather than returned: the re-match itself succeeded, and
// failing it because the sharing bookkeeping stumbled would be the wrong
// half to fail. The next pass reports the title as waiting, which is the
// visible version of the same problem.
func (s *Server) moveEntitlements(kind string, oldTmdb, oldTvdb, newTmdb, newTvdb int) {
	n, err := s.Catalog.MoveEntitlements(kind, oldTmdb, oldTvdb, newTmdb, newTvdb)
	if err != nil {
		log.Printf("reely: sharing — move grants for %s: %v", kind, err)
		return
	}
	if n > 0 {
		log.Printf("reely: sharing — moved %d grant(s) to the re-matched %s", n, kind)
	}
}

// renameAfterRematch moves a re-matched title's files to where its NEW
// identity belongs, and reports how many moved.
//
// Only ever because the person re-matching asked for it. Correcting one
// entry should be able to rename that entry — walking the whole library
// to fix one film is how a small correction stops being worth making —
// but which files move is their call, not an inference reely makes.
func (s *Server) renameAfterRematch(kind string, id int64) int {
	org := s.organizer()
	plan, err := org.PlanTitle(kind, id)
	if err != nil {
		log.Printf("reely: rematch rename: %v", err)
		return 0
	}
	if len(plan.Moves) == 0 {
		return 0
	}
	res := org.Apply(plan)
	for _, e := range res.Errors {
		log.Printf("reely: rematch rename: %s", e)
	}
	return res.Moved
}
