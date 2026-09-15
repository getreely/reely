// Package db owns the SQLite connection and schema migrations.
package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"strings"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

// Open opens (creating if needed) the SQLite database at path and applies
// pending migrations. WAL mode keeps reads cheap while background loops write.
// A restore staged by the backup service (path + ".restore") is applied
// first — swapping files while nothing holds the database open is the only
// safe moment.
func Open(path string) (*sql.DB, error) {
	if err := applyStagedRestore(path); err != nil {
		return nil, fmt.Errorf("apply staged restore: %w", err)
	}
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)", path)
	conn, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	if err := migrate(conn); err != nil {
		conn.Close()
		return nil, err
	}
	return conn, nil
}

// applyStagedRestore swaps a staged restore file into place: the current
// database survives as .pre-restore, and the old WAL/SHM sidecars are
// dropped so they can't graft stale pages onto the restored file.
func applyStagedRestore(path string) error {
	staged := path + ".restore"
	if _, err := os.Stat(staged); err != nil {
		return nil // nothing staged — the normal case
	}
	if _, err := os.Stat(path); err == nil {
		if err := os.Rename(path, path+".pre-restore"); err != nil {
			return err
		}
	}
	_ = os.Remove(path + "-wal")
	_ = os.Remove(path + "-shm")
	return os.Rename(staged, path)
}

func migrate(conn *sql.DB) error {
	if _, err := conn.Exec(`CREATE TABLE IF NOT EXISTS schema_migrations (
		name TEXT PRIMARY KEY,
		applied_at TEXT NOT NULL DEFAULT (datetime('now'))
	)`); err != nil {
		return fmt.Errorf("create schema_migrations: %w", err)
	}

	entries, err := fs.Glob(migrations, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(entries)

	for _, name := range entries {
		var applied int
		if err := conn.QueryRow(`SELECT COUNT(*) FROM schema_migrations WHERE name = ?`, name).Scan(&applied); err != nil {
			return err
		}
		if applied > 0 {
			continue
		}
		body, err := migrations.ReadFile(name)
		if err != nil {
			return err
		}
		if strings.HasPrefix(string(body), rebuildMarker) {
			if err := applyRebuild(conn, name, string(body)); err != nil {
				return err
			}
			continue
		}
		tx, err := conn.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(body)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply %s: %w", name, err)
		}
		if _, err := tx.Exec(`INSERT INTO schema_migrations (name) VALUES (?)`, name); err != nil {
			_ = tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

// rebuildMarker flags a migration that recreates tables (SQLite's only way
// to change a UNIQUE constraint). Those must run with foreign_keys OFF —
// otherwise DROP TABLE fires the children's ON DELETE actions — and the
// pragma is a no-op inside a transaction, so the whole dance happens on one
// pinned connection: FK off, transaction, verify, FK on.
const rebuildMarker = "-- reely:rebuild"

func applyRebuild(conn *sql.DB, name, body string) error {
	ctx := context.Background()
	c, err := conn.Conn(ctx)
	if err != nil {
		return err
	}
	defer c.Close() //nolint:errcheck // returning the conn to the pool; nothing to recover
	if _, err := c.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return err
	}
	// FK enforcement returns with the pragma regardless of how we exit
	defer c.ExecContext(ctx, `PRAGMA foreign_keys=ON`) //nolint:errcheck
	if _, err := c.ExecContext(ctx, `BEGIN`); err != nil {
		return err
	}
	rollback := func() { _, _ = c.ExecContext(ctx, `ROLLBACK`) }
	if _, err := c.ExecContext(ctx, body); err != nil {
		rollback()
		return fmt.Errorf("apply %s: %w", name, err)
	}
	if _, err := c.ExecContext(ctx, `INSERT INTO schema_migrations (name) VALUES (?)`, name); err != nil {
		rollback()
		return err
	}
	// the rebuild must not have orphaned anybody before it becomes real
	var fkTable string
	err = c.QueryRowContext(ctx, `SELECT "table" FROM pragma_foreign_key_check LIMIT 1`).Scan(&fkTable)
	if err != sql.ErrNoRows {
		rollback()
		if err == nil {
			return fmt.Errorf("apply %s: foreign key check failed on %s", name, fkTable)
		}
		return err
	}
	if _, err := c.ExecContext(ctx, `COMMIT`); err != nil {
		rollback()
		return err
	}
	return nil
}
