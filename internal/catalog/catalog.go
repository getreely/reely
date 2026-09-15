// Package catalog owns the domain rows: libraries, movies, shows, seasons,
// episodes, people. The skeleton ships libraries and empty title lists; the
// importer and grab loop fill the rest in.
package catalog

import (
	"database/sql"
	"errors"
	"fmt"
)

type Store struct{ db *sql.DB }

func New(db *sql.DB) *Store { return &Store{db: db} }

// Library is a folder with a kind — movie libraries hold movies, show
// libraries hold shows.
type Library struct {
	ID               int64  `json:"id"`
	Name             string `json:"name"`
	Path             string `json:"path"`
	Kind             string `json:"kind"` // movies | shows
	MonitorNew       bool   `json:"monitorNew"`
	QualityProfileID int64  `json:"qualityProfileId"` // what the grab loop fetches for this library
	CreatedAt        string `json:"createdAt"`
}

func (s *Store) CreateLibrary(name, path, kind string) (*Library, error) {
	if kind != "movies" && kind != "shows" {
		return nil, errors.New("library kind must be movies or shows")
	}
	if name == "" || path == "" {
		return nil, errors.New("library needs a name and a path")
	}
	// a new library starts on the oldest profile — the seeded default unless
	// the admin has replaced it
	res, err := s.db.Exec(`INSERT INTO libraries (name, path, kind, quality_profile_id)
		VALUES (?, ?, ?, (SELECT id FROM quality_profiles ORDER BY id LIMIT 1))`, name, path, kind)
	if err != nil {
		return nil, fmt.Errorf("create library: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.GetLibrary(id)
}

func (s *Store) GetLibrary(id int64) (*Library, error) {
	l := &Library{}
	var monitor int
	err := s.db.QueryRow(`SELECT id, name, path, kind, monitor_new, COALESCE(quality_profile_id, 0), created_at
		FROM libraries WHERE id = ?`, id).
		Scan(&l.ID, &l.Name, &l.Path, &l.Kind, &monitor, &l.QualityProfileID, &l.CreatedAt)
	if err != nil {
		return nil, err
	}
	l.MonitorNew = monitor != 0
	return l, nil
}

func (s *Store) ListLibraries() ([]Library, error) {
	rows, err := s.db.Query(`SELECT id, name, path, kind, monitor_new, COALESCE(quality_profile_id, 0), created_at
		FROM libraries ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Library
	for rows.Next() {
		var l Library
		var monitor int
		if err := rows.Scan(&l.ID, &l.Name, &l.Path, &l.Kind, &monitor, &l.QualityProfileID, &l.CreatedAt); err != nil {
			return nil, err
		}
		l.MonitorNew = monitor != 0
		out = append(out, l)
	}
	return out, rows.Err()
}

func (s *Store) RemoveLibrary(id int64) error {
	_, err := s.db.Exec(`DELETE FROM libraries WHERE id = ?`, id)
	return err
}
