package api

import (
	"database/sql"
	"errors"
	"net/http"

	"github.com/getreely/reely/internal/quality"
)

// Editing quality profiles is install-wide configuration and admin-only,
// same posture as settings. Reading the list is open to any signed-in user —
// the per-title picker on detail pages needs it.

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	profiles, err := s.Catalog.ListProfiles()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"profiles": profiles})
}

func (s *Server) handleCreateProfile(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var p quality.Profile
	if err := decodeJSON(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	created, err := s.Catalog.CreateProfile(&p)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusCreated, created)
}

func (s *Server) handleUpdateProfile(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var p quality.Profile
	if err := decodeJSON(r, &p); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	p.ID = id
	updated, err := s.Catalog.UpdateProfile(&p)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeErr(w, http.StatusNotFound, errors.New("not found"))
			return
		}
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Catalog.DeleteProfile(id); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSetLibraryProfile(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	profileID, ok := decodeProfileID(w, r)
	if !ok {
		return
	}
	if err := s.Catalog.SetLibraryProfile(id, profileID); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func decodeProfileID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	var req struct {
		QualityProfileID int64 `json:"qualityProfileId"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return 0, false
	}
	return req.QualityProfileID, true
}

// Per-title overrides follow the monitor toggles' access rule: anyone with
// the library may retune a title in it; outside the scope the id answers 404.

func (s *Server) handleSetMovieProfile(w http.ResponseWriter, r *http.Request) {
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
	profileID, ok := decodeProfileID(w, r)
	if !ok {
		return
	}
	if err := s.Catalog.SetMovieProfile(id, profileID); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// handleSetMovieAvailability stores the automatic paths' availability
// gate for one movie: 'released' (default, blocks pre-release fakes) or
// 'announced' (grab the moment anything plausible appears).
func (s *Server) handleSetMovieAvailability(w http.ResponseWriter, r *http.Request) {
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
	var req struct {
		MinAvailability string `json:"minAvailability"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Catalog.SetMovieMinAvailability(id, req.MinAvailability); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSetShowProfile(w http.ResponseWriter, r *http.Request) {
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
	profileID, ok := decodeProfileID(w, r)
	if !ok {
		return
	}
	if err := s.Catalog.SetShowProfile(id, profileID); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
