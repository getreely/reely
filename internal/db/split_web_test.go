package db

import (
	"path/filepath"
	"strings"
	"testing"
)

// The 0016 migration moves everything already stored off the collapsed
// "web" source. Getting it wrong is expensive in a specific way: a value
// the loop no longer recognizes ranks as unknown, which reads as below
// every cutoff, which sends a whole library hunting upgrades it does not
// need. So this runs the shipped SQL over realistic legacy rows and
// checks each landing spot.
func TestSplitWebSourcesMigration(t *testing.T) {
	conn, err := Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// legacy rows, as an install from before the split holds them
	if _, err := conn.Exec(`INSERT INTO quality_profiles
		(name, qualities, cutoff, upgrades, hdr, min_mb_per_min, max_mb_per_min, sources, source_cutoff)
		VALUES ('Legacy', '["1080p"]', '1080p', 1, 'allow', 8, 80, '["remux","bluray","web"]', 'web')`); err != nil {
		t.Fatal(err)
	}
	lib, err := conn.Exec(`INSERT INTO libraries (name, path, kind) VALUES ('M', '/tmp/m', 'movies')`)
	if err != nil {
		t.Fatal(err)
	}
	libID, _ := lib.LastInsertId()
	if _, err := conn.Exec(`INSERT INTO movies (tmdb_id, title, library_id, monitored, file_path, quality, source)
		VALUES (1, 'Arrival', ?, 1, '/tmp/m/a.mkv', '1080p', 'web')`, libID); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(`INSERT INTO custom_formats (name, score, specs, applies_to)
		VALUES ('Any web', 5, '[{"kind":"source","value":"web","negate":false,"required":false}]', 'movies')`); err != nil {
		t.Fatal(err)
	}

	// re-run the shipped migration over those rows
	sqlBytes, err := migrations.ReadFile("migrations/0016_split_web_sources.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(string(sqlBytes)); err != nil {
		t.Fatalf("migration failed: %v", err)
	}

	var sources, cutoff string
	if err := conn.QueryRow(`SELECT sources, source_cutoff FROM quality_profiles WHERE name = 'Legacy'`).
		Scan(&sources, &cutoff); err != nil {
		t.Fatal(err)
	}
	// a profile that allowed web allowed both kinds, and must go on doing so
	if sources != `["remux","bluray","webdl","webrip"]` {
		t.Fatalf("sources = %s", sources)
	}
	// webrip is the LOWER rung: aiming there keeps both satisfying the
	// cutoff, which is what "web was good enough" meant
	if cutoff != "webrip" {
		t.Fatalf("source_cutoff = %q, want webrip — webdl would put every existing file below cutoff", cutoff)
	}

	var movieSource string
	if err := conn.QueryRow(`SELECT source FROM movies WHERE title = 'Arrival'`).Scan(&movieSource); err != nil {
		t.Fatal(err)
	}
	if movieSource != "webrip" {
		t.Fatalf("movie source = %q, want the conservative webrip", movieSource)
	}

	var specs string
	if err := conn.QueryRow(`SELECT specs FROM custom_formats WHERE name = 'Any web'`).Scan(&specs); err != nil {
		t.Fatal(err)
	}
	// "any web release" survives as a title rule, since no single source
	// value can say it any more
	if specs == `[{"kind":"source","value":"web","negate":false,"required":false}]` {
		t.Fatal("a source=web format spec was left pointing at a value that no longer exists")
	}
	for _, want := range []string{`"kind":"title"`, `WEB`, `DL|Rip`} {
		if !strings.Contains(specs, want) {
			t.Fatalf("specs = %s, missing %s", specs, want)
		}
	}

	// running it twice must not double anything
	if _, err := conn.Exec(string(sqlBytes)); err != nil {
		t.Fatal(err)
	}
	if err := conn.QueryRow(`SELECT sources FROM quality_profiles WHERE name = 'Legacy'`).Scan(&sources); err != nil {
		t.Fatal(err)
	}
	if sources != `["remux","bluray","webdl","webrip"]` {
		t.Fatalf("re-running the migration changed sources to %s", sources)
	}
}
