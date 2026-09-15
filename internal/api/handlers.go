package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/naming"
	"github.com/getreely/reely/internal/settings"
)

// ---- libraries ----

func (s *Server) handleLibraries(w http.ResponseWriter, r *http.Request) {
	libs, err := s.Catalog.ListLibraries()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// plain users see only their assigned libraries
	if a := s.access(r); !a.admin() && a.user != nil {
		scoped := libs[:0]
		for _, l := range libs {
			if a.user.MayAccess(l.ID) {
				scoped = append(scoped, l)
			}
		}
		libs = scoped
	}
	writeJSON(w, http.StatusOK, map[string]any{"libraries": libs})
}

func (s *Server) handleCreateLibrary(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var req struct {
		Name string `json:"name"`
		Path string `json:"path"`
		Kind string `json:"kind"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// library roots live on the media mount — that is what makes imports
	// hardlinks, and it is the same fence the path autocomplete draws
	path, err := s.fencedPath(req.Path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	lib, err := s.Catalog.CreateLibrary(req.Name, path, req.Kind)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// a fresh library scans itself right away when matching is possible —
	// creating one IS asking for its contents
	if s.TMDB.Configured() {
		go func() {
			if _, err := s.Importer.ScanLibrary(context.Background(), lib); err != nil {
				log.Printf("reely: initial scan %s failed: %v", lib.Name, err)
			}
		}()
	}
	writeJSON(w, http.StatusCreated, lib)
}

func (s *Server) handleDeleteLibrary(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("bad library id"))
		return
	}
	if err := s.Catalog.RemoveLibrary(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// ---- scan + library contents ----

func (s *Server) handleScanLibrary(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("bad library id"))
		return
	}
	lib, err := s.Catalog.GetLibrary(id)
	if err != nil {
		writeErr(w, http.StatusNotFound, errors.New("no such library"))
		return
	}
	// Scanning walks the disk and hits TMDB per new title — it can take a
	// while, so run it in the background and return immediately. Progress
	// shows up as the grid fills on the next poll.
	go func() {
		if _, err := s.Importer.ScanLibrary(context.Background(), lib); err != nil {
			log.Printf("reely: scan %s failed: %v", lib.Name, err)
		}
	}()
	writeJSON(w, http.StatusAccepted, map[string]string{"status": "scanning", "library": lib.Name})
}

func (s *Server) handleMovies(w http.ResponseWriter, r *http.Request) {
	libraryID := queryInt(r, "libraryId")
	if libraryID > 0 && !s.access(r).mayLibrary(libraryID) {
		writeErr(w, http.StatusForbidden, errors.New("no access to this library"))
		return
	}
	movies, err := s.Catalog.ListMovies(libraryID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	movies = filterByAccess(movies, s.access(r), func(m catalog.Movie) int64 { return m.LibraryID })
	movies, err = s.scopeMovies(r, movies)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"movies": movies, "imageBase": metadata.ImageBase})
}

func (s *Server) handleShows(w http.ResponseWriter, r *http.Request) {
	libraryID := queryInt(r, "libraryId")
	if libraryID > 0 && !s.access(r).mayLibrary(libraryID) {
		writeErr(w, http.StatusForbidden, errors.New("no access to this library"))
		return
	}
	shows, err := s.Catalog.ListShows(libraryID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	shows = filterByAccess(shows, s.access(r), func(sh catalog.Show) int64 { return sh.LibraryID })
	shows, err = s.scopeShows(r, shows)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shows": shows, "imageBase": metadata.ImageBase})
}

// filterByAccess drops rows in libraries a scoped user can't reach. Admins
// and the pre-auth wizard keep everything.
func filterByAccess[T any](rows []T, a access, libOf func(T) int64) []T {
	if a.admin() {
		return rows
	}
	out := rows[:0]
	for _, row := range rows {
		if a.mayLibrary(libOf(row)) {
			out = append(out, row)
		}
	}
	return out
}

func queryInt(r *http.Request, key string) int64 {
	n, _ := strconv.ParseInt(r.URL.Query().Get(key), 10, 64)
	return n
}

// ---- search (TMDB proxy) ----

func (s *Server) handleSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	if q == "" {
		writeErr(w, http.StatusBadRequest, errors.New("missing q"))
		return
	}
	if !s.TMDB.Configured() {
		writeErr(w, http.StatusPreconditionFailed, errors.New("no TMDB API key configured — add one in Settings"))
		return
	}
	kind := r.URL.Query().Get("kind") // movie | show | "" (both)
	var out []metadata.SearchResult
	if kind == "" || kind == "movie" {
		movies, err := s.TMDB.SearchMovies(r.Context(), q, 0)
		if err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		out = append(out, movies...)
	}
	if kind == "" || kind == "show" {
		// with a TVDB key configured, shows come from TheTVDB — the source
		// whose numbering releases follow, and where a revival is one
		// continuing series instead of TMDB's split entries
		var shows []metadata.SearchResult
		var err error
		if s.tvdbEnabled() {
			shows, err = s.TVDB.SearchShows(r.Context(), q)
		} else {
			shows, err = s.TMDB.SearchShows(r.Context(), q, 0)
		}
		if err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		out = append(out, shows...)
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": out, "imageBase": metadata.ImageBase})
}

// ---- settings ----

func (s *Server) handleGetSetting(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	key := r.PathValue("key")
	value := s.Settings.Get(key)
	// secrets never leave the server — the UI only learns whether one is set
	if settings.IsSecret(key) {
		writeJSON(w, http.StatusOK, map[string]any{"key": key, "set": value != ""})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"key": key, "value": value})
}

func (s *Server) handlePutSetting(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var req struct {
		Value string `json:"value"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	key := r.PathValue("key")
	// a naming template becomes a path under a library root, and the
	// organize pass moves real files to match it — so it is checked here
	// rather than discovered later by a library renamed around a typo
	if kind, ok := namingKind(key); ok {
		if err := naming.Validate(kind, req.Value); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		req.Value = strings.TrimSpace(req.Value)
	}
	// writing an empty value to a secret clears it; a non-empty one replaces
	if err := s.Settings.Set(key, req.Value); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// namingKind maps a settings key to the template kind it holds.
func namingKind(key string) (string, bool) {
	switch key {
	case "naming_movie":
		return "movie", true
	case "naming_show":
		return "show", true
	}
	return "", false
}

// handleNamingPreview renders both templates against a fixed example so
// Settings can show what a file would actually be called before the
// template is saved. Validation and preview run the same code the
// importer does, so what you see is what you get.
func (s *Server) handleNamingPreview(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var req struct {
		Movie string `json:"movie"`
		Show  string `json:"show"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"movie":  previewOf("movie", req.Movie),
		"show":   previewOf("show", req.Show),
		"tokens": map[string][]string{"movie": naming.MovieTokens, "show": naming.ShowTokens},
		"defaults": map[string]string{
			"movie": settings.Default("naming_movie"),
			"show":  settings.Default("naming_show"),
		},
	})
}

// previewOf is one field's answer: the path it would produce, or why it
// can't be used. Both are reported per field so a bad movie template
// doesn't hide a good show one.
func previewOf(kind, template string) map[string]string {
	if err := naming.Validate(kind, template); err != nil {
		return map[string]string{"path": "", "error": err.Error()}
	}
	return map[string]string{"path": naming.Sample(kind, template), "error": ""}
}

// handleQualityRepairStatus reports how many attached files are missing
// a recorded quality or source — the ones the loop can't fully reason
// about.
func (s *Server) handleQualityRepairStatus(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	files, err := s.Catalog.FilesNeedingRepair(0)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	quality, source := 0, 0
	for _, f := range files {
		if f.NeedQuality {
			quality++
		}
		if f.NeedSource {
			source++
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"pending": len(files), "pendingQuality": quality, "pendingSource": source,
	})
}

// handleQualityRepair runs the probe over those files now, rather than
// waiting for the next restart.
func (s *Server) handleQualityRepair(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	writeJSON(w, http.StatusOK, s.Importer.RepairUnknownQuality(r.Context()))
}
