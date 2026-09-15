package catalog

// GetState reads one runtime-state value; missing keys read as "".
func (s *Store) GetState(key string) string {
	var v string
	if err := s.db.QueryRow(`SELECT value FROM app_state WHERE key = ?`, key).Scan(&v); err != nil {
		return ""
	}
	return v
}

// SetState writes one runtime-state value, replacing what was there — the
// table holds the latest state per key, never a history.
func (s *Store) SetState(key, value string) error {
	_, err := s.db.Exec(`INSERT INTO app_state (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
