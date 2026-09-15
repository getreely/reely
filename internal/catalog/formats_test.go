package catalog

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/getreely/reely/internal/db"
	"github.com/getreely/reely/internal/quality"
)

// Formats are scoped: the movie and show versions of a name live as
// separate rows, and attaching for a search kind loads only that scope.
func TestFormatsPerScope(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	cat := New(conn)

	spec := []quality.FormatSpec{{Kind: "title", Value: `\bATMOS\b`}}
	store := func(name, scope string, score int) {
		t.Helper()
		f := &StoredFormat{AppliesTo: scope}
		f.Name, f.Score, f.Specs = name, score, spec
		if err := cat.UpsertFormat(f); err != nil {
			t.Fatal(err)
		}
	}
	store("ATMOS", "movies", 100)
	store("ATMOS", "shows", 50) // same name, its own row per scope
	store("Movies only", "movies", 25)

	bad := &StoredFormat{AppliesTo: "anime"}
	bad.Name, bad.Specs = "nope", spec
	if err := cat.UpsertFormat(bad); err == nil {
		t.Fatal("unknown scope accepted")
	}

	all, err := cat.ListFormats()
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 3 {
		t.Fatalf("listed %d formats, want 3: %+v", len(all), all)
	}

	// re-import refreshes in place within its scope, no duplicate row
	store("ATMOS", "movies", 90)
	all, _ = cat.ListFormats()
	if len(all) != 3 {
		t.Fatalf("re-import duplicated: %d rows", len(all))
	}

	p := &quality.Profile{}
	if err := cat.AttachFormats(p, "movies"); err != nil {
		t.Fatal(err)
	}
	if len(p.Formats) != 2 {
		t.Fatalf("movie attach loaded %d formats, want 2", len(p.Formats))
	}
	for _, f := range p.Formats {
		if f.Name == "ATMOS" && f.Score != 90 {
			t.Errorf("re-import did not refresh the score: %d", f.Score)
		}
	}
	if err := cat.AttachFormats(p, "shows"); err != nil {
		t.Fatal(err)
	}
	if len(p.Formats) != 1 || p.Formats[0].Score != 50 {
		t.Fatalf("show attach loaded the wrong formats: %+v", p.Formats)
	}
}

// Migration 0019 rewrites stored web-source specs from kind "title" to
// "source_title" by exact JSON substring — this pins the replace strings
// to what json.Marshal actually stores, escaping included.
func TestMigration0019RewritesStoredWebSpecs(t *testing.T) {
	conn, err := db.Open(filepath.Join(t.TempDir(), "reely.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	s := New(conn)
	f := &StoredFormat{AppliesTo: "shows", Format: quality.Format{
		Name: "HMAX", Score: 75, Specs: []quality.FormatSpec{
			{Kind: "title", Value: `\[(HMAX)\b|\b(HMAX)\]`},
			{Kind: "title", Value: `\bWEB[-_. ]?DL\b`},
			{Kind: "title", Value: `\bWEB[-_. ]?Rip\b`, Negate: true},
			{Kind: "title", Value: `\bWEB[-_. ]?(DL|Rip)\b`},
		},
	}}
	if err := s.UpsertFormat(f); err != nil {
		t.Fatal(err)
	}
	sqlBytes, err := os.ReadFile("../db/migrations/0019_format_source_title.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Exec(string(sqlBytes)); err != nil {
		t.Fatal(err)
	}
	got, err := s.ListFormats()
	if err != nil || len(got) != 1 {
		t.Fatalf("list: %v (%d)", err, len(got))
	}
	kinds := make([]string, 0, 4)
	for _, sp := range got[0].Specs {
		kinds = append(kinds, sp.Kind)
	}
	want := []string{"title", "source_title", "source_title", "source_title"}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("kinds = %v, want %v", kinds, want)
		}
	}
	// the negate flag on the rewritten spec rode along
	if !got[0].Specs[2].Negate {
		t.Fatal("negate flag lost in the rewrite")
	}
}
