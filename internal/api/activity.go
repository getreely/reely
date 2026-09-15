package api

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strings"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/grab"
)

// pageParams reads limit/offset query params under the given names, with
// a sane default and cap — every Activity tab pages the same way.
func pageParams(r *http.Request, limitKey, offsetKey string) (int, int) {
	limit := int(queryInt(r, limitKey))
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	offset := int(queryInt(r, offsetKey))
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

// The Activity surface: what's downloading, what's stuck, what happened.
// Admin-only — it spans every library, and its actions (retry, resolve)
// move files around.

// searchRow is one queued search dressed for display: the raw target plus
// the title it points at, resolved here because the grab queue only ever
// holds ids.
type searchRow struct {
	grab.QueuedSearch
	Title string `json:"title"`
}

// searchPage resolves titles for the waiting searches, narrows them to
// those matching filter, and cuts one page. Titles are resolved before
// filtering because the filter is on the name a person sees, not on the
// row ids the queue actually holds; ids are deduped first, so a
// two-hundred-episode enqueue is still one show to look up.
//
// A target whose title has since been deleted keeps its row — showing
// "removed title" beats silently hiding work that is still queued to run.
func (s *Server) searchPage(all []grab.QueuedSearch, filter string, offset, limit int) ([]searchRow, int) {
	seenMovies, seenShows := map[int64]bool{}, map[int64]bool{}
	movieIDs, showIDs := []int64{}, []int64{}
	for _, q := range all {
		if q.MovieID != 0 && !seenMovies[q.MovieID] {
			seenMovies[q.MovieID] = true
			movieIDs = append(movieIDs, q.MovieID)
		}
		if q.ShowID != 0 && !seenShows[q.ShowID] {
			seenShows[q.ShowID] = true
			showIDs = append(showIDs, q.ShowID)
		}
	}
	movies, shows, err := s.Catalog.TitlesByID(movieIDs, showIDs)
	if err != nil {
		log.Printf("reely: resolving queued search titles: %v", err)
		movies, shows = map[int64]string{}, map[int64]string{}
	}
	needle := strings.ToLower(strings.TrimSpace(filter))
	rows := make([]searchRow, 0, len(all))
	for _, q := range all {
		title := movies[q.MovieID]
		if q.Kind == "episode" {
			title = shows[q.ShowID]
		}
		if title == "" {
			title = "removed title"
		}
		if needle != "" && !strings.Contains(strings.ToLower(title), needle) {
			continue
		}
		rows = append(rows, searchRow{QueuedSearch: q, Title: title})
	}
	total := len(rows)
	if offset >= total {
		return nil, total
	}
	// limit <= 0 means every match, which is what a bulk cancel over a
	// filtered queue asks for
	if limit <= 0 {
		return rows[offset:], total
	}
	return rows[offset:min(offset+limit, total)], total
}

func (s *Server) handleActivity(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	limit, offset := pageParams(r, "historyLimit", "historyOffset")
	history, total, err := s.Catalog.ListHistoryPage(limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	queueLimit, queueOffset := pageParams(r, "queueLimit", "queueOffset")
	queue, queueTotal := s.Grab.Queue(r.Context(), r.URL.Query().Get("queueFilter"), queueOffset, queueLimit)
	searchLimit, searchOffset := pageParams(r, "searchLimit", "searchOffset")
	searches, searchTotal := s.searchPage(
		s.Grab.QueuedSearches(), r.URL.Query().Get("searchFilter"), searchOffset, searchLimit)
	writeJSON(w, http.StatusOK, map[string]any{
		"queue":         queue,
		"queueTotal":    queueTotal,
		"problems":      s.Grab.Problems(),
		"history":       history,
		"historyTotal":  total,
		"searches":      searches,
		"searchesTotal": searchTotal,
	})
}

// handleActivityCancel stops downloads that are still in flight and bins
// their partial files. Bulk by design — the Activity view selects rows —
// and one refusal doesn't abandon the rest of the selection. With
// blocklist set, each cancelled job's release is banned too, so the
// automatic paths don't simply fetch the same thing again.
func (s *Server) handleActivityCancel(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var req struct {
		NzoIDs    []string `json:"nzoIds"`
		All       bool     `json:"all"`
		Filter    string   `json:"filter"`
		Blocklist bool     `json:"blocklist"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ids := req.NzoIDs
	// job names back the blocklist fallback for jobs reely never grabbed,
	// so they're collected whenever a ban is wanted — not just for "all"
	names := map[string]string{}
	if req.All || req.Blocklist {
		items, _ := s.Grab.Queue(r.Context(), req.Filter, 0, 0)
		if req.All {
			ids = make([]string, 0, len(items))
		}
		for _, it := range items {
			if req.All {
				ids = append(ids, it.ID)
			}
			names[it.ID] = it.Name
		}
	}
	if len(ids) == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("no downloads to cancel"))
		return
	}
	cancelled := 0
	var failures []string
	for _, id := range ids {
		if err := s.Grab.CancelDownload(r.Context(), id); err != nil {
			log.Printf("reely: cancel download %s: %v", id, err)
			failures = append(failures, err.Error())
			continue
		}
		cancelled++
		if req.Blocklist {
			s.Grab.BlocklistJob(id, names[id], "cancelled and blocked")
		}
	}
	// every one failed — the client asked for something and got nothing,
	// so this is an error, not a report
	if cancelled == 0 {
		writeErr(w, http.StatusBadGateway, errors.New(failures[0]))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": cancelled, "failed": len(failures)})
}

// handleActivityPriority changes queued downloads' priority in SAB —
// Force (2) starts a job immediately, High/Normal/Low (1/0/-1) reorder
// the queue. Bulk like cancel: the Activity view selects rows.
func (s *Server) handleActivityPriority(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var req struct {
		NzoIDs   []string `json:"nzoIds"`
		Priority int      `json:"priority"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	switch req.Priority {
	case -1, 0, 1, 2:
	default:
		writeErr(w, http.StatusBadRequest, errors.New("priority must be -1 (low), 0 (normal), 1 (high) or 2 (force)"))
		return
	}
	if len(req.NzoIDs) == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("no downloads selected"))
		return
	}
	changed := 0
	var failures []string
	for _, id := range req.NzoIDs {
		if err := s.Grab.SetDownloadPriority(r.Context(), id, req.Priority); err != nil {
			log.Printf("reely: set priority %s: %v", id, err)
			failures = append(failures, err.Error())
			continue
		}
		changed++
	}
	if changed == 0 {
		writeErr(w, http.StatusBadGateway, errors.New(failures[0]))
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"changed": changed, "failed": len(failures)})
}

