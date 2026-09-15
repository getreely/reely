package api

import (
	"context"
	"errors"
	"fmt"
	"log"

	"github.com/getreely/reely/internal/auth"
	"github.com/getreely/reely/internal/plex"
)

// Turning "shared with Ana in Plex" into "Ana has an account here and
// reaches these libraries".
//
// Plex is the source of truth for both halves. Who may sign in is the
// sharing list; which libraries they reach is that list's section keys
// resolved through the server's own folders. Neither is reely's to
// decide, which is the point — the owner manages access in one place,
// the place they already manage it.

// plexSettings is what the owner linked, read back together because
// none of it is useful alone.
type plexSettings struct {
	ClientID  string
	Token     string
	MachineID string
	ServerURL string
	// ServerName is what plex.tv called the server when it was linked.
	// Empty on an install linked before it was recorded, which is what
	// the media server's own name covers.
	ServerName string
}

func (s *Server) plexSettings() plexSettings {
	return plexSettings{
		ClientID:   s.Settings.Get("plex_client_id"),
		Token:      s.Settings.Get("plex_owner_token"),
		MachineID:  s.Settings.Get("plex_machine_id"),
		ServerURL:  s.Settings.Get("plex_server_url"),
		ServerName: s.Settings.Get("plex_server_name"),
	}
}

// plexClient is the client for this install, or an error naming what is
// missing. The client identifier is generated once and kept: a PIN is
// bound to the identifier that made it, so a regenerated one strands
// every sign-in in progress.
func (s *Server) plexClient() (*plex.Client, plexSettings, error) {
	cfg := s.plexSettings()
	if cfg.ClientID == "" {
		id := auth.NewClientID()
		if err := s.Settings.Set("plex_client_id", id); err != nil {
			return nil, cfg, err
		}
		cfg.ClientID = id
	}
	client := plex.New(cfg.ClientID)
	if s.plexBase != "" {
		client.SetBaseURL(s.plexBase)
	}
	return client, cfg, nil
}

// SyncResult is what one sync did, for the admin UI to report.
type SyncResult struct {
	Accounts     int `json:"accounts"`
	Deactivated  int `json:"deactivated"`
	Unmatched    int `json:"unmatched"`
	PendingInvit int `json:"pendingInvites"`
}

// syncPlexUsers reconciles reely's accounts with the sharing list.
//
// Everyone accepted gets an account; everyone no longer on the list is
// deactivated and their sessions dropped. Library grants follow the
// sections each person reaches, matched to reely libraries by folder.
//
// A person whose sections match no reely library still gets an account
// with no libraries. They can sign in and see nothing, which is honest —
// the alternative is refusing them at the door for a mapping problem
// that is the owner's to fix, with nothing anywhere saying so.
func (s *Server) syncPlexUsers(ctx context.Context) (SyncResult, error) {
	var out SyncResult
	client, cfg, err := s.plexClient()
	if err != nil {
		return out, err
	}
	if cfg.Token == "" || cfg.MachineID == "" {
		return out, errors.New("link your Plex account first")
	}
	shares, err := client.SharedServers(ctx, cfg.Token, cfg.MachineID)
	if err != nil {
		return out, err
	}

	// the section→library map is read once for the whole sync rather than
	// per person: it is the same answer every time, and it is a call to
	// the media server
	byKey, err := s.plexLibraryMap(ctx, client, cfg)
	if err != nil {
		return out, err
	}

	keep := make([]int64, 0, len(shares))
	for _, share := range shares {
		if !share.Accepted {
			// invited and never took it up: they reach nothing in Plex, so
			// they get nothing here and cannot sign in
			out.PendingInvit++
			continue
		}
		keep = append(keep, share.AccountID)
		id, err := s.Auth.UpsertPlexUser(auth.PlexIdentity{
			AccountID: share.AccountID, Username: share.Username, Email: share.Email,
		})
		if err != nil {
			log.Printf("reely: plex sync: account %d: %v", share.AccountID, err)
			continue
		}
		s.ensureSharing(id, share.Username)
		out.Accounts++

		libs := make([]int64, 0, len(share.Sections))
		seen := map[int64]bool{}
		for _, sec := range share.SharedSections() {
			libID, ok := byKey[sec.Key]
			if !ok || seen[libID] {
				continue
			}
			seen[libID] = true
			libs = append(libs, libID)
		}
		if len(libs) == 0 {
			out.Unmatched++
		}
		if err := s.Auth.SetUserLibraries(id, libs); err != nil {
			log.Printf("reely: plex sync: libraries for account %d: %v", share.AccountID, err)
		}
	}

	closed, err := s.Auth.DeactivatePlexUsersExcept(keep)
	if err != nil {
		return out, err
	}
	out.Deactivated = closed
	return out, nil
}

// plexLibraryMap resolves Plex section keys to reely library ids by
// folder. An unmapped key simply isn't in the result — Match leaves
// anything ambiguous out rather than guessing, because a wrong pairing
// sends one person's requests into another person's library.
func (s *Server) plexLibraryMap(ctx context.Context, client *plex.Client, cfg plexSettings) (map[string]int64, error) {
	if cfg.ServerURL == "" {
		return nil, errors.New("set your Plex server address so libraries can be matched")
	}
	sections, err := client.Sections(ctx, cfg.ServerURL, cfg.Token)
	if err != nil {
		return nil, fmt.Errorf("read Plex libraries: %w", err)
	}
	libs, err := s.Catalog.ListLibraries()
	if err != nil {
		return nil, err
	}
	targets := make([]plex.Target, 0, len(libs))
	for _, l := range libs {
		targets = append(targets, plex.Target{ID: l.ID, Path: l.Path, Kind: l.Kind})
	}
	return plex.Match(sections, targets), nil
}
