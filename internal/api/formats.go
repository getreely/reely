package api

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/getreely/reely/internal/catalog"
)

// Custom formats (Settings → Custom Formats, admin): install-wide scoring
// rules applied by every profile's ranking, importable from the TRaSH
// Guides. All endpoints are admin-only, like the rest of profile tuning.

func (s *Server) handleListFormats(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	formats, err := s.Catalog.ListFormats()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if formats == nil {
		formats = []catalog.StoredFormat{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"formats": formats})
}

// handleImportFormats upserts a batch — the import dialog sends the picked
// formats in one call; same-named formats update in place.
func (s *Server) handleImportFormats(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var req struct {
		Formats []catalog.StoredFormat `json:"formats"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || len(req.Formats) == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("send formats to import"))
		return
	}
	for i := range req.Formats {
		if err := s.Catalog.UpsertFormat(&req.Formats[i]); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": len(req.Formats)})
}

func (s *Server) handleSetFormatScore(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Score int `json:"score"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Catalog.SetFormatScore(id, req.Score); err != nil {
		notFoundOr500(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDeleteFormat(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if err := s.Catalog.DeleteFormat(id); err != nil {
		notFoundOr500(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleTrashFormats lists the guides' importable formats for one kind
// ("movies" or "shows"), fetched and converted server-side.
func (s *Server) handleTrashFormats(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if s.Trash == nil {
		writeErr(w, http.StatusServiceUnavailable, errors.New("guides import is not available"))
		return
	}
	formats, err := s.Trash.Formats(r.Context(), r.URL.Query().Get("kind"))
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"formats": formats})
}
