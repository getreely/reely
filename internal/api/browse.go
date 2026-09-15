package api

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Path inputs (library roots, server-side imports) autocomplete against
// the server's filesystem — and everything they may point at is fenced to
// the media mount (/data in the documented deployment). Listing the rest
// of the host would only advertise paths the server rejects anyway.

// underRoot reports whether path sits at or below root.
func underRoot(path, root string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// fencedPath validates a user-supplied path against the media mount,
// symlink-safe: the path (or for creations, its nearest existing ancestor)
// must resolve inside the mount. Returns the cleaned path.
func (s *Server) fencedPath(raw string) (string, error) {
	if s.MediaRoot == "" {
		return filepath.Clean(raw), nil // fence disabled (tests)
	}
	path := filepath.Clean(raw)
	if !filepath.IsAbs(path) || !underRoot(path, s.MediaRoot) {
		return "", errors.New("paths live under " + s.MediaRoot)
	}
	// resolve symlinks on the nearest existing ancestor, so a link planted
	// under the mount can't smuggle the real target outside it
	probe := path
	for {
		if real, err := filepath.EvalSymlinks(probe); err == nil {
			if !underRoot(real, s.MediaRoot) {
				return "", errors.New("paths live under " + s.MediaRoot)
			}
			break
		}
		parent := filepath.Dir(probe)
		if parent == probe {
			break
		}
		probe = parent
	}
	return path, nil
}

// handleBrowse suggests directories for path inputs: everything up to the
// last slash is the directory to list, the rest is a prefix filter.
// Directories only, capped, admin-only — the only path inputs (library
// roots, server imports) are admin territory too. Above the mount the sole
// suggestion is the way in ("/" → "/data/"); anywhere else outside it,
// nothing.
func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	root := s.MediaRoot
	if root == "" {
		root = "/data"
	}
	raw := r.URL.Query().Get("path")
	if raw == "" {
		raw = root + "/"
	}
	dir, partial := raw, ""
	if !strings.HasSuffix(raw, "/") {
		dir, partial = filepath.Dir(raw), strings.ToLower(filepath.Base(raw))
	}
	dir = filepath.Clean(dir)
	if !underRoot(dir, root) {
		dirs := []string{}
		if underRoot(root, dir) {
			// an ancestor of the mount: suggest the next component on the
			// way down, subject to the same prefix filter as a real listing
			rel, err := filepath.Rel(dir, root)
			if err == nil {
				next := strings.SplitN(rel, string(filepath.Separator), 2)[0]
				if partial == "" || strings.HasPrefix(strings.ToLower(next), partial) {
					dirs = append(dirs, filepath.Join(dir, next)+"/")
				}
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"dirs": dirs})
		return
	}
	// resolve symlinks before reading, so a link under the mount can't
	// serve listings of directories outside it
	real, err := filepath.EvalSymlinks(dir)
	if err != nil || !underRoot(real, root) {
		writeJSON(w, http.StatusOK, map[string]any{"dirs": []string{}})
		return
	}
	entries, err := os.ReadDir(real)
	if err != nil {
		writeJSON(w, http.StatusOK, map[string]any{"dirs": []string{}})
		return
	}
	dirs := []string{}
	for _, e := range entries {
		name := e.Name()
		if !e.IsDir() || strings.HasPrefix(name, ".") {
			continue
		}
		if partial != "" && !strings.HasPrefix(strings.ToLower(name), partial) {
			continue
		}
		dirs = append(dirs, filepath.Join(dir, name)+"/")
		if len(dirs) >= 25 {
			break
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"dirs": dirs})
}

// Server-side imports: point reely at a file or folder already on the
// media mount and it hard-links into the title's library — the source
// stays put. Admin-only, same as the browse that feeds the path input.

func (s *Server) handleImportMoviePath(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := s.Catalog.GetMovie(id); err != nil {
		notFoundOr500(w, err)
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Path == "" {
		writeErr(w, http.StatusBadRequest, errors.New("body needs {\"path\": \"/data/...\"}"))
		return
	}
	path, err := s.fencedPath(req.Path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Grab.ImportMovieFromPath(id, path); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "imported"})
}

func (s *Server) handleImportShowPath(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Path string `json:"path"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Path == "" {
		writeErr(w, http.StatusBadRequest, errors.New("body needs {\"path\": \"/data/...\"}"))
		return
	}
	path, err := s.fencedPath(req.Path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	results, err := s.Grab.ImportShowFromPath(id, path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
