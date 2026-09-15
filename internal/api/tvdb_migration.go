package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/refresh"
)

// Migrating shows from TMDB to TVDB sourcing. Most shows already know
// their TVDB id (TMDB's external ids supplied it for the id-keyed
// searches), so the bulk move is one admin button; shows TMDB couldn't
// map get a name+year search, and whatever remains lands in a manual-fix
// list. Nothing migrates on its own: titles change ("Kitchen Nightmares"
// becomes TVDB's "Kitchen Nightmares (US)") and revivals gain seasons, so
// the sweep runs when an admin asks.

// migrationRow is one show in the migration report.
type migrationRow struct {
	ID       int64  `json:"id"`
	Title    string `json:"title"`
	Year     int    `json:"year,omitempty"`
	NewTitle string `json:"newTitle,omitempty"`
	// NewSeasons counts seasons the TVDB tree added — they arrive
	// unmonitored, so a revival doesn't kick off a download sweep unasked
	NewSeasons int    `json:"newSeasons,omitempty"`
	Reason     string `json:"reason,omitempty"`
}

// handleTvdbMigrationStatus answers the Settings panel's headline: how
// many shows are on each source, and which TMDB rows would need a human.
func (s *Server) handleTvdbMigrationStatus(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	tmdbCount, tvdbCount, err := s.Catalog.CountShowsBySource()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	pending, err := s.Catalog.TmdbSourcedShows()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	ready, unresolved := 0, []migrationRow{}
	for _, p := range pending {
		if p.TvdbID > 0 {
			ready++
		} else {
			unresolved = append(unresolved, migrationRow{ID: p.ID, Title: p.Title, Year: p.Year,
				Reason: "no TVDB id on record — search below or migrate manually"})
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"enabled":    s.tvdbEnabled(),
		"tmdbShows":  tmdbCount,
		"tvdbShows":  tvdbCount,
		"ready":      ready,
		"unresolved": unresolved,
	})
}

// handleTvdbMigrate runs the sweep: every TMDB-sourced show with a known
// TVDB id migrates; the rest get one careful name+year search; whatever
// is still ambiguous is reported for the manual picker.
func (s *Server) handleTvdbMigrate(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !s.tvdbEnabled() {
		writeErr(w, http.StatusPreconditionFailed, errors.New("add a TVDB API key in Settings first"))
		return
	}
	pending, err := s.Catalog.TmdbSourcedShows()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	var migrated, unresolved, failed []migrationRow
	for _, p := range pending {
		if err := r.Context().Err(); err != nil {
			break
		}
		tvdbID := p.TvdbID
		if tvdbID == 0 {
			tvdbID = s.tvdbLookup(r.Context(), p.Title, p.Year)
		}
		if tvdbID == 0 {
			unresolved = append(unresolved, migrationRow{ID: p.ID, Title: p.Title, Year: p.Year,
				Reason: "no confident TVDB match — pick one below"})
			continue
		}
		row, err := s.migrateShowToTVDB(r.Context(), p.ID, tvdbID)
		if err != nil {
			failed = append(failed, migrationRow{ID: p.ID, Title: p.Title, Year: p.Year, Reason: err.Error()})
			continue
		}
		migrated = append(migrated, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"migrated": migrated, "unresolved": unresolved, "failed": failed,
	})
}

