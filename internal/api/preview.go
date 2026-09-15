package api

import (
	"errors"
	"net/http"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/metadata"
)

// Preview: the look-before-you-add page. Full TMDB detail for a title
// that may not be in any library yet — description, seasons, episodes —
// so adding can be a decision, not a leap. Available to every signed-in
// user; which libraries already hold the title is filtered to the ones
// they may see.

type previewEpisode struct {
	Season   int    `json:"season"`
	Episode  int    `json:"episode"`
	Title    string `json:"title"`
	Overview string `json:"overview"`
	AirDate  string `json:"airDate"`
	Runtime  int    `json:"runtime"`
}

type previewSeason struct {
	Number   int              `json:"number"`
	Name     string           `json:"name"`
	Episodes []previewEpisode `json:"episodes"`
}

type preview struct {
	TmdbID         int      `json:"tmdbId"`
	TvdbID         int      `json:"tvdbId,omitempty"`
	Kind           string   `json:"kind"`
	Title          string   `json:"title"`
	Year           int      `json:"year"`
	Overview       string   `json:"overview"`
	Status         string   `json:"status,omitempty"`
	Runtime        int      `json:"runtime,omitempty"`
	Genres         []string `json:"genres"`
	ReleaseDate    string   `json:"releaseDate,omitempty"`
	DigitalRelease string   `json:"digitalRelease,omitempty"`
	Poster         string   `json:"poster"`
	Backdrop       string   `json:"backdrop"`
	ImdbID         string   `json:"imdbId,omitempty"`
	// Cast comes straight from the TMDB lookup this handler already makes —
	// a title you haven't added yet is exactly when you want to see who is
	// in it.
	Cast    []metadata.Person `json:"cast,omitempty"`
	Seasons []previewSeason   `json:"seasons,omitempty"`
}

func (s *Server) handlePreview(w http.ResponseWriter, r *http.Request) {
	if !s.TMDB.Configured() {
		writeErr(w, http.StatusPreconditionFailed, errors.New("no TMDB API key configured — add one in Settings"))
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	kind := r.PathValue("kind")
	var p preview
	switch kind {
	case "movie":
		d, err := s.TMDB.Movie(r.Context(), int(id))
		if err != nil {
			tmdbErr(w, err)
			return
		}
		p = preview{
			TmdbID: d.TmdbID, Kind: "movie", Title: d.Title, Year: d.Year, Overview: d.Overview,
			Runtime: d.Runtime, Genres: d.Genres, ReleaseDate: d.ReleaseDate,
			DigitalRelease: d.DigitalRelease, Poster: d.Poster, Backdrop: d.Backdrop, ImdbID: d.ImdbID,
			Cast: d.Cast,
		}
	case "show":
		// src=tvdb: the id is a TVDB series id — how the add-search flows
		// preview shows when a TVDB key is configured
		var d *metadata.ShowDetail
		var err error
		if r.URL.Query().Get("src") == "tvdb" && s.tvdbEnabled() {
			if d, err = s.TVDB.Show(r.Context(), int(id)); err == nil {
				metadata.EnrichShowFromTMDB(r.Context(), s.TMDB, d)
			}
		} else {
			d, err = s.TMDB.Show(r.Context(), int(id))
		}
		if err != nil {
			tmdbErr(w, err)
			return
		}
		p = preview{
			TmdbID: d.TmdbID, TvdbID: d.TvdbID, Kind: "show", Title: d.Title, Year: d.Year, Overview: d.Overview,
			Status: d.Status, Genres: d.Genres, Poster: d.Poster, Backdrop: d.Backdrop, ImdbID: d.ImdbID,
			Cast: d.Cast,
		}
		for _, se := range d.Seasons {
			ps := previewSeason{Number: se.Number, Name: se.Name}
			for _, e := range se.Episodes {
				ps.Episodes = append(ps.Episodes, previewEpisode{
					Season: e.Season, Episode: e.Episode, Title: e.Title,
					Overview: e.Overview, AirDate: e.AirDate, Runtime: e.Runtime,
				})
			}
			p.Seasons = append(p.Seasons, ps)
		}
	default:
		writeErr(w, http.StatusBadRequest, errors.New("kind must be movie or show"))
		return
	}

	table := "movies"
	if kind == "show" {
		table = "shows"
	}
	var libIDs []int64
	var err error
	if kind == "show" && r.URL.Query().Get("src") == "tvdb" {
		libIDs, err = s.Catalog.LibrariesHoldingTvdb(int(id))
	} else {
		libIDs, err = s.Catalog.LibrariesHolding(table, int(id))
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	acc := s.access(r)
	visible := []int64{}
	for _, lid := range libIDs {
		if acc.mayLibrary(lid) {
			visible = append(visible, lid)
		}
	}
	// Reaching the library is not the same as being given the title. For
	// somebody whose share reely writes, "already in" has to mean
	// "already yours" — otherwise a film they were never tagged for reads
	// as theirs, and the Request button that would have tagged them for
	// it is the thing the page takes away. In the house and out of reach,
	// with the one action that fixes it removed.
	if len(visible) > 0 {
		mine, err := s.watchable(r, kind, p.TmdbID, p.TvdbID)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		if !mine {
			visible = []int64{}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"preview": p, "imageBase": metadata.ImageBase, "inLibraries": visible,
	})
}

// handleEpisode is one episode's own page: the row plus enough of its
// show to draw a header. Scoped like every title — out of reach is 404.
func (s *Server) handleEpisode(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	ep, err := s.Catalog.GetEpisode(id)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	sh, err := s.Catalog.GetShow(ep.ShowID)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	if !s.access(r).mayLibrary(sh.LibraryID) {
		writeErr(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"episode": ep,
		"show": map[string]any{
			"id": sh.ID, "tmdbId": sh.TmdbID, "title": sh.Title, "year": sh.Year,
			"poster": sh.Poster, "backdrop": sh.Backdrop, "libraryId": sh.LibraryID,
		},
		"imageBase": metadata.ImageBase,
	})
}

// handleRecentEpisodes feeds the home view's "recently added" row for
// shows: one card per show with recent imports, scoped to libraries the
// caller may see — a season pack is one card, not thirty.
func (s *Server) handleRecentEpisodes(w http.ResponseWriter, r *http.Request) {
	rows, err := s.Catalog.RecentShowImports(60)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	acc := s.access(r)
	out := []catalog.RecentShowImport{}
	for _, row := range rows {
		if acc.mayLibrary(row.LibraryID) {
			out = append(out, row)
			if len(out) == 20 {
				break
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"shows": out, "imageBase": metadata.ImageBase})
}

// tmdbErr answers a metadata fetch that failed. A title reely refuses to
// serve reads as "not found" rather than as an upstream fault: it is not a
// TMDB outage, and reely has nothing to say about whether such a title
// exists.
func tmdbErr(w http.ResponseWriter, err error) {
	if errors.Is(err, metadata.ErrAdultContent) {
		writeErr(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	writeErr(w, http.StatusBadGateway, err)
}
