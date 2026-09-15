package catalog

import (
	"path/filepath"
	"testing"

	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/metadata"
)

// Quality and source describe a file. When the file goes they go with
// it: a title whose file was deleted used to keep its badge and read as
// something present, which is the opposite of what it is.

func fileStore(t *testing.T) (*Store, int64) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	s := New(conn)
	lib, err := s.CreateLibrary("Films", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	return s, lib.ID
}

func TestClearingAMovieFileClearsWhatDescribedIt(t *testing.T) {
	s, lib := fileStore(t)
	id, err := s.UpsertMovie(&metadata.MovieDetail{TmdbID: 603, Title: "The Matrix"}, lib)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AttachMovieFile(id, "/films/matrix.mkv", 1234, "720p", "webdl"); err != nil {
		t.Fatal(err)
	}
	before, err := s.GetMovie(id)
	if err != nil || before.Quality != "720p" {
		t.Fatalf("setup: %+v (%v)", before, err)
	}

	if err := s.ClearMovieFile(id); err != nil {
		t.Fatal(err)
	}
	after, err := s.GetMovie(id)
	if err != nil {
		t.Fatal(err)
	}
	if after.FilePath != "" || after.FileSize != 0 {
		t.Errorf("file not detached: %+v", after)
	}
	if after.Quality != "" || after.Source != "" {
		t.Errorf("quality/source = %q/%q, want both cleared — they describe a file that is gone",
			after.Quality, after.Source)
	}
	// the row itself stays, still monitored, so the hunt continues
	if after.Title != "The Matrix" || !after.Monitored {
		t.Errorf("the title should survive losing its file: %+v", after)
	}
}