// handleMigrateShow migrates one show onto an explicit TVDB series — the
// manual-fix path, fed by the picker in Settings.
func (s *Server) handleMigrateShow(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	if !s.tvdbEnabled() {
		writeErr(w, http.StatusPreconditionFailed, errors.New("add a TVDB API key in Settings first"))
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		TvdbID int `json:"tvdbId"`
	}
	if err := decodeJSON(r, &req); err != nil || req.TvdbID <= 0 {
		writeErr(w, http.StatusBadRequest, errors.New("body needs a positive tvdbId"))
		return
	}
	row, err := s.migrateShowToTVDB(r.Context(), id, req.TvdbID)
	if err != nil {
		writeErr(w, http.StatusConflict, err)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

// tvdbLookup finds a series by name when no id is on record — accepted
// only when exactly one result matches the title (country tags folded)
// with a compatible year. Anything less certain is a human's call.
func (s *Server) tvdbLookup(ctx context.Context, title string, year int) int {
	hits, err := s.TVDB.SearchShows(ctx, title)
	if err != nil {
		return 0
	}
	fold := func(t string) string {
		t = strings.ToLower(strings.TrimSpace(t))
		if i := strings.LastIndex(t, " ("); i > 0 && strings.HasSuffix(t, ")") {
			switch t[i+2 : len(t)-1] {
			case "us", "uk", "gb", "au", "nz", "ca":
				t = t[:i]
			}
		}
		return t
	}
	want := fold(title)
	match := 0
	for _, h := range hits {
		if fold(h.Title) != want {
			continue
		}
		if year > 0 && h.Year > 0 && (h.Year-year > 1 || year-h.Year > 1) {
			continue
		}
		if match != 0 && match != h.TvdbID {
			return 0 // two plausible series — ambiguity is a human's call
		}
		match = h.TvdbID
	}
	return match
}

// migrateShowToTVDB re-sources one show: fetch the TVDB record, refresh
// the row in place (files and monitor state survive — RefreshShow merges
// by season/episode number), stamp the source, drop the now-obsolete
// numbering overrides, and quiet any seasons the TVDB tree introduced.
func (s *Server) migrateShowToTVDB(ctx context.Context, showID int64, tvdbID int) (migrationRow, error) {
	sh, err := s.Catalog.GetShow(showID)
	if err != nil {
		return migrationRow{}, err
	}
	row := migrationRow{ID: showID, Title: sh.Title, Year: sh.Year}
	if sh.Source == "tvdb" {
		row.NewTitle = sh.Title
		return row, nil // already migrated — idempotent
	}
	if other, err := s.Catalog.ShowIDByTvdb(sh.LibraryID, tvdbID); err != nil {
		return row, err
	} else if other != 0 && other != showID {
		otherShow, err := s.Catalog.GetShow(other)
		name := "another show"
		if err == nil {
			name = fmt.Sprintf("%q", otherShow.Title)
		}
		return row, fmt.Errorf("TVDB series %d is already covered by %s in this library — if this entry is a duplicate (a split revival), delete it and let the other one carry those seasons", tvdbID, name)
	}
	d, err := s.TVDB.Show(ctx, tvdbID)
	if err != nil {
		return row, err
	}
	metadata.EnrichShowFromTMDB(ctx, s.TMDB, d)
	before, err := s.Catalog.ShowSeasonNumbers(showID)
	if err != nil {
		return row, err
	}
	if _, err := s.Catalog.RefreshShow(showID, d); err != nil {
		return row, err
	}
	if err := s.Catalog.MarkShowSource(showID, "tvdb", d.TmdbID); err != nil {
		return row, err
	}
	// the row's numbering is TheTVDB's now, so its scene mapping is too —
	// and MarkShowSource just cleared the manual offset the show may have
	// been carrying, which this replaces
	(&refresh.Refresher{Catalog: s.Catalog, TMDB: s.TMDB, TVDB: s.TVDB, XEM: s.XEM, Verbose: true}).
		SyncScene(ctx, showID, "tvdb", tvdbID)
	newSeasons, err := s.Catalog.UnmonitorSeasonsExcept(showID, before)
	if err != nil {
		return row, err
	}
	row.NewTitle, row.NewSeasons = d.Title, newSeasons
	detail, _ := json.Marshal(map[string]any{
		"title": fmt.Sprintf("%s → TVDB %d (%s)", sh.Title, tvdbID, d.Title),
	})
	if err := s.Catalog.AddHistory("migrated", 0, showID, 0, string(detail)); err != nil {
		// sqlite's own error text, no request-derived data
		log.Printf("reely: record migration: %v", err) //nolint:gosec // G706: see above
	}
	return row, nil
}
