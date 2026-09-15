package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/getreely/reely/internal/metadata"
)

// Deleting a title (files included) from one collection must not touch the
// hard-linked copy in another — each library holds its own directory entry;
// the bytes only go when the last link does.
func TestDeleteInOneLibraryKeepsSiblingHardlink(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()

	libA, err := srv.Catalog.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	libB, err := srv.Catalog.CreateLibrary("Guest Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	detail := &metadata.MovieDetail{TmdbID: 603, Title: "The Matrix", Year: 1999, Runtime: 136}
	idA, err := srv.Catalog.UpsertMovie(detail, libA.ID)
	if err != nil {
		t.Fatal(err)
	}
	idB, err := srv.Catalog.UpsertMovie(detail, libB.ID)
	if err != nil {
		t.Fatal(err)
	}

	fileA := filepath.Join(libA.Path, "The Matrix (1999).mkv")
	if err := os.WriteFile(fileA, make([]byte, 2048), 0o600); err != nil {
		t.Fatal(err)
	}
	fileB := filepath.Join(libB.Path, "The Matrix (1999).mkv")
	if err := os.Link(fileA, fileB); err != nil {
		t.Fatal(err)
	}
	if err := srv.Catalog.AttachMovieFile(idA, fileA, 2048, "1080p", ""); err != nil {
		t.Fatal(err)
	}
	if err := srv.Catalog.AttachMovieFile(idB, fileB, 2048, "1080p", ""); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest("DELETE", "/api/v1/movies/"+strconv.FormatInt(idA, 10)+"?deleteFiles=true", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body)
	}

	if _, err := os.Stat(fileA); !os.IsNotExist(err) {
		t.Fatalf("library A's file should be gone: %v", err)
	}
	if info, err := os.Stat(fileB); err != nil || info.Size() != 2048 {
		t.Fatalf("library B's hardlink must survive: %v", err)
	}
	// and B's row is untouched
	b, err := srv.Catalog.GetMovie(idB)
	if err != nil || b.FilePath != fileB {
		t.Fatalf("sibling row damaged: %+v (%v)", b, err)
	}
}
