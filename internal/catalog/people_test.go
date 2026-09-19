package catalog

import (
	"path/filepath"
	"testing"

	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/metadata"
)

func peopleStore(t *testing.T) (*Store, int64) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	s := New(conn)
	lib, err := s.CreateLibrary("Shows", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	id, err := s.UpsertShow(&metadata.ShowDetail{TvdbID: 446831, Title: "MobLand", Year: 2025}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	return s, id
}

func castOf(t *testing.T, s *Store, showID int64) map[string]struct{ tmdb, tvdb int } {
	t.Helper()
	rows, err := s.db.Query(`SELECT p.name, COALESCE(p.tmdb_id,0), COALESCE(p.tvdb_id,0)
		FROM credits c JOIN people p ON p.id = c.person_id WHERE c.show_id = ?`, showID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	out := map[string]struct{ tmdb, tvdb int }{}
	for rows.Next() {
		var name string
		var tmdb, tvdb int
		if err := rows.Scan(&name, &tmdb, &tvdb); err != nil {
			t.Fatal(err)
		}
		out[name] = struct{ tmdb, tvdb int }{tmdb, tvdb}
	}
	return out
}

// Several actors TMDB has never heard of used to be unstorable: with no
// TMDB id they all landed on tmdb_id = 0 and collided with each other on
// the unique index, so a cast list could hold at most one of them.
func TestActorsWithNoTMDBIDDoNotCollide(t *testing.T) {
	s, showID := peopleStore(t)
	if err := s.saveCast([]metadata.Person{
		{TvdbID: 378153, Name: "Geoff Bell", Character: "Richie Stevenson", Order: 8},
		{TvdbID: 401111, Name: "Daniel Betts", Character: "Freddie", Order: 9},
		{TmdbID: 2524, TvdbID: 373728, Name: "Tom Hardy", Character: "Harry Da Souza", Order: 1},
	}, 0, showID); err != nil {
		t.Fatal(err)
	}

	cast := castOf(t, s, showID)
	if len(cast) != 3 {
		t.Fatalf("stored %d of 3 cast members: %v", len(cast), cast)
	}
	if got := cast["Geoff Bell"]; got.tvdb != 378153 || got.tmdb != 0 {
		t.Errorf("Geoff Bell = %+v, want the TVDB id alone", got)
	}
	// somebody TVDB and TMDB both know carries both, so the next sighting
	// from either side matches the same row
	if got := cast["Tom Hardy"]; got.tmdb != 2524 || got.tvdb != 373728 {
		t.Errorf("Tom Hardy = %+v, want both ids", got)
	}
}

// The reason the TMDB id stays the key wherever there is one: an actor
// met through TVDB and again through a movie is one person, not two.
func TestTheSameActorFromBothSourcesIsOnePerson(t *testing.T) {
	s, showID := peopleStore(t)
	// first sighting: from TVDB, carrying both ids
	if err := s.saveCast([]metadata.Person{
		{TmdbID: 2524, TvdbID: 373728, Name: "Tom Hardy", Character: "Harry Da Souza"},
	}, 0, showID); err != nil {
		t.Fatal(err)
	}
	// later, from a movie: TMDB only
	lib, err := s.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	movieID, err := s.UpsertMovie(&metadata.MovieDetail{TmdbID: 272, Title: "Batman Begins", Year: 2005}, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.saveCast([]metadata.Person{
		{TmdbID: 2524, Name: "Tom Hardy", Character: "Bane"},
	}, movieID, 0); err != nil {
		t.Fatal(err)
	}

	var people int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM people WHERE name = 'Tom Hardy'`).Scan(&people); err != nil {
		t.Fatal(err)
	}
	if people != 1 {
		t.Fatalf("Tom Hardy became %d people — his filmography is split", people)
	}
	// and the TVDB id learned from the show is not lost by a sighting
	// that never had one
	var tvdb int
	if err := s.db.QueryRow(`SELECT COALESCE(tvdb_id,0) FROM people WHERE tmdb_id = 2524`).Scan(&tvdb); err != nil {
		t.Fatal(err)
	}
	if tvdb != 373728 {
		t.Errorf("tvdb_id = %d, want it kept", tvdb)
	}
}

// Somebody with neither id cannot be identified at all, so they are
// skipped rather than stored on a shared zero.
func TestAPersonWithNoIDsIsSkipped(t *testing.T) {
	s, showID := peopleStore(t)
	if err := s.saveCast([]metadata.Person{
		{Name: "Nobody At All"},
		{TmdbID: 2524, Name: "Tom Hardy"},
	}, 0, showID); err != nil {
		t.Fatal(err)
	}
	cast := castOf(t, s, showID)
	if _, found := cast["Nobody At All"]; found {
		t.Error("a person with no id was stored anyway")
	}
	if _, found := cast["Tom Hardy"]; !found {
		t.Error("an identifiable person was dropped alongside them")
	}
}

// tmdb_id is NULL for somebody reely met through TVDB alone. Reading a
// title's cast used to scan that column straight into an int, so one
// such actor would have failed the whole detail page rather than simply
// having no filmography.
func TestADetailPageLoadsWithAnActorTMDBDoesNotKnow(t *testing.T) {
	s, showID := peopleStore(t)
	if err := s.saveCast([]metadata.Person{
		{TmdbID: 2524, TvdbID: 373728, Name: "Tom Hardy", Character: "Harry Da Souza", Order: 1},
		{TvdbID: 378153, Name: "Geoff Bell", Character: "Richie Stevenson", Order: 2},
	}, 0, showID); err != nil {
		t.Fatal(err)
	}

	sh, err := s.GetShow(showID)
	if err != nil {
		t.Fatalf("the show would not load at all: %v", err)
	}
	if len(sh.Cast) != 2 {
		t.Fatalf("cast = %d, want 2: %+v", len(sh.Cast), sh.Cast)
	}
	// billing order is kept, and the unknown one reads as having no
	// filmography rather than as person zero
	if sh.Cast[1].Name != "Geoff Bell" || sh.Cast[1].TmdbID != 0 {
		t.Errorf("second billed = %+v", sh.Cast[1])
	}
	// every row is still tellable apart, which is what the id is for
	if sh.Cast[0].ID == 0 || sh.Cast[0].ID == sh.Cast[1].ID {
		t.Errorf("cast rows share an id: %d and %d", sh.Cast[0].ID, sh.Cast[1].ID)
	}
}
