package api

import (
	"errors"
	"net/http"

	"github.com/getreely/reely/internal/grab"
)

// Interactive release search: ask the indexers, show every answer with the
// scorer's verdict attached. Grabbing the winner comes with the download
// client piece.

func (s *Server) handleMovieReleases(w http.ResponseWriter, r *http.Request) {
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
	if !s.Grab.Indexer.Configured() {
		writeErr(w, http.StatusPreconditionFailed, errors.New("no Prowlarr configured — add its URL and API key in Settings"))
		return
	}
	views, err := s.Grab.MovieReleases(r.Context(), m)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"releases": views})
}

func (s *Server) handleShowReleases(w http.ResponseWriter, r *http.Request) {
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
	season := int(queryInt(r, "season"))
	if season <= 0 {
		writeErr(w, http.StatusBadRequest, errors.New("missing season"))
		return
	}
	if !s.Grab.Indexer.Configured() {
		writeErr(w, http.StatusPreconditionFailed, errors.New("no Prowlarr configured — add its URL and API key in Settings"))
		return
	}
	episode := int(queryInt(r, "episode"))
	var out any
	if episode > 0 {
		out, err = s.Grab.EpisodeReleases(r.Context(), sh, season, episode)
	} else {
		out, err = s.Grab.SeasonReleases(r.Context(), sh, season)
	}
	if err != nil {
		if errors.Is(err, grab.ErrUnknownTarget) {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"releases": out})
}

// ---- grabbing ----

// A grab needs somewhere to send the release. Either client will do,
// and which one depends on the release's protocol.
const noClient = "no download client configured — add SABnzbd or qBittorrent in Settings"

func (s *Server) handleGrabMovie(w http.ResponseWriter, r *http.Request) {
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
	if !s.Grab.CanDownload() {
		writeErr(w, http.StatusPreconditionFailed, errors.New(noClient))
		return
	}
	var req grab.GrabRequest
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := req.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	req.Origin = "manual"
	if err := s.Grab.GrabMovie(r.Context(), m, req); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "grabbed"})
}

func (s *Server) handleGrabShow(w http.ResponseWriter, r *http.Request) {
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
	if !s.Grab.CanDownload() {
		writeErr(w, http.StatusPreconditionFailed, errors.New(noClient))
		return
	}
	var req struct {
		grab.GrabRequest
		Season  int `json:"season"`
		Episode int `json:"episode"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Season <= 0 {
		writeErr(w, http.StatusBadRequest, errors.New("missing season"))
		return
	}
	if err := req.Validate(); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	req.Origin = "manual"
	if err := s.Grab.GrabForShow(r.Context(), sh, req.Season, req.Episode, req.GrabRequest); err != nil {
		if errors.Is(err, grab.ErrUnknownTarget) {
			writeErr(w, http.StatusNotFound, err)
			return
		}
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "grabbed"})
}
