package sabnzbd

import (
	"context"
	"net/url"
	"strconv"
)

// Status is SAB's vital signs: reachable at all, paused, and how much room
// the download disk has left — low disk is the classic silent killer of a
// download setup.
type Status struct {
	Version    string
	Paused     bool
	DiskFreeGB float64
}

// Status asks SAB how it's doing via a one-slot queue call (the queue
// envelope carries version, paused state, and disk space).
func (c *Client) Status(ctx context.Context) (*Status, error) {
	q := url.Values{"mode": {"queue"}, "start": {"0"}, "limit": {"1"}}
	var body struct {
		Queue struct {
			Version    string `json:"version"`
			Paused     bool   `json:"paused"`
			Diskspace1 string `json:"diskspace1"` // GB free, quoted
		} `json:"queue"`
	}
	if err := c.call(ctx, q, &body); err != nil {
		return nil, err
	}
	free, _ := strconv.ParseFloat(body.Queue.Diskspace1, 64)
	return &Status{Version: body.Queue.Version, Paused: body.Queue.Paused, DiskFreeGB: free}, nil
}
