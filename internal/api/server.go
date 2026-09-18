// Package api is the HTTP surface: JSON endpoints under /api/v1 plus the
// embedded SPA. Every route declares who may reach it where it is
// registered, and the router refuses everything else — see router.go.
package api

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/getreely/reely/internal/auth"
	"github.com/getreely/reely/internal/backup"
	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/grab"
	"github.com/getreely/reely/internal/importer"
	"github.com/getreely/reely/internal/lists"
	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/prowlarr"
	"github.com/getreely/reely/internal/qbittorrent"
	"github.com/getreely/reely/internal/sabnzbd"
	"github.com/getreely/reely/internal/scene"
	"github.com/getreely/reely/internal/settings"
	"github.com/getreely/reely/internal/trakt"
	"github.com/getreely/reely/internal/trash"
)

type Server struct {
	db      *sql.DB
	version string
	spa     fs.FS
	Catalog *catalog.Store
	TMDB    *metadata.TMDB
	TVDB    *metadata.TVDB // optional — shows route through it when configured
	// XEM supplies scene numbering for TVDB-sourced shows: the season and
	// episode a release carries where the scene disagrees with TheTVDB.
	// Keyless and optional; nil simply means shows keep their own numbers.
	XEM      *scene.XEM
	Importer *importer.Importer
	Settings *settings.Store
	Auth     *auth.Store
	Grab     *grab.Service
	// the concrete clients, for the health panel — the grab service only
	// sees them through its narrow interfaces
	Prowlarr *prowlarr.Client
	Sab      *sabnzbd.Client
	Qbit     *qbittorrent.Client
	Trakt    *trakt.Client
	Lists    *lists.Syncer
	Backup   *backup.Service
	Trash    *trash.Service
	// the public sign-in routes are rate limited per caller; they are
	// built once with the server so the buckets outlive a request
	plexPinLimit   *limiter
	plexCheckLimit *limiter
	plexHookLimit  *limiter
	// A sharing pass walks the whole library, so bursts of callers are
	// coalesced rather than each getting their own. recQueued guards the
	// waiting one; recRunning serialises the passes themselves.
	recMu      sync.Mutex
	recQueued  bool
	recRunning sync.Mutex
	// recGather is how long a request for a pass waits for company. Per
	// server rather than a package variable so a test that shortens it
	// cannot reach into another test's still-waiting pass.
	recGather time.Duration
	// plexBase points the plex.tv client somewhere else. Empty in every
	// real install — there is one plex.tv — and set by tests, which have
	// to stand in for it because a sign-in cannot be exercised against
	// the real thing.
	plexBase string
	// MediaRoot fences every path input (library roots, server imports,
	// the browse endpoint). Empty disables the fence (tests).
	MediaRoot string
	// Restart asks the process to exit so a supervisor brings it back —
	// how a staged restore gets applied. Nil (tests) means no restart.
	Restart func()

	trending trendingCache // the home view's hour-cached TMDB rows
}

type Deps struct {
	DB        *sql.DB
	Version   string
	Dist      fs.FS
	Catalog   *catalog.Store
	TMDB      *metadata.TMDB
	TVDB      *metadata.TVDB
	XEM       *scene.XEM
	Importer  *importer.Importer
	Settings  *settings.Store
	Auth      *auth.Store
	Grab      *grab.Service
	Prowlarr  *prowlarr.Client
	Sab       *sabnzbd.Client
	Qbit      *qbittorrent.Client
	Trakt     *trakt.Client
	Lists     *lists.Syncer
	Backup    *backup.Service
	Trash     *trash.Service
	MediaRoot string
	Restart   func()
}

func New(d Deps) (*Server, error) {
	sub, err := fs.Sub(d.Dist, "dist")
	if err != nil {
		return nil, fmt.Errorf("frontend dist: %w", err)
	}
	return &Server{
		db: d.DB, version: d.Version, spa: sub,
		Catalog: d.Catalog, TMDB: d.TMDB, TVDB: d.TVDB, XEM: d.XEM,
		Importer: d.Importer, Settings: d.Settings, Auth: d.Auth,
		Grab: d.Grab, Prowlarr: d.Prowlarr, Sab: d.Sab, Qbit: d.Qbit, Trakt: d.Trakt, Lists: d.Lists,
		Backup: d.Backup, Trash: d.Trash, MediaRoot: d.MediaRoot, Restart: d.Restart,
		// one PIN starts a sign-in; the check is polled every couple of
		// seconds while somebody is over at plex.tv approving it
		plexPinLimit:   newLimiter(10),
		plexCheckLimit: newLimiter(90),
		// a season pack landing is one webhook per episode, so the ceiling
		// is well above anything Plex sends and still bounds a stranger
		plexHookLimit: newLimiter(120),
		recGather:     reconcileGather,
	}, nil
}

