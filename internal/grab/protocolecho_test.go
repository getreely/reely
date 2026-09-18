package grab

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/prowlarr"
)

// torrentOnlyWorld is an install exactly like the one that reported
// this: qBittorrent wired up, SABnzbd absent, the setting on torrents
// only.
func torrentOnlyWorld(t *testing.T) (*Service, *stubIndexer, *stubDownloader) {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	idx := &stubIndexer{}
	qb := &stubDownloader{}
	return &Service{
		Indexer: idx, Catalog: catalog.New(conn), Torrent: qb,
		Settings: stubSettings{DownloadProtocolKey: TorrentOnly},
	}, idx, qb
}

// A torrent release on a torrent-only install was refused with "usenet
// is off, or no usenet client is set up".
//
// GrabRequest.Protocol was added so the search could tell the grab which
// client to use — "the search already knew, and echoing it back beats
// guessing from the URL" — but nothing ever echoed it. Every builder
// left the field empty, and send() read an empty protocol as usenet, so
// every automatic grab asked for the one client that was not there.
func TestATorrentOnlyInstallGrabsTorrents(t *testing.T) {
	svc, idx, qb := torrentOnlyWorld(t)
	m := seedMovie(t, svc.Catalog, t.TempDir())
	idx.releases = []prowlarr.Release{
		{Title: "Inception.2010.1080p.WEB-DL", Size: gb(5), Protocol: "torrent",
			DownloadURL: "magnet:?xt=urn:btih:ABCDEF0123456789", Indexer: "tpb"},
	}

	out := svc.SearchNow(context.Background(), m.ID, 0, 0, 0)
	if out.Grabbed == "" {
		t.Fatalf("torrent refused on a torrent-only install: %q", out.Reason)
	}
	if qb.calls != 1 {
		t.Fatalf("torrent client reached %d times, want 1", qb.calls)
	}
	if qb.lastURL != "magnet:?xt=urn:btih:ABCDEF0123456789" {
		t.Errorf("sent %q, want the magnet", qb.lastURL)
	}
}

// The RSS sweep builds its own request and had the same gap.
func TestRSSGrabsATorrentOnATorrentOnlyInstall(t *testing.T) {
	svc, idx, qb := torrentOnlyWorld(t)
	seedMovie(t, svc.Catalog, t.TempDir())
	idx.releases = []prowlarr.Release{
		{Title: "Inception.2010.1080p.WEB-DL", Size: gb(5), Protocol: "torrent",
			DownloadURL: "https://tpb/t/1.torrent", Indexer: "tpb"},
	}

	if n := svc.RSSSync(context.Background()); n != 1 {
		t.Fatalf("rss grabbed %d, want 1", n)
	}
	if qb.calls != 1 {
		t.Fatalf("torrent client reached %d times, want 1", qb.calls)
	}
}

// The protocol the search judged is the one the grab acts on, so a
// request echoing it back reaches that client and no other.
func TestGrabRequestProtocolPicksTheClient(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	sab, qb := &stubDownloader{}, &stubDownloader{}
	svc := &Service{
		Indexer: &stubIndexer{}, Catalog: catalog.New(conn),
		Usenet: sab, Torrent: qb, Settings: stubSettings{},
	}
	m := seedMovie(t, svc.Catalog, t.TempDir())

	if err := svc.GrabMovie(context.Background(), m, GrabRequest{
		Title: "Inception.2010.1080p.WEB-DL", DownloadURL: "https://tpb/t/1.torrent",
		Protocol: "torrent", Indexer: "tpb",
	}); err != nil {
		t.Fatal(err)
	}
	if qb.calls != 1 || sab.calls != 0 {
		t.Fatalf("torrent went to qbit %d times, sab %d — want 1 and 0", qb.calls, sab.calls)
	}

	if err := svc.GrabMovie(context.Background(), m, GrabRequest{
		Title: "Inception.2010.1080p.WEB-DL.nzb", DownloadURL: "https://idx/nzb/1",
		Protocol: "usenet", Indexer: "nzbs",
	}); err != nil {
		t.Fatal(err)
	}
	if sab.calls != 1 {
		t.Fatalf("usenet reached sab %d times, want 1", sab.calls)
	}
}

// A caller that says nothing still must not be handed to a client that
// is not there. A magnet answers for itself; otherwise the only protocol
// on offer takes the job rather than the historical usenet default.
func TestAnUnstatedProtocolDoesNotInventAUsenetClient(t *testing.T) {
	svc, _, qb := torrentOnlyWorld(t)
	m := seedMovie(t, svc.Catalog, t.TempDir())

	if err := svc.GrabMovie(context.Background(), m, GrabRequest{
		Title: "Inception.2010.1080p.WEB-DL", DownloadURL: "https://tpb/t/1.torrent",
		Indexer: "tpb", // no protocol
	}); err != nil {
		t.Fatalf("an unstated protocol was refused: %v", err)
	}
	if qb.calls != 1 {
		t.Fatalf("torrent client reached %d times, want 1", qb.calls)
	}
}

// With both clients up, the backstop cannot rescue a lost protocol: an
// unstated one reads as usenet, which is the historical default and a
// perfectly reachable client here. So this is what actually pins the
// echo — a torrent the search judged must reach qBittorrent, not SAB.
func TestAJudgedTorrentReachesTheTorrentClientWithBothUp(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	idx := &stubIndexer{}
	sab, qb := &stubDownloader{}, &stubDownloader{}
	svc := &Service{
		Indexer: idx, Catalog: catalog.New(conn),
		Usenet: sab, Torrent: qb, Settings: stubSettings{DownloadProtocolKey: PreferTorrent},
	}
	m := seedMovie(t, svc.Catalog, t.TempDir())
	idx.releases = []prowlarr.Release{
		{Title: "Inception.2010.1080p.WEB-DL", Size: gb(5), Protocol: "torrent",
			DownloadURL: "https://tpb/t/1.torrent", Indexer: "tpb"},
	}

	out := svc.SearchNow(context.Background(), m.ID, 0, 0, 0)
	if out.Grabbed == "" {
		t.Fatalf("nothing grabbed: %q", out.Reason)
	}
	if qb.calls != 1 || sab.calls != 0 {
		t.Fatalf("torrent went to qbit %d times and sab %d — want 1 and 0", qb.calls, sab.calls)
	}
}