// handleSearchesCancel drops queued searches — the selected keys, or the
// whole queue when all is set.
func (s *Server) handleSearchesCancel(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var req struct {
		Keys   []string `json:"keys"`
		All    bool     `json:"all"`
		Filter string   `json:"filter"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if !req.All && len(req.Keys) == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("no searches to cancel"))
		return
	}
	var cancelled int
	switch {
	case req.All && strings.TrimSpace(req.Filter) == "":
		cancelled = s.Grab.ClearSearchQueue()
	case req.All:
		// everything matching the filter, not just the page it fits on
		rows, _ := s.searchPage(s.Grab.QueuedSearches(), req.Filter, 0, 0)
		keys := make([]string, 0, len(rows))
		for _, row := range rows {
			keys = append(keys, row.Key)
		}
		cancelled = s.Grab.CancelSearches(keys)
	default:
		cancelled = s.Grab.CancelSearches(req.Keys)
	}
	writeJSON(w, http.StatusOK, map[string]any{"cancelled": cancelled})
}

func (s *Server) handleActivityRetry(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var req struct {
		NzoID string `json:"nzoId"`
	}
	if err := decodeJSON(r, &req); err != nil || req.NzoID == "" {
		writeErr(w, http.StatusBadRequest, errors.New("missing nzoId"))
		return
	}
	s.Grab.RetryNow(req.NzoID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "retrying"})
}

// The blocklist tab: banned releases, with single and bulk pardons.

func (s *Server) handleBlocklist(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	limit, offset := pageParams(r, "limit", "offset")
	entries, total, err := s.Catalog.ListBlocklistPage(limit, offset)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if entries == nil {
		entries = []catalog.BlocklistEntry{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"blocklist": entries, "total": total})
}

func (s *Server) handleBlocklistRemove(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var req struct {
		IDs []int64 `json:"ids"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if len(req.IDs) == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("no ids to remove"))
		return
	}
	removed, err := s.Catalog.RemoveBlocklist(req.IDs)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"removed": removed})
}

// handleHistoryBlocklist bans the release a "grabbed" history row records
// — the undo for a grab that turned out to be the wrong file. The row
// already knows the release name, the indexer it came from, and the title
// it was for, so the ban carries the same scope a failed download's would.
func (s *Server) handleHistoryBlocklist(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	entry, err := s.Catalog.HistoryByID(id)
	if err != nil {
		notFoundOr500(w, err)
		return
	}
	if entry.Kind != "grabbed" {
		writeErr(w, http.StatusBadRequest, errors.New("only grabbed history entries can be blocklisted"))
		return
	}
	var detail struct {
		Title   string `json:"title"`
		Indexer string `json:"indexer"`
	}
	if err := json.Unmarshal([]byte(entry.Detail), &detail); err != nil || detail.Title == "" {
		writeErr(w, http.StatusBadRequest, errors.New("this history entry doesn't record a release name"))
		return
	}
	if err := s.Catalog.AddBlocklist(detail.Title, detail.Indexer,
		entry.MovieID, entry.ShowID, "blocked from history"); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"blocked": detail.Title})
}

// handleActivityDelete bins a stuck download: files and SAB entry both.
func (s *Server) handleActivityDelete(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var req struct {
		NzoID string `json:"nzoId"`
	}
	if err := decodeJSON(r, &req); err != nil || req.NzoID == "" {
		writeErr(w, http.StatusBadRequest, errors.New("missing nzoId"))
		return
	}
	if err := s.Grab.DeleteJob(r.Context(), req.NzoID); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (s *Server) handleActivityResolve(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	var req struct {
		NzoID   string `json:"nzoId"`
		MovieID int64  `json:"movieId"`
		ShowID  int64  `json:"showId"`
		Season  int    `json:"season"`  // optional: pin the job's files to a season
		Episode int    `json:"episode"` // optional: pin a single file to one episode
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.NzoID == "" || (req.MovieID == 0) == (req.ShowID == 0) {
		writeErr(w, http.StatusBadRequest, errors.New("need nzoId and exactly one of movieId / showId"))
		return
	}
	if (req.Season != 0 || req.Episode != 0) && req.ShowID == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("season/episode pins only apply to a show"))
		return
	}
	if req.Season < 0 || req.Episode < 0 || (req.Episode > 0 && req.Season == 0) {
		writeErr(w, http.StatusBadRequest, errors.New("an episode pin needs its season"))
		return
	}
	if err := s.Grab.ImportJobAs(r.Context(), req.NzoID, req.MovieID, req.ShowID, req.Season, req.Episode); err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "imported"})
}
