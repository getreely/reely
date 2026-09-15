package qbittorrent

import (
	"context"
	"encoding/json"
)

// Status is qBittorrent's vital signs.
//
// Free disk space is here for the same reason it is in SABnzbd's: a
// download client that has quietly run out of room fails in a way
// nobody reads until a week of grabs has gone missing. Torrents make it
// worse, because a seeding torrent holds its data indefinitely rather
// than being cleared out after an import.
type Status struct {
	Version    string
	DiskFreeGB float64
	Torrents   int
	// AltSpeedOn reports the alternate rate limits being in force —
	// qBittorrent's nearest thing to SAB's paused, and a plausible
	// answer to "why is everything crawling".
	AltSpeedOn bool
}

// Status asks qBittorrent how it is doing.
//
// sync/maindata is the call that carries the server's own state rather
// than just a version string, so this proves the URL, the credentials
// and the session in one go and comes back with something worth showing.
func (c *Client) Status(ctx context.Context) (*Status, error) {
	version, err := c.Version(ctx)
	if err != nil {
		return nil, err
	}
	st := &Status{Version: version}

	body, err := c.call(ctx, "/api/v2/sync/maindata", nil)
	if err != nil {
		// the version answered, so qBittorrent is reachable and signed
		// in to — report that much rather than nothing
		return st, nil //nolint:nilerr // a partial answer beats failing a health panel
	}
	var main struct {
		Torrents    map[string]json.RawMessage `json:"torrents"`
		ServerState struct {
			FreeSpaceOnDisk int64 `json:"free_space_on_disk"`
			UseAltSpeed     bool  `json:"use_alt_speed_limits"`
		} `json:"server_state"`
	}
	if err := json.Unmarshal([]byte(body), &main); err != nil {
		return st, nil //nolint:nilerr // same: the version is still good news
	}
	st.DiskFreeGB = float64(main.ServerState.FreeSpaceOnDisk) / (1 << 30)
	st.AltSpeedOn = main.ServerState.UseAltSpeed
	st.Torrents = len(main.Torrents)
	return st, nil
}