// Handler builds the API. Every route names who may reach it — see
// router.go for why that is declared here rather than inside the
// handlers.
func (s *Server) Handler() http.Handler { return s.routes().mux }

// ExternalHandler serves the internet-facing surface: the same app with
// every admin route absent rather than refused.
//
// The set is DERIVED from the levels the routes already declare, not
// listed here. That is the whole point — an allowlist kept alongside the
// route table is a list somebody has to remember to update, and the one
// time they don't is the leak. Register a route as admin and it is
// unreachable from outside by construction.
//
// This is defence in depth, not the defence. The guard still refuses an
// admin route to a non-admin on the internal listener; this removes the
// surface so a mistake in that guard is not internet-facing.
//
// Requests arriving here are marked, so the app can tell somebody they
// are on the outside rather than rendering an admin UI whose every call
// answers 404.
func (s *Server) ExternalHandler() http.Handler {
	r := newRouter(s)
	r.portalOnly = true
	s.register(r)
	mux := r.mux
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		mux.ServeHTTP(w, req.WithContext(markExternal(req.Context())))
	})
}

// routes builds the router. Handler serves from it; tests read the level
// it recorded for each pattern, so the auth surface is checked against
// what was actually registered rather than a re-parse of this file.
func (s *Server) routes() *router {
	r := newRouter(s)
	s.register(r)
	return r
}

