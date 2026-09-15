package grab

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/getreely/reely/internal/download"
	"github.com/getreely/reely/internal/prowlarr"
)

// withClients builds a service with whichever clients the test wants.
func withClients(usenet, torrent bool, set stubSettings) *Service {
	svc := &Service{Settings: set}
	if usenet {
		svc.Usenet = &stubDownloader{}
	}
	if torrent {
		svc.Torrent = &stubDownloader{}
	}
	return svc
}

// What can be grabbed follows from what is set up. A protocol with no
// client cannot be downloaded, whatever the setting says, because there
// is nothing to download it with.
func TestConfiguredClientsDecideWhatIsPossible(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		usenet, torrent         bool
		setting                 string
		wantUsenet, wantTorrent bool
		wantPrefer              string
	}{
		{name: "usenet only, nothing set", usenet: true,
			wantUsenet: true, wantTorrent: false},
		{name: "torrent only, nothing set", torrent: true,
			wantUsenet: false, wantTorrent: true},
		{name: "both, default prefers usenet", usenet: true, torrent: true,
			wantUsenet: true, wantTorrent: true, wantPrefer: download.Usenet},
		{name: "both, prefer torrent", usenet: true, torrent: true, setting: PreferTorrent,
			wantUsenet: true, wantTorrent: true, wantPrefer: download.Torrent},
		{name: "both, torrents switched off", usenet: true, torrent: true, setting: UsenetOnly,
			wantUsenet: true, wantTorrent: false},
		{name: "both, usenet switched off", usenet: true, torrent: true, setting: TorrentOnly,
			wantUsenet: false, wantTorrent: true},
		// the setting cannot conjure a client that is not there
		{name: "prefer torrent with no torrent client", usenet: true, setting: PreferTorrent,
			wantUsenet: true, wantTorrent: false},
		{name: "torrent only with no torrent client", usenet: true, setting: TorrentOnly,
			wantUsenet: false, wantTorrent: false},
		// nothing set up at all: the search stays useful and the grab
		// path is what says there is nowhere to send a release
		{name: "no clients at all",
			wantUsenet: true, wantTorrent: true, wantPrefer: download.Usenet},
	} {
		t.Run(tc.name, func(t *testing.T) {
			set := stubSettings{}
			if tc.setting != "" {
				set[DownloadProtocolKey] = tc.setting
			}
			p := withClients(tc.usenet, tc.torrent, set).protocols()
			if p.usenet != tc.wantUsenet || p.torrent != tc.wantTorrent {
				t.Errorf("usenet=%v torrent=%v, want usenet=%v torrent=%v",
					p.usenet, p.torrent, tc.wantUsenet, tc.wantTorrent)
			}
			if p.prefer != tc.wantPrefer {
				t.Errorf("prefer = %q, want %q", p.prefer, tc.wantPrefer)
			}
		})
	}
}

