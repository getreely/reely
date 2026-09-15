// Package backup owns database backups and restore staging. Backups are
// plain SQLite files written with VACUUM INTO — consistent even while the
// app is writing — kept in a rotating folder next to the database. Restore
// never swaps the live database out from under the running process:
// the incoming file is validated, staged next to the database, and applied
// by db.Open on the next start.
package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite" // the validation probe opens staged files directly
)

// keep is how many rotating backups survive pruning. Dailies plus a few
// hand-made ones; at SQLite sizes this is megabytes, not gigabytes.
const keep = 14

// backupName matches the files this package writes — everything else in
// the folder is ignored and never served or deleted.
var backupName = regexp.MustCompile(`^reely-\d{8}-\d{6}\.db$`)

type Service struct {
	DB     *sql.DB
	DBPath string // the live database file
	Dir    string // where backups live (created on demand)
}

// Entry is one backup on disk.
type Entry struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"createdAt"`
}

// Create writes a new backup now and prunes old ones. Returns the entry.
func (s *Service) Create(ctx context.Context) (*Entry, error) {
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return nil, err
	}
	name := time.Now().UTC().Format("reely-20060102-150405.db")
	path := filepath.Join(s.Dir, name)
	// VACUUM INTO refuses to overwrite; a same-second re-run just returns
	// the existing file
	if _, err := os.Stat(path); err == nil {
		return s.entry(name)
	}
	if _, err := s.DB.ExecContext(ctx, `VACUUM INTO ?`, path); err != nil {
		return nil, fmt.Errorf("backup: %w", err)
	}
	s.prune()
	return s.entry(name)
}

func (s *Service) entry(name string) (*Entry, error) {
	info, err := os.Stat(filepath.Join(s.Dir, name))
	if err != nil {
		return nil, err
	}
	return &Entry{Name: name, Size: info.Size(), CreatedAt: info.ModTime().UTC().Format(time.RFC3339)}, nil
}

// List returns the backups on disk, newest first.
func (s *Service) List() ([]Entry, error) {
	entries, err := os.ReadDir(s.Dir)
	if errors.Is(err, os.ErrNotExist) {
		return []Entry{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []Entry{}
	for _, e := range entries {
		if e.IsDir() || !backupName.MatchString(e.Name()) {
			continue
		}
		if en, err := s.entry(e.Name()); err == nil {
			out = append(out, *en)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out, nil
}

// Path resolves a backup name to its file, refusing anything that isn't
// exactly one of ours — no separators, no traversal, no stray files.
func (s *Service) Path(name string) (string, error) {
	if !backupName.MatchString(name) {
		return "", errors.New("no such backup")
	}
	path := filepath.Join(s.Dir, name)
	if _, err := os.Stat(path); err != nil {
		return "", errors.New("no such backup")
	}
	return path, nil
}

// Delete removes one backup.
func (s *Service) Delete(name string) error {
	path, err := s.Path(name)
	if err != nil {
		return err
	}
	return os.Remove(path)
}

// prune keeps the newest `keep` backups. Best-effort; the names sort
// chronologically by construction.
func (s *Service) prune() {
	list, err := s.List()
	if err != nil {
		return
	}
	for _, e := range list[min(keep, len(list)):] {
		if err := os.Remove(filepath.Join(s.Dir, e.Name)); err != nil {
			log.Printf("reely: prune backup %s: %v", e.Name, err)
		}
	}
}

// MaybeDaily creates a backup when the newest one is older than a day (or
// none exists). The watcher calls this on a slow cadence; quiet when
// nothing is due.
func (s *Service) MaybeDaily(ctx context.Context) {
	list, err := s.List()
	if err != nil {
		log.Printf("reely: backup check: %v", err)
		return
	}
	if len(list) > 0 {
		if t, err := time.Parse(time.RFC3339, list[0].CreatedAt); err == nil && time.Since(t) < 24*time.Hour {
			return
		}
	}
	if _, err := s.Create(ctx); err != nil {
		log.Printf("reely: daily backup: %v", err)
		return
	}
	log.Printf("reely: daily backup written")
}

// StageUpload validates an uploaded database and stages it for restore on
// the next start. The live database is untouched until then.
func (s *Service) StageUpload(r io.Reader) error {
	tmp, err := os.CreateTemp(filepath.Dir(s.DBPath), ".reely-restore-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, r); err != nil {
		tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := validate(tmpName); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, s.DBPath+".restore")
}

// StageExisting stages one of the rotating backups for restore.
func (s *Service) StageExisting(name string) error {
	path, err := s.Path(name)
	if err != nil {
		return err
	}
	src, err := os.Open(path)
	if err != nil {
		return err
	}
	defer src.Close() //nolint:errcheck // read-only handle
	return s.StageUpload(src)
}

// validate makes sure the staged file is a healthy reely database before
// it can replace the real one: SQLite magic, a passing integrity check,
// and our migration table.
func validate(path string) error {
	f, err := os.Open(path) //nolint:gosec // G304: our own temp file
	if err != nil {
		return err
	}
	magic := make([]byte, 16)
	_, readErr := io.ReadFull(f, magic)
	f.Close() //nolint:errcheck,gosec // read-only handle
	if readErr != nil || !strings.HasPrefix(string(magic), "SQLite format 3") {
		return errors.New("that file is not a SQLite database")
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return err
	}
	defer db.Close() //nolint:errcheck // read-only handle
	var result string
	if err := db.QueryRow(`PRAGMA integrity_check`).Scan(&result); err != nil || result != "ok" {
		return fmt.Errorf("database failed its integrity check (%s)", result)
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM schema_migrations`).Scan(&n); err != nil || n == 0 {
		return errors.New("that database is not a reely backup (no migration history)")
	}
	return nil
}