// register declares every route and its level. Both the internal and the
// external handler build from this one function, so the two surfaces
// cannot describe different route tables.
func (s *Server) register(r *router) {
	r.public("GET /api/v1/system/status", s.handleStatus)
	r.public("POST /api/v1/auth/login", s.handleLogin)
	r.portal("POST /api/v1/auth/logout", s.handleLogout)
	r.public("GET /api/v1/auth/me", s.handleMe)
	// Signing in is necessarily public: the caller has no session yet,
	// which is what these two are for. The gate is inside — plex.tv says
	// who they are, and the owner's sharing list says whether that
	// account reaches this server.
	// A PIN request costs a round trip to plex.tv, and polling is meant
	// to be frequent — the ceilings clear an honest sign-in and bound a
	// script. Password login is limited separately, on the username.
	r.public("POST /api/v1/auth/plex/pin", limit(s.plexPinLimit, s.handlePlexPIN))
	r.public("POST /api/v1/auth/plex/check", limit(s.plexCheckLimit, s.handlePlexCheck))
	// Plex cannot sign in, so the token in the URL is the guard. Public
	// rather than portal: this is the media server talking to reely over
	// the LAN, not anything the requesting portal serves.
	r.lan("POST /api/v1/plex/webhook", limit(s.plexHookLimit, s.handlePlexHook))
	r.admin("GET /api/v1/plex/webhook/token", s.handleGetPlexHook)
	r.admin("POST /api/v1/plex/webhook/token", s.handleNewPlexHookToken)
	r.admin("DELETE /api/v1/plex/webhook/token", s.handleDeletePlexHookToken)
	r.admin("GET /api/v1/plex/status", s.handlePlexStatus)
	r.admin("GET /api/v1/plex/test", s.handlePlexTest)
	r.admin("POST /api/v1/plex/sync", s.handlePlexSync)
	r.admin("DELETE /api/v1/plex/link", s.handlePlexUnlink)
	r.admin("GET /api/v1/users", s.handleUsers)
	r.admin("POST /api/v1/users", s.handleCreateUser)
	r.admin("PUT /api/v1/users/{id}/libraries", s.handleSetUserLibraries)
	r.admin("DELETE /api/v1/users/{id}", s.handleDeleteUser)
	r.portal("GET /api/v1/libraries", s.handleLibraries)
	r.admin("POST /api/v1/libraries", s.handleCreateLibrary)
	r.admin("DELETE /api/v1/libraries/{id}", s.handleDeleteLibrary)
	r.admin("POST /api/v1/libraries/{id}/scan", s.handleScanLibrary)
	r.admin("PUT /api/v1/libraries/{id}/profile", s.handleSetLibraryProfile)
	r.user("GET /api/v1/profiles", s.handleProfiles)
	r.admin("POST /api/v1/profiles", s.handleCreateProfile)
	r.admin("PUT /api/v1/profiles/{id}", s.handleUpdateProfile)
	r.admin("DELETE /api/v1/profiles/{id}", s.handleDeleteProfile)
	r.portal("GET /api/v1/movies", s.handleMovies)
	r.acts("POST /api/v1/movies", s.handleAddMovie)
	r.user("GET /api/v1/movies/{id}", s.handleMovie)
	r.user("DELETE /api/v1/movies/{id}", s.handleDeleteMovie)
	r.user("DELETE /api/v1/movies/{id}/file", s.handleDeleteMovieFile)
	r.user("POST /api/v1/movies/{id}/search", s.handleSearchMovie)
	r.user("POST /api/v1/movies/{id}/refresh", s.handleRefreshTitle("movie"))
	r.user("PUT /api/v1/movies/{id}/monitor", s.handleMonitorMovie)
	r.user("PUT /api/v1/movies/{id}/profile", s.handleSetMovieProfile)
	r.user("PUT /api/v1/movies/{id}/availability", s.handleSetMovieAvailability)
	r.user("GET /api/v1/movies/{id}/releases", s.handleMovieReleases)
	r.user("POST /api/v1/movies/{id}/grab", s.handleGrabMovie)
	r.portal("GET /api/v1/shows", s.handleShows)
	r.acts("POST /api/v1/shows", s.handleAddShow)
	r.user("GET /api/v1/shows/{id}", s.handleShow)
	r.user("DELETE /api/v1/shows/{id}", s.handleDeleteShow)
	r.user("DELETE /api/v1/shows/{id}/files", s.handleDeleteShowFiles)
	r.user("POST /api/v1/shows/{id}/search", s.handleSearchShow)
	r.user("POST /api/v1/shows/{id}/refresh", s.handleRefreshTitle("show"))
	r.user("POST /api/v1/search/missing", s.handleSearchMissing)
	r.admin("GET /api/v1/libraries/{id}/organize", s.handleOrganize)
	r.admin("POST /api/v1/libraries/{id}/organize", s.handleOrganize)
	r.user("PUT /api/v1/shows/{id}/monitor", s.handleMonitorShow)
	r.user("PUT /api/v1/shows/{id}/seasons/{season}/monitor", s.handleMonitorSeason)
	r.user("PUT /api/v1/shows/{id}/profile", s.handleSetShowProfile)
	r.user("PUT /api/v1/shows/{id}/numbering", s.handleShowNumbering)
	r.user("GET /api/v1/shows/{id}/releases", s.handleShowReleases)
	r.user("POST /api/v1/shows/{id}/grab", s.handleGrabShow)
	r.user("PUT /api/v1/episodes/{id}/monitor", s.handleMonitorEpisode)
	r.portal("GET /api/v1/people/{id}", s.handlePerson)
	r.portal("GET /api/v1/preview/{kind}/{id}", s.handlePreview)
	r.portal("GET /api/v1/explore", s.handleExplore)
	r.portal("GET /api/v1/similar/{kind}/{id}", s.handleSimilar)
	r.admin("GET /api/v1/formats", s.handleListFormats)
	r.admin("POST /api/v1/formats", s.handleImportFormats)
	r.admin("PUT /api/v1/formats/{id}", s.handleSetFormatScore)
	r.admin("DELETE /api/v1/formats/{id}", s.handleDeleteFormat)
	r.admin("GET /api/v1/trash/formats", s.handleTrashFormats)
	r.user("GET /api/v1/episodes/recent", s.handleRecentEpisodes)
	r.user("GET /api/v1/episodes/{id}", s.handleEpisode)
	r.admin("GET /api/v1/health", s.handleHealth)
	r.admin("GET /api/v1/stats", s.handleStats)
	r.portal("GET /api/v1/lists", s.handleLists)
	r.portal("POST /api/v1/lists", s.handleCreateList)
	r.portal("PUT /api/v1/lists/{id}/enabled", s.handleSetListEnabled)
	r.portal("DELETE /api/v1/lists/{id}", s.handleDeleteList)
	r.portalAdmin("PUT /api/v1/lists/{id}/groups", s.handleSetListGroups)
	r.portal("POST /api/v1/lists/{id}/sync", s.handleSyncList)
	r.admin("GET /api/v1/system/browse", s.handleBrowse)
	r.admin("POST /api/v1/movies/{id}/import-path", s.handleImportMoviePath)
	r.admin("POST /api/v1/shows/{id}/import-path", s.handleImportShowPath)
	// The cadence is one install-wide setting rather than anybody's own,
	// so it stays off the portal: a watchlist is a person's, but how
	// often every list on the server syncs is not.
	r.user("GET /api/v1/lists/cadence", s.handleGetListCadence)
	r.user("PUT /api/v1/lists/cadence", s.handlePutListCadence)
	r.portal("GET /api/v1/mdblist/key", s.handleGetMdblistKey)
	r.portal("PUT /api/v1/mdblist/key", s.handlePutMdblistKey)
	r.portal("GET /api/v1/search", s.handleSearch)
	r.portal("GET /api/v1/calendar", s.handleCalendar)
	r.admin("GET /api/v1/activity", s.handleActivity)
	r.admin("POST /api/v1/activity/retry", s.handleActivityRetry)
	r.admin("POST /api/v1/activity/resolve", s.handleActivityResolve)
	r.admin("POST /api/v1/activity/delete", s.handleActivityDelete)
	r.admin("POST /api/v1/activity/cancel", s.handleActivityCancel)
	r.admin("POST /api/v1/activity/priority", s.handleActivityPriority)
	r.admin("POST /api/v1/activity/pause", s.handleActivityPause)
	r.admin("POST /api/v1/activity/resume", s.handleActivityResume)
	r.admin("POST /api/v1/activity/searches/cancel", s.handleSearchesCancel)
	r.admin("GET /api/v1/blocklist", s.handleBlocklist)
	r.admin("POST /api/v1/blocklist/remove", s.handleBlocklistRemove)
	r.admin("POST /api/v1/history/{id}/blocklist", s.handleHistoryBlocklist)
	r.user("POST /api/v1/movies/{id}/upload", s.handleUploadMovie)
	r.user("POST /api/v1/shows/{id}/upload", s.handleUploadShow)
	r.admin("GET /api/v1/backups", s.handleBackups)
	r.admin("POST /api/v1/backups", s.handleCreateBackup)
	r.admin("GET /api/v1/backups/{name}", s.handleDownloadBackup)
	r.admin("DELETE /api/v1/backups/{name}", s.handleDeleteBackup)
	r.admin("POST /api/v1/restore", s.handleRestore)
	r.admin("GET /api/v1/settings/{key}", s.handleGetSetting)
	r.admin("PUT /api/v1/settings/{key}", s.handlePutSetting)
	r.admin("POST /api/v1/settings/naming/preview", s.handleNamingPreview)
	r.admin("GET /api/v1/system/quality-repair", s.handleQualityRepairStatus)
	r.admin("POST /api/v1/system/quality-repair", s.handleQualityRepair)
	r.admin("GET /api/v1/system/tvdb-migration", s.handleTvdbMigrationStatus)
	r.admin("POST /api/v1/system/tvdb-migration", s.handleTvdbMigrate)
	r.admin("POST /api/v1/shows/{id}/migrate-tvdb", s.handleMigrateShow)
	r.acts("POST /api/v1/movies/{id}/rematch", s.handleRematchMovie)
	r.acts("POST /api/v1/shows/{id}/rematch", s.handleRematchShow)
	// what a re-match would rename, so the dialog can offer the choice
	// with the files named instead of guessing on the owner's behalf
	// what is inside the file, as the media server reports it — extra
	// information about a title, so it answers empty rather than failing
	r.user("GET /api/v1/movies/{id}/streams", s.handleTitleStreams("movie"))
	r.user("GET /api/v1/episodes/{id}/streams", s.handleTitleStreams("episode"))
	r.acts("GET /api/v1/movies/{id}/rematch/files", s.handleRematchFiles("movie"))
	r.acts("GET /api/v1/shows/{id}/rematch/files", s.handleRematchFiles("episode"))
	// sharing: who may see which titles in Plex. All the owner's, and
	// none of it on the portal — deciding who sees what is not something
	// a requester does, and the group list names every household here.
	r.portal("GET /api/v1/sharing/me/groups", s.handleMyGroups)
	r.admin("GET /api/v1/sharing/groups", s.handleGroups)
	r.admin("POST /api/v1/sharing/groups", s.handleCreateGroup)
	r.admin("PATCH /api/v1/sharing/groups/{id}", s.handleRenameGroup)
	r.admin("PUT /api/v1/sharing/groups/{id}/everyone", s.handleSetGroupEveryone)
	r.admin("DELETE /api/v1/sharing/groups/{id}", s.handleDeleteGroup)
	r.admin("PUT /api/v1/sharing/groups/{id}/members", s.handleSetMembers)
	r.admin("GET /api/v1/sharing/titles/{kind}/{id}", s.handleTitleGroups)
	r.admin("PUT /api/v1/sharing/titles/{kind}/{id}", s.handleSetTitleGroups)
	r.admin("POST /api/v1/sharing/titles/bulk", s.handleBulkShare)
	r.admin("POST /api/v1/sharing/seed", s.handleSeed)
	r.admin("GET /api/v1/sharing/users", s.handleShareStates)
	r.admin("GET /api/v1/sharing/plex-accounts", s.handlePlexAccounts)
	r.admin("PUT /api/v1/sharing/users/{id}/managed", s.handleSetManaged)
	r.admin("POST /api/v1/sharing/reconcile", s.handleReconcile)

	// requests: asking is a user's, deciding is the owner's
	r.portal("GET /api/v1/requests", s.handleRequests)
	r.portal("POST /api/v1/requests", s.handleCreateRequest)
	r.portal("PUT /api/v1/users/me/default-library", s.handleSetDefaultLibrary)
	// deciding is the owner's, and the owner is often not at home when
	// somebody asks — so these three are served on the portal too, at
	// admin level. Everything else administrative stays off it.
	r.portalAdmin("GET /api/v1/requests/queue", s.handleRequestQueue)
	r.portalAdmin("POST /api/v1/requests/{id}/approve", s.handleDecideRequest("approved"))
	r.portalAdmin("POST /api/v1/requests/{id}/deny", s.handleDecideRequest("denied"))
	r.admin("PUT /api/v1/users/{id}/requests", s.handleSetRequestSettings)
	r.admin("PUT /api/v1/users/{id}/password-sign-in", s.handleSetPasswordSignIn)
	r.admin("PUT /api/v1/users/{id}/role", s.handleSetRole)
	// anything else is the SPA, and anything else under /api/ is a typo
	r.mux.HandleFunc("/", s.spaOrNotFound())
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		log.Printf("api: encode response: %v", err)
	}
}

