package api

import (
	"errors"
	"net/http"
)

// Backups are admin territory: the database holds every user's catalog,
// settings, and account records, so only admins may copy or replace it.
// Restore is staged, never live — the handler stages the file, then asks
// the process to restart; db.Open applies the swap before anything holds
// the database open.

func (s *Server) handleBackups(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	list, err := s.Backup.List()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"backups": list})
}

func (s *Server) handleCreateBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	entry, err := s.Backup.Create(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusCreated, entry)
}

func (s *Server) handleDownloadBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	name := r.PathValue("name")
	path, err := s.Backup.Path(name)
	if err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	// Backup.Path only ever resolves names matching ^reely-\d{8}-\d{6}\.db$
	// inside the backups folder — no separators or traversal survive it
	http.ServeFile(w, r, path) //nolint:gosec // G703: path is allowlisted above
}

func (s *Server) handleDeleteBackup(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if err := s.Backup.Delete(r.PathValue("name")); err != nil {
		writeErr(w, http.StatusNotFound, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// handleRestore stages a restore — either one of the rotating backups by
// name (JSON body) or an uploaded .db file (multipart) — then restarts the
// app to apply it.
func (s *Server) handleRestore(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if ct := r.Header.Get("Content-Type"); ct != "" && len(ct) >= 19 && ct[:19] == "multipart/form-data" {
		// a database upload is bounded — even a huge library is far under this
		r.Body = http.MaxBytesReader(w, r.Body, 512<<20)
		if err := r.ParseMultipartForm(64 << 20); err != nil { //nolint:gosec // G120: body capped by MaxBytesReader above
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		file, _, err := r.FormFile("file")
		if err != nil {
			writeErr(w, http.StatusBadRequest, errors.New("attach the backup as the \"file\" field"))
			return
		}
		defer file.Close() //nolint:errcheck // read-only handle
		if err := s.Backup.StageUpload(file); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	} else {
		var req struct {
			Name string `json:"name"`
		}
		if err := decodeJSON(r, &req); err != nil || req.Name == "" {
			writeErr(w, http.StatusBadRequest, errors.New("body needs {\"name\": \"reely-....db\"} or a multipart file"))
			return
		}
		if err := s.Backup.StageExisting(req.Name); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"staged": true, "restarting": s.Restart != nil})
	if s.Restart != nil {
		s.Restart()
	}
}
