package api

import (
	"database/sql"
	"errors"
	"log"
	"net/http"
	"os"
	"strconv"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/metadata"
)

// Detail pages: one movie or show with cast (and, for shows, the full
// season/episode tree), plus the monitor toggles that hang off them.
//
// Scoping note: a title outside a plain user's libraries answers 404, not
// 403 — an id they can't reach shouldn't confirm what it points at.

func pathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, http.StatusBadRequest, errors.New("bad id"))
		return 0, false
	}
	return id, true
}

func notFoundOr500(w http.ResponseWriter, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	writeErr(w, http.StatusInternalServerError, err)
}

// The detail pages verify "on disk" against the disk itself: a file
// deleted behind reely's back stops showing as present the moment its
// page loads, without waiting for a library scan.
//
// A missing file only counts as gone while its library root is reachable
// — an unmounted share stats exactly like a deleted file, and clearing
// attachments over a flaky mount would re-download the whole library.

// libraryRootReachable reports whether a library's folder answers a stat.
func (s *Server) libraryRootReachable(libraryID int64) bool {
	lib, err := s.Catalog.GetLibrary(libraryID)
	if err != nil {
		return false
	}
	// the path is the admin-configured library root, never request input —
	// gosec's taint analysis can't see that
	_, err = os.Stat(lib.Path) //nolint:gosec // G703: catalog-owned path, see above
	return err == nil
}

// verifyMovieOnDisk clears a movie's file attachment when the file is
// provably gone, healing both the row and the response in hand.
func (s *Server) verifyMovieOnDisk(m *catalog.MovieDetails) {
	if m.FilePath == "" || !s.libraryRootReachable(m.LibraryID) {
		return
	}
	// the path is reely's own catalog row (written by the importer), never
	// request input — gosec's taint analysis can't see that
	if _, err := os.Stat(m.FilePath); !os.IsNotExist(err) { //nolint:gosec // G703: catalog-owned path, see above
		return
	}
	if err := s.Catalog.ClearMovieFile(m.ID); err != nil {
		// the id is numeric and the error is sqlite's own text
		log.Printf("reely: clearing missing file for movie %d: %v", m.ID, err) //nolint:gosec // G706: see above
		return
	}
	m.FilePath, m.FileSize = "", 0
}

// verifyShowOnDisk clears episode attachments whose files are provably
// gone, reporting whether anything changed (the caller re-reads the show
// so its on-disk counts stay honest).
func (s *Server) verifyShowOnDisk(sh *catalog.ShowDetails) bool {
	rootChecked, rootOK := false, false
	cleared := false
	for si := range sh.Seasons {
		for ei := range sh.Seasons[si].Episodes {
			ep := &sh.Seasons[si].Episodes[ei]
			if ep.FilePath == "" {
				continue
			}
			if !rootChecked {
				rootChecked, rootOK = true, s.libraryRootReachable(sh.LibraryID)
			}
			if !rootOK {
				return false
			}
			// catalog-owned path, never request input — same as the movie case
			if _, err := os.Stat(ep.FilePath); !os.IsNotExist(err) { //nolint:gosec // G703: catalog-owned path, see above
				continue
			}
			if err := s.Catalog.ClearEpisodeFile(ep.ID); err != nil {
				// numeric id, sqlite's own error text
				log.Printf("reely: clearing missing file for episode %d: %v", ep.ID, err) //nolint:gosec // G706: see above
				continue
			}
			cleared = true
		}
	}
	return cleared
}

