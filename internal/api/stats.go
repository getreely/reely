package api

import "net/http"

// The stats dashboard is install-wide information — per-library disk
// usage, other people's grab activity — so it is admin territory, same as
// Health.
func (s *Server) handleStats(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	stats, err := s.Catalog.GetStats()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, stats)
}
