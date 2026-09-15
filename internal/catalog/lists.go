package catalog

import (
	"database/sql"
	"fmt"
)

// Watched lists: stored list definitions the sync engine walks. The config
// column is source-specific JSON the lists package interprets.

type List struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Source     string `json:"source"`
	Config     string `json:"config"`
	LibraryID  int64  `json:"libraryId"`
	ItemLimit  int    `json:"itemLimit"`
	Enabled    bool   `json:"enabled"`
	CreatedBy  int64  `json:"createdBy,omitempty"`
	LastSynced string `json:"lastSynced,omitempty"`
	// GroupIDs is who gets what this list adds, from now on. Empty means
	// nobody, which is what a list did before it could say.
	GroupIDs []int64 `json:"groupIds"`
}

// CreateList stores one list definition.
func (s *Store) CreateList(l *List) (int64, error) {
	res, err := s.db.Exec(`INSERT INTO lists (name, source, config, library_id, item_limit, enabled, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		l.Name, l.Source, l.Config, l.LibraryID, l.ItemLimit, boolInt(l.Enabled), nullID(l.CreatedBy))
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// ListLists returns every stored list.
func (s *Store) ListLists() ([]List, error) {
	rows, err := s.db.Query(`SELECT id, name, source, config, library_id, item_limit, enabled,
		COALESCE(created_by, 0), COALESCE(last_synced, '') FROM lists ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []List
	for rows.Next() {
		var l List
		var enabled int
		if err := rows.Scan(&l.ID, &l.Name, &l.Source, &l.Config, &l.LibraryID,
			&l.ItemLimit, &enabled, &l.CreatedBy, &l.LastSynced); err != nil {
			return nil, err
		}
		l.Enabled = enabled != 0
		out = append(out, l)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		if out[i].GroupIDs, err = s.ListGroups(out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ListGroups is the audience set on one list. Never nil, so a caller
// rendering it does not have to tell "none" from "not loaded".
func (s *Store) ListGroups(listID int64) ([]int64, error) {
	rows, err := s.db.Query(
		`SELECT group_id FROM list_groups WHERE list_id = ? ORDER BY group_id`, listID)
	if err != nil {
		return nil, fmt.Errorf("list groups: %w", err)
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("list groups: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetListGroups replaces a list's audience.
func (s *Store) SetListGroups(listID int64, groupIDs []int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("set list groups: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed
	if _, err := tx.Exec(`DELETE FROM list_groups WHERE list_id = ?`, listID); err != nil {
		return fmt.Errorf("set list groups: %w", err)
	}
	for _, gid := range groupIDs {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO list_groups (list_id, group_id)
			VALUES (?, ?)`, listID, gid); err != nil {
			return fmt.Errorf("set list groups: %w", err)
		}
	}
	return tx.Commit()
}

// GetList returns one list, or sql.ErrNoRows.
func (s *Store) GetList(id int64) (*List, error) {
	var l List
	var enabled int
	err := s.db.QueryRow(`SELECT id, name, source, config, library_id, item_limit, enabled,
		COALESCE(created_by, 0), COALESCE(last_synced, '') FROM lists WHERE id = ?`, id).
		Scan(&l.ID, &l.Name, &l.Source, &l.Config, &l.LibraryID, &l.ItemLimit, &enabled,
			&l.CreatedBy, &l.LastSynced)
	if err != nil {
		return nil, err
	}
	l.Enabled = enabled != 0
	if l.GroupIDs, err = s.ListGroups(l.ID); err != nil {
		return nil, err
	}
	return &l, nil
}

// SetListEnabled flips a list's enabled switch.
func (s *Store) SetListEnabled(id int64, enabled bool) error {
	res, err := s.db.Exec(`UPDATE lists SET enabled = ? WHERE id = ?`, boolInt(enabled), id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// TouchListSynced stamps a list's last successful sync.
func (s *Store) TouchListSynced(id int64) error {
	_, err := s.db.Exec(`UPDATE lists SET last_synced = datetime('now') WHERE id = ?`, id)
	return err
}

// RemoveList deletes a list definition. Titles it added stay — the list
// was only ever the reason they arrived.
func (s *Store) RemoveList(id int64) error {
	res, err := s.db.Exec(`DELETE FROM lists WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// HasTitle reports whether a library already carries a TMDB title of the
// given kind — the sync's skip check.
func (s *Store) HasTitle(kind string, tmdbID int, libraryID int64) (bool, error) {
	q := `SELECT COUNT(*) FROM movies WHERE tmdb_id = ? AND library_id = ?`
	if kind == "show" {
		q = `SELECT COUNT(*) FROM shows WHERE tmdb_id = ? AND library_id = ?`
	}
	var n int
	err := s.db.QueryRow(q, tmdbID, libraryID).Scan(&n)
	return n > 0, err
}