func (s *Server) handleMovie(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	m, err := s.Catalog.GetMovie(id)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	if !s.access(r).mayLibrary(m.LibraryID) {
		writeErr(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	s.verifyMovieOnDisk(m)
	movies, _, _ := s.Grab.Downloading(r.Context())
	m.Downloading = movies[m.ID]
	writeJSON(w, http.StatusOK, map[string]any{"movie": m, "imageBase": metadata.ImageBase})
}

func (s *Server) handleShow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	sh, err := s.Catalog.GetShow(id)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	if !s.access(r).mayLibrary(sh.LibraryID) {
		writeErr(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	// clearing a stale attachment changes the show's on-disk counts, which
	// are computed in SQL — re-read so the page and the numbers agree
	if s.verifyShowOnDisk(sh) {
		if sh, err = s.Catalog.GetShow(id); err != nil {
			notFoundOr500(w, err)
			return
		}
	}
	// a season pack is grabbed against the show, with no one episode to
	// point at, so the show's own flag covers that case
	_, episodes, shows := s.Grab.Downloading(r.Context())
	sh.Downloading = shows[sh.ID]
	for si := range sh.Seasons {
		for ei := range sh.Seasons[si].Episodes {
			ep := &sh.Seasons[si].Episodes[ei]
			ep.Downloading = episodes[ep.ID]
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"show": sh, "imageBase": metadata.ImageBase})
}

// handleShowNumbering stores a show's release-numbering mapping: how far
// its releases' season numbers sit from its own (a TMDB-split revival's S1
// releasing as S8 is offset 7), plus an optional TVDB id for id-keyed
// searches when TMDB records none. 0/0 restores the defaults.
func (s *Server) handleShowNumbering(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	sh, err := s.Catalog.GetShow(id)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	if !s.access(r).mayLibrary(sh.LibraryID) {
		writeErr(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	var req struct {
		SeasonOffset int `json:"seasonOffset"`
		TvdbID       int `json:"tvdbId"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.SeasonOffset < -100 || req.SeasonOffset > 100 || req.TvdbID < 0 {
		writeErr(w, http.StatusBadRequest, errors.New("season offset must be between -100 and 100, tvdb id non-negative"))
		return
	}
	if err := s.Catalog.SetShowNumbering(id, req.SeasonOffset, req.TvdbID); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"seasonOffset": req.SeasonOffset, "tvdbId": req.TvdbID})
}

// ---- monitor toggles ----

func decodeMonitor(w http.ResponseWriter, r *http.Request) (bool, bool) {
	var req struct {
		Monitored *bool `json:"monitored"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return false, false
	}
	if req.Monitored == nil {
		writeErr(w, http.StatusBadRequest, errors.New("body needs {\"monitored\": true|false}"))
		return false, false
	}
	return *req.Monitored, true
}

func (s *Server) handleMonitorMovie(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	m, err := s.Catalog.GetMovie(id)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	if !s.access(r).mayLibrary(m.LibraryID) {
		writeErr(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	monitored, ok := decodeMonitor(w, r)
	if !ok {
		return
	}
	if err := s.Catalog.SetMovieMonitored(id, monitored); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// monitoring something IS asking for it — go looking right away
	if monitored && m.FilePath == "" {
		s.Grab.EnqueueMovie(id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "monitored": monitored})
}

func (s *Server) handleMonitorShow(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	sh, err := s.Catalog.GetShow(id)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	if !s.access(r).mayLibrary(sh.LibraryID) {
		writeErr(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	monitored, ok := decodeMonitor(w, r)
	if !ok {
		return
	}
	if err := s.Catalog.SetShowMonitored(id, monitored); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if monitored {
		s.Grab.EnqueueShow(id, 0)
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "monitored": monitored})
}

// handleMonitorSeason flips a whole season's episodes at once — the middle
// ground between the show switch (everything) and per-episode rows.
func (s *Server) handleMonitorSeason(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	season, err := strconv.Atoi(r.PathValue("season"))
	if err != nil || season <= 0 {
		writeErr(w, http.StatusBadRequest, errors.New("bad season number"))
		return
	}
	sh, err := s.Catalog.GetShow(id)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	if !s.access(r).mayLibrary(sh.LibraryID) {
		writeErr(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	monitored, ok := decodeMonitor(w, r)
	if !ok {
		return
	}
	if err := s.Catalog.SetSeasonMonitored(id, season, monitored); err != nil {
		notFoundOr500(w, err)
		return
	}
	if monitored {
		s.Grab.EnqueueShow(id, season)
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "monitored": monitored})
}

func (s *Server) handleMonitorEpisode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	_, libraryID, err := s.Catalog.EpisodeLibrary(id)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	if !s.access(r).mayLibrary(libraryID) {
		writeErr(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	monitored, ok := decodeMonitor(w, r)
	if !ok {
		return
	}
	if err := s.Catalog.SetEpisodeMonitored(id, monitored); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if monitored {
		if showID, season, episode, err := s.Catalog.EpisodeAddress(id); err == nil {
			s.Grab.EnqueueEpisode(showID, season, episode)
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "monitored": monitored})
}
