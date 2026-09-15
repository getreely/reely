package api

import (
	"context"
	"net/http"
	"time"

	"github.com/getreely/reely/internal/plex"
)

// The health panel: one admin-only endpoint that asks each companion
// service how it's doing. Prowlarr names which of its indexers are benched
// and why it's unhappy; SAB reports paused state and free disk. The UI
// polls this while Settings is open — nothing here writes anything.

type indexerHealth struct {
	Name         string `json:"name"`
	Enabled      bool   `json:"enabled"`
	Healthy      bool   `json:"healthy"`
	DisabledTill string `json:"disabledTill,omitempty"`
}

type prowlarrHealth struct {
	Configured bool            `json:"configured"`
	OK         bool            `json:"ok"`
	Error      string          `json:"error,omitempty"`
	Indexers   []indexerHealth `json:"indexers"`
	Warnings   []string        `json:"warnings"`
}

type sabHealth struct {
	Configured bool    `json:"configured"`
	OK         bool    `json:"ok"`
	Error      string  `json:"error,omitempty"`
	Version    string  `json:"version,omitempty"`
	Paused     bool    `json:"paused,omitempty"`
	DiskFreeGB float64 `json:"diskFreeGb,omitempty"`
}

// qBittorrent, when one is configured. Reported apart from SAB rather
// than folded into one "download client" row: an install may run both,
// and which one is unwell is the first thing worth knowing.
type qbitHealth struct {
	Configured bool    `json:"configured"`
	OK         bool    `json:"ok"`
	Error      string  `json:"error,omitempty"`
	Version    string  `json:"version,omitempty"`
	DiskFreeGB float64 `json:"diskFreeGb,omitempty"`
	Torrents   int     `json:"torrents,omitempty"`
	// AltSpeedOn is qBittorrent's nearest thing to SAB's paused, and a
	// plausible answer to "why is everything crawling".
	AltSpeedOn bool `json:"altSpeedOn,omitempty"`
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, map[string]any{
		"tmdb":     map[string]any{"configured": s.TMDB.Configured()},
		"prowlarr": s.prowlarrHealth(ctx),
		"sab":      s.sabHealth(ctx),
		"qbit":     s.qbitHealth(ctx),
		"plex":     s.plexHealth(ctx),
	})
}

func (s *Server) prowlarrHealth(ctx context.Context) prowlarrHealth {
	h := prowlarrHealth{Configured: s.Prowlarr.Configured(), Indexers: []indexerHealth{}, Warnings: []string{}}
	if !h.Configured {
		return h
	}
	indexers, err := s.Prowlarr.Indexers(ctx)
	if err != nil {
		h.Error = err.Error()
		return h
	}
	h.OK = true
	// the bench: which indexers Prowlarr has sidelined after failures
	benched := map[int]string{}
	if statuses, err := s.Prowlarr.IndexerStatuses(ctx); err == nil {
		now := time.Now()
		for _, st := range statuses {
			till, err := time.Parse(time.RFC3339, st.DisabledTill)
			if err == nil && till.After(now) {
				benched[st.IndexerID] = st.DisabledTill
			}
		}
	}
	for _, ix := range indexers {
		till := benched[ix.ID]
		h.Indexers = append(h.Indexers, indexerHealth{
			Name: ix.Name, Enabled: ix.Enable,
			Healthy:      ix.Enable && till == "",
			DisabledTill: till,
		})
	}
	// Prowlarr's own health check carries the human-readable reasons
	if warnings, err := s.Prowlarr.Health(ctx); err == nil {
		for _, wn := range warnings {
			h.Warnings = append(h.Warnings, wn.Message)
		}
	}
	return h
}

func (s *Server) sabHealth(ctx context.Context) sabHealth {
	h := sabHealth{Configured: s.Sab.Configured()}
	if !h.Configured {
		return h
	}
	st, err := s.Sab.Status(ctx)
	if err != nil {
		h.Error = err.Error()
		return h
	}
	h.OK = true
	h.Version, h.Paused, h.DiskFreeGB = st.Version, st.Paused, st.DiskFreeGB
	return h
}

