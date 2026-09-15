package catalog

import (
	"path/filepath"
	"testing"

	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/metadata"
)

// Home orders its movies row by when a title became watchable, which is not
// when it was added: a movie monitored months before release should surface
// the day its file lands. ImportedAt is what carries that, and it must be
// empty rather than wrong for a movie that has no import event.
func TestMovieImportedAtTracksTheFileLanding(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	cat := New(conn)
	lib, err := cat.CreateLibrary("Films", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}

	add := func(tmdbID int, title string) int64 {
		t.Helper()
		id, err := cat.UpsertMovie(&metadata.MovieDetail{TmdbID: tmdbID, Title: title}, lib.ID)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	waited := add(1, "Waited Months")  // added long ago, file just landed
	scanned := add(2, "Came By Scan")  // has a file, no import event
	pending := add(3, "Still Missing") // monitored, nothing on disk

	if err := cat.AttachMovieFile(waited, "/films/waited.mkv", 1, "1080p", "web"); err != nil {
		t.Fatal(err)
	}
	if err := cat.AttachMovieFile(scanned, "/films/scanned.mkv", 1, "1080p", "web"); err != nil {
		t.Fatal(err)
	}
	if err := cat.AddHistory("imported", waited, 0, 0, `{"title":"Waited"}`); err != nil {
		t.Fatal(err)
	}
	// a grab is not a landing — only the import counts
	if err := cat.AddHistory("grabbed", pending, 0, 0, `{"title":"Still Missing"}`); err != nil {
		t.Fatal(err)
	}

	movies, err := cat.ListMovies(0)
	if err != nil {
		t.Fatal(err)
	}
	got := map[int64]Movie{}
	for _, m := range movies {
		got[m.ID] = m
	}
	if got[waited].ImportedAt == "" {
		t.Error("an imported movie has no ImportedAt — Home cannot order on the landing")
	}
	if got[scanned].ImportedAt != "" {
		t.Errorf("a scanned file invented an import time: %q", got[scanned].ImportedAt)
	}
	if got[pending].ImportedAt != "" {
		t.Errorf("a grabbed-but-not-imported movie looks landed: %q", got[pending].ImportedAt)
	}
	// the row shows what is watchable, so the fileless one is not a candidate
	if got[pending].FilePath != "" {
		t.Error("the pending movie should have no file")
	}
}
