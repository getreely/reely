package api

import (
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/lists"
)

// Watched lists are self-service: anyone may manage lists that feed a
// library they have access to — a user with their own collection runs
// their own lists, no admin needed. Only the app-level Trakt credentials
// (client id/secret, in Settings) stay admin territory.

// validListSources names the sources the sync engine knows.
var validListSources = map[string]bool{
	"tmdb_chart": true, "tmdb_list": true, "trakt_chart": true, "trakt_list": true,
	"mdblist": true,
}

func (s *Server) handleLists(w http.ResponseWriter, r *http.Request) {
	acc := s.access(r)
	all, err := s.Catalog.ListLists()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// A list is its creator's. Two people sharing a library is the
	// ordinary shape of a household and not a reason either should be
	// handed the other's lists — which scoping on library access alone
	// did, and not only to look at: the same check guarded disabling,
	// deleting and syncing.
	//
	// The owner sees every list on the install, whoever made it. That is
	// what makes setting a list's audience their job, and it is the same
	// reach they have over every other row here.
	mine := s.sessionUserID(r)
	visible := make([]catalog.List, 0, len(all))
	for _, l := range all {
		if !acc.mayLibrary(l.LibraryID) {
			continue
		}
		if !acc.admin() && l.CreatedBy != mine {
			continue
		}
		visible = append(visible, l)
	}
	// The groups a list's audience can be chosen from ride along here
	// rather than the portal opening the install-wide sharing endpoint,
	// which stays LAN-only with the rest of its siblings — creating,
	// renaming and deleting a group are not portal work. Owner only: it
	// names every household on the install.
	shareGroups := []catalog.Group{}
	if acc.admin() {
		all, err := s.Catalog.Groups()
		if err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		for _, g := range all {
			if !g.Personal {
				shareGroups = append(shareGroups, g)
			}
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"lists":           visible,
		"shareGroups":     shareGroups,
		"traktConfigured": s.Trakt.Configured(),
		// configured FOR THIS USER: their personal key or the install-wide one
		"mdblistConfigured": s.Lists.MdblistKeyFor(s.sessionUserID(r)) != "",
	})
}

