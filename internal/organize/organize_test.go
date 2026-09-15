package organize

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/metadata"
)

func testService(t *testing.T) (*Service, *catalog.Store, string) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	cat := catalog.New(conn)
	root := t.TempDir()
	if _, err := cat.CreateLibrary("TV", root, "shows"); err != nil {
		t.Fatal(err)
	}
	return &Service{Catalog: cat}, cat, root
}

func touch(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// seedEpisode stores a show with one episode already attached to a file at
// a path of the caller's choosing — the shape a library left behind by
// another tool's naming has.
func seedEpisode(t *testing.T, cat *catalog.Store, path string) int64 {
	t.Helper()
	libs, err := cat.ListLibraries()
	if err != nil || len(libs) == 0 {
		t.Fatal("no library")
	}
	showID, err := cat.UpsertShow(&metadata.ShowDetail{
		TmdbID: 1, Title: "The Proof Is Out There", Year: 2021,
		Seasons: []metadata.SeasonDetail{{Number: 6, Name: "Season 6", Episodes: []metadata.EpisodeDetail{
			{TmdbID: 11, Season: 6, Episode: 11, Title: "Sky Fire", AirDate: "2026-01-01"},
		}}},
	}, libs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	d, err := cat.GetShow(showID)
	if err != nil {
		t.Fatal(err)
	}
	epID := d.Seasons[0].Episodes[0].ID
	touch(t, path)
	if err := cat.AttachEpisodeFile(epID, path, 1, "1080p", "webdl"); err != nil {
		t.Fatal(err)
	}
	return showID
}

// seedMovie stores a film in its own movie library, attached to a file
// at a path of the caller's choosing.
func seedMovie(t *testing.T, cat *catalog.Store, path string) int64 {
	t.Helper()
	lib, err := cat.CreateLibrary("Films", filepath.Dir(filepath.Dir(path)), "movies")
	if err != nil {
		t.Fatal(err)
	}
	id, err := cat.UpsertMovie(&metadata.MovieDetail{
		TmdbID: 603, Title: "The Matrix", Year: 1999,
	}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	touch(t, path)
	if err := cat.AttachMovieFile(id, path, 1, "1080p", "webdl"); err != nil {
		t.Fatal(err)
	}
	return id
}

// A file another tool named lands where reely's template says it belongs,
// and the catalog follows it — a moved file whose row still points at the
// old path is worse than not moving it at all.
func TestOrganizeMovesAStrayFileAndRepointsTheCatalog(t *testing.T) {
	svc, cat, root := testService(t)
	stray := filepath.Join(root, "The Proof is Out There", "Season 06",
		"The.Proof.Is.Out.There.S06E11.1080p.WEB.h264-EDITH.mkv")
	seedEpisode(t, cat, stray)

	plan, err := svc.Plan(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Moves) != 1 {
		t.Fatalf("planned %d moves, want 1: %+v", len(plan.Moves), plan.Moves)
	}
	want := filepath.Join(root, "The Proof Is Out There (2021)", "Season 06",
		"The Proof Is Out There - S06E11 - Sky Fire [1080p WEB-DL].mkv")
	if plan.Moves[0].To != want {
		t.Fatalf("destination = %q, want %q", plan.Moves[0].To, want)
	}
	if len(plan.Emptied) != 1 {
		t.Fatalf("emptied = %v, want the old season folder", plan.Emptied)
	}

	res := svc.Apply(plan)
	if res.Moved != 1 || len(res.Errors) != 0 {
		t.Fatalf("apply = %+v", res)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("file is not at its destination: %v", err)
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Fatal("the old file is still there")
	}
	files, err := cat.OrganizeFiles(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 1 || files[0].Path != want {
		t.Fatalf("catalog still points at %+v", files)
	}
}

// A library already matching its template has nothing to do — planning
// must be a no-op rather than a churn of moves onto identical paths.
func TestOrganizeLeavesAnAlreadyTidyLibraryAlone(t *testing.T) {
	svc, cat, root := testService(t)
	tidy := filepath.Join(root, "The Proof Is Out There (2021)", "Season 06",
		"The Proof Is Out There - S06E11 - Sky Fire [1080p WEB-DL].mkv")
	seedEpisode(t, cat, tidy)

	plan, err := svc.Plan(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Moves) != 0 {
		t.Fatalf("planned %d moves on a tidy library: %+v", len(plan.Moves), plan.Moves)
	}
}

// Two rows wanting one destination must not silently overwrite each other:
// a collision is a question for a person, and the file that could not move
// stays exactly where it was.
func TestOrganizeRefusesToOverwriteAnExistingFile(t *testing.T) {
	svc, cat, root := testService(t)
	stray := filepath.Join(root, "The Proof is Out There", "Season 06", "s06e11.mkv")
	seedEpisode(t, cat, stray)
	dest := filepath.Join(root, "The Proof Is Out There (2021)", "Season 06",
		"The Proof Is Out There - S06E11 - Sky Fire [1080p WEB-DL].mkv")
	touch(t, dest)
	if err := os.WriteFile(dest, []byte("someone else's file"), 0o600); err != nil {
		t.Fatal(err)
	}

	plan, err := svc.Plan(0)
	if err != nil {
		t.Fatal(err)
	}
	res := svc.Apply(plan)
	if res.Moved != 0 || len(res.Errors) != 1 {
		t.Fatalf("apply = %+v, want a refusal", res)
	}
	body, err := os.ReadFile(dest)
	if err != nil || string(body) != "someone else's file" {
		t.Fatalf("the existing file was overwritten: %q, %v", body, err)
	}
	if _, err := os.Stat(stray); err != nil {
		t.Fatalf("the source was lost after a refused move: %v", err)
	}
}

// A row pointing at a path that no longer exists must not appear in the
// plan: the preview is what a person approves, and filling it with moves
// that are certain to fail makes it useless for judging what will happen.
func TestOrganizeIgnoresRowsWhoseFileIsGone(t *testing.T) {
	svc, cat, root := testService(t)
	seedEpisode(t, cat, filepath.Join(root, "The Proof is Out There", "Season 06", "s06e11.mkv"))
	// the file disappears from under reely, as a moved library would
	if err := os.RemoveAll(filepath.Join(root, "The Proof is Out There")); err != nil {
		t.Fatal(err)
	}

	plan, err := svc.Plan(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Moves) != 0 {
		t.Fatalf("planned %d moves for a file that is gone: %+v", len(plan.Moves), plan.Moves)
	}
}

// Renaming a file in place does not empty its folder, and claiming it
// would is not a harmless cosmetic slip: Apply prunes what the plan said
// would empty, and a person reading the preview is being told a folder is
// about to disappear when it is not.
func TestOrganizeDoesNotClaimAnInPlaceRenameEmptiesTheFolder(t *testing.T) {
	svc, cat, root := testService(t)
	dir := filepath.Join(root, "The Proof Is Out There (2021)", "Season 06")
	seedEpisode(t, cat, filepath.Join(dir, "wrongname.mkv"))

	plan, err := svc.Plan(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Moves) != 1 {
		t.Fatalf("planned %d moves, want 1", len(plan.Moves))
	}
	if len(plan.Emptied) != 0 {
		t.Fatalf("claims %v would empty, but the file stays in that folder", plan.Emptied)
	}
	svc.Apply(plan)
	if _, err := os.Stat(dir); err != nil {
		t.Fatalf("the folder was removed out from under its own file: %v", err)
	}
}

// A folder the plan said would empty has to actually go.
//
// countFiles counts FILES at any depth, so a folder holding nothing but
// empty subdirectories — a Subs/ or Featurettes/ another tool made, or a
// season folder whose episodes all left — counts zero and is predicted
// emptied. os.Remove refuses a directory that still has directories in
// it, and the refusal was swallowed, so the folder stayed and nothing
// said why.
func TestOrganizeRemovesAFolderLeftHoldingOnlyEmptyFolders(t *testing.T) {
	svc, cat, root := testService(t)
	old := filepath.Join(root, "The Proof is Out There", "Season 06")
	seedEpisode(t, cat, filepath.Join(old, "s06e11.mkv"))
	// the shapes another tool leaves behind: empty, and nested empty
	for _, dir := range []string{
		filepath.Join(old, "Subs"),
		filepath.Join(old, "Featurettes", "Behind the Scenes"),
	} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	plan, err := svc.Plan(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Moves) != 1 {
		t.Fatalf("moves = %d, want the one episode", len(plan.Moves))
	}
	res := svc.Apply(plan)
	if res.Moved != 1 || len(res.Errors) != 0 {
		t.Fatalf("apply = %+v", res)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Errorf("%s survived with only empty folders in it", old)
	}
	if len(res.Removed) == 0 {
		t.Error("the removal was not reported")
	}
}

// A file reely did not put there keeps its folder. Nothing here deletes
// somebody else's data to tidy up: a stray .nfo, artwork or a sample is
// a reason to leave the folder alone, not to remove it.
func TestOrganizeKeepsAFolderThatStillHoldsAFile(t *testing.T) {
	svc, cat, root := testService(t)
	old := filepath.Join(root, "The Proof is Out There", "Season 06")
	seedEpisode(t, cat, filepath.Join(old, "s06e11.mkv"))
	touch(t, filepath.Join(old, "Subs", "s06e11.en.srt"))

	plan, err := svc.Plan(0)
	if err != nil {
		t.Fatal(err)
	}
	res := svc.Apply(plan)
	if res.Moved != 1 {
		t.Fatalf("apply = %+v", res)
	}
	if _, err := os.Stat(filepath.Join(old, "Subs", "s06e11.en.srt")); err != nil {
		t.Fatalf("a file reely did not place was lost: %v", err)
	}
}

// A library root is never removed, however empty it looks.
//
// A flat library — files sitting directly in the library folder, which
// is exactly the shape somebody runs organize to fix — has every file
// leaving the root at once. The root then counts as emptied like any
// other folder, and removing it would delete the library itself:
// reely's own configured path, gone, because it tidied it too well.
func TestOrganizeNeverRemovesTheLibraryRoot(t *testing.T) {
	svc, cat, root := testService(t)
	// flat: the episode sits directly in the library folder
	seedEpisode(t, cat, filepath.Join(root, "s06e11.mkv"))

	plan, err := svc.Plan(0)
	if err != nil {
		t.Fatal(err)
	}
	if len(plan.Moves) != 1 {
		t.Fatalf("moves = %d, want the one episode", len(plan.Moves))
	}
	for _, dir := range plan.Emptied {
		if dir == root {
			t.Fatalf("the plan promised to remove the library root %s", root)
		}
	}
	res := svc.Apply(plan)
	if res.Moved != 1 {
		t.Fatalf("apply = %+v", res)
	}
	if _, err := os.Stat(root); err != nil {
		t.Fatalf("the library root was removed: %v", err)
	}
}

// A subtitle travels with the file it belongs to. Left behind it stops
// being a subtitle — Plex reads those off disk beside the video — and it
// is also the reason the old folder cannot be pruned, which is the same
// complaint from the other end.
func TestOrganizeCarriesSidecarsAndPrunesWhatIsLeft(t *testing.T) {
	svc, cat, root := testService(t)
	old := filepath.Join(root, "Proof", "S06")
	video := filepath.Join(old, "s06e11.mkv")
	seedEpisode(t, cat, video)
	touch(t, filepath.Join(old, "s06e11.en.srt"))
	touch(t, filepath.Join(old, "s06e11.en.forced.srt"))
	touch(t, filepath.Join(old, "s06e11.nfo"))
	// a neighbour's file, which must not be swept up
	touch(t, filepath.Join(old, "s06e12.en.srt"))

	plan, err := svc.Plan(0)
	if err != nil {
		t.Fatal(err)
	}
	moved := map[string]string{}
	for _, m := range plan.Moves {
		moved[filepath.Base(m.From)] = m.To
	}
	for _, want := range []string{"s06e11.en.srt", "s06e11.en.forced.srt", "s06e11.nfo"} {
		if _, ok := moved[want]; !ok {
			t.Errorf("%s was left behind", want)
		}
	}
	if _, ok := moved["s06e12.en.srt"]; ok {
		t.Error("another episode's subtitle was swept up")
	}
	// the carried name keeps everything after the stem
	if to := moved["s06e11.en.forced.srt"]; !strings.HasSuffix(to, ".en.forced.srt") {
		t.Errorf("forced subtitle landed at %s", to)
	}

	res := svc.Apply(plan)
	if len(res.Errors) != 0 {
		t.Fatalf("apply = %+v", res)
	}
	// s06e12's subtitle is still there, so the folder stays — and says so
	if _, err := os.Stat(old); err != nil {
		t.Error("a folder still holding somebody else's file was removed")
	}
	if len(res.Kept) == 0 {
		t.Error("the surviving folder was not reported")
	}
}

// Conventional artwork names no title, so it is only claimed where the
// folder can only be about one film.
func TestOrganizeTakesArtOnlyForASoleFilm(t *testing.T) {
	svc, cat, root := testService(t)
	old := filepath.Join(root, "The Matrix 1999 1080p")
	seedMovie(t, cat, filepath.Join(old, "matrix.mkv"))
	touch(t, filepath.Join(old, "poster.jpg"))
	touch(t, filepath.Join(old, "RARBG.txt"))

	plan, err := svc.Plan(0)
	if err != nil {
		t.Fatal(err)
	}
	moved := map[string]bool{}
	for _, m := range plan.Moves {
		moved[filepath.Base(m.From)] = true
	}
	if !moved["poster.jpg"] {
		t.Error("the film's poster was left behind")
	}
	if moved["RARBG.txt"] {
		t.Error("a file reely did not place was moved")
	}
}
