package settings

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/secrets"
)

func testStoreWithKeeper(t *testing.T) *Store {
	t.Helper()
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	s := New(conn)
	k, err := secrets.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.keeper = k
	return s
}

// Secret settings are sealed at rest: the database row never carries the
// plaintext, while Get still returns it.
func TestSecretSettingsSealedAtRest(t *testing.T) {
	s := testStoreWithKeeper(t)
	if err := s.Set("tmdb_api_key", "tmdb-key-plain"); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = 'tmdb_api_key'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "tmdb-key-plain") || !strings.HasPrefix(raw, encPrefix) {
		t.Fatalf("stored value is not sealed: %q", raw)
	}
	if got := s.Get("tmdb_api_key"); got != "tmdb-key-plain" {
		t.Fatalf("Get = %q", got)
	}

	// non-secret keys stay plain
	if err := s.Set("naming_scheme", "{Author}/{Title}"); err != nil {
		t.Fatal(err)
	}
	s.db.QueryRow(`SELECT value FROM settings WHERE key = 'naming_scheme'`).Scan(&raw) //nolint:errcheck
	if raw != "{Author}/{Title}" {
		t.Fatalf("non-secret sealed: %q", raw)
	}
}

// UseKeeper re-seals legacy plaintext rows from installs that predate
// encryption — idempotently.
func TestUseKeeperMigratesLegacyPlaintext(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	s := New(conn)
	// legacy plaintext row, written without a keeper
	if err := s.Set("sab_api_key", "old-plain-key"); err != nil {
		t.Fatal(err)
	}

	k, err := secrets.Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	s.UseKeeper(k)

	var raw string
	if err := s.db.QueryRow(`SELECT value FROM settings WHERE key = 'sab_api_key'`).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(raw, "old-plain-key") {
		t.Fatalf("legacy plaintext not sealed: %q", raw)
	}
	if got := s.Get("sab_api_key"); got != "old-plain-key" {
		t.Fatalf("Get after migration = %q", got)
	}

	// running the sweep again changes nothing
	s.UseKeeper(k)
	var raw2 string
	s.db.QueryRow(`SELECT value FROM settings WHERE key = 'sab_api_key'`).Scan(&raw2) //nolint:errcheck
	if raw != raw2 {
		t.Fatal("re-sweep must be a no-op on sealed values")
	}
}
