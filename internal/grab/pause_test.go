package grab

import (
	"context"
	"testing"
)

// An id belongs to at most one client — SAB's nzo ids and a torrent's
// info hash cannot be mistaken for one another — so pausing tries each
// in turn rather than being told which one to ask.
func TestPauseReachesWhicheverClientHasTheJob(t *testing.T) {
	sab, qb := &stubDownloader{}, &stubDownloader{}
	svc := &Service{Usenet: sab, Torrent: qb, Settings: stubSettings{}}

	if err := svc.PauseDownload(context.Background(), "nzo_1"); err != nil {
		t.Fatal(err)
	}
	if err := svc.ResumeDownload(context.Background(), "nzo_1"); err != nil {
		t.Fatal(err)
	}
	// the stub accepts everything, so the first client tried answers —
	// what matters is that the verb arrived at a client at all, with the
	// id intact
	if len(sab.paused) != 1 || sab.paused[0] != "nzo_1" {
		t.Errorf("pause reached the client as %v, want [nzo_1]", sab.paused)
	}
	if len(sab.resumed) != 1 || sab.resumed[0] != "nzo_1" {
		t.Errorf("resume reached the client as %v, want [nzo_1]", sab.resumed)
	}
}

// With no client at all there is nothing to pause, and saying so beats a
// silent success.
func TestPauseWithNoClientSaysSo(t *testing.T) {
	svc := &Service{Settings: stubSettings{}}
	if err := svc.PauseDownload(context.Background(), "nzo_1"); err == nil {
		t.Error("pausing with no download client reported success")
	}
	if err := svc.ResumeDownload(context.Background(), "nzo_1"); err == nil {
		t.Error("resuming with no download client reported success")
	}
}
