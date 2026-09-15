// Package settings is the single-row-per-key app configuration store.
package settings

import (
	"database/sql"
	"encoding/base64"
	"github.com/getreely/reely/internal/naming"
	"log"
	"strings"

	"github.com/getreely/reely/internal/secrets"
)

// SecretKeys are settings whose values are credentials. With a keeper wired
// (production), they're sealed AES-256-GCM before touching the database — the
// key lives outside reely.db, so a downloaded backup can't yield them. The
// API additionally never echoes them (see the handlers' masking).
var SecretKeys = map[string]bool{
	"tmdb_api_key": true, "tvdb_api_key": true, "prowlarr_api_key": true, "sab_api_key": true,
	"trakt_client_id": true, "trakt_access_token": true, "trakt_refresh_token": true,
	"mdblist_api_key": true,
	// qBittorrent has no API key: the WebUI password IS the credential,
	// so it is sealed like one
	"qbit_password": true,
	// the owner's Plex token reads their whole account, so it is sealed
	// and never echoed — the admin card only learns whether one is set
	"plex_owner_token": true,
}

// secretPrefixes cover dynamic credential keys — each user's personal
// mdblist key lives under "mdblist_key.<user id>".
var secretPrefixes = []string{"mdblist_key."}

// IsSecret reports whether a key holds a credential (sealed at rest,
// never echoed by the API).
func IsSecret(key string) bool {
	if SecretKeys[key] {
		return true
	}
	for _, p := range secretPrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// encPrefix marks a sealed value; anything without it is legacy plaintext,
// re-sealed by the startup sweep.
const encPrefix = "enc1:"

// Defaults applied when a key has never been written.
var defaults = map[string]string{
	"tmdb_api_key": "",
	// optional: with a TVDB key, shows come from TheTVDB (whose numbering
	// release groups follow); without one they stay on TMDB
	"tvdb_api_key": "",
	// One template per kind — slashes in the template make the folders.
	// Defined in the naming package, which renders them.
	"naming_movie": naming.DefaultMovieTemplate,
	"naming_show":  naming.DefaultShowTemplate,
	// The RSS sync matches releases against anything monitored and missing,
	// with no date gate — early releases get scooped the moment they appear.
	"rss_poll_seconds": "900",
	// The wanted sweep re-searches the whole monitored-and-missing backlog
	// on a schedule. 0 = off (the default): it's real indexer traffic, so
	// it's opt-in. Floored at 6 hours when enabled.
	"wanted_search_hours": "0",
	// Watched lists sync twice a day by default; Sync Now covers the
	// impatient case.
	"lists_sync_hours": "12",
	// SAB categories reely files its downloads under — and the ones the
	// import sweep watches. Change them to match your SAB setup.
	"sab_category_movies": "movies",
	"sab_category_tv":     "tvshows",
	"server_url":          "",
	"backup_enabled":      "true",
	"backup_keep":         "4",
	"prowlarr_enabled":    "true",
	// Leave Japanese animation out of the home view's discovery rows.
	// Off by default; searching for a title by name is unaffected, since
	// that is always a deliberate ask.
	"hide_anime": "false",
	// Keep the discovery rows to English-language titles. Off by default.
	// Broader than hide_anime and subsumes it — anime is Japanese, so it
	// goes either way — but the two are independent: wanting anime gone
	// while keeping Parasite is a real preference.
	"discover_english_only": "false",
	// Which country's streaming catalogues the Explore rows describe —
	// "on Netflix" only means something with a region attached.
	"watch_region": "US",
}

type Store struct {
	db *sql.DB
	// keeper seals SecretKeys values at rest; nil (tests, tools) stores
	// them as-is.
	keeper *secrets.Keeper
}

func New(db *sql.DB) *Store { return &Store{db: db} }

// UseKeeper turns on at-rest encryption for secret settings and re-seals any
// legacy plaintext values already in the table (idempotent — sealed values
// carry a marker prefix).
func (s *Store) UseKeeper(k *secrets.Keeper) {
	s.keeper = k
	for key := range SecretKeys {
		var v string
		if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v); err != nil {
			continue
		}
		if v == "" || strings.HasPrefix(v, encPrefix) {
			continue
		}
		if err := s.Set(key, v); err != nil {
			log.Printf("settings: sealing %s: %v", key, err)
		}
	}
}

// Default is the value a key falls back to when it has never been
// written. Exported so the UI can offer "reset to default" without
// keeping its own copy of the string to drift out of sync.
func Default(key string) string { return defaults[key] }

func (s *Store) Get(key string) string {
	var v string
	err := s.db.QueryRow(`SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if err != nil {
		return defaults[key]
	}
	if strings.HasPrefix(v, encPrefix) {
		if s.keeper == nil {
			log.Printf("settings: %s is sealed but no secret key is loaded", key)
			return ""
		}
		ct, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(v, encPrefix))
		if err != nil {
			log.Printf("settings: %s: corrupt sealed value", key)
			return ""
		}
		plain, err := s.keeper.Open(ct)
		if err != nil {
			log.Printf("settings: %s: %v", key, err)
			return ""
		}
		return plain
	}
	return v
}

func (s *Store) Set(key, value string) error {
	if IsSecret(key) && s.keeper != nil && value != "" {
		ct, err := s.keeper.Seal(value)
		if err != nil {
			return err
		}
		value = encPrefix + base64.StdEncoding.EncodeToString(ct)
	}
	_, err := s.db.Exec(`INSERT INTO settings (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
