package api

import (
	"errors"
	"net/http"
	"strings"
)

// Who may reach a route is declared where the route is registered, and
// there is no way to register one without saying.
//
// It used to be declared inside the handlers: each of forty-six called
// requireAdmin by hand, and the rest were reachable by any signed-in
// account by saying nothing at all. That reads fine until somebody adds
// the forty-seventh and forgets — nothing fails, no test goes red, and
// the route is simply open. On a LAN that is survivable. It is not
// survivable on an install exposed to the internet, and the point of a
// default-deny arrangement is that forgetting has to be impossible
// rather than merely unlikely.
//
// The router carries the coarse level only. WHICH library an account may
// touch stays with the handlers, because that answer needs the resource:
// mayLibrary takes an id the router never sees. So a route says "any
// signed-in account", and the handler behind it still refuses the
// libraries this particular account has no business in.

type level int

const (
	// levelPublic is reachable with no session at all: signing in, the
	// SPA's own auth probe, and the health endpoint a monitor hits.
	levelPublic level = iota
	// levelUser is any signed-in account, subject to library scope. It is
	// the READ tier plus asking: browsing, searching, requesting.
	levelUser
	// levelActor puts a NEW title into the install without asking. That
	// is what may_add has always meant, and it is deliberately narrow:
	// somebody who holds a library still curates it — deleting, changing
	// what is monitored, searching for a better release — because those
	// act on what they were already given rather than adding to it.
	//
	// It is a level rather than a check inside the two add handlers,
	// which is where it used to live, so the answer is declared with the
	// route and pinned by a test instead of remembered.
	levelActor
	// levelAdmin owns the install: accounts, libraries, settings, the
	// system operations, and anything holding a credential.
	levelAdmin
)

func (l level) String() string {
	switch l {
	case levelPublic:
		return "public"
	case levelUser:
		return "user"
	case levelActor:
		return "actor"
	default:
		return "admin"
	}
}

// router registers routes against a mux, wrapping each in the check its
// declared level implies and remembering the level so a test can pin the
// whole table.
type router struct {
	mux    *http.ServeMux
	s      *Server
	levels map[string]level
	// onPortal marks the routes the requesting portal needs. Every public
	// route is in it implicitly — signing in is what the portal is for.
	onPortal map[string]bool
	// offPortal is the other exception: an unauthenticated route that is
	// NOT for the portal. Public routes are on it implicitly — signing in
	// is what the portal is for — and that implicit rule is right for
	// every route it was written for. It is wrong for a machine on the
	// LAN talking to reely, which needs no session and has no business
	// being reachable from the internet.
	offPortal map[string]bool
	// portalOnly builds the internet-facing surface: anything outside the
	// portal is not registered at all, so it is absent rather than
	// refused. See ExternalHandler.
	portalOnly bool
}

func newRouter(s *Server) *router {
	return &router{mux: http.NewServeMux(), s: s,
		levels: map[string]level{}, onPortal: map[string]bool{},
		offPortal: map[string]bool{}}
}

func (r *router) route(l level, pattern string, h http.HandlerFunc) {
	if _, dup := r.levels[pattern]; dup {
		// two registrations of one pattern is a programming error the mux
		// would panic on anyway; saying so here names the pattern
		panic("api: route registered twice: " + pattern)
	}
	r.levels[pattern] = l
	if r.portalOnly && (r.offPortal[pattern] ||
		(l != levelPublic && !r.onPortal[pattern])) {
		return
	}
	r.mux.HandleFunc(pattern, r.s.guard(l, h))
}

func (r *router) public(pattern string, h http.HandlerFunc) { r.route(levelPublic, pattern, h) }

// lan registers an unauthenticated route that the portal does NOT serve.
//
// For a machine rather than a person: it carries no session because the
// thing calling it cannot have one, and it is kept off the
// internet-facing listener because nothing out there should be able to
// reach it. Whatever guards it — a token in the URL, for the Plex
// webhook — is then the only guard on the LAN and no guard at all
// outside, which is the point.
func (r *router) lan(pattern string, h http.HandlerFunc) {
	r.offPortal[pattern] = true
	r.route(levelPublic, pattern, h)
}
func (r *router) user(pattern string, h http.HandlerFunc)  { r.route(levelUser, pattern, h) }
func (r *router) admin(pattern string, h http.HandlerFunc) { r.route(levelAdmin, pattern, h) }

// acts registers a route that changes what the install holds. An account
// that asks rather than adds is refused, and told where to ask instead.
func (r *router) acts(pattern string, h http.HandlerFunc) { r.route(levelActor, pattern, h) }

// portal registers a signed-in route that is ALSO part of the requesting
// portal — the surface the internet-facing listener serves.
//
// It is a second axis rather than a level because it answers a different
// question. A level says who may reach a route; this says whether the
// route belongs to the narrow job of finding something and asking for
// it. Browsing a library and deleting a movie are both "any signed-in
// account", and only one of them belongs on a portal.
//
// Default-deny again: a route registered with user() is simply not on
// the portal. Forgetting to mark one leaves it missing from a surface
// nobody has yet used, which is noticed. Forgetting the other way would
// have published it.
func (r *router) portal(pattern string, h http.HandlerFunc) {
	r.onPortal[pattern] = true
	r.route(levelUser, pattern, h)
}

// portalAdmin serves an ADMIN route on the portal. The two axes were
// always separate — portal() simply fused them, because until now
// everything the portal needed was open to any signed-in account.
//
// Deciding a request is the exception. The owner is the only one who
// can, and the portal is where they are when they are not at home, so
// refusing it there means requests wait for them to walk back to a LAN
// machine. The guard still refuses a non-admin; what this gives up is
// the second layer, that a bug in the guard could not be reached from
// the internet at all. Use it only where the alternative is a feature
// that does not work where people actually are.
func (r *router) portalAdmin(pattern string, h http.HandlerFunc) {
	r.onPortal[pattern] = true
	r.route(levelAdmin, pattern, h)
}

// guard enforces one route's level.
//
// The open install — no accounts yet — passes everything, because the
// setup wizard has to reach the API to create the first account. That is
// the same escape hatch access.open has always been, kept in one place
// now instead of being re-derived per handler.
func (s *Server) guard(l level, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if l == levelPublic {
			h(w, r)
			return
		}
		a := s.access(r)
		switch {
		case a.open:
			h(w, r)
		case a.user == nil:
			writeErr(w, http.StatusUnauthorized, errors.New("login required"))
		case l == levelAdmin && !a.user.IsAdmin():
			writeErr(w, http.StatusForbidden, errors.New("admin access required"))
		case l == levelActor && !a.user.IsAdmin() && !a.user.MayAdd:
			writeErr(w, http.StatusForbidden,
				errors.New("this account asks for titles rather than adding them — use Request"))
		default:
			h(w, r)
		}
	}
}

// spaOrNotFound serves the built frontend, and refuses anything under
// /api/ that no route claimed. Without the second half an API typo
// answers with the index page, which reads as a bewildering success.
func (s *Server) spaOrNotFound() http.HandlerFunc {
	spa := s.spaHandler()
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
			writeErr(w, http.StatusNotFound, errors.New("no such endpoint"))
			return
		}
		spa.ServeHTTP(w, r)
	}
}
