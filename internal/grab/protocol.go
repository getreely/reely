package grab

import (
	"context"
	"strings"

	"github.com/getreely/reely/internal/download"
)

// Which download client gets a release, and which protocols may be used
// at all.
//
// reely can hold two clients at once — SABnzbd for usenet, qBittorrent
// for torrents — and most installs will run one. The rules are meant to
// need no configuring in that case: a protocol whose client is not set
// up cannot be grabbed, because there is nothing to grab it with.
//
// The setting exists for the install that runs both and wants one of
// them off, or preferred, without tearing down a working client.

// DownloadProtocolKey is the setting naming the policy.
const DownloadProtocolKey = "download_protocol"

// The values it takes. Anything unrecognised reads as PreferUsenet,
// which is what every install before this setting existed was doing.
const (
	PreferUsenet  = "prefer_usenet"
	PreferTorrent = "prefer_torrent"
	UsenetOnly    = "usenet_only"
	TorrentOnly   = "torrent_only"
)

// policy is what the settings and the configured clients add up to.
type policy struct {
	usenet  bool // a usenet release could be grabbed
	torrent bool // a torrent could be
	// prefer breaks a tie between two equally good releases. Empty when
	// only one protocol is in play and there is nothing to break.
	prefer string
}

// allows reports whether a release of this protocol can be acted on.
func (p policy) allows(protocol string) bool {
	switch protocol {
	case download.Usenet:
		return p.usenet
	case download.Torrent:
		return p.torrent
	}
	return false
}

// refusal explains, for a search row, why a protocol is not on offer —
// so the row says something more useful than nothing at all.
func (p policy) refusal(protocol string) string {
	switch protocol {
	case download.Usenet:
		if !p.usenet {
			return "usenet is off, or no usenet client is set up"
		}
	case download.Torrent:
		if !p.torrent {
			return "torrents are off, or no torrent client is set up"
		}
	default:
		return "unknown protocol " + protocol
	}
	return ""
}

// protocols works out the current policy.
//
// Configuration is the floor and the setting can only narrow it: no
// amount of preferring torrents makes one grabbable without a torrent
// client, and that is a truth about the install rather than a choice to
// be overridden.
func (s *Service) protocols() policy {
	p := policy{
		usenet:  s.Usenet != nil && s.Usenet.Configured(),
		torrent: s.Torrent != nil && s.Torrent.Configured(),
	}
	// An install with NO client is a different case from one that has
	// picked a side. Judging every release unusable would only be a
	// second way of saying "nothing is set up", and it would make a
	// search run while exploring look like every release failed. The
	// grab path says it once and plainly, so searching stays useful.
	if !p.usenet && !p.torrent {
		p.usenet, p.torrent = true, true
	}
	switch strings.TrimSpace(s.setting(DownloadProtocolKey, PreferUsenet)) {
	case UsenetOnly:
		p.torrent = false
	case TorrentOnly:
		p.usenet = false
	case PreferTorrent:
		p.prefer = download.Torrent
	default: // PreferUsenet, and every install that never set this
		p.prefer = download.Usenet
	}
	// a preference between one thing is not a preference
	if !p.usenet || !p.torrent {
		p.prefer = ""
	}
	return p
}

// clientFor is the client that handles one protocol, or nil when that
// protocol has none.
func (s *Service) clientFor(protocol string) Downloader {
	switch protocol {
	case download.Usenet:
		if s.Usenet != nil && s.Usenet.Configured() {
			return s.Usenet
		}
	case download.Torrent:
		if s.Torrent != nil && s.Torrent.Configured() {
			return s.Torrent
		}
	}
	return nil
}

// clients is every configured client, for the sweeps that have to look
// at all of them: a queue or an import pass covers the whole install,
// not one protocol.
func (s *Service) clients() []Downloader {
	var out []Downloader
	if s.Usenet != nil && s.Usenet.Configured() {
		out = append(out, s.Usenet)
	}
	if s.Torrent != nil && s.Torrent.Configured() {
		out = append(out, s.Torrent)
	}
	return out
}

// haveClient reports whether anything can download at all.
func (s *Service) haveClient() bool { return len(s.clients()) > 0 }

// actOn runs one id-keyed operation against whichever client owns the
// id — cancelling a download, reprioritising it.
//
// The id alone does not say which client it came from, so each is tried
// in turn. That is safe rather than sloppy: SAB's nzo ids and a
// torrent's info hash cannot be mistaken for one another, so at most one
// client will recognise any id. An install with a single client tries
// exactly that one.
func (s *Service) actOn(ctx context.Context, id string, do func(Downloader) error) error {
	clients := s.clients()
	var lastErr error
	for _, c := range clients {
		if err := do(c); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

// finished is every completed job in one category, across every
// configured client. An install running both protocols has finished
// jobs waiting in each, and sweeping only one would leave the other's
// imports to pile up unnoticed.
//
// A client that errors does not sink the sweep — the other one's work
// still gets imported — but an error with nothing to show for it is
// returned so the caller can say so.
func (s *Service) finished(ctx context.Context, category string) ([]download.HistoryItem, error) {
	var merged []download.HistoryItem
	var lastErr error
	for _, c := range s.clients() {
		items, err := c.History(ctx, category, 50)
		if err != nil {
			lastErr = err
			continue
		}
		merged = append(merged, items...)
	}
	if len(merged) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return merged, nil
}

// inFlight is every still-downloading job in one category, across every
// configured client — the same reasoning as finished.
func (s *Service) inFlight(ctx context.Context, category string) ([]download.QueueItem, error) {
	var merged []download.QueueItem
	var lastErr error
	for _, c := range s.clients() {
		items, _, err := c.Queue(ctx, category, 0, queueScanMax)
		if err != nil {
			lastErr = err
			continue
		}
		merged = append(merged, items...)
	}
	if len(merged) == 0 && lastErr != nil {
		return nil, lastErr
	}
	return merged, nil
}

// SeedingCategoryKey names where an imported torrent is put out to
// pasture, and DefaultSeedingCategory is where it goes by default.
const (
	SeedingCategoryKey     = "qbit_seeding_category"
	DefaultSeedingCategory = "reely-seeding"
)

// Retirer is a download client that can let go of a finished job
// without destroying it.
//
// Only torrents need this, which is why it is not on Downloader: a
// usenet job is finished when it finishes and its history entry is
// deleted. A torrent is still seeding the files it just handed over, so
// "done with it" has to mean something other than "delete it", and the
// answer is to move it out of the category reely sweeps.
type Retirer interface {
	SetCategory(ctx context.Context, id, category string) error
}

// retire takes a finished torrent out of reely's way without stopping
// it. Reports whether it managed to.
//
// Getting this wrong is not a small thing: a torrent that stays in the
// swept category gets imported again on every pass, forever, because
// unlike SAB history it never clears itself.
func (s *Service) retire(ctx context.Context, id string) bool {
	client, ok := s.clientFor(download.Torrent).(Retirer)
	if !ok {
		return false
	}
	category := s.setting(SeedingCategoryKey, DefaultSeedingCategory)
	if err := client.SetCategory(ctx, id, category); err != nil {
		return false
	}
	return true
}

// CanDownload reports whether reely has any download client at all —
// what a caller outside this package means by "is downloading set up".
func (s *Service) CanDownload() bool { return s.haveClient() }
