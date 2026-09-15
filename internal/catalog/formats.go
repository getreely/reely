package catalog

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/getreely/reely/internal/quality"
)

// Custom-format storage: one install-wide list (Settings → Custom
// Formats), applied by every profile's ranking. The Format type and its
// matching live in internal/quality.

// StoredFormat is a custom format with its row identity. AppliesTo scopes
// it to one kind of search — movies or shows — set by which guides
// collection it was imported from.
type StoredFormat struct {
	ID        int64  `json:"id"`
	TrashID   string `json:"trashId,omitempty"`
	AppliesTo string `json:"appliesTo"`
	quality.Format
}

func scanFormat(row interface{ Scan(...any) error }) (*StoredFormat, error) {
	f := &StoredFormat{}
	var specs string
	if err := row.Scan(&f.ID, &f.Name, &f.AppliesTo, &f.Score, &specs, &f.TrashID); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(specs), &f.Specs); err != nil {
		return nil, fmt.Errorf("format %d specs: %w", f.ID, err)
	}
	return f, nil
}

func (s *Store) ListFormats() ([]StoredFormat, error) {
	rows, err := s.db.Query(`SELECT id, name, applies_to, score, specs, trash_id
		FROM custom_formats ORDER BY applies_to, name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []StoredFormat
	for rows.Next() {
		f, err := scanFormat(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *f)
	}
	return out, rows.Err()
}

// UpsertFormat stores one format, replacing any existing one of the same
// name within its scope — re-importing from the guides refreshes patterns
// and scores rather than piling up duplicates, while the movie and show
// versions of a name stay separate rows.
func (s *Store) UpsertFormat(f *StoredFormat) error {
	if err := quality.ValidateFormat(f.Format); err != nil {
		return err
	}
	if f.AppliesTo != "movies" && f.AppliesTo != "shows" {
		return fmt.Errorf("format %q: appliesTo must be movies or shows", f.Name)
	}
	specs, _ := json.Marshal(f.Specs)
	_, err := s.db.Exec(`INSERT INTO custom_formats (name, applies_to, score, specs, trash_id)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(name, applies_to) DO UPDATE SET score = excluded.score,
			specs = excluded.specs, trash_id = excluded.trash_id`,
		strings.TrimSpace(f.Name), f.AppliesTo, f.Score, string(specs), f.TrashID)
	return err
}

// SetFormatScore adjusts one format's score in place.
func (s *Store) SetFormatScore(id int64, score int) error {
	res, err := s.db.Exec(`UPDATE custom_formats SET score = ? WHERE id = ?`, score, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *Store) DeleteFormat(id int64) error {
	res, err := s.db.Exec(`DELETE FROM custom_formats WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// AttachFormats loads the custom formats scoped to one kind of search
// ("movies" or "shows") onto a profile, so a movie hunt is judged by movie
// formats and a show hunt by show formats.
func (s *Store) AttachFormats(p *quality.Profile, kind string) error {
	rows, err := s.db.Query(`SELECT id, name, applies_to, score, specs, trash_id
		FROM custom_formats WHERE applies_to = ? ORDER BY name`, kind)
	if err != nil {
		return err
	}
	defer rows.Close()
	p.Formats = nil
	for rows.Next() {
		f, err := scanFormat(rows)
		if err != nil {
			return err
		}
		p.Formats = append(p.Formats, f.Format)
	}
	return rows.Err()
}
