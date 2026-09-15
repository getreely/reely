package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/getreely/reely/internal/refresh"
)

// Title actions: kick off an automatic search, or remove a title (with or
// without its files). Searches are queued and processed by the watcher —
// grabs surface in Activity.

func (s *Server) handleSearchMovie(w http.ResponseWriter, r *http.Request) {
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
	if !s.Grab.Indexer.Configured() || !s.Grab.CanDownload() {
		writeErr(w, http.StatusPreconditionFailed,
			errors.New("automatic search needs Prowlarr and a download client configured in Settings"))
		return
	}
	// one title, someone watching: search now and answer with what it did
	out := s.Grab.SearchNow(r.Context(), id, 0, 0, 0)
	writeJSON(w, http.StatusOK, map[string]any{
		"queued": 1, "grabbed": out.Grabbed, "reason": out.Reason,
	})
}

func (s *Server) handleSearchShow(w http.ResponseWriter, r *http.Request) {
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
	if !s.Grab.Indexer.Configured() || !s.Grab.CanDownload() {
		writeErr(w, http.StatusPreconditionFailed,
			errors.New("automatic search needs Prowlarr and a download client configured in Settings"))
		return
	}
	var req struct {
		Season  int `json:"season"`
		Episode int `json:"episode"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	// one episode is a search someone is waiting on — run it now and say
	// what happened. A whole show or season can be hundreds of searches, so
	// those still go through the paced queue.
	if req.Episode > 0 {
		out := s.Grab.SearchNow(r.Context(), 0, id, req.Season, req.Episode)
		writeJSON(w, http.StatusOK, map[string]any{
			"queued": 1, "grabbed": out.Grabbed, "reason": out.Reason,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"queued": s.Grab.EnqueueShow(id, req.Season)})
}

func deleteFilesWanted(r *http.Request) bool {
	return r.URL.Query().Get("deleteFiles") == "true"
}

func removalDetail(title string, files []string) string {
	detail, _ := json.Marshal(map[string]any{"title": title, "filesDeleted": len(files)})
	return string(detail)
}

// removeFiles deletes a title's files and then the folders the deletion
// left with nothing in them.
//
// Removing the file and leaving "Some Film (1999) - {tmdb-1234}/" behind
// makes a library that fills with empty folders — and one of them will
// later be read by a scanner as a title with no file. Only genuinely
// empty directories go, walking up from each file, and never the
// library's own root however empty it gets.
//
// One rule covers the three cases: a film's folder empties and goes; a
// whole show empties its seasons and then itself; deleting one episode
// out of a season removes nothing, because the season still has the
// others in it.
func (s *Server) removeFiles(libraryID int64, paths []string) {
	dirs := map[string]bool{}
	for _, p := range paths {
		if p == "" {
			continue
		}
		// the paths are reely's own catalog rows (written by the importer),
		// never request input — gosec's taint analysis can't see that
		err := os.Remove(p) //nolint:gosec // G703: catalog-owned path, see above
		if err != nil && !os.IsNotExist(err) {
			// the PathError already names the file; logging err alone keeps
			// the raw path out of the format args
			log.Printf("reely: delete library file: %v", err)
			continue
		}
		dirs[filepath.Dir(p)] = true
	}
	if len(dirs) == 0 {
		return
	}
	lib, err := s.Catalog.GetLibrary(libraryID)
	if err != nil {
		return // no root to bound the walk with, so nothing is pruned
	}
	root := filepath.Clean(lib.Path)
	for dir := range dirs {
		pruneEmpty(dir, root)
	}
}

// pruneEmpty removes dir and each empty parent, stopping at root.
//
// os.Remove refuses a directory that is not empty, which is the whole
// safety property: anything still holding a file — subtitles, artwork,
// another episode — simply stays.
func pruneEmpty(dir, root string) {
	for dir = filepath.Clean(dir); strings.HasPrefix(dir, root+string(filepath.Separator)); {
		if err := os.Remove(dir); err != nil { //nolint:gosec // G703: catalog-owned path
			return // not empty, or not ours to remove
		}
		dir = filepath.Dir(dir)
	}
}

func (s *Server) handleDeleteMovie(w http.ResponseWriter, r *http.Request) {
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
	var files []string
	if deleteFilesWanted(r) && m.FilePath != "" {
		files = append(files, m.FilePath)
	}
	if err := s.Catalog.RemoveMovie(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.removeFiles(m.LibraryID, files)
	if err := s.Catalog.AddHistory("removed", 0, 0, 0, removalDetail(m.Title, files)); err != nil {
		log.Printf("reely: record removal: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

func (s *Server) handleDeleteShow(w http.ResponseWriter, r *http.Request) {
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
	var files []string
	if deleteFilesWanted(r) {
		for _, se := range sh.Seasons {
			for _, ep := range se.Episodes {
				if ep.FilePath != "" {
					files = append(files, ep.FilePath)
				}
			}
		}
	}
	if err := s.Catalog.RemoveShow(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.removeFiles(sh.LibraryID, files)
	if err := s.Catalog.AddHistory("removed", 0, 0, 0, removalDetail(sh.Title, files)); err != nil {
		log.Printf("reely: record removal: %v", err)
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// The third delete option: throw the file away but keep the title in the
// library, monitored as it was — for when the file that landed is simply
// the wrong one. A monitored title goes straight back on the search queue
// so the replacement hunt starts now rather than at the next sweep.

func keptFileRemovalDetail(title string, files []string) string {
	detail, _ := json.Marshal(map[string]any{
		"title": title, "filesDeleted": len(files), "keptInLibrary": true,
	})
	return string(detail)
}

func (s *Server) handleDeleteMovieFile(w http.ResponseWriter, r *http.Request) {
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
	// no file is not an error: a bulk "delete files" over a mixed selection
	// shouldn't trip over the titles that never had one
	var files []string
	if m.FilePath != "" {
		files = append(files, m.FilePath)
	}
	if err := s.Catalog.ClearMovieFile(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.removeFiles(m.LibraryID, files)
	if len(files) > 0 {
		if err := s.Catalog.AddHistory("removed", id, 0, 0, keptFileRemovalDetail(m.Title, files)); err != nil {
			log.Printf("reely: record file removal: %v", err)
		}
	}
	queued := 0
	if m.Monitored {
		queued = s.Grab.EnqueueMovie(id)
	}
	writeJSON(w, http.StatusOK, map[string]any{"filesDeleted": len(files), "queued": queued})
}

func (s *Server) handleDeleteShowFiles(w http.ResponseWriter, r *http.Request) {
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
	var files []string
	for _, se := range sh.Seasons {
		for _, ep := range se.Episodes {
			if ep.FilePath != "" {
				files = append(files, ep.FilePath)
			}
		}
	}
	if _, err := s.Catalog.ClearShowFiles(id); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.removeFiles(sh.LibraryID, files)
	if len(files) > 0 {
		if err := s.Catalog.AddHistory("removed", 0, id, 0, keptFileRemovalDetail(sh.Title, files)); err != nil {
			log.Printf("reely: record file removal: %v", err)
		}
	}
	// the enqueue re-reads the show, which now has no files, so every
	// monitored aired episode goes back on the hunt
	queued := 0
	if sh.Monitored {
		queued = s.Grab.EnqueueShow(id, 0)
	}
	writeJSON(w, http.StatusOK, map[string]any{"filesDeleted": len(files), "queued": queued})
}

// handleSearchMissing is the "search missing" button on a library page: one
// click instead of select-all-then-search. It covers exactly what the page
// shows — one kind, optionally one library — and only what the viewer is
// allowed to see, so a plain user can't sweep a library they can't open.
func (s *Server) handleSearchMissing(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Kind      string `json:"kind"`      // movies | shows
		LibraryID int64  `json:"libraryId"` // 0 = every library of that kind
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Kind != "movies" && req.Kind != "shows" {
		writeErr(w, http.StatusBadRequest, errors.New("kind must be movies or shows"))
		return
	}
	if !s.Grab.Indexer.Configured() || !s.Grab.CanDownload() {
		writeErr(w, http.StatusPreconditionFailed,
			errors.New("automatic search needs Prowlarr and a download client configured in Settings"))
		return
	}

	libs, err := s.Catalog.ListLibraries()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	a := s.access(r)
	// the scope is always an explicit id list, never "everything": that way
	// a user with no libraries sweeps nothing instead of the whole install
	ids := []int64{}
	for _, l := range libs {
		if l.Kind != req.Kind || !a.mayLibrary(l.ID) {
			continue
		}
		if req.LibraryID == 0 || l.ID == req.LibraryID {
			ids = append(ids, l.ID)
		}
	}
	if req.LibraryID != 0 && len(ids) == 0 {
		writeErr(w, http.StatusNotFound, errors.New("not found"))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"queued": s.Grab.SearchMissing(req.Kind, ids)})
}

// handleRefreshTitle re-fetches one title's metadata from TMDB on demand:
// new episodes, slipped dates, the TVDB id for id-keyed searches. kind is
// "movie" or "show", fixed by the route.
func (s *Server) handleRefreshTitle(kind string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id, ok := pathID(w, r)
		if !ok {
			return
		}
		var libID int64
		var err error
		if kind == "movie" {
			m, gerr := s.Catalog.GetMovie(id)
			if gerr != nil {
				notFoundOr500(w, gerr)
				return
			}
			libID = m.LibraryID
		} else {
			sh, gerr := s.Catalog.GetShow(id)
			if gerr != nil {
				notFoundOr500(w, gerr)
				return
			}
			libID = sh.LibraryID
		}
		if !s.access(r).mayLibrary(libID) {
			writeErr(w, http.StatusNotFound, errors.New("not found"))
			return
		}
		if !s.TMDB.Configured() {
			writeErr(w, http.StatusPreconditionFailed, errors.New("no TMDB API key configured — add one in Settings"))
			return
		}
		// a refresh somebody clicked is one title: let it say what it did
		ref := &refresh.Refresher{Catalog: s.Catalog, TMDB: s.TMDB, TVDB: s.TVDB, XEM: s.XEM, Verbose: true}
		if err = ref.One(r.Context(), kind, id); err != nil {
			writeErr(w, http.StatusBadGateway, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	}
}
