package prowlarr

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Plenty of torrent indexers publish no .torrent file to fetch and hand
// over a magnet instead. Prowlarr reports the two in separate fields, so
// such a release arrived with an empty DownloadURL — and the Grab button
// is drawn only where there is a link, so torrent rows quietly lost it.
func TestAMagnetStandsInForAMissingDownloadURL(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`[
			{"title":"Magnet Only 2024 1080p","protocol":"torrent",
			 "downloadUrl":"","magnetUrl":"magnet:?xt=urn:btih:ABCDEF0123456789"},
			{"title":"Torrent File 2024 1080p","protocol":"torrent",
			 "downloadUrl":"https://idx/t/2.torrent","magnetUrl":"magnet:?xt=urn:btih:9999"},
			{"title":"Usenet 2024 1080p","protocol":"usenet",
			 "downloadUrl":"https://idx/nzb/3"}
		]`))
	}))
	defer srv.Close()

	c := New(func() string { return srv.URL }, func() string { return "k" })
	got, err := c.Search(context.Background(), "q", MovieCats)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"Magnet Only 2024 1080p":  "magnet:?xt=urn:btih:ABCDEF0123456789",
		"Torrent File 2024 1080p": "https://idx/t/2.torrent", // a real file still wins
		"Usenet 2024 1080p":       "https://idx/nzb/3",
	}
	if len(got) != len(want) {
		t.Fatalf("got %d releases, want %d", len(got), len(want))
	}
	for _, r := range got {
		if r.DownloadURL != want[r.Title] {
			t.Errorf("%s: downloadUrl = %q, want %q", r.Title, r.DownloadURL, want[r.Title])
		}
	}
}
