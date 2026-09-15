package api

import (
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// Manual upload: hand reely a file (or several) and it flows through the
// same import pipeline as a download — naming template, attach, history.
// Uploads stream part-by-part to temp files, so a 20 GB remux never sits
// in memory.

const maxUploadBytes = 64 << 30 // generous; the LAN is the real limit

var uploadExts = map[string]bool{
	".mkv": true, ".mp4": true, ".avi": true, ".m4v": true,
	".mov": true, ".wmv": true, ".mpg": true, ".mpeg": true, ".ts": true,
}

// receiveUploads streams every file part to a temp file. Callers own the
// temp files; anything left after import is swept.
func receiveUploads(r *http.Request) ([]struct{ Path, Name string }, error) {
	mr, err := r.MultipartReader()
	if err != nil {
		return nil, fmt.Errorf("expected a multipart file upload: %w", err)
	}
	var out []struct{ Path, Name string }
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out, err
		}
		name := part.FileName()
		if name == "" {
			continue // a plain form field, not a file
		}
		// canonExt comes from the allowlist's own keys, not the request —
		// the temp filename never carries caller-controlled bytes
		canonExt := ""
		for e := range uploadExts {
			if e == strings.ToLower(filepath.Ext(name)) {
				canonExt = e
			}
		}
		if canonExt == "" {
			drain(part)
			return out, fmt.Errorf("%s is not a video file", name)
		}
		tmp, err := os.CreateTemp("", "reely-upload-*"+canonExt)
		if err != nil {
			return out, err
		}
		if _, err := io.Copy(tmp, part); err != nil {
			tmp.Close()
			_ = os.Remove(tmp.Name())
			return out, err
		}
		if err := tmp.Close(); err != nil {
			_ = os.Remove(tmp.Name())
			return out, err
		}
		out = append(out, struct{ Path, Name string }{tmp.Name(), filepath.Base(name)})
	}
	if len(out) == 0 {
		return out, errors.New("no files in the upload")
	}
	return out, nil
}

func drain(p *multipart.Part) { _, _ = io.Copy(io.Discard, p) }

func sweepTemp(files []struct{ Path, Name string }) {
	for _, f := range files {
		_ = os.Remove(f.Path) // already-imported files have moved; gone is fine
	}
}

func (s *Server) handleUploadMovie(w http.ResponseWriter, r *http.Request) {
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
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	files, err := receiveUploads(r)
	if err != nil {
		sweepTemp(files)
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	defer sweepTemp(files)
	// a movie takes exactly one file — the first
	if err := s.Grab.ImportUploadedMovie(id, files[0].Path, files[0].Name); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "imported"})
}

func (s *Server) handleUploadShow(w http.ResponseWriter, r *http.Request) {
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
	r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
	files, err := receiveUploads(r)
	if err != nil {
		sweepTemp(files)
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	defer sweepTemp(files)
	results, err := s.Grab.ImportUploadedShowFiles(id, files)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}
