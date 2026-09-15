package db

import (
	"path/filepath"
	"testing"
)

// The 0017 migration re-encodes the format-score floor: zero used to mean
// "no floor", so stored zeros become NULL and a stored positive floor
// survives verbatim. Getting this wrong flips floors on or off across an
// upgrade without anyone touching a setting.
func TestFormatFloorMigrationPreservesMeaning(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// rows as a pre-0017 install held them: 0 meant no floor, 50 meant 50
	if _, err := conn.Exec(`INSERT INTO quality_profiles (name, min_format_score) VALUES
		('No floor', NULL), ('Had a real floor', 50)`); err != nil {
		t.Fatal(err)
	}
	// simulate legacy encoding by writing the old sentinel directly
	if _, err := conn.Exec(`UPDATE quality_profiles SET min_format_score = 0 WHERE name = 'No floor'`); err != nil {
		t.Fatal(err)
	}

	sqlBytes, err := migrations.ReadFile("migrations/0017_format_floor_nullable.sql")
	if err != nil {
		t.Fatal(err)
	}
	if err := applyRebuild(conn, "re-run-0017", string(sqlBytes)); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	var floor *int64
	if err := conn.QueryRow(`SELECT min_format_score FROM quality_profiles WHERE name = 'No floor'`).
		Scan(&floor); err != nil {
		t.Fatal(err)
	}
	if floor != nil {
		t.Fatalf("legacy 0 should migrate to NULL, got %d — that turns a disabled floor ON", *floor)
	}
	if err := conn.QueryRow(`SELECT min_format_score FROM quality_profiles WHERE name = 'Had a real floor'`).
		Scan(&floor); err != nil {
		t.Fatal(err)
	}
	if floor == nil || *floor != 50 {
		t.Fatalf("a real floor of 50 must survive, got %v", floor)
	}
}
