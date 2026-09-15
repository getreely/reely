package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// "On disk" must mean on disk: a file deleted behind reely's back stops
// showing the moment its detail page loads, no library scan required.
func TestMovieDetailHealsStaleOnDisk(t *testing.T) {
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

	get := func() string {
		rec, _ := doJSON(t, h, "GET", "/api/v1/movies/"+strconv.FormatInt(id, 10), nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("detail: %d %s", rec.Code, rec.Body)
		}
		var out struct {
			Movie struct {
				FilePath string `json:"filePath"`
			} `json:"movie"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out.Movie.FilePath
	}

	if got := get(); got != file {
		t.Fatalf("with the file present, filePath = %q", got)
	}
	// the file vanishes outside reely
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if got := get(); got != "" {
		t.Fatalf("with the file gone, filePath = %q — still claiming on disk", got)
	}
	m, err := srv.Catalog.GetMovie(id)
	if err != nil || m.FilePath != "" {
		t.Fatalf("row not healed: %q (%v)", m.FilePath, err)
	}
}

// An unmounted share stats exactly like a deleted file. While the library
// root itself is unreachable, nothing may be cleared — or a flaky mount
// would strip the whole library and re-download it.
func TestMovieDetailKeepsClaimWhileLibraryRootUnreachable(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	root := filepath.Join(t.TempDir(), "mount", "movies") // never created
	lib, err := srv.Catalog.CreateLibrary("Movies", root, "movies")
	if err != nil {
		t.Fatal(err)
	}
	id := seedMovie(t, srv, lib.ID)
	file := filepath.Join(root, "The Matrix (1999).mkv")
	if err := srv.Catalog.AttachMovieFile(id, file, 1024, "1080p", "web"); err != nil {
		t.Fatal(err)
	}

	rec, _ := doJSON(t, h, "GET", "/api/v1/movies/"+strconv.FormatInt(id, 10), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail: %d %s", rec.Code, rec.Body)
	}
	m, err := srv.Catalog.GetMovie(id)
	if err != nil || m.FilePath != file {
		t.Fatalf("attachment cleared over an unreachable root: %q (%v)", m.FilePath, err)
	}
}

func TestShowDetailHealsStaleOnDisk(t *testing.T) {
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
	ep := sh.Seasons[0].Episodes[0]
	file := filepath.Join(lib.Path, "Breaking Bad - S01E01.mkv")
	if err := os.WriteFile(file, make([]byte, 512), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := srv.Catalog.AttachEpisodeFile(ep.ID, file, 512, "1080p", "web"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}

	rec, _ := doJSON(t, h, "GET", "/api/v1/shows/"+strconv.FormatInt(id, 10), nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("detail: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Show struct {
			OnDisk  int `json:"onDisk"`
			Seasons []struct {
				Episodes []struct {
					FilePath string `json:"filePath"`
				} `json:"episodes"`
			} `json:"seasons"`
		} `json:"show"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	// both the episode row and the derived counts must agree the file is gone
	if out.Show.OnDisk != 0 {
		t.Fatalf("onDisk = %d with the file gone", out.Show.OnDisk)
	}
	if got := out.Show.Seasons[0].Episodes[0].FilePath; got != "" {
		t.Fatalf("episode still claims %q", got)
	}
}
