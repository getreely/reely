package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/plex"
)

// The track list a detail page draws comes from Plex, which means three
// things have to line up: reely's title, the plex_items row that says
// what Plex calls it, and a server willing to answer. These check what
// happens when they do — and, more importantly, when they don't, since
// this is extra detail about a title and must never be what stops the
// page from loading.

func streamsOf(t *testing.T, h http.Handler, path string) []plex.Stream {
	t.Helper()
	w := as(t, h, nil, "GET", path, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET %s = %d: %s", path, w.Code, w.Body)
	}
	var out struct {
		Streams []plex.Stream `json:"streams"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Streams == nil {
		t.Fatalf("GET %s answered with no streams key: %s", path, w.Body)
	}
	return out.Streams
}

func TestStreamsComeFromPlexForAMovieAndAnEpisode(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	fake := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/xml")
		switch r.URL.Path {
		case "/library/metadata/4001":
			fmt.Fprint(w, `<MediaContainer><Video ratingKey="4001"><Media><Part>
			<Stream id="1" streamType="1" codec="hevc" height="2160"></Stream>
			<Stream id="2" streamType="2" codec="truehd" channels="8"></Stream>
			</Part></Media></Video></MediaContainer>`)
		case "/library/metadata/4002/allLeaves":
			fmt.Fprint(w, `<MediaContainer>
			<Video ratingKey="5001" parentIndex="1" index="1"><Media><Part>
			<Stream id="3" streamType="2" codec="ac3" channels="6"></Stream>
			</Part></Media></Video>
			<Video ratingKey="5002" parentIndex="1" index="2"><Media><Part>
			<Stream id="4" streamType="2" codec="opus" channels="2"></Stream>
			</Part></Media></Video></MediaContainer>`)
		default:
			t.Errorf("unexpected call: %s", r.URL.Path)
			http.Error(w, "no", http.StatusNotFound)
		}
	}))
	defer fake.Close()
	for _, kv := range [][2]string{
		{"plex_client_id", "reely-test-install"},
		{"plex_owner_token", "owner-tok"},
		{"plex_server_url", fake.URL},
	} {
		if err := srv.Settings.Set(kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}

	films, err := srv.Catalog.CreateLibrary("Films", "/data/films", "movies")
	if err != nil {
		t.Fatal(err)
	}
	series, err := srv.Catalog.CreateLibrary("Series", "/data/series", "shows")
	if err != nil {
		t.Fatal(err)
	}
	movieID := seedMovie(t, srv, films.ID)
	showID := seedShow(t, srv, series.ID)
	if err := srv.Catalog.CachePlexItems(9, []catalog.PlexItem{
		{Kind: "movie", RatingKey: 4001, SectionID: 9, TmdbID: 603, Title: "The Matrix"},
		{Kind: "show", RatingKey: 4002, SectionID: 9, TmdbID: 1396, Title: "Breaking Bad"},
	}); err != nil {
		t.Fatal(err)
	}

	got := streamsOf(t, h, fmt.Sprintf("/api/v1/movies/%d/streams", movieID))
	if len(got) != 2 || got[0].Codec != "hevc" || got[1].Codec != "truehd" {
		t.Fatalf("movie streams = %+v", got)
	}

	// the second episode of the show, so the answer proves the season and
	// number were carried through rather than the first leaf taken
	epID, err := srv.Catalog.EpisodeID(showID, 1, 2)
	if err != nil {
		t.Fatal(err)
	}
	got = streamsOf(t, h, fmt.Sprintf("/api/v1/episodes/%d/streams", epID))
	if len(got) != 1 || got[0].Codec != "opus" {
		t.Fatalf("episode streams = %+v", got)
	}
}

// An install with no Plex still draws its detail pages. So does one
// whose Plex has never heard of the title, and one whose server is
// refusing to answer — all three answer an empty list, not an error.
func TestStreamsAreEmptyRatherThanAFailure(t *testing.T) {
	srv := testServer(t)
	h := srv.Handler()
	films, err := srv.Catalog.CreateLibrary("Films", "/data/films", "movies")
	if err != nil {
		t.Fatal(err)
	}
	movieID := seedMovie(t, srv, films.ID)
	path := fmt.Sprintf("/api/v1/movies/%d/streams", movieID)

	if got := streamsOf(t, h, path); len(got) != 0 {
		t.Fatalf("no plex configured: %+v", got)
	}

	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "no", http.StatusInternalServerError)
	}))
	defer dead.Close()
	for _, kv := range [][2]string{
		{"plex_client_id", "reely-test-install"},
		{"plex_owner_token", "owner-tok"},
		{"plex_server_url", dead.URL},
	} {
		if err := srv.Settings.Set(kv[0], kv[1]); err != nil {
			t.Fatal(err)
		}
	}
	// still nothing cached, so this one never reaches the server at all
	if got := streamsOf(t, h, path); len(got) != 0 {
		t.Fatalf("title not in plex: %+v", got)
	}
	if err := srv.Catalog.CachePlexItems(9, []catalog.PlexItem{
		{Kind: "movie", RatingKey: 4001, SectionID: 9, TmdbID: 603, Title: "The Matrix"},
	}); err != nil {
		t.Fatal(err)
	}
	if got := streamsOf(t, h, path); len(got) != 0 {
		t.Fatalf("plex refusing: %+v", got)
	}

	// a title that does not exist is still a 404, because that is about
	// the request rather than about Plex
	if w := as(t, h, nil, "GET", "/api/v1/episodes/9999/streams", nil); w.Code != http.StatusNotFound {
		t.Fatalf("unknown episode = %d, want 404", w.Code)
	}
}
