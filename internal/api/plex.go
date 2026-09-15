package api

import (
	"database/sql"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/getreely/reely/internal/auth"
	"github.com/getreely/reely/internal/plex"
)

// Signing in with Plex, and the owner linking the account that makes it
// possible.
//
// Two flows share one mechanism. The owner links their own account so
// reely can read the sharing list; everyone else signs in and is checked
// against it. Both are Plex's PIN flow, and neither ever sees a
// password.
//
// The gate is checked at every sign-in rather than trusted from the last
// sync. A sync is a convenience that keeps accounts and library grants
// current; it is not what decides whether somebody gets in today.

// handlePlexPIN starts a sign-in and hands back somewhere to send the
// person. Public: signing in is what it is for.
func (s *Server) handlePlexPIN(w http.ResponseWriter, r *http.Request) {
	client, cfg, err := s.plexClient()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// An install nobody has linked cannot check anyone against anything,
	// so it does not offer the flow at all. Without this, the first
	// stranger to find the endpoint would get a PIN and then a refusal
	// they could not act on.
	//
	// Except to an admin, who is the one person with a reason to start a
	// sign-in on an unlinked install: linking is what sets the token this
	// tests for. Without the exception the refusal is a deadlock — the
	// owner cannot link because nothing is linked.
	if cfg.Token == "" && s.Auth.Enabled() && !s.access(r).admin() {
		writeErr(w, http.StatusPreconditionFailed,
			errors.New("signing in with Plex isn't set up on this server"))
		return
	}
	pin, err := client.NewPIN(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, pin)
}

