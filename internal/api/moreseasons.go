package api

import (
	"errors"
	"log"
	"net/http"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/metadata"
)

// askForMoreSeasons is a request for a show this library already holds:
// somebody asked for season 1, it arrived, and now they want season 2.
//
// It is approved on the spot for the same reason any request for a held
// title is — the show is here, and what an approval would do is search
// for the seasons that are missing, which is what it does. The open
// request that brought the first seasons is widened to cover the new
// ones rather than a second opened beside it: one title, one library,
// one open request, as the unique index has it.
//
// Seasons already asked for are not asked for again: a request that adds
// nothing is "already requested", as it would be for a film.
func (s *Server) askForMoreSeasons(w http.ResponseWriter, r *http.Request, userID, libraryID, showID int64, req catalog.Request) {
	asked, err := s.Catalog.SeasonsAsked(showID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if req.Seasons != nil && len(catalog.SeasonsNotIn(req.Seasons, asked)) == 0 {
		writeErr(w, http.StatusConflict, catalog.ErrAlreadyRequested)
		return
	}

	req.Status = "approved"
	id, err := s.Catalog.CreateRequest(req)
	if errors.Is(err, catalog.ErrAlreadyRequested) {
		open, ferr := s.Catalog.OpenRequestFor(libraryID, req.Kind, req.TmdbID)
		switch {
		case ferr != nil:
			writeErr(w, http.StatusInternalServerError, ferr)
			return
		case open == nil || open.Status != "approved":
			// waiting on the owner already: this ask is that one
			writeErr(w, http.StatusConflict, catalog.ErrAlreadyRequested)
			return
		}
		if err := s.Catalog.SetRequestSeasons(open.ID, catalog.UnionSeasons(open.Seasons, req.Seasons)); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		id, err = open.ID, nil
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// fulfilled with what this ask added: the seasons already asked for
	// stay as they are, in seasonsToMonitor
	req.ID = id
	if err := s.fulfil(r.Context(), req); err != nil {
		log.Printf("reely: more of %s for user %d (request %d): %v", req.Title, userID, id, err)
	}
	writeJSON(w, http.StatusCreated, map[string]any{"id": id, "status": "approved"})
}

// seasonsToMonitor is which seasons a fulfilled show request leaves
// monitored. For a show new to the library, what was asked for, as it
// always was. For one already here, what was asked for before as well:
// asking for season 2 must not switch season 1 off. A whole-show ask of
// a show already here is every season, where nil alone would leave the
// old choice standing and add nothing.
func (s *Server) seasonsToMonitor(req catalog.Request, detail *metadata.ShowDetail) ([]int, error) {
	showID, err := s.Catalog.HeldShowID(req.LibraryID, req.TmdbID, req.TvdbID)
	if err != nil || showID == 0 {
		return req.Seasons, err
	}
	if req.Seasons == nil {
		all := make([]int, 0, len(detail.Seasons))
		for _, se := range detail.Seasons {
			all = append(all, se.Number)
		}
		return all, nil
	}
	asked, err := s.Catalog.SeasonsAsked(showID)
	if err != nil {
		return nil, err
	}
	return catalog.UnionSeasons(asked, req.Seasons), nil
}
