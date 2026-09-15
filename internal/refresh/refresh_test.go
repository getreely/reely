package refresh

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/metadata"
)

// fakeTMDB serves a movie whose digital date has since firmed up, and a
// show that has grown a second season since it was stored.
func fakeTMDB(t *testing.T, season2 *atomic.Bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/movie/27205":
			_, _ = fmt.Fprint(w, `{"title":"Inception","release_date":"2010-07-15","runtime":148,
				"credits":{"cast":[]},"genres":[],"external_ids":{},
				"release_dates":{"results":[{"iso_3166_1":"US","release_dates":[
					{"type":4,"release_date":"2010-11-30"}]}]}}`)
		case "/tv/1396":
			seasons := `[{"season_number":1}]`
			if season2.Load() {
				seasons = `[{"season_number":1},{"season_number":2}]`
			}
			_, _ = fmt.Fprintf(w, `{"name":"Breaking Bad","first_air_date":"2008-01-20","status":"Returning Series",
				"credits":{"cast":[]},"genres":[],"external_ids":{},"seasons":%s}`, seasons)
		case "/tv/1396/season/1":
			_, _ = fmt.Fprint(w, `{"name":"Season 1","episodes":[
				{"id":1,"episode_number":1,"name":"Pilot","air_date":"2008-01-20","runtime":58}]}`)
		case "/tv/1396/season/2":
			_, _ = fmt.Fprint(w, `{"name":"Season 2","episodes":[
				{"id":10,"episode_number":1,"name":"Seven Thirty-Seven","air_date":"2009-03-08","runtime":47}]}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestRefreshPassPicksUpNewEpisodesAndDates(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	cat := catalog.New(conn)
	var season2 atomic.Bool
	tmdb := metadata.NewTMDB(func() string { return "k" })
	tmdb.SetBaseURL(fakeTMDB(t, &season2).URL)
	r := &Refresher{Catalog: cat, TMDB: tmdb}

	movLib, err := cat.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	tvLib, err := cat.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	// stored before the digital date existed
	movieID, err := cat.UpsertMovie(&metadata.MovieDetail{
		TmdbID: 27205, Title: "Inception", Year: 2010, Runtime: 148,
	}, movLib.ID)
	if err != nil {
		t.Fatal(err)
	}
	// stored when only season 1 existed
	showID, err := cat.UpsertShow(&metadata.ShowDetail{
		TmdbID: 1396, Title: "Breaking Bad", Year: 2008, Status: "Returning Series",
		Seasons: []metadata.SeasonDetail{{Number: 1, Name: "Season 1", Episodes: []metadata.EpisodeDetail{
			{TmdbID: 1, Season: 1, Episode: 1, Title: "Pilot", AirDate: "2008-01-20"},
		}}},
	}, tvLib.ID)
	if err != nil {
		t.Fatal(err)
	}

	// freshly upserted rows aren't due yet
	if n := r.Pass(context.Background()); n != 0 {
		t.Fatalf("fresh rows refreshed: %d", n)
	}

	// two days later, TMDB has grown a season and a digital date
	season2.Store(true)
	backdate(t, conn, "movies", movieID)
	backdate(t, conn, "shows", showID)
	if n := r.Pass(context.Background()); n != 2 {
		t.Fatalf("refreshed = %d, want 2", n)
	}

	m, err := cat.GetMovie(movieID)
	if err != nil || m.DigitalRelease == "" {
		t.Fatalf("digital date not learned: %+v (%v)", m, err)
	}
	sh, err := cat.GetShow(showID)
	if err != nil || len(sh.Seasons) != 2 {
		t.Fatalf("new season not learned: %d seasons (%v)", len(sh.Seasons), err)
	}
	ep := sh.Seasons[1].Episodes[0]
	if !ep.Monitored || ep.Title != "Seven Thirty-Seven" {
		t.Fatalf("new episode wrong: %+v", ep)
	}

	// refreshed rows are stamped — nothing due right after
	if n := r.Pass(context.Background()); n != 0 {
		t.Fatalf("re-refreshed immediately: %d", n)
	}
}

func TestRefreshTiersEndedShowsWeekly(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	cat := catalog.New(conn)

	tvLib, err := cat.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	showID, err := cat.UpsertShow(&metadata.ShowDetail{
		TmdbID: 1396, Title: "Breaking Bad", Status: "Ended",
	}, tvLib.ID)
	if err != nil {
		t.Fatal(err)
	}

	// two days stale: an ended show is NOT due (weekly tier)
	backdate(t, conn, "shows", showID)
	due, err := cat.DueForRefresh(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(due) != 0 {
		t.Fatalf("ended show due after 2 days: %+v", due)
	}
	// eight days stale: due — this is the resurrection check
	if _, err := conn.Exec(`UPDATE shows SET refreshed_at = datetime('now', '-8 days') WHERE id = ?`, showID); err != nil {
		t.Fatal(err)
	}
	due, err = cat.DueForRefresh(10)
	if err != nil || len(due) != 1 || due[0].Kind != "show" {
		t.Fatalf("ended show not due after 8 days: %+v (%v)", due, err)
	}
}

// backdate makes a row two days stale.
func backdate(t *testing.T, conn *sql.DB, table string, id int64) {
	t.Helper()
	q := `UPDATE movies SET refreshed_at = datetime('now', '-2 days') WHERE id = ?`
	if table == "shows" {
		q = `UPDATE shows SET refreshed_at = datetime('now', '-2 days') WHERE id = ?`
	}
	if _, err := conn.Exec(q, id); err != nil {
		t.Fatal(err)
	}
}

// The resurrection bug: a pass captures its targets, then spends the
// better part of a minute fetching TMDB — and a title deleted mid-pass
// was re-INSERTED by the (tmdb, library) upsert. It came back with every
// episode monitored and no files, which the search loop reads as a whole
// show worth downloading. The refresher now updates by row id, and an
// UPDATE against a deleted id is an atomic no-op.
func TestRefreshDoesNotResurrectADeletedTitle(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	cat := catalog.New(conn)
	var season2 atomic.Bool
	tmdb := metadata.NewTMDB(func() string { return "k" })
	tmdb.SetBaseURL(fakeTMDB(t, &season2).URL)
	r := &Refresher{Catalog: cat, TMDB: tmdb}

	movLib, err := cat.CreateLibrary("Movies", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	tvLib, err := cat.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	movieID, err := cat.UpsertMovie(&metadata.MovieDetail{
		TmdbID: 27205, Title: "Inception", Year: 2010, Runtime: 148,
	}, movLib.ID)
	if err != nil {
		t.Fatal(err)
	}
	showID, err := cat.UpsertShow(&metadata.ShowDetail{
		TmdbID: 1396, Title: "Breaking Bad", Year: 2008, Status: "Returning Series",
		Seasons: []metadata.SeasonDetail{{Number: 1, Name: "Season 1", Episodes: []metadata.EpisodeDetail{
			{TmdbID: 1, Season: 1, Episode: 1, Title: "Pilot", AirDate: "2008-01-20"},
		}}},
	}, tvLib.ID)
	if err != nil {
		t.Fatal(err)
	}
	backdate(t, conn, "movies", movieID)
	backdate(t, conn, "shows", showID)

	// the mid-pass delete: targets captured while both rows existed, the
	// rows gone by the time their TMDB answers land
	targets, err := cat.DueForRefresh(10)
	if err != nil || len(targets) != 2 {
		t.Fatalf("targets = %v (%v)", targets, err)
	}
	if err := cat.RemoveMovie(movieID); err != nil {
		t.Fatal(err)
	}
	if err := cat.RemoveShow(showID); err != nil {
		t.Fatal(err)
	}
	for _, target := range targets {
		// errors are fine; resurrection is not
		_ = r.one(context.Background(), target)
	}

	var n int
	if err := conn.QueryRow(`SELECT COUNT(*) FROM shows`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("shows = %d after a mid-pass delete — the refresher resurrected it", n)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM movies`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("movies = %d after a mid-pass delete — the refresher resurrected it", n)
	}
	if err := conn.QueryRow(`SELECT COUNT(*) FROM episodes`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("episodes = %d — resurrected monitored episodes are what set off the downloads", n)
	}
}
