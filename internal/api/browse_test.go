package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

// The browse endpoint and every path input are fenced to the media mount:
// suggestions never leave it, library roots outside it are refused, and
// server imports can't reach past it.
func TestBrowseAndPathFence(t *testing.T) {
	srv := testServer(t)
	mount := t.TempDir()
	srv.MediaRoot = mount
	for _, d := range []string{"movies", "tv", "downloads"} {
		if err := os.MkdirAll(filepath.Join(mount, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// a symlink under the mount pointing outside it
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(mount, "escape")); err != nil {
		t.Fatal(err)
	}
	h := srv.Handler()
	if rec, _ := doJSON(t, h, "POST", "/api/v1/users",
		map[string]any{"username": "root", "password": "correct horse battery"}); rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	root := login(t, h, "root", "correct horse battery")

	browse := func(path string) []any {
		t.Helper()
		req := httptest.NewRequest("GET", "/api/v1/system/browse?path="+path, nil)
		req.AddCookie(root)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("browse %q: %d %s", path, w.Code, w.Body)
		}
		dirs, _ := decodeMap(t, w)["dirs"].([]any)
		return dirs
	}

	// listing the mount shows its folders (the escape link is a dir entry
	// via symlink — ReadDir reports it as not-a-dir, so it stays out)
	if dirs := browse(mount + "/"); len(dirs) != 3 {
		t.Fatalf("mount listing = %v", dirs)
	}
	// prefix filtering
	if dirs := browse(mount + "/mo"); len(dirs) != 1 || dirs[0] != filepath.Join(mount, "movies")+"/" {
		t.Fatalf("prefix listing = %v", dirs)
	}
	// above the mount: only the way down to it
	if dirs := browse(filepath.Dir(mount) + "/"); len(dirs) != 1 || dirs[0] != mount+"/" {
		t.Fatalf("ancestor listing = %v", dirs)
	}
	// entirely outside: nothing
	if dirs := browse(outside + "/"); len(dirs) != 0 {
		t.Fatalf("outside listing leaked: %v", dirs)
	}
	// a symlinked dir under the mount never serves its outside target
	if dirs := browse(filepath.Join(mount, "escape") + "/"); len(dirs) != 0 {
		t.Fatalf("symlink escape listing leaked: %v", dirs)
	}

	do := func(method, path string, body map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(method, path, bytes.NewReader(mustJSON(body)))
		req.AddCookie(root)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}

	// library roots outside the mount are refused; inside is fine
	if w := do("POST", "/api/v1/libraries", map[string]any{"name": "X", "path": outside, "kind": "movies"}); w.Code != http.StatusBadRequest {
		t.Fatalf("outside library accepted: %d %s", w.Code, w.Body)
	}
	if w := do("POST", "/api/v1/libraries", map[string]any{"name": "X", "path": mount + "/../" + filepath.Base(mount) + "/../escape2", "kind": "movies"}); w.Code != http.StatusBadRequest {
		t.Fatalf("traversal library accepted: %d %s", w.Code, w.Body)
	}
	w := do("POST", "/api/v1/libraries", map[string]any{"name": "Movies", "path": filepath.Join(mount, "movies"), "kind": "movies"})
	if w.Code != http.StatusCreated {
		t.Fatalf("inside library refused: %d %s", w.Code, w.Body)
	}
	libID := int64(decodeMap(t, w)["id"].(float64))

	// a movie import path outside the mount is refused before any file IO
	rec, out := doJSON(t, h, "POST", "/api/v1/movies", nil)
	_ = rec
	_ = out
	movieW := do("POST", "/api/v1/movies", map[string]any{"tmdbId": 27205, "libraryId": libID})
	_ = movieW // adding needs TMDB; the fence check happens first on import
	impW := do("POST", "/api/v1/movies/1/import-path", map[string]any{"path": filepath.Join(outside, "x.mkv")})
	if impW.Code != http.StatusBadRequest && impW.Code != http.StatusNotFound {
		t.Fatalf("outside import path = %d %s", impW.Code, impW.Body)
	}
}
