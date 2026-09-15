package backup

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/getreely/reely/internal/db"
)

func testService(t *testing.T) (*Service, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "reely.db")
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if _, err := conn.Exec(`INSERT INTO settings (key, value) VALUES ('marker', 'original')`); err != nil {
		t.Fatal(err)
	}
	return &Service{DB: conn, DBPath: dbPath, Dir: filepath.Join(dir, "backups")}, dbPath
}

func TestCreateListDeleteBackups(t *testing.T) {
	s, _ := testService(t)
	entry, err := s.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(entry.Name, "reely-") || entry.Size == 0 {
		t.Fatalf("entry = %+v", entry)
	}
	// the backup is a healthy database with our data in it
	path, err := s.Path(entry.Name)
	if err != nil {
		t.Fatal(err)
	}
	if err := validate(path); err != nil {
		t.Fatalf("backup fails validation: %v", err)
	}

	list, err := s.List()
	if err != nil || len(list) != 1 || list[0].Name != entry.Name {
		t.Fatalf("list = %+v (%v)", list, err)
	}

	// traversal and stray names never resolve
	for _, bad := range []string{"../reely.db", "reely.db", "reely-20260101-000000.db.evil", "notes.txt"} {
		if _, err := s.Path(bad); err == nil {
			t.Errorf("Path(%q) resolved", bad)
		}
	}

	if err := s.Delete(entry.Name); err != nil {
		t.Fatal(err)
	}
	if list, _ := s.List(); len(list) != 0 {
		t.Fatalf("after delete, list = %+v", list)
	}
}

func TestStagedRestoreAppliesOnOpen(t *testing.T) {
	s, dbPath := testService(t)
	entry, err := s.Create(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// the live database moves on after the backup
	if _, err := s.DB.Exec(`UPDATE settings SET value = 'changed' WHERE key = 'marker'`); err != nil {
		t.Fatal(err)
	}
	if err := s.StageExisting(entry.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dbPath + ".restore"); err != nil {
		t.Fatalf("nothing staged: %v", err)
	}

	// "restart": close and reopen — the staged file replaces the live one
	// (the running handle in testService stays open harmlessly; SQLite files
	// are swapped by rename, not truncation)
	conn, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	var v string
	if err := conn.QueryRow(`SELECT value FROM settings WHERE key = 'marker'`).Scan(&v); err != nil || v != "original" {
		t.Fatalf("marker after restore = %q (%v), want original", v, err)
	}
	if _, err := os.Stat(dbPath + ".pre-restore"); err != nil {
		t.Fatalf("no safety copy kept: %v", err)
	}
	if _, err := os.Stat(dbPath + ".restore"); !os.IsNotExist(err) {
		t.Fatal("staged file still present after apply")
	}
}

func TestStageUploadRejectsGarbage(t *testing.T) {
	s, _ := testService(t)
	if err := s.StageUpload(strings.NewReader("this is not a database")); err == nil {
		t.Fatal("garbage upload staged")
	}
	// a real SQLite file that is not a reely database is refused too
	other := filepath.Join(t.TempDir(), "other.db")
	conn, err := db.Open(other)
	if err != nil {
		t.Fatal(err)
	}
	// strip the migration history so it stops being one of ours
	if _, err := conn.Exec(`DROP TABLE schema_migrations`); err != nil {
		t.Fatal(err)
	}
	conn.Close()
	f, err := os.Open(other)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := s.StageUpload(f); err == nil {
		t.Fatal("foreign database staged")
	}
}

func TestPruneKeepsTheNewest(t *testing.T) {
	s, _ := testService(t)
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		t.Fatal(err)
	}
	// fabricate an over-full folder with sortable names
	for _, name := range []string{
		"reely-20260101-000000.db", "reely-20260102-000000.db", "reely-20260103-000000.db",
		"reely-20260104-000000.db", "reely-20260105-000000.db", "reely-20260106-000000.db",
		"reely-20260107-000000.db", "reely-20260108-000000.db", "reely-20260109-000000.db",
		"reely-20260110-000000.db", "reely-20260111-000000.db", "reely-20260112-000000.db",
		"reely-20260113-000000.db", "reely-20260114-000000.db",
	} {
		if err := os.WriteFile(filepath.Join(s.Dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.Create(context.Background()); err != nil {
		t.Fatal(err)
	}
	list, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != keep {
		t.Fatalf("after prune, %d backups, want %d", len(list), keep)
	}
	// the oldest fell off; the fresh one leads
	for _, e := range list {
		if e.Name == "reely-20260101-000000.db" {
			t.Fatal("oldest backup survived the prune")
		}
	}
}
