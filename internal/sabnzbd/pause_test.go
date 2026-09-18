package sabnzbd

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
)

// Pausing is per job, not the whole queue: SAB can hold one download
// while the rest carry on, and stopping everything because somebody
// wanted one thing to wait would be a different button.
func TestPauseAndResumeActOnOneJob(t *testing.T) {
	for _, tc := range []struct {
		call func(*Client) error
		name string
	}{
		{func(c *Client) error { return c.Pause(context.Background(), "SABnzbd_nzo_x1") }, "pause"},
		{func(c *Client) error { return c.Resume(context.Background(), "SABnzbd_nzo_x1") }, "resume"},
	} {
		c, got := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]any{"status": true})
		})
		if err := tc.call(c); err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		q := *got
		if q.Get("mode") != "queue" || q.Get("name") != tc.name ||
			q.Get("value") != "SABnzbd_nzo_x1" {
			t.Errorf("%s asked for %v", tc.name, q)
		}
	}
}

// SAB answers 200 with {"status": false} rather than an HTTP error when
// it will not do something — an id it no longer holds, most often — so
// the body is what decides, the same way cancelling reads it.
func TestARefusedPauseIsAnError(t *testing.T) {
	c, _ := testClient(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"status": false})
	})
	if err := c.Pause(context.Background(), "SABnzbd_nzo_gone"); err == nil {
		t.Fatal("a refused pause reported success")
	}
}
