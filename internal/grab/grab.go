package grab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/download"
	"github.com/getreely/reely/internal/qbittorrent"
	"github.com/getreely/reely/internal/sabnzbd"
)

// Downloader is the download-client surface the service needs —
// sabnzbd.Client today, and whatever else gets wired in later.
//
// Worded in the neutral vocabulary of internal/download rather than
// SAB's: id is whatever handle the client gave the job, and releaseURL
// is the link the indexer published. Nothing here says usenet, which is
// the point — a second client implements this and the import pipeline
// above it does not change.
type Downloader interface {
	AddURL(ctx context.Context, releaseURL, releaseName, category string) (string, error)
	Queue(ctx context.Context, category string, start, limit int) ([]download.QueueItem, int, error)
	History(ctx context.Context, category string, limit int) ([]download.HistoryItem, error)
	DeleteHistory(ctx context.Context, id string, delFiles bool) error
	DeleteQueue(ctx context.Context, id string, delFiles bool) error
	SetPriority(ctx context.Context, id string, priority int) error
	Configured() bool
}

// GrabRequest is the release the user (or later, the RSS loop) picked. It
// echoes fields straight off a ReleaseView, so the UI sends back what the
// search handed it.
type GrabRequest struct {
	Title       string `json:"title"`
	DownloadURL string `json:"downloadUrl"`
	Indexer     string `json:"indexer"`
	// Protocol is which client should take this — the search already
	// knew, and echoing it back beats guessing from the URL. Empty means
	// usenet, which is what every caller predating torrents meant.
	Protocol string `json:"protocol"`
	Size     int64  `json:"size"`
	// Origin names the path that decided to grab — rss, release day,
	// backlog, search missing, search, manual — purely for the history
	// trail: "which mechanism found this" is the first question asked of
	// a grab someone didn't click themselves.
	Origin string `json:"-"`
}

// Validate rejects a request that could never reach the download client —
// handlers call it for a clean 400, the service repeats it as a backstop.
func (g *GrabRequest) Validate() error {
	if g.Title == "" {
		return errors.New("grab needs the release title")
	}
	// a magnet is a legitimate way for an indexer to hand over a torrent
	// and has no host, so it is checked on its own terms
	if strings.HasPrefix(strings.ToLower(g.DownloadURL), "magnet:") {
		if g.Protocol == download.Usenet {
			return errors.New("a magnet link is not a usenet release")
		}
		return nil
	}
	u, err := url.Parse(g.DownloadURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("grab needs the release's http(s) download link, or a magnet")
	}
	return nil
}

// GrabMovie sends one release to the download client, filed under the
// movies category, and records it in history.
func (s *Service) GrabMovie(ctx context.Context, m *catalog.MovieDetails, req GrabRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	id, err := s.send(ctx, req, s.movieCategory())
	if err != nil {
		return err
	}
	return s.recordGrab(m.ID, 0, 0, req, id)
}

// GrabForShow sends one release for a show — a single episode when episode
// is set, a season pack otherwise. Episode grabs pin the episode id in
// history so the activity view can point at the exact row.
func (s *Service) GrabForShow(ctx context.Context, sh *catalog.ShowDetails, season, episode int, req GrabRequest) error {
	if err := req.Validate(); err != nil {
		return err
	}
	var episodeID int64
	if episode > 0 {
		ep := findEpisode(sh, season, episode)
		if ep == nil {
			return fmt.Errorf("%w: no S%02dE%02d on this show", ErrUnknownTarget, season, episode)
		}
		episodeID = ep.ID
	}
	id, err := s.send(ctx, req, s.tvCategory())
	if err != nil {
		return err
	}
	return s.recordGrab(0, sh.ID, episodeID, req, id)
}

func (s *Service) recordGrab(movieID, showID, episodeID int64, req GrabRequest, id string) error {
	entry := map[string]any{
		"title": req.Title, "indexer": req.Indexer, "size": req.Size, "nzoId": id,
	}
	if req.Origin != "" {
		entry["via"] = req.Origin
	}
	detail, _ := json.Marshal(entry)
	return s.Catalog.AddHistory("grabbed", movieID, showID, episodeID, string(detail))
}

// Both clients really do satisfy the interface, checked here rather
// than at the one place they are wired in: a method that drifts should
// fail the package that owns the contract.
var (
	_ Downloader = (*sabnzbd.Client)(nil)
	_ Downloader = (*qbittorrent.Client)(nil)
)

// protocolFor settles which client a request is for.
//
// The protocol rides on the request because the search that produced it
// already knew — asking again here would mean guessing from a URL. So
// everything below is a backstop for a caller that did not say, and for
// a while every caller was one: the field was added and then never
// filled in, which is how a torrent-only install came to be told that
// usenet was switched off.
//
// A magnet answers for itself, since nothing but a torrent client can
// take one. Failing that the old default stands, usenet, which is what
// every caller predating torrents meant. But defaulting to a protocol
// the install does not offer is only a roundabout way of failing, so
// where exactly one is on offer it takes the job.
func (s *Service) protocolFor(req GrabRequest) string {
	if req.Protocol != "" {
		return req.Protocol
	}
	if strings.HasPrefix(strings.ToLower(req.DownloadURL), "magnet:") {
		return download.Torrent
	}
	if p := s.protocols(); !p.usenet && p.torrent {
		return download.Torrent
	}
	return download.Usenet
}

// send hands one release to the client that speaks its protocol.
func (s *Service) send(ctx context.Context, req GrabRequest, category string) (string, error) {
	protocol := s.protocolFor(req)
	if reason := s.protocols().refusal(protocol); reason != "" {
		return "", errors.New(reason)
	}
	client := s.clientFor(protocol)
	if client == nil {
		return "", errors.New("no " + protocol + " client is set up")
	}
	return client.AddURL(ctx, req.DownloadURL, req.Title, category)
}
