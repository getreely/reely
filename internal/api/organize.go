package api

import (
	"errors"
	"net/http"

	"github.com/getreely/reely/internal/organize"
)

// Organize: bring a library's files into line with its naming templates.
// Admin-only, because it moves files — and always in two steps, so the
// list of moves is something a person has read before anything happens.

func (s *Server) organizer() *organize.Service {
	return &organize.Service{Catalog: s.Catalog, Settings: s.Settings}
}

// handleOrganize plans a library's moves, and performs them only when the
// caller says so explicitly. A GET is always a preview; the POST carries
// apply:true and re-plans first, so what moves is what the plan says right
// now rather than a list that went stale in a browser tab.
func (s *Server) handleOrganize(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	lib, err := s.Catalog.GetLibrary(id)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	org := s.organizer()
	plan, err := org.Plan(lib.ID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if r.Method == http.MethodGet {
		writeJSON(w, http.StatusOK, map[string]any{
			"library": lib.Name, "moves": plan.Moves, "emptied": plan.Emptied,
			// what will still be standing afterwards, so a library with
			// hundreds of folders does not have to be walked by hand to
			// find the few that still need somebody
			"leftover": plan.Leftover,
		})
		return
	}
	var req struct {
		Apply bool `json:"apply"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !req.Apply {
		writeErr(w, http.StatusBadRequest, errors.New("nothing to do: apply was not set"))
		return
	}
	res := org.Apply(plan)
	writeJSON(w, http.StatusOK, map[string]any{
		"moved": res.Moved, "removed": res.Removed, "errors": res.Errors,
		"kept":    res.Kept,
		"planned": len(plan.Moves),
	})
}