// A switched-off protocol shows in the results saying why, rather than
// vanishing. A row that is simply missing looks like the indexer had
// nothing, which is a different problem with a different fix.
func TestARefusedProtocolSaysSo(t *testing.T) {
	svc, idx, cat := testService(t)
	m := seedMovie(t, cat, t.TempDir())
	svc.Usenet, svc.Torrent = &stubDownloader{}, &stubDownloader{}
	svc.Settings = stubSettings{DownloadProtocolKey: UsenetOnly}

	idx.releases = []prowlarr.Release{
		{Title: "Inception.2010.1080p.BluRay.x264-GROUP", Size: gb(9),
			Protocol: download.Usenet, Indexer: "nzbs"},
		{Title: "Inception.2010.1080p.WEB-DL", Size: gb(5),
			Protocol: download.Torrent, Indexer: "rarbg"},
	}
	views, err := svc.MovieReleases(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	byProtocol := map[string]ReleaseView{}
	for _, v := range views {
		byProtocol[v.Protocol] = v
	}
	if v := byProtocol[download.Torrent]; v.Accepted || v.Reason == "" {
		t.Errorf("a torrent with torrents off should be refused, with a reason: %+v", v)
	}
	if v := byProtocol[download.Usenet]; !v.Accepted {
		t.Errorf("usenet should still be accepted: %+v", v)
	}
}

// The preference breaks a tie and nothing more. Preferring torrents
// must not mean accepting a worse release than the usenet one beside
// it — quality decides first, always.
func TestThePreferenceOnlyBreaksATie(t *testing.T) {
	svc, idx, cat := testService(t)
	m := seedMovie(t, cat, t.TempDir())
	svc.Usenet, svc.Torrent = &stubDownloader{}, &stubDownloader{}
	svc.Settings = stubSettings{DownloadProtocolKey: PreferTorrent}

	// identical releases but for the protocol: nothing to choose between
	// them on quality, so the preference decides
	idx.releases = []prowlarr.Release{
		{Title: "Inception.2010.1080p.BluRay.x264-GROUP", Size: gb(9),
			Protocol: download.Usenet, Indexer: "nzbs"},
		{Title: "Inception.2010.1080p.BluRay.x264-OTHER", Size: gb(9),
			Protocol: download.Torrent, Indexer: "rarbg"},
	}
	views, err := svc.MovieReleases(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	if !views[0].Accepted || views[0].Protocol != download.Torrent {
		t.Errorf("preferring torrents should put the torrent first: %+v", views[0])
	}

	// now the usenet release is plainly better. The preference must not
	// override that.
	svc2, idx2, cat2 := testService(t)
	m2 := seedMovie(t, cat2, t.TempDir())
	svc2.Usenet, svc2.Torrent = &stubDownloader{}, &stubDownloader{}
	svc2.Settings = stubSettings{DownloadProtocolKey: PreferTorrent}
	idx2.releases = []prowlarr.Release{
		{Title: "Inception.2010.1080p.BluRay.x264-GROUP", Size: gb(9),
			Protocol: download.Usenet, Indexer: "nzbs"},
		{Title: "Inception.2010.720p.WEB", Size: gb(3),
			Protocol: download.Torrent, Indexer: "rarbg"},
	}
	views, err = svc2.MovieReleases(context.Background(), m2)
	if err != nil {
		t.Fatal(err)
	}
	if views[0].Protocol != download.Usenet {
		t.Errorf("a better usenet release must still win: %+v", views[0])
	}
}

// A torrent's files are still being seeded, so importing one hard-links
// it into the library and leaves the original alone. Moving it would
// stop the seeding and leave qBittorrent pointed at nothing.
func TestATorrentIsLinkedAndAUsenetJobIsMoved(t *testing.T) {
	for _, tc := range []struct {
		name        string
		protocol    string
		sourceLives bool
	}{
		{name: "torrent keeps its file", protocol: download.Torrent, sourceLives: true},
		{name: "usenet job gives it up", protocol: download.Usenet, sourceLives: false},
		{name: "an upload is reely's own file", protocol: "", sourceLives: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "download", "Thing.mkv")
			if err := os.MkdirAll(filepath.Dir(src), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(src, []byte("video"), 0o600); err != nil {
				t.Fatal(err)
			}
			lib := filepath.Join(dir, "library")

			svc := &Service{}
			dest, size, err := svc.placeInto(lib, "Thing (2024)/Thing (2024)", src, tc.protocol)
			if err != nil {
				t.Fatal(err)
			}
			if size != 5 {
				t.Errorf("size = %d, want 5", size)
			}
			if _, err := os.Stat(dest); err != nil {
				t.Fatalf("nothing landed in the library: %v", err)
			}
			_, err = os.Stat(src)
			if tc.sourceLives && err != nil {
				t.Errorf("the source was taken away: %v", err)
			}
			if !tc.sourceLives && err == nil {
				t.Error("the source is still there — it should have moved")
			}
		})
	}
}

// Retiring a finished torrent is what stops it being imported again.
// Unlike SAB history, a torrent never leaves the client's list on its
// own: it seeds indefinitely, so without this it would come back around
// on every single pass.
func TestAFinishedTorrentIsRetiredNotDeleted(t *testing.T) {
	qb := &stubRetirer{stubDownloader: &stubDownloader{}}
	svc := &Service{Torrent: qb, Settings: stubSettings{}}

	if !svc.retire(context.Background(), "abc") {
		t.Fatal("retire reported failure")
	}
	if qb.category != DefaultSeedingCategory {
		t.Errorf("category = %q, want %q", qb.category, DefaultSeedingCategory)
	}
	if len(qb.deleted) != 0 {
		t.Errorf("the torrent was deleted, which stops the seeding: %v", qb.deleted)
	}

	// and the category is somebody's to change
	svc.Settings = stubSettings{SeedingCategoryKey: "done"}
	if !svc.retire(context.Background(), "abc") {
		t.Fatal("retire reported failure")
	}
	if qb.category != "done" {
		t.Errorf("category = %q, want the configured one", qb.category)
	}
}

// stubRetirer is a torrent client that can be moved between categories.
type stubRetirer struct {
	*stubDownloader
	category string
}

func (r *stubRetirer) SetCategory(_ context.Context, _, category string) error {
	r.category = category
	return nil
}