// handlePlexCheck polls a PIN and, once claimed, signs the person in.
//
// The whole authorization decision happens here: plex.tv says who they
// are, the sharing list says whether that account reaches this server,
// and only then does a session exist.
func (s *Server) handlePlexCheck(w http.ResponseWriter, r *http.Request) {
	var req struct {
		PinID int64 `json:"pinId"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.PinID == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("which sign-in?"))
		return
	}
	client, cfg, err := s.plexClient()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	token, err := client.Token(r.Context(), req.PinID)
	switch {
	case errors.Is(err, plex.ErrPending):
		// not an error: they are still on plex.tv, and the UI keeps waiting
		writeJSON(w, http.StatusOK, map[string]any{"status": "pending"})
		return
	case errors.Is(err, plex.ErrExpired):
		writeErr(w, http.StatusGone, err)
		return
	case err != nil:
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	account, err := client.Account(r.Context(), token)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}

	// The owner linking their own account: this is the one sign-in that
	// cannot be checked against a sharing list, because reading the list
	// is what it enables. It is admitted only while an admin is already
	// signed in and asking for it.
	if s.access(r).admin() && r.URL.Query().Get("link") == "1" {
		s.linkPlexOwner(w, r, client, token, account)
		return
	}

	if cfg.Token == "" {
		writeErr(w, http.StatusPreconditionFailed,
			errors.New("signing in with Plex isn't set up on this server"))
		return
	}

	// The owner signs in as themselves. They are never in their own
	// sharing list — that list is people you shared WITH — so the gate
	// below can never admit them, and without this the person who set
	// the whole thing up is the one account that cannot use it.
	//
	// The match is on the id recorded when they linked, and nothing
	// else: not "is an admin", not the email, not the username. Only the
	// account that actually performed the link opens the door.
	if owner := s.Settings.Get("plex_owner_account_id"); owner != "" && owner == itoa64(account.ID) {
		if u := s.Auth.UserByPlexID(account.ID); u != nil {
			token, user, err := s.Auth.StartPlexSession(u.ID)
			if err != nil {
				writeErr(w, http.StatusForbidden, err)
				return
			}
			s.setSessionCookie(w, r, token)
			writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "user": user})
			return
		}
	}

	shares, err := client.SharedServers(r.Context(), cfg.Token, cfg.MachineID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	var share *plex.Share
	for i := range shares {
		if shares[i].AccountID == account.ID && shares[i].Accepted {
			share = &shares[i]
			break
		}
	}
	if share == nil {
		// One answer for "never shared with", "invited but never
		// accepted" and "unshared yesterday". Which of the three it is
		// tells a stranger about the server's sharing arrangements, and
		// there is nothing any of them can do differently.
		writeErr(w, http.StatusForbidden,
			errors.New("this server isn't shared with your Plex account"))
		return
	}

	userID, err := s.Auth.UpsertPlexUser(auth.PlexIdentity{
		AccountID: account.ID, Username: account.Username, Email: account.Email,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	s.ensureSharing(userID, account.Username)
	// grants are refreshed on the way in, so somebody who gained a
	// library this morning has it now rather than after the next sync
	if libs, err := s.plexGrants(r, client, cfg, *share); err == nil {
		if err := s.Auth.SetUserLibraries(userID, libs); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	sessionToken, user, err := s.Auth.StartPlexSession(userID)
	if err != nil {
		writeErr(w, http.StatusForbidden, err)
		return
	}
	s.setSessionCookie(w, r, sessionToken)
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "user": user})
}

// plexGrants resolves one share's sections to reely library ids.
func (s *Server) plexGrants(r *http.Request, client *plex.Client, cfg plexSettings, share plex.Share) ([]int64, error) {
	byKey, err := s.plexLibraryMap(r.Context(), client, cfg)
	if err != nil {
		return nil, err
	}
	var libs []int64
	seen := map[int64]bool{}
	for _, sec := range share.SharedSections() {
		if id, ok := byKey[sec.Key]; ok && !seen[id] {
			seen[id] = true
			libs = append(libs, id)
		}
	}
	return libs, nil
}

// linkPlexOwner stores the owner's token and works out which server it
// belongs to, so nobody has to find a machine id in a URL.
func (s *Server) linkPlexOwner(w http.ResponseWriter, r *http.Request, client *plex.Client, token string, account *plex.Account) {
	servers, err := client.Servers(r.Context(), token)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	var owned []plex.Server
	for _, srv := range servers {
		if srv.Owned {
			owned = append(owned, srv)
		}
	}
	if len(owned) == 0 {
		writeErr(w, http.StatusBadRequest,
			errors.New("that Plex account doesn't own a server"))
		return
	}
	if err := s.Settings.Set("plex_owner_token", token); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// Remember whose account this is, and tie it to the reely account
	// doing the linking, so the owner can sign in with Plex like anybody
	// else. Their password still works: taking it away is a separate
	// choice, made once they have seen the Plex path work.
	if err := s.Settings.Set("plex_owner_account_id", itoa64(account.ID)); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	if admin := s.sessionUser(r); admin != nil {
		if err := s.Auth.LinkPlexAccount(admin.ID, auth.PlexIdentity{
			AccountID: account.ID, Username: account.Username, Email: account.Email,
		}); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		// Linking takes the password away, because somebody linking their
		// own account is saying they want to sign in with Plex — and a
		// password left behind is a second door nobody is watching.
		//
		// It is safe at exactly this moment and no other: the Plex sign-in
		// that got here just succeeded for this account, and the id it
		// matches on has been stored. Unlinking puts the password back,
		// and REELY_RESTORE_PASSWORD_LOGIN is the way back when Plex
		// itself is the thing that broke.
		if err := s.Auth.SetPasswordSignIn(admin.ID, false); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	// one owned server needs no question; several do, and the admin picks
	// from the list this returns
	if len(owned) == 1 {
		if err := s.Settings.Set("plex_machine_id", owned[0].MachineID); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
		// Remembered from the answer plex.tv already gave, which is a
		// source I have seen. The health check also asks the media server
		// what it calls itself, which needs no re-link but rests on an
		// endpoint I have not — so this is the one that is certain and
		// that is the fallback.
		if err := s.Settings.Set("plex_server_name", owned[0].Name); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "linked", "account": account.Username, "servers": owned,
	})
}

// handlePlexStatus is what the admin card reads: whether an account is
// linked, and what it points at. The token itself is never echoed.
func (s *Server) handlePlexStatus(w http.ResponseWriter, r *http.Request) {
	cfg := s.plexSettings()
	// Two separate facts. The install can be linked — a token that reads
	// the sharing list — while the admin's OWN account is not tied to a
	// Plex identity, which is the state every install that linked before
	// this existed is in. The card has to be able to say so, or there is
	// nothing to press.
	accountLinked := false
	if u := s.sessionUser(r); u != nil {
		accountLinked = u.PlexAccountID != 0
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"linked":        cfg.Token != "",
		"accountLinked": accountLinked,
		"machineId":     cfg.MachineID,
		"serverUrl":     cfg.ServerURL,
	})
}

// handlePlexSync reconciles accounts and library grants with Plex now,
// rather than waiting for somebody to sign in.
func (s *Server) handlePlexSync(w http.ResponseWriter, r *http.Request) {
	out, err := s.syncPlexUsers(r.Context())
	if err != nil {
		writeErr(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}

// handlePlexUnlink forgets the Plex link. Accounts are deactivated
// rather than deleted — their request history is still somebody's — and
// their sessions go immediately.
func (s *Server) handlePlexUnlink(w http.ResponseWriter, r *http.Request) {
	for _, k := range []string{"plex_owner_token", "plex_machine_id",
		"plex_owner_account_id", "plex_server_name"} {
		if err := s.Settings.Set(k, ""); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	if _, err := s.Auth.DeactivatePlexUsersExcept(nil); err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	// The owner's own account goes back to a password. Leaving it
	// Plex-only after the link it depended on is gone would lock them out
	// of their own install with the button they just pressed.
	if admin := s.sessionUser(r); admin != nil {
		if err := s.Auth.UnlinkPlexAccount(admin.ID); err != nil {
			writeErr(w, http.StatusInternalServerError, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "unlinked"})
}

// setSessionCookie is the one place a session cookie is written, so the
// Plex path and the password path cannot drift apart on flags that
// matter.
func (s *Server) setSessionCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure follows how the request arrived
		Name: sessionCookie, Value: token, Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: isHTTPS(r),
		MaxAge: int((30 * 24 * time.Hour).Seconds()),
	})
}

// itoa64 renders an id for the settings store, which holds strings.
func itoa64(v int64) string { return strconv.FormatInt(v, 10) }

// handleSetPasswordSignIn turns the password path on or off for one
// account. Off is refused unless a Plex account is linked, so nobody can
// arrive at an account nothing opens.
func (s *Server) handleSetPasswordSignIn(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Enabled *bool `json:"enabled"`
	}
	if err := decodeJSON(r, &req); err != nil || req.Enabled == nil {
		writeErr(w, http.StatusBadRequest, errors.New(`body needs {"enabled": true|false}`))
		return
	}
	err := s.Auth.SetPasswordSignIn(id, *req.Enabled)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		writeErr(w, http.StatusNotFound, errors.New("no such user"))
		return
	case err != nil:
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"passwordSignIn": *req.Enabled})
}

// handleSetRole promotes an account to admin or puts it back. It works
// on any account, Plex-linked or not: how somebody signs in and what
// they may do are separate questions.
func (s *Server) handleSetRole(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	var req struct {
		Role string `json:"role"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Auth.SetRole(id, req.Role); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"role": req.Role})
}