func writeErr(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

const maxBodySize = 1 << 20 // requests are small JSON; anything bigger is hostile

func decodeJSON(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, maxBodySize))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid request body: %w", err)
	}
	return nil
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	status := "ok"
	code := http.StatusOK
	if err := s.db.Ping(); err != nil {
		status = "degraded"
		code = http.StatusServiceUnavailable
	}
	// open for health probes, but the exact build number is for signed-in
	// eyes — telling strangers the version tells them which bugs to try
	if s.Auth.Enabled() && s.sessionUser(r) == nil {
		writeJSON(w, code, map[string]string{"status": status})
		return
	}
	writeJSON(w, code, map[string]string{"app": "reely", "version": s.version, "status": status})
}

// spaHandler serves the built frontend, falling back to index.html for
// client routes. index.html revalidates on every load so a container update
// is picked up immediately; hashed bundles under assets/ cache hard.
func (s *Server) spaHandler() http.Handler {
	fileServer := http.FileServerFS(s.spa)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p != "" {
			if f, err := s.spa.Open(p); err == nil {
				f.Close()
				if strings.HasPrefix(p, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				} else {
					w.Header().Set("Cache-Control", "no-cache")
				}
				fileServer.ServeHTTP(w, r)
				return
			}
		}
		r.URL.Path = "/"
		w.Header().Set("Cache-Control", "no-cache")
		fileServer.ServeHTTP(w, r)
	})
}
