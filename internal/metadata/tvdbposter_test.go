package metadata

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// TVDB's data.image is whatever its contributors ranked first, in any
// language — which is how an English-language show wore a Spanish cover
// while the overview beside it was chosen as English on purpose.
func TestEnglishPosterIsPreferred(t *testing.T) {
	for _, tc := range []struct {
		name     string
		artworks []tvdbArtwork
		want     string
	}{
		{name: "nothing to choose from"},
		{
			name: "the best-scoring English poster wins",
			artworks: []tvdbArtwork{
				{Image: "low.jpg", Language: "eng", Type: seriesPosterArtwork, Score: 10},
				{Image: "high.jpg", Language: "eng", Type: seriesPosterArtwork, Score: 90},
				{Image: "mid.jpg", Language: "eng", Type: seriesPosterArtwork, Score: 50},
			},
			want: "high.jpg",
		},
		{
			name: "a better-scoring Spanish poster does not win",
			artworks: []tvdbArtwork{
				{Image: "spa.jpg", Language: "spa", Type: seriesPosterArtwork, Score: 99},
				{Image: "eng.jpg", Language: "eng", Type: seriesPosterArtwork, Score: 1},
			},
			want: "eng.jpg",
		},
		{
			name: "other artwork kinds are not posters",
			artworks: []tvdbArtwork{
				{Image: "background.jpg", Language: "eng", Type: 3, Score: 99},
				{Image: "banner.jpg", Language: "eng", Type: 1, Score: 99},
			},
		},
		{
			// the guard that keeps a wrong type id from putting a wide
			// image in a poster's slot
			name: "a landscape shape is refused however it is labelled",
			artworks: []tvdbArtwork{
				{Image: "wide.jpg", Language: "eng", Type: seriesPosterArtwork,
					Score: 99, Width: 1920, Height: 1080},
			},
		},
		{
			name: "portrait passes, and art with no dimensions is taken at its word",
			artworks: []tvdbArtwork{
				{Image: "tall.jpg", Language: "eng", Type: seriesPosterArtwork,
					Score: 10, Width: 680, Height: 1000},
				{Image: "unsized.jpg", Language: "eng", Type: seriesPosterArtwork, Score: 5},
			},
			want: "tall.jpg",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := englishPoster(tc.artworks); got != tc.want {
				t.Errorf("englishPoster = %q, want %q", got, tc.want)
			}
		})
	}
}

// posterTVDB serves one series whose ranked art is Spanish, with the
// English poster available in the artwork list — MobLand's shape.
func posterTVDB(t *testing.T, artworks string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/login":
			fmt.Fprint(w, `{"status":"success","data":{"token":"tok"}}`)
		case "/series/999/extended":
			_, _ = fmt.Fprintf(w, `{"data":{
				"name":"MobLand","image":"https://art.tvdb/spanish-poster.jpg",
				"year":"2025","status":{"name":"Continuing"},
				"remoteIds":[{"id":"12345","sourceName":"TheMovieDB.com"}],
				"artworks":[%s]}}`, artworks)
		case "/series/999/episodes/default":
			fmt.Fprint(w, `{"data":{"episodes":[]},"links":{"next":""}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestShowTakesTheEnglishPosterOverTheRankedOne(t *testing.T) {
	srv := posterTVDB(t, `{"image":"https://art.tvdb/english-poster.jpg","language":"eng","type":2,"score":10},
		{"image":"https://art.tvdb/spanish-poster.jpg","language":"spa","type":2,"score":99}`)
	tv := NewTVDB(func() string { return "good-key" })
	tv.SetBaseURL(srv.URL)

	d, err := tv.Show(context.Background(), 999)
	if err != nil {
		t.Fatal(err)
	}
	if d.Poster != "https://art.tvdb/english-poster.jpg" {
		t.Errorf("poster = %q, want the English one", d.Poster)
	}
	if d.PosterLocalised {
		t.Error("a poster chosen for its language is not a stand-in and must not be replaced")
	}
}

// With no English artwork the ranked one is kept — something beats
// nothing — but marked, so the TMDB pass may replace it.
func TestShowMarksARankedPosterAsAStandIn(t *testing.T) {
	srv := posterTVDB(t, `{"image":"https://art.tvdb/spanish-poster.jpg","language":"spa","type":2,"score":99}`)
	tv := NewTVDB(func() string { return "good-key" })
	tv.SetBaseURL(srv.URL)

	d, err := tv.Show(context.Background(), 999)
	if err != nil {
		t.Fatal(err)
	}
	if d.Poster != "https://art.tvdb/spanish-poster.jpg" {
		t.Errorf("poster = %q, want the ranked one kept", d.Poster)
	}
	if !d.PosterLocalised {
		t.Error("the ranked poster was not marked, so nothing would ever replace it")
	}
}

// The fallback the stand-in exists for: TMDB's poster is the show's
// primary art in English, so it replaces a stand-in but never a poster
// that was chosen.
func TestTMDBReplacesOnlyAStandInPoster(t *testing.T) {
	tmdbSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"name":"MobLand","poster_path":"/tmdb-poster.jpg",
			"backdrop_path":"/tmdb-backdrop.jpg","credits":{"cast":[]},"seasons":[]}`)
	}))
	defer tmdbSrv.Close()
	tmdb := NewTMDB(func() string { return "k" })
	tmdb.SetBaseURL(tmdbSrv.URL)

	standIn := &ShowDetail{TmdbID: 12345, Poster: "spanish.jpg", PosterLocalised: true}
	EnrichShowFromTMDB(context.Background(), tmdb, standIn)
	if standIn.Poster != "/tmdb-poster.jpg" || standIn.PosterLocalised {
		t.Errorf("a stand-in was not replaced: %q localised=%v", standIn.Poster, standIn.PosterLocalised)
	}

	chosen := &ShowDetail{TmdbID: 12345, Poster: "english.jpg"}
	EnrichShowFromTMDB(context.Background(), tmdb, chosen)
	if chosen.Poster != "english.jpg" {
		t.Errorf("a chosen English poster was overwritten with %q", chosen.Poster)
	}
}
