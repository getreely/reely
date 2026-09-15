package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// The third delete option: the file goes, the title stays — monitored,
// so the hunt for a replacement starts instead of the title vanishing.
func TestDeleteMovieFileKeepsTheMovie(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	lib, err := srv.Catalog.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	id := seedMovie(t, srv, lib.ID)
	file := filepath.Join(lib.Path, "The Matrix (1999).mkv")
	if err := os.WriteFile(file, make([]byte, 1024), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := srv.Catalog.AttachMovieFile(id, file, 1024, "1080p", "web"); err != nil {
		t.Fatal(err)
	}

	rec, body := doJSON(t, h, "DELETE", "/api/v1/movies/"+strconv.FormatInt(id, 10)+"/file", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete file: %d %s", rec.Code, rec.Body)
	}
	if body["filesDeleted"] != float64(1) {
		t.Fatalf("filesDeleted = %v", body["filesDeleted"])
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("file should be gone: %v", err)
	}
	m, err := srv.Catalog.GetMovie(id)
	if err != nil {
		t.Fatalf("the movie must survive its file: %v", err)
	}
	if m.FilePath != "" || m.FileSize != 0 {
		t.Fatalf("attachment not cleared: %q / %d", m.FilePath, m.FileSize)
	}
	if !m.Monitored {
		t.Fatal("monitoring must survive — the whole point is re-searching")
	}
	// a monitored title goes straight back on the search queue
	if n := srv.Grab.QueueLen(); n != 1 {
		t.Fatalf("search queue holds %d, want 1", n)
	}

	// a second call finds nothing to delete and says so, without erroring —
	// bulk selections include titles that never had a file
	rec, body = doJSON(t, h, "DELETE", "/api/v1/movies/"+strconv.FormatInt(id, 10)+"/file", nil)
	if rec.Code != http.StatusOK || body["filesDeleted"] != float64(0) {
		t.Fatalf("no-file delete: %d %v", rec.Code, body)
	}
}

func TestDeleteShowFilesKeepsTheShow(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	lib, err := srv.Catalog.CreateLibrary("Shows", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	id := seedShow(t, srv, lib.ID)
	sh, err := srv.Catalog.GetShow(id)
	if err != nil {
		t.Fatal(err)
	}
	var files []string
	for i, ep := range sh.Seasons[0].Episodes {
		file := filepath.Join(lib.Path, "Breaking Bad - S01E0"+strconv.Itoa(i+1)+".mkv")
		if err := os.WriteFile(file, make([]byte, 512), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := srv.Catalog.AttachEpisodeFile(ep.ID, file, 512, "1080p", "web"); err != nil {
			t.Fatal(err)
		}
		files = append(files, file)
	}

	rec, body := doJSON(t, h, "DELETE", "/api/v1/shows/"+strconv.FormatInt(id, 10)+"/files", nil)
	if rec.Code != http.StatusOK || body["filesDeleted"] != float64(2) {
		t.Fatalf("delete files: %d %v", rec.Code, body)
	}
	for _, f := range files {
		if _, err := os.Stat(f); !os.IsNotExist(err) {
			t.Fatalf("%s should be gone: %v", f, err)
		}
	}
	sh, err = srv.Catalog.GetShow(id)
	if err != nil {
		t.Fatalf("the show must survive its files: %v", err)
	}
	if sh.OnDisk != 0 {
		t.Fatalf("onDisk = %d after deleting every file", sh.OnDisk)
	}
	// every monitored aired episode goes back on the hunt
	if n := srv.Grab.QueueLen(); n != 3 {
		t.Fatalf("search queue holds %d, want the 3 aired episodes", n)
	}
}

// A user outside the library must not be able to delete its files.
func TestDeleteMovieFileRespectsLibraryScope(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	lib, err := srv.Catalog.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	id := seedMovie(t, srv, lib.ID)
	if rec, _ := doJSON(t, h, "POST", "/api/v1/users",
		map[string]any{"username": "root", "password": "correct horse battery"}); rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	root := login(t, h, "root", "correct horse battery")
	// a scoped user whose access covers a DIFFERENT library only
	other, err := srv.Catalog.CreateLibrary("Other", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	reqUser := httptest.NewRequest("POST", "/api/v1/users", bytes.NewReader(mustJSON(
		map[string]any{"username": "sam", "password": "another passphrase", "role": "user", "libraryIds": []any{other.ID}})))
	reqUser.AddCookie(root)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, reqUser)
	if w.Code != http.StatusCreated {
		t.Fatalf("create scoped user: %d %s", w.Code, w.Body)
	}
	sam := login(t, h, "sam", "another passphrase")

	del := httptest.NewRequest("DELETE", "/api/v1/movies/"+strconv.FormatInt(id, 10)+"/file", nil)
	del.AddCookie(sam)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, del)
	if w.Code != http.StatusNotFound {
		t.Fatalf("scoped delete-file: %d, want 404", w.Code)
	}
}