func (s *Server) handleCreateList(w http.ResponseWriter, r *http.Request) {
	acc := s.access(r)
	var req struct {
		Name      string          `json:"name"`
		Source    string          `json:"source"`
		Config    json.RawMessage `json:"config"`
		LibraryID int64           `json:"libraryId"`
		ItemLimit int             `json:"itemLimit"`
		// who gets what this list adds, from now on. The owner's call,
		// so it is only honoured from an admin — a requester's list still
		// shares with their own households, as approving their asks does.
		GroupIDs []int64 `json:"groupIds"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Name == "" || !validListSources[req.Source] {
		writeErr(w, http.StatusBadRequest, errors.New("need a name and a known source"))
		return
	}
	if _, err := s.Catalog.GetLibrary(req.LibraryID); err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("no such library"))
		return
	}
	if !acc.mayLibrary(req.LibraryID) {
		writeErr(w, http.StatusForbidden, errors.New("no access to this library"))
		return
	}
	cfg := string(req.Config)
	if cfg == "" {
		cfg = "{}"
	}
	list := &catalog.List{
		Name: req.Name, Source: req.Source, Config: cfg,
		LibraryID: req.LibraryID, ItemLimit: req.ItemLimit, Enabled: true,
		// the creator's identity picks which mdblist key syncs this list
		CreatedBy: s.sessionUserID(r),
	}
	id, err := s.Catalog.CreateList(list)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	list.ID = id
	if len(req.GroupIDs) > 0 && acc.admin() {
		if err := s.Catalog.SetListGroups(id, req.GroupIDs); err != nil {
			writeErr(w, http.StatusBadRequest, err)
			return
		}
		list.GroupIDs = req.GroupIDs
	}
	if list.GroupIDs == nil {
		list.GroupIDs = []int64{}
	}
	writeJSON(w, http.StatusCreated, list)
}

// handleSetListGroups changes who gets what a list adds.
//
// Admin-only, like the per-title "Shared with" it mirrors: naming an
// audience is deciding what other households may watch, which is the
// owner's decision wherever it is made.
//
// It applies to what the list adds from here. Nothing records which
// titles a given list added, and inferring it from a chart's current
// contents would overwrite tagging done by hand on titles that merely
// happen to be on it.
func (s *Server) handleSetListGroups(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	if _, err := s.Catalog.GetList(id); err != nil {
		notFoundOr500(w, err)
		return
	}
	var body struct {
		GroupIDs []int64 `json:"groupIds"`
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Catalog.SetListGroups(id, body.GroupIDs); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// scopedList loads a list and answers 404 for one outside the caller's
// libraries — same rule as titles.
func (s *Server) scopedList(w http.ResponseWriter, r *http.Request) (*catalog.List, bool) {
	id, ok := pathID(w, r)
	if !ok {
		return nil, false
	}
	list, err := s.Catalog.GetList(id)
	if errors.Is(err, sql.ErrNoRows) {
		writeErr(w, http.StatusNotFound, errors.New("not found"))
		return nil, false
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return nil, false
	}
	acc := s.access(r)
	if !acc.mayLibrary(list.LibraryID) {
		writeErr(w, http.StatusNotFound, errors.New("not found"))
		return nil, false
	}
	// Somebody else's list is not theirs to disable, delete or sync.
	// Syncing is the one that bites hardest: a list syncs AS ITS CREATOR
	// — with their mdblist key, filing requests in their name against
	// their weekly quota — so a button press on a list that was never
	// theirs spent somebody else's ration of asks.
	//
	// 404 rather than 403, like every other out-of-reach row: a refusal
	// that names what it is refusing tells them the list exists.
	if !acc.admin() && list.CreatedBy != s.sessionUserID(r) {
		writeErr(w, http.StatusNotFound, errors.New("not found"))
		return nil, false
	}
	return list, true
}

func (s *Server) handleSetListEnabled(w http.ResponseWriter, r *http.Request) {
	list, ok := s.scopedList(w, r)
	if !ok {
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Enabled == nil {
		writeErr(w, http.StatusBadRequest, errors.New("body needs {\"enabled\": true|false}"))
		return
	}
	if err := s.Catalog.SetListEnabled(list.ID, *req.Enabled); err != nil {
		notFoundOr500(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "enabled": *req.Enabled})
}

func (s *Server) handleDeleteList(w http.ResponseWriter, r *http.Request) {
	list, ok := s.scopedList(w, r)
	if !ok {
		return
	}
	if err := s.Catalog.RemoveList(list.ID); err != nil {
		notFoundOr500(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "removed"})
}

// handleSyncList runs one list right now — the impatient path; the watcher
// covers the schedule.
func (s *Server) handleSyncList(w http.ResponseWriter, r *http.Request) {
	list, ok := s.scopedList(w, r)
	if !ok {
		return
	}
	added, requested, err := s.Lists.SyncList(r.Context(), list)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	// the two are reported apart because they are different outcomes: a
	// list owned by an account that asks files requests, and telling that
	// person "0 added" when five went to the queue would read as failure
	writeJSON(w, http.StatusOK, map[string]any{"added": added, "requested": requested})
}

// sessionUserID is the signed-in user's id, or 0 in the open (pre-account)
// install.
func (s *Server) sessionUserID(r *http.Request) int64 {
	if u := s.sessionUser(r); u != nil {
		return u.ID
	}
	return 0
}

// Personal mdblist keys: every user may bring their own (free from
// mdblist.com → Preferences → API Access) to pull their own and public
// lists — no admin needed. In the open install the key lands install-wide.

// Per-user list cadence: how often YOUR lists re-sync. Every account owns
// one timer covering all its lists; the open (pre-account) install writes
// the shared default instead.

func (s *Server) handleGetListCadence(w http.ResponseWriter, r *http.Request) {
	minutes := 30
	uid := s.sessionUserID(r)
	v := ""
	if uid > 0 {
		v = s.Settings.Get(lists.UserCadenceKey(uid))
	}
	if v == "" {
		v = s.Settings.Get("lists_sync_minutes")
	}
	if n, err := strconv.Atoi(v); err == nil && n > 0 {
		minutes = n
	}
	writeJSON(w, http.StatusOK, map[string]any{"minutes": minutes})
}

func (s *Server) handlePutListCadence(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Minutes *int `json:"minutes"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Minutes == nil || *req.Minutes < 15 {
		writeErr(w, http.StatusBadRequest, errors.New("body needs {\"minutes\": 15|30|60|...}"))
		return
	}
	key := "lists_sync_minutes" // open install: the shared default
	if uid := s.sessionUserID(r); uid > 0 {
		key = lists.UserCadenceKey(uid)
	}
	if err := s.Settings.Set(key, strconv.Itoa(*req.Minutes)); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "minutes": *req.Minutes})
}

func (s *Server) handleGetMdblistKey(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"set": s.Lists.MdblistKeyFor(s.sessionUserID(r)) != "",
	})
}

func (s *Server) handlePutMdblistKey(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Value string `json:"value"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	key := "mdblist_api_key" // open install: there is only the shared key
	if uid := s.sessionUserID(r); uid > 0 {
		key = lists.MdblistUserKey(uid)
	}
	if err := s.Settings.Set(key, strings.TrimSpace(req.Value)); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
