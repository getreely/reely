package qbittorrent

import (
	"context"
	"testing"
)

// qBittorrent 5.0 renamed pause and resume to stop and start, keeping
// the old names as deprecated aliases. reely asks for the new name and
// falls back only where the build has never heard of it, so both a 4.x
// and a 5.x install work from the same code.
func TestPauseAndResumeUseTheModernNames(t *testing.T) {
	q := &stubQB{torrents: "[]"}
	c := testClient(t, q)
	// sign in first: the very first authenticated call spends a 403 and a
	// retry, which would count the path twice for reasons unrelated to
	// which name reely asked for
	if _, err := c.Version(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := c.Pause(context.Background(), "abc123"); err != nil {
		t.Fatal(err)
	}
	if n := q.called("/api/v2/torrents/stop"); n != 1 {
		t.Errorf("stop called %d times, want 1", n)
	}
	if n := q.called("/api/v2/torrents/pause"); n != 0 {
		t.Errorf("the deprecated pause was called %d times on a build that has stop", n)
	}
	if got := q.formFor("/api/v2/torrents/stop").Get("hashes"); got != "abc123" {
		t.Errorf("hashes = %q, want the info hash", got)
	}

	if err := c.Resume(context.Background(), "abc123"); err != nil {
		t.Fatal(err)
	}
	if n := q.called("/api/v2/torrents/start"); n != 1 {
		t.Errorf("start called %d times, want 1", n)
	}
}

// An older build has no stop/start at all, and answers 404. That is the
// one answer worth a second attempt: it says this qBittorrent does not
// have the endpoint, not that the endpoint refused.
func TestPauseFallsBackOnAnOlderBuild(t *testing.T) {
	q := &stubQB{torrents: "[]", missing: map[string]bool{
		"/api/v2/torrents/stop":  true,
		"/api/v2/torrents/start": true,
	}}
	c := testClient(t, q)
	if _, err := c.Version(context.Background()); err != nil {
		t.Fatal(err)
	}

	if err := c.Pause(context.Background(), "abc123"); err != nil {
		t.Fatalf("pause failed on a build that only has the old name: %v", err)
	}
	if n := q.called("/api/v2/torrents/pause"); n != 1 {
		t.Errorf("the legacy pause was called %d times, want 1", n)
	}
	if got := q.formFor("/api/v2/torrents/pause").Get("hashes"); got != "abc123" {
		t.Errorf("hashes = %q, want the info hash", got)
	}

	if err := c.Resume(context.Background(), "abc123"); err != nil {
		t.Fatalf("resume failed on a build that only has the old name: %v", err)
	}
	if n := q.called("/api/v2/torrents/resume"); n != 1 {
		t.Errorf("the legacy resume was called %d times, want 1", n)
	}
}

// A refusal is not a missing endpoint. Anything but a 404 is this
// qBittorrent answering the endpoint it does have, so it surfaces rather
// than being retried under another name.
func TestARefusedPauseIsNotRetriedUnderTheOldName(t *testing.T) {
	q := &stubQB{torrents: "[]", badPass: true}
	c := testClient(t, q)

	if err := c.Pause(context.Background(), "abc123"); err == nil {
		t.Fatal("a refused pause reported success")
	}
	if n := q.called("/api/v2/torrents/pause"); n != 0 {
		t.Errorf("a refusal was retried under the deprecated name %d times", n)
	}
}

// qBittorrent 5 renamed the states too. A paused download reads as
// Paused either way — the button that offers to resume it keys on that
// word — and a stopped-but-complete torrent still counts as finished,
// which is what decides whether it is ever imported.
func TestVersionFiveStateNames(t *testing.T) {
	for _, tc := range []struct {
		state string
		want  string
	}{
		{"pausedDL", "Paused"},
		{"stoppedDL", "Paused"},
	} {
		if got := queueStatus(tc.state); got != tc.want {
			t.Errorf("queueStatus(%q) = %q, want %q", tc.state, got, tc.want)
		}
	}
	for _, state := range []string{"pausedUP", "stoppedUP"} {
		if !(torrent{State: state}).done() {
			t.Errorf("a %s torrent is not seen as finished, so it would never import", state)
		}
	}
}
