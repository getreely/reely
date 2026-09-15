package catalog

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/getreely/reely/internal/quality"
)

// Quality profile storage. The Profile type and its validation live in
// internal/quality with the scorer; this file only persists them.

func scanProfile(row interface{ Scan(...any) error }) (*quality.Profile, error) {
	p := &quality.Profile{}
	var qualities, sources, required, blocked, preferred string
	var minFloor sql.NullInt64
	var upgrades int
	if err := row.Scan(&p.ID, &p.Name, &qualities, &p.Cutoff, &upgrades, &p.HDR,
		&p.MinMBPerMin, &p.MaxMBPerMin, &sources, &p.SourceCutoff,
		&required, &blocked, &preferred, &minFloor); err != nil {
		return nil, err
	}
	for name, into := range map[string]any{
		"qualities": &p.Qualities, "sources": &p.Sources,
		"required": &p.Required, "blocked": &p.Blocked, "preferred": &p.Preferred,
	} {
		raw := map[string]string{
			"qualities": qualities, "sources": sources,
			"required": required, "blocked": blocked, "preferred": preferred,
		}[name]
		if err := json.Unmarshal([]byte(raw), into); err != nil {
			return nil, fmt.Errorf("profile %d %s: %w", p.ID, name, err)
		}
	}
	p.Upgrades = upgrades != 0
	if minFloor.Valid {
		v := int(minFloor.Int64)
		p.MinFormatScore = &v
	}
	return p, nil
}

// floorValue turns the nullable floor into what the driver wants: an
// int64, or NULL when no floor is set.
func floorValue(p *quality.Profile) any {
	if p.MinFormatScore == nil {
		return nil
	}
	return int64(*p.MinFormatScore)
}

const profileCols = `id, name, qualities, cutoff, upgrades, hdr, min_mb_per_min, max_mb_per_min, sources, source_cutoff, required, blocked, preferred, min_format_score`

func (s *Store) ListProfiles() ([]quality.Profile, error) {
	rows, err := s.db.Query(`SELECT ` + profileCols + ` FROM quality_profiles ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []quality.Profile
	for rows.Next() {
		p, err := scanProfile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *p)
	}
	return out, rows.Err()
}

func (s *Store) GetProfile(id int64) (*quality.Profile, error) {
	return scanProfile(s.db.QueryRow(`SELECT `+profileCols+` FROM quality_profiles WHERE id = ?`, id))
}

func (s *Store) CreateProfile(p *quality.Profile) (*quality.Profile, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	qualities, _ := json.Marshal(p.Qualities)
	sources, _ := json.Marshal(nonNil(p.Sources))
	required, _ := json.Marshal(nonNil(p.Required))
	blocked, _ := json.Marshal(nonNil(p.Blocked))
	preferred, _ := json.Marshal(nonNilTerms(p.Preferred))
	res, err := s.db.Exec(`INSERT INTO quality_profiles
		(name, qualities, cutoff, upgrades, hdr, min_mb_per_min, max_mb_per_min,
		 sources, source_cutoff, required, blocked, preferred, min_format_score)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		strings.TrimSpace(p.Name), string(qualities), p.Cutoff, boolInt(p.Upgrades), p.HDR,
		p.MinMBPerMin, p.MaxMBPerMin, string(sources), p.SourceCutoff,
		string(required), string(blocked), string(preferred), floorValue(p))
	if err != nil {
		return nil, fmt.Errorf("create profile: %w", err)
	}
	id, _ := res.LastInsertId()
	return s.GetProfile(id)
}

func (s *Store) UpdateProfile(p *quality.Profile) (*quality.Profile, error) {
	if err := p.Validate(); err != nil {
		return nil, err
	}
	qualities, _ := json.Marshal(p.Qualities)
	sources, _ := json.Marshal(nonNil(p.Sources))
	required, _ := json.Marshal(nonNil(p.Required))
	blocked, _ := json.Marshal(nonNil(p.Blocked))
	preferred, _ := json.Marshal(nonNilTerms(p.Preferred))
	res, err := s.db.Exec(`UPDATE quality_profiles SET
		name = ?, qualities = ?, cutoff = ?, upgrades = ?, hdr = ?,
		min_mb_per_min = ?, max_mb_per_min = ?, sources = ?, source_cutoff = ?,
		required = ?, blocked = ?, preferred = ?, min_format_score = ? WHERE id = ?`,
		strings.TrimSpace(p.Name), string(qualities), p.Cutoff, boolInt(p.Upgrades), p.HDR,
		p.MinMBPerMin, p.MaxMBPerMin, string(sources), p.SourceCutoff,
		string(required), string(blocked), string(preferred), floorValue(p), p.ID)
	if err != nil {
		return nil, fmt.Errorf("update profile: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, sql.ErrNoRows
	}
	return s.GetProfile(p.ID)
}

// DeleteProfile removes a profile unless it is the last one — every library
// needs somewhere to fall; libraries pointing at it fall back via SET NULL
// and the next read re-defaults them.
func (s *Store) DeleteProfile(id int64) error {
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM quality_profiles`).Scan(&count); err != nil {
		return err
	}
	if count <= 1 {
		return errors.New("cannot delete the last quality profile")
	}
	res, err := s.db.Exec(`DELETE FROM quality_profiles WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// SetLibraryProfile points a library at a profile — the default new titles
// in it inherit on import. Existing titles keep their own.
func (s *Store) SetLibraryProfile(libraryID, profileID int64) error {
	return s.setProfile("libraries", libraryID, profileID)
}

// SetMovieProfile points one movie at a profile.
func (s *Store) SetMovieProfile(movieID, profileID int64) error {
	return s.setProfile("movies", movieID, profileID)
}

// SetShowProfile points one show at a profile; its episodes follow it.
func (s *Store) SetShowProfile(showID, profileID int64) error {
	return s.setProfile("shows", showID, profileID)
}

// one fully-literal statement per target keeps gosec's G202 (SQL string
// concatenation) out of the picture entirely
var setProfileStmt = map[string]string{
	"libraries": `UPDATE libraries SET quality_profile_id = ? WHERE id = ?`,
	"movies":    `UPDATE movies SET quality_profile_id = ? WHERE id = ?`,
	"shows":     `UPDATE shows SET quality_profile_id = ? WHERE id = ?`,
}

func (s *Store) setProfile(table string, id, profileID int64) error {
	if _, err := s.GetProfile(profileID); err != nil {
		return errors.New("no such quality profile")
	}
	res, err := s.db.Exec(setProfileStmt[table], profileID, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// nonNil keeps an empty slice JSON-encoding as [] rather than null.
func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

func nonNilTerms(v []quality.PreferredTerm) []quality.PreferredTerm {
	if v == nil {
		return []quality.PreferredTerm{}
	}
	return v
}
