// Reely — movie and TV automation for your screening room.
package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/getreely/reely/internal/api"
	"github.com/getreely/reely/internal/auth"
	"github.com/getreely/reely/internal/backup"
	"github.com/getreely/reely/internal/catalog"
	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/grab"
	"github.com/getreely/reely/internal/importer"
	"github.com/getreely/reely/internal/lists"
	"github.com/getreely/reely/internal/mdblist"
	"github.com/getreely/reely/internal/metadata"
	"github.com/getreely/reely/internal/prowlarr"
	"github.com/getreely/reely/internal/qbittorrent"
	"github.com/getreely/reely/internal/refresh"
	"github.com/getreely/reely/internal/sabnzbd"
	"github.com/getreely/reely/internal/scene"
	"github.com/getreely/reely/internal/secrets"
	"github.com/getreely/reely/internal/settings"
	"github.com/getreely/reely/internal/trakt"
	"github.com/getreely/reely/internal/trash"
	"github.com/getreely/reely/internal/watcher"
	"github.com/getreely/reely/web"
)

// set via -ldflags "-X main.version=..."
var version = "dev"

func main() {
	configDir := envOr("REELY_CONFIG_DIR", "/config")
	port := envOr("REELY_PORT", "8788")

	if err := os.MkdirAll(configDir, 0o755); err != nil {
		log.Fatalf("create config dir %s: %v", configDir, err)
	}
	database, err := db.Open(filepath.Join(configDir, "reely.db"))
	if err != nil {
		log.Fatalf("open database: %v", err)
	}
	defer database.Close()

	cfg := settings.New(database)
	// credentials (the TMDB key, Prowlarr/SAB keys) are sealed at rest with a
	// key kept OUTSIDE the database, so backups of reely.db can't yield them
	keeper, err := secrets.Load(configDir)
	if err != nil {
		log.Fatalf("load secret key: %v", err)
	}
	cfg.UseKeeper(keeper)

	cat := catalog.New(database)
	tmdb := metadata.NewTMDB(func() string { return cfg.Get("tmdb_api_key") })
	if base := os.Getenv("REELY_TMDB_BASE"); base != "" {
		tmdb.SetBaseURL(base)
	}
	// optional: with a TVDB key in Settings, shows come from TheTVDB
	tvdb := metadata.NewTVDB(func() string { return cfg.Get("tvdb_api_key") })
	if base := os.Getenv("REELY_TVDB_BASE"); base != "" {
		tvdb.SetBaseURL(base)
	}
	// TheXEM: scene numbering for TVDB-sourced shows. No key, no setting —
	// it either has a map for a show or it doesn't, and a service that is
	// down costs nothing but the numbering reely already had.
	xem := scene.NewXEM()
	xem.SetUserAgent("reely/" + version + " (+https://github.com/getreely/reely)")
	if base := os.Getenv("REELY_XEM_BASE"); base != "" {
		xem.SetBaseURL(base)
	}
	indexer := prowlarr.New(
		func() string { return cfg.Get("prowlarr_url") },
		func() string { return cfg.Get("prowlarr_api_key") })
	sab := sabnzbd.New(
		func() string { return cfg.Get("sab_url") },
		func() string { return cfg.Get("sab_api_key") })
	// qBittorrent signs in with the WebUI's own username and password —
	// it has no API key — and both may be blank, which is what an
	// install that bypasses auth on the local subnet looks like
	qbit := qbittorrent.New(
		func() string { return cfg.Get("qbit_url") },
		func() string { return cfg.Get("qbit_username") },
		func() string { return cfg.Get("qbit_password") })
	grabber := &grab.Service{Indexer: indexer, Usenet: sab, Torrent: qbit, Catalog: cat, Settings: cfg}
	traktClient := trakt.New(func() string { return cfg.Get("trakt_client_id") })
	mdb := mdblist.New()
	accounts := auth.New(database)
	// The way back in when Plex-only sign-in is the thing that broke.
	// Set on the container and start it once: every admin gets its
	// password back, and the variable can then be removed. Host access is
	// the right privilege for this — whoever can set it owns the machine
	// anyway — and it is loud in the log so it cannot happen unnoticed.
	if os.Getenv("REELY_RESTORE_PASSWORD_LOGIN") != "" {
		n, err := accounts.RestorePasswordSignIn()
		if err != nil {
			log.Fatalf("reely: restoring password sign-in: %v", err)
		}
		log.Printf("reely: REELY_RESTORE_PASSWORD_LOGIN set — %d admin account(s) can sign in with a password again; unset it once you are back in", n)
	}
	syncer := &lists.Syncer{Catalog: cat, TMDB: tmdb, TVDB: tvdb, Trakt: traktClient,
		Mdblist: mdb, Grab: grabber, Settings: cfg, Accounts: accounts}
	backups := &backup.Service{DB: database, DBPath: filepath.Join(configDir, "reely.db"), Dir: filepath.Join(configDir, "backups")}
	imp := importer.New(cat, tmdb, tvdb)
	server, err := api.New(api.Deps{
		DB: database, Version: version, Dist: web.Dist,
		Catalog:   cat,
		TMDB:      tmdb,
		TVDB:      tvdb,
		XEM:       xem,
		Importer:  imp,
		Settings:  cfg,
		Auth:      accounts,
		Grab:      grabber,
		Prowlarr:  indexer,
		Sab:       sab,
		Qbit:      qbit,
		Trakt:     traktClient,
		Lists:     syncer,
		Backup:    backups,
		Trash:     trash.New(),
		MediaRoot: envOr("REELY_MEDIA_ROOT", "/data"),
		// a staged restore applies on the next start; under Docker's restart
		// policy exiting IS the restart
		Restart: func() {
			go func() {
				time.Sleep(1500 * time.Millisecond)
				log.Printf("reely: restarting to apply staged restore")
				os.Exit(0)
			}()
		},
	})
	if err != nil {
		log.Fatalf("init server: %v", err)
	}

	// The other half of the Plex loop, set here because the importer is
	// built before the server exists. reely places a file, tells Plex to
	// look at that folder, and Plex's webhook says when it has finished —
	// which is when there is finally an item to label.
	grabber.Placed = server.ScanPlexIn

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Plex is brought back into line on its own: a title that finished
	// downloading after the last pass, or a share somebody edited over
	// there. Every edit in the UI already triggers one, so this is the
	// safety net rather than the main path.
	go server.RunSharingLoop(ctx)

	// finished downloads import even when nobody has the UI open
	go watcher.New(grabber, syncer, &refresh.Refresher{Catalog: cat, TMDB: tmdb, TVDB: tvdb, XEM: xem}, backups).Run(ctx)

	httpServer := &http.Server{
		Addr:              ":" + port,
		Handler:           server.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	// The internet-facing listener, when REELY_EXTERNAL_PORT is set. It
	// serves the same app with every admin route absent rather than
	// refused, so forwarding a port or pointing a tunnel at it exposes
	// the requesting side and nothing else.
	//
	// Opt-in, and a separate port rather than a path prefix: what gets
	// published is then a deployment decision made where deployments are
	// made, and an install that sets nothing is unchanged.
	var externalServer *http.Server
	if raw := os.Getenv("REELY_EXTERNAL_PORT"); raw != "" {
		// parsed rather than pasted into an address: this is the port
		// somebody publishes to the internet, and a value that isn't a
		// port should stop the listener rather than open something
		// unexpected
		ext, err := strconv.Atoi(strings.TrimSpace(raw))
		if err != nil || ext < 1 || ext > 65535 {
			log.Fatalf("reely: REELY_EXTERNAL_PORT must be a port number between 1 and 65535")
		}
		externalServer = &http.Server{
			Addr:              fmt.Sprintf(":%d", ext),
			Handler:           server.ExternalHandler(),
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			log.Printf("reely: requests-only listener on :%d", ext)
			if err := externalServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				log.Printf("reely: requests-only listener: %v", err)
			}
		}()
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if externalServer != nil {
			_ = externalServer.Shutdown(shutdownCtx)
		}
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	log.Printf("reely %s listening on :%s (config: %s)", version, port, configDir)
	if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