func (s *Server) qbitHealth(ctx context.Context) qbitHealth {
	h := qbitHealth{Configured: s.Qbit.Configured()}
	if !h.Configured {
		return h
	}
	st, err := s.Qbit.Status(ctx)
	if err != nil {
		// the useful failures here are a refused password and an
		// unreachable WebUI, and the client already words both
		h.Error = err.Error()
		return h
	}
	h.OK = true
	h.Version, h.DiskFreeGB = st.Version, st.DiskFreeGB
	h.Torrents, h.AltSpeedOn = st.Torrents, st.AltSpeedOn
	return h
}

// Plex, when the owner has linked it. Two legs fail independently and
// for different reasons, so they are reported apart: plex.tv answers
// who the server is shared with, and the media server itself answers
// where its libraries sit on disk.
//
// The mapping is the point. "Connected" is nearly worthless here — the
// thing that silently fails is the folder match, and its symptom is
// somebody signing in perfectly well and reaching nothing. So this says
// which Plex library landed on which reely one, and names the ones that
// landed nowhere.
type plexLibraryHealth struct {
	Title string `json:"title"`
	Key   string `json:"key"`
	Type  string `json:"type"`
	// Matched is the reely library this section resolved to, empty when
	// nothing matched — which is the case worth showing.
	Matched string `json:"matched,omitempty"`
}

type plexHealth struct {
	// Configured is whether an account is linked at all.
	Configured bool   `json:"configured"`
	OK         bool   `json:"ok"`
	Error      string `json:"error,omitempty"`
	// ServerOK is the media server leg, which fails on its own — a wrong
	// address here is the ordinary mistake, and it leaves sign-in working
	// while nobody reaches a library.
	ServerOK    bool   `json:"serverOk"`
	ServerError string `json:"serverError,omitempty"`
	// ServerName is what the server calls itself. Empty when it could not
	// be read, which the UI renders as something generic rather than
	// treating as a failure.
	ServerName string `json:"serverName,omitempty"`

	People    int                 `json:"people"`
	Pending   int                 `json:"pending"`
	Libraries []plexLibraryHealth `json:"libraries"`
	Unmatched int                 `json:"unmatched"`
}

func (s *Server) plexHealth(ctx context.Context) plexHealth {
	h := plexHealth{Libraries: []plexLibraryHealth{}}
	client, cfg, err := s.plexClient()
	if err != nil {
		h.Error = err.Error()
		return h
	}
	if cfg.Token == "" || cfg.MachineID == "" {
		return h
	}
	h.Configured = true

	shares, err := client.SharedServers(ctx, cfg.Token, cfg.MachineID)
	if err != nil {
		h.Error = err.Error()
		return h
	}
	h.OK = true
	for _, sh := range shares {
		if sh.Accepted {
			h.People++
		} else {
			h.Pending++
		}
	}

	if cfg.ServerURL == "" {
		h.ServerError = "no server address set — people can sign in but reach no libraries"
		return h
	}
	sections, err := client.Sections(ctx, cfg.ServerURL, cfg.Token)
	if err != nil {
		h.ServerError = err.Error()
		return h
	}
	h.ServerOK = true
	// What plex.tv said at link time is the certain source; asking the
	// media server covers an install linked before that was recorded, and
	// rests on an endpoint that may not answer. Either way an empty name
	// is a label, not a failure.
	h.ServerName = cfg.ServerName
	if h.ServerName == "" {
		h.ServerName = client.ServerName(ctx, cfg.ServerURL, cfg.Token)
	}

	libs, err := s.Catalog.ListLibraries()
	if err != nil {
		h.ServerError = err.Error()
		return h
	}
	byID := make(map[int64]string, len(libs))
	targets := make([]plex.Target, 0, len(libs))
	for _, l := range libs {
		byID[l.ID] = l.Name
		targets = append(targets, plex.Target{ID: l.ID, Path: l.Path, Kind: l.Kind})
	}
	matched := plex.Match(sections, targets)
	for _, sec := range sections {
		row := plexLibraryHealth{Title: sec.Title, Key: sec.Key, Type: sec.Type}
		if id, ok := matched[sec.Key]; ok {
			row.Matched = byID[id]
		} else {
			h.Unmatched++
		}
		h.Libraries = append(h.Libraries, row)
	}
	return h
}

// handlePlexTest is the owner's "is this working?" button. It runs the
// same check the health panel shows, so the two cannot disagree about
// what working means.
func (s *Server) handlePlexTest(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	writeJSON(w, http.StatusOK, s.plexHealth(ctx))
}
