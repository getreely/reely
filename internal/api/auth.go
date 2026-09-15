package api

import (
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"

	"github.com/getreely/reely/internal/auth"
)

const sessionCookie = "reely_session"

// Authorization sits on two independent axes: role decides who configures
// the install (admins own settings, libraries, accounts); library scope
// decides where a plain user may work. Before the first account exists the
// whole API is open so the setup wizard can run.

func (s *Server) sessionUser(r *http.Request) *auth.User {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return nil
	}
	return s.Auth.SessionUser(c.Value)
}

type access struct {
	// open is the pre-auth state: no accounts exist yet.
	open bool
	user *auth.User
}

func (s *Server) access(r *http.Request) access {
	if !s.Auth.Enabled() {
		return access{open: true}
	}
	return access{user: s.sessionUser(r)}
}

func (a access) admin() bool { return a.open || a.user.IsAdmin() }

func (a access) mayLibrary(libraryID int64) bool { return a.open || a.user.MayAccess(libraryID) }

func (s *Server) requireAdmin(w http.ResponseWriter, r *http.Request) bool {
	if s.access(r).admin() {
		return true
	}
	writeErr(w, http.StatusForbidden, errors.New("admin access required"))
	return false
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	ip, _, _ := net.SplitHostPort(r.RemoteAddr)
	token, user, err := s.Auth.Login(req.Username, req.Password, ip)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, err)
		return
	}
	// Browsers refuse Secure cookies over plain HTTP, which would break the
	// documented HTTP-on-LAN deployment — Secure follows how the request
	// actually arrived; HttpOnly + SameSite=Strict hold either way. The
	// Plex path writes the same cookie through the same helper, so the two
	// cannot drift apart on flags that matter.
	s.setSessionCookie(w, r, token)
	writeJSON(w, http.StatusOK, user)
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		s.Auth.Logout(c.Value)
	}
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: expiring the cookie; Secure matches how it was set
		Name: sessionCookie, Value: "", Path: "/",
		HttpOnly: true, SameSite: http.SameSiteStrictMode, Secure: isHTTPS(r),
		MaxAge: -1,
	})
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// handleMe reports auth state: whether login is required at all, and who (if
// anyone) this session belongs to. The SPA decides between login and app.
func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{"authRequired": s.Auth.Enabled()}
	if u := s.sessionUser(r); u != nil {
		resp["user"] = u
	}
	// on the internet-facing listener the admin routes do not exist, so
	// the app hides the controls that would call them rather than
	// rendering tabs whose every request answers 404
	if isExternal(r.Context()) {
		resp["external"] = true
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleUsers(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	users, err := s.Auth.ListUsers()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"users": users})
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	// the very first account may be created unauthenticated (that IS the
	// setup); after that only admins add accounts
	if s.Auth.Enabled() && !s.requireAdmin(w, r) {
		return
	}
	var req struct {
		Username   string  `json:"username"`
		Password   string  `json:"password"`
		Role       string  `json:"role"`
		LibraryIDs []int64 `json:"libraryIds"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if req.Role == "" {
		req.Role = "user"
	}
	// the very first account must be an admin, or nobody can manage anything
	if !s.Auth.Enabled() {
		req.Role = "admin"
	}
	if req.Role == "user" && len(req.LibraryIDs) == 0 {
		writeErr(w, http.StatusBadRequest, errors.New("pick at least one library this user may access"))
		return
	}
	id, err := s.Auth.CreateUser(req.Username, req.Password, req.Role, req.LibraryIDs)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	s.ensureSharing(id, req.Username)
	writeJSON(w, http.StatusCreated, s.Auth.UserByID(id))
}

func (s *Server) handleSetUserLibraries(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("bad user id"))
		return
	}
	var req struct {
		LibraryIDs []int64 `json:"libraryIds"`
	}
	if err := decodeJSON(r, &req); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if err := s.Auth.SetUserLibraries(id, req.LibraryIDs); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	if !s.requireAdmin(w, r) {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, errors.New("bad user id"))
		return
	}
	if err := s.Auth.DeleteUser(id); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
