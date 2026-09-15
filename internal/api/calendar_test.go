package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/metadata"
)

func TestCalendar(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	rec, lib := doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "TV", "path": t.TempDir(), "kind": "shows"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	tvLib := int64(lib["id"].(float64))
	rec, lib = doJSON(t, h, "POST", "/api/v1/libraries",
		map[string]any{"name": "Movies", "path": t.TempDir(), "kind": "movies"})
	if rec.Code != http.StatusCreated {
		t.Fatal(rec.Body)
	}
	movieLib := int64(lib["id"].(float64))

	day := func(offset int) string { return time.Now().AddDate(0, 0, offset).Format("2006-01-02") }

	showID, err := srv.Catalog.UpsertShow(&metadata.ShowDetail{
		TmdbID: 100, Title: "Upcoming Show", Year: 2026,
		Seasons: []metadata.SeasonDetail{{Number: 1, Name: "Season 1", Episodes: []metadata.EpisodeDetail{
			{TmdbID: 1, Season: 1, Episode: 1, Title: "Aired, missing", AirDate: day(-3)},
			{TmdbID: 2, Season: 1, Episode: 2, Title: "Aired, on disk", AirDate: day(-2)},
			{TmdbID: 3, Season: 1, Episode: 3, Title: "Airs soon", AirDate: day(2)},
			{TmdbID: 4, Season: 1, Episode: 4, Title: "Far future", AirDate: day(200)},
		}}},
	}, tvLib)
	if err != nil {
		t.Fatal(err)
	}
	epID, _ := srv.Catalog.EpisodeID(showID, 1, 2)
	if err := srv.Catalog.AttachEpisodeFile(epID, "/x/e2.mkv", 1, "1080p", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Catalog.UpsertMovie(&metadata.MovieDetail{
		TmdbID: 200, Title: "Digital Soon", Year: 2026, DigitalRelease: day(5),
	}, movieLib); err != nil {
		t.Fatal(err)
	}
	// unmonitored things never appear
	unmonID, err := srv.Catalog.UpsertMovie(&metadata.MovieDetail{
		TmdbID: 201, Title: "Unmonitored", Year: 2026, DigitalRelease: day(6),
	}, movieLib)
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Catalog.SetMovieMonitored(unmonID, false); err != nil {
		t.Fatal(err)
	}

	rec, _ = doJSON(t, h, "GET", "/api/v1/calendar", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("calendar: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Upcoming []catalog.CalendarItem `json:"upcoming"`
		Missing  []catalog.CalendarItem `json:"missing"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}

	if len(out.Upcoming) != 2 {
		t.Fatalf("upcoming = %+v", out.Upcoming)
	}
	if out.Upcoming[0].Kind != "episode" || out.Upcoming[0].EpisodeTitle != "Airs soon" {
		t.Fatalf("upcoming[0] = %+v", out.Upcoming[0])
	}
	if out.Upcoming[1].Kind != "movie" || out.Upcoming[1].Title != "Digital Soon" {
		t.Fatalf("upcoming[1] = %+v", out.Upcoming[1])
	}

	// only the aired-and-absent episode is "missing" — the on-disk one and
	// the unmonitored movie stay out
	if len(out.Missing) != 1 || out.Missing[0].EpisodeTitle != "Aired, missing" {
		t.Fatalf("missing = %+v", out.Missing)
	}
}

// A calendar row has to be recognisable and openable on its own. The
// poster is what makes it recognisable at a glance, and the external ids
// are what make it reachable from the portal, which is not served
// reely's own detail routes and can only open a title by its metadata
// id. An episode row carries its SHOW's poster and ids: there is no
// episode page out there to open.
func TestCalendarRowsCarryTheirPosterAndExternalIds(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	day := func(offset int) string { return time.Now().AddDate(0, 0, offset).Format("2006-01-02") }
	tv, err := srv.Catalog.CreateLibrary("TV", t.TempDir(), "shows")
	if err != nil {
		t.Fatal(err)
	}
	films, err := srv.Catalog.CreateLibrary("Films", t.TempDir(), "movies")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Catalog.UpsertShow(&metadata.ShowDetail{
		TmdbID: 213713, TvdbID: 389492, Title: "Monster", Year: 2022, Poster: "/monster.jpg",
		Seasons: []metadata.SeasonDetail{{Number: 1, Episodes: []metadata.EpisodeDetail{
			{TmdbID: 9, Season: 1, Episode: 1, Title: "One", AirDate: day(3)},
		}}},
	}, tv.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Catalog.UpsertMovie(&metadata.MovieDetail{
		TmdbID: 603, Title: "The Matrix", Year: 1999,
		DigitalRelease: day(4), Poster: "/matrix.jpg",
	}, films.ID); err != nil {
		t.Fatal(err)
	}

	rec, _ := doJSON(t, h, "GET", "/api/v1/calendar", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("calendar: %d %s", rec.Code, rec.Body)
	}
	var out struct {
		Upcoming  []catalog.CalendarItem `json:"upcoming"`
		ImageBase string                 `json:"imageBase"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	// a poster path is not an image without the base in front of it
	if out.ImageBase == "" {
		t.Error("no imageBase, so no row can draw its poster")
	}
	byKind := map[string]catalog.CalendarItem{}
	for _, it := range out.Upcoming {
		byKind[it.Kind] = it
	}
	ep, ok := byKind["episode"]
	if !ok {
		t.Fatalf("no episode row: %+v", out.Upcoming)
	}
	if ep.Poster != "/monster.jpg" {
		t.Errorf("episode poster = %q, want its show's", ep.Poster)
	}
	// both ids, because the portal prefers TVDB for a show and reely may
	// hold one with either
	if ep.TmdbID != 213713 || ep.TvdbID != 389492 {
		t.Errorf("episode ids = tmdb %d tvdb %d, want its show's both", ep.TmdbID, ep.TvdbID)
	}
	mv, ok := byKind["movie"]
	if !ok {
		t.Fatalf("no movie row: %+v", out.Upcoming)
	}
	if mv.Poster != "/matrix.jpg" || mv.TmdbID != 603 {
		t.Errorf("movie row = %+v, want its poster and tmdb id", mv)
	}
	if mv.TvdbID != 0 {
		t.Errorf("movie tvdb = %d, want none — a movie has one id", mv.TvdbID)
	}
}
