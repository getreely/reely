package catalog

import (
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Sharing decides who may see which titles in Plex.
//
// The unit of entitlement is a GROUP, never a user. Somebody's own
// titles live in their personal group — one member — and a household is
// a group with several. One code path covers both, and moving a person
// in or out of a household costs one restriction string rather than a
// re-tag of every title the household holds.
//
// Nothing here talks to Plex. These rows are the source of truth and are
// written the moment a request is approved; the labels and restriction
// strings on the media server are a projection applied later, by a
// reconcile pass that can run as late as it likes without anybody's
// entitlement being in doubt in the meantime.

// LabelPrefix marks a label as reely's to manage. Labels without it were
// put there by hand and are never removed — the prefix is the whole
// implementation of "revoke mine, leave theirs", because a label absent
// from what reely computed is otherwise indistinguishable between "this
// was revoked" and "this is not mine to touch".
const LabelPrefix = "reely."

// NoneLabel is what a member entitled to nothing gets. An empty
// restriction does not restrict — it removes the restriction, showing
// that person the whole library — so the empty case has to be spelled
// with a label no title carries rather than with an empty string.
const NoneLabel = LabelPrefix + "none"

// ErrGroupInUse is a group that still has members. Deleting one would
// revoke every title it grants, so it is refused rather than cascaded.
var ErrGroupInUse = errors.New("group still has members")

// Group is a set of people who see the same titles. Label is what Plex
// sees, Name what the owner sees. OwnerUserID set means it is somebody's
// personal group, created with their account and not deletable.
type Group struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Label       string `json:"label"`
	OwnerUserID int64  `json:"ownerUserId,omitempty"`
	Personal    bool   `json:"personal"`
	Members     int    `json:"members"`
	Titles      int    `json:"titles"`
	// Backfill marks the group holding what was already in Plex before
	// the split. Everybody is in it, so nothing new ever goes there —
	// otherwise every request would reach everybody and there would be no
	// split at all.
	Backfill bool `json:"backfill"`
	// Everyone marks the group that IS the household: every account
	// joins it as it is created, and like the backfill group it is never
	// a request default — only the owner puts a title in front of the
	// whole house. The two differ on membership, which is why they are
	// separate flags: a backfill group's members are frozen at seed,
	// because a latecomer never had the old library.
	Everyone  bool   `json:"everyone"`
	CreatedAt string `json:"createdAt"`
}

// CreateGroup adds a shared group, labelled after its name so that
// somebody looking at a film in Plex can tell what the tag means without
// coming back here.
func (s *Store) CreateGroup(name string) (*Group, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("a group needs a name")
	}
	label, err := s.freeLabel(name, 0)
	if err != nil {
		return nil, err
	}
	res, err := s.db.Exec(`INSERT INTO share_groups (name, label) VALUES (?, ?)`, name, label)
	if err != nil {
		return nil, fmt.Errorf("create group: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("create group: %w", err)
	}
	return s.Group(id)
}

// Slug turns a name into the readable half of a label: lowercase, with
// anything that is not a letter or a digit collapsed to a single
// underscore, and apostrophes dropped rather than collapsed. Plex accepts far more than this in a tag, and the
// restriction string percent-encodes every value anyway — the narrowing
// is for legibility, not safety.
func Slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			dash = false
		case r == '\'' || r == '\u2019':
			// an apostrophe joins rather than separates, so "Jolene's"
			// reads as jolenes and not as jolene_s
		case b.Len() > 0 && !dash:
			b.WriteByte('_')
			dash = true
		}
	}
	return strings.Trim(b.String(), "_")
}

// freeLabel is Slug plus whatever suffix it takes to be unique. Two
// groups may legitimately be called the same thing; their labels may
// not, because the label is what Plex matches on.
//
// "none" is taken even when no group holds it: NoneLabel is what a
// member entitled to nothing gets, and a group that answered to the same
// tag would hand them somebody else's titles.
func (s *Store) freeLabel(name string, exclude int64) (string, error) {
	base := Slug(name)
	if base == "" {
		base = "group"
	}
	for n := 1; n <= 100; n++ {
		label := LabelPrefix + base
		if n > 1 {
			label = fmt.Sprintf("%s%s_%d", LabelPrefix, base, n)
		}
		if strings.EqualFold(label, NoneLabel) {
			continue
		}
		var taken int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM share_groups
			WHERE label = ? AND id <> ?`, label, exclude).Scan(&taken); err != nil {
			return "", fmt.Errorf("label: %w", err)
		}
		if taken == 0 {
			return label, nil
		}
	}
	return "", errors.New("too many groups share that name")
}

// Managed reports whether a label is reely's to add and remove.
func Managed(label string) bool {
	return strings.HasPrefix(strings.ToLower(label), LabelPrefix)
}

// Group reads one group with its counts.
func (s *Store) Group(id int64) (*Group, error) {
	g := &Group{}
	var owner sql.NullInt64
	err := s.db.QueryRow(`
		SELECT g.id, g.name, g.label, g.owner_user_id, g.created_at, g.backfill,
		       g.everyone,
		       (SELECT COUNT(*) FROM share_group_members m WHERE m.group_id = g.id),
		       (SELECT COUNT(*) FROM entitlements e WHERE e.group_id = g.id)
		FROM share_groups g WHERE g.id = ?`, id).
		Scan(&g.ID, &g.Name, &g.Label, &owner, &g.CreatedAt, &g.Backfill,
			&g.Everyone, &g.Members, &g.Titles)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("no such group")
	}
	if err != nil {
		return nil, fmt.Errorf("group: %w", err)
	}
	g.OwnerUserID, g.Personal = owner.Int64, owner.Valid
	return g, nil
}

// Groups lists every group, personal ones last — the owner cares about
// the households they built, not the one-per-account bookkeeping.
func (s *Store) Groups() ([]Group, error) {
	rows, err := s.db.Query(`
		SELECT g.id, g.name, g.label, g.owner_user_id, g.created_at, g.backfill,
		       g.everyone,
		       (SELECT COUNT(*) FROM share_group_members m WHERE m.group_id = g.id),
		       (SELECT COUNT(*) FROM entitlements e WHERE e.group_id = g.id)
		FROM share_groups g
		ORDER BY g.owner_user_id IS NOT NULL, g.name COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("groups: %w", err)
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		var g Group
		var owner sql.NullInt64
		if err := rows.Scan(&g.ID, &g.Name, &g.Label, &owner, &g.CreatedAt,
			&g.Backfill, &g.Everyone, &g.Members, &g.Titles); err != nil {
			return nil, fmt.Errorf("groups: %w", err)
		}
		g.OwnerUserID, g.Personal = owner.Int64, owner.Valid
		out = append(out, g)
	}
	return out, rows.Err()
}

// RenameGroup moves the label with the name, so the tag in Plex keeps
// meaning what it says. Nothing has to be re-tagged by hand: the
// reconcile pass writes the whole reely-owned label set for each item it
// visits, so the old label falls off the next time it runs, and the
// entitlements that decide which items those are do not move at all.
func (s *Store) RenameGroup(id int64, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("a group needs a name")
	}
	label, err := s.freeLabel(name, id)
	if err != nil {
		return err
	}
	res, err := s.db.Exec(`UPDATE share_groups SET name = ?, label = ? WHERE id = ?`,
		name, label, id)
	if err != nil {
		return fmt.Errorf("rename group: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return errors.New("no such group")
	}
	return nil
}

// DeleteGroup removes an empty shared group. A group with members is
// refused because deleting it silently revokes every title it grants;
// emptying it first makes that consequence the caller's decision.
func (s *Store) DeleteGroup(id int64) error {
	var owner sql.NullInt64
	var members int
	err := s.db.QueryRow(`SELECT g.owner_user_id,
		(SELECT COUNT(*) FROM share_group_members m WHERE m.group_id = g.id)
		FROM share_groups g WHERE g.id = ?`, id).Scan(&owner, &members)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("no such group")
	}
	if err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	if owner.Valid {
		return errors.New("a personal group belongs to its account")
	}
	if members > 0 {
		return ErrGroupInUse
	}
	if _, err := s.db.Exec(`DELETE FROM share_groups WHERE id = ?`, id); err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	return nil
}

// AddMember puts somebody in a group. Idempotent: joining twice is the
// same as joining once.
func (s *Store) AddMember(groupID, userID int64) error {
	_, err := s.db.Exec(`INSERT INTO share_group_members (group_id, user_id)
		VALUES (?, ?) ON CONFLICT (group_id, user_id) DO NOTHING`, groupID, userID)
	if err != nil {
		return fmt.Errorf("add member: %w", err)
	}
	return nil
}

// RemoveMember takes somebody out of a group, which revokes every title
// that group grants them. A personal group is refused — an account with
// nowhere to put its own titles has no valid state.
func (s *Store) RemoveMember(groupID, userID int64) error {
	var owner sql.NullInt64
	if err := s.db.QueryRow(`SELECT owner_user_id FROM share_groups WHERE id = ?`,
		groupID).Scan(&owner); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("no such group")
		}
		return fmt.Errorf("remove member: %w", err)
	}
	if owner.Valid {
		return errors.New("a personal group keeps its owner")
	}
	if _, err := s.db.Exec(`DELETE FROM share_group_members
		WHERE group_id = ? AND user_id = ?`, groupID, userID); err != nil {
		return fmt.Errorf("remove member: %w", err)
	}
	return nil
}

// GroupsOf is every group a person belongs to.
func (s *Store) GroupsOf(userID int64) ([]Group, error) {
	rows, err := s.db.Query(`
		SELECT g.id, g.name, g.label, g.owner_user_id, g.created_at
		FROM share_groups g
		JOIN share_group_members m ON m.group_id = g.id
		WHERE m.user_id = ?
		ORDER BY g.owner_user_id IS NOT NULL, g.name COLLATE NOCASE`, userID)
	if err != nil {
		return nil, fmt.Errorf("groups of: %w", err)
	}
	defer rows.Close()
	out := []Group{}
	for rows.Next() {
		var g Group
		var owner sql.NullInt64
		if err := rows.Scan(&g.ID, &g.Name, &g.Label, &owner, &g.CreatedAt); err != nil {
			return nil, fmt.Errorf("groups of: %w", err)
		}
		g.OwnerUserID, g.Personal = owner.Int64, owner.Valid
		out = append(out, g)
	}
	return out, rows.Err()
}

// DefaultGroup is where a person's own requests land: whichever group
// they marked default, else their personal one.
func (s *Store) DefaultGroup(userID int64) (*Group, error) {
	var id int64
	err := s.db.QueryRow(`
		SELECT g.id FROM share_groups g
		JOIN share_group_members m ON m.group_id = g.id
		WHERE m.user_id = ?
		ORDER BY m.is_default DESC, g.owner_user_id IS NULL
		LIMIT 1`, userID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("this account has no group")
	}
	if err != nil {
		return nil, fmt.Errorf("default group: %w", err)
	}
	return s.Group(id)
}

// EnsurePersonalGroup creates the group a new account puts its own
// titles in. Called when an account is created; safe to call again.
func (s *Store) EnsurePersonalGroup(userID int64, name string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("personal group: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed
	label, err := s.freeLabel(name, 0)
	if err != nil {
		return err
	}
	res, err := tx.Exec(`INSERT INTO share_groups (name, label, owner_user_id)
		VALUES (?, ?, ?)
		ON CONFLICT (owner_user_id) WHERE owner_user_id IS NOT NULL DO NOTHING`,
		name, label, userID)
	if err != nil {
		return fmt.Errorf("personal group: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return tx.Commit() // already had one
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("personal group: %w", err)
	}
	if _, err := tx.Exec(`INSERT INTO share_group_members (group_id, user_id, is_default)
		VALUES (?, ?, 1)`, id, userID); err != nil {
		return fmt.Errorf("personal group: %w", err)
	}
	return tx.Commit()
}

// Grant entitles a group to a title. Idempotent, so approving the same
// title twice — or a reconcile re-running — costs nothing.
func (s *Store) Grant(groupID int64, kind string, tmdbID, tvdbID int, title string, by int64) error {
	if kind != "movie" && kind != "show" {
		return errors.New("kind must be movie or show")
	}
	if tmdbID <= 0 && tvdbID <= 0 {
		return errors.New("a title needs a tmdb or tvdb id")
	}
	_, err := s.db.Exec(`INSERT INTO entitlements
		(group_id, kind, tmdb_id, tvdb_id, title, granted_by)
		VALUES (?, ?, ?, ?, ?, ?)`,
		groupID, kind, nullID(int64(tmdbID)), nullID(int64(tvdbID)), title, nullID(by))
	if err != nil && strings.Contains(err.Error(), "UNIQUE") {
		return nil // already entitled
	}
	if err != nil {
		return fmt.Errorf("grant: %w", err)
	}
	return nil
}

// Revoke removes one entitlement.
func (s *Store) Revoke(groupID int64, kind string, tmdbID, tvdbID int) error {
	_, err := s.db.Exec(`DELETE FROM entitlements
		WHERE group_id = ? AND kind = ?
		  AND ((? > 0 AND tmdb_id = ?) OR (? > 0 AND tvdb_id = ?))`,
		groupID, kind, tmdbID, tmdbID, tvdbID, tvdbID)
	if err != nil {
		return fmt.Errorf("revoke: %w", err)
	}
	return nil
}

// LabelsFor is every label a title should carry: one per group entitled
// to it. Labels reely does not own never appear here — merging those
// back in is the writer's job, and it is the only thing standing between
// a reconcile pass and somebody's hand-made tags.
func (s *Store) LabelsFor(kind string, tmdbID, tvdbID int) ([]string, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT g.label FROM share_groups g
		JOIN entitlements e ON e.group_id = g.id
		WHERE e.kind = ?
		  AND ((? > 0 AND e.tmdb_id = ?) OR (? > 0 AND e.tvdb_id = ?))
		ORDER BY g.label`, kind, tmdbID, tmdbID, tvdbID, tvdbID)
	if err != nil {
		return nil, fmt.Errorf("labels for: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, fmt.Errorf("labels for: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// RestrictionFor is the string to write to somebody's Plex share: the
// labels of every group they belong to.
//
// Every group, including ones holding nothing yet. Naming a label no
// item carries matches no item, which costs nothing — and it keeps the
// two projections apart: a person's restriction depends on which groups
// they are IN, and never on what those groups contain. Granting a title
// is then a label write and not also a share write, and the string stops
// being rewritten the first time anything lands in somebody's own group.
//
// kind is unused for the same reason. It stays in the signature because
// Plex stores the restriction per kind and the caller writes two of
// them, so a future rule that does differ by kind has somewhere to go.
//
// A person in no group at all gets NoneLabel rather than "". Plex reads
// an empty restriction as no restriction and shows them everything, so
// the empty case is the one that has to be spelled out.
func (s *Store) RestrictionFor(userID int64, _ string) (string, error) {
	rows, err := s.db.Query(`
		SELECT DISTINCT g.label FROM share_groups g
		JOIN share_group_members m ON m.group_id = g.id
		WHERE m.user_id = ?
		ORDER BY g.label`, userID)
	if err != nil {
		return "", fmt.Errorf("restriction: %w", err)
	}
	defer rows.Close()
	var labels []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return "", fmt.Errorf("restriction: %w", err)
		}
		labels = append(labels, l)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("restriction: %w", err)
	}
	return Restriction(labels), nil
}

// Restriction formats labels the way Plex stores them: "label=" followed
// by the names, comma-separated, with the commas percent-encoded. The
// values themselves are encoded too, so a label with a space or a comma
// in it cannot break out and widen the restriction.
func Restriction(labels []string) string {
	if len(labels) == 0 {
		labels = []string{NoneLabel}
	}
	enc := make([]string, 0, len(labels))
	for _, l := range labels {
		enc = append(enc, url.QueryEscape(l))
	}
	return "label=" + strings.Join(enc, "%2C")
}

// Viewer is one person who can see a title, and the group that grants it
// to them. Two rows for the same person mean two groups grant it — worth
// showing, because revoking one of them changes nothing.
type Viewer struct {
	UserID    int64  `json:"userId"`
	Username  string `json:"username"`
	GroupID   int64  `json:"groupId"`
	GroupName string `json:"groupName"`
	Label     string `json:"label"`
	Personal  bool   `json:"personal"`
}

// SharedWith is who can see a title and why. This is the answer to the
// question an owner actually asks — "why can Sam see this?" — which the
// labels alone cannot give, since a label names a group and not the
// people in it.
func (s *Store) SharedWith(kind string, tmdbID, tvdbID int) ([]Viewer, error) {
	rows, err := s.db.Query(`
		SELECT u.id, u.username, g.id, g.name, g.label, g.owner_user_id IS NOT NULL
		FROM entitlements e
		JOIN share_groups g ON g.id = e.group_id
		JOIN share_group_members m ON m.group_id = g.id
		JOIN users u ON u.id = m.user_id
		WHERE e.kind = ?
		  AND ((? > 0 AND e.tmdb_id = ?) OR (? > 0 AND e.tvdb_id = ?))
		ORDER BY u.username COLLATE NOCASE, g.name COLLATE NOCASE`,
		kind, tmdbID, tmdbID, tvdbID, tvdbID)
	if err != nil {
		return nil, fmt.Errorf("shared with: %w", err)
	}
	defer rows.Close()
	out := []Viewer{}
	for rows.Next() {
		var v Viewer
		if err := rows.Scan(&v.UserID, &v.Username, &v.GroupID, &v.GroupName,
			&v.Label, &v.Personal); err != nil {
			return nil, fmt.Errorf("shared with: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// PlexItem is one title as the media server holds it.
type PlexItem struct {
	Kind      string
	RatingKey int64
	SectionID int64
	TmdbID    int
	TvdbID    int
	ImdbID    string
	// Title is what Plex calls it. Kept because a title that exists only
	// in Plex has no other name anywhere in reely: seeding entitles it,
	// and without this the entitlement can only be shown as its id.
	Title string
}

// CachePlexItems replaces what is known about one section. The cache is
// a projection of the media server and nothing depends on it surviving —
// an entitlement whose row is missing is simply not labelled yet, which
// is the ordinary state of a title Plex has not scanned.
func (s *Store) CachePlexItems(sectionID int64, items []PlexItem) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("cache plex items: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed
	if _, err := tx.Exec(`DELETE FROM plex_items WHERE section_id = ?`, sectionID); err != nil {
		return fmt.Errorf("cache plex items: %w", err)
	}
	for _, it := range items {
		if _, err := tx.Exec(`INSERT INTO plex_items
			(kind, rating_key, section_id, tmdb_id, tvdb_id, imdb_id, title)
			VALUES (?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (kind, rating_key) DO UPDATE SET
				section_id = excluded.section_id,
				tmdb_id = excluded.tmdb_id,
				tvdb_id = excluded.tvdb_id,
				imdb_id = excluded.imdb_id,
				title = excluded.title,
				seen_at = datetime('now')`,
			it.Kind, it.RatingKey, sectionID, nullID(int64(it.TmdbID)),
			nullID(int64(it.TvdbID)), it.ImdbID, it.Title); err != nil {
			return fmt.Errorf("cache plex items: %w", err)
		}
	}
	return tx.Commit()
}

// PlexItemFor resolves a title to what Plex calls it. A show prefers its
// TVDB id: one sourced from TheTVDB may have no TMDB id at all, and the
// server carries both.
func (s *Store) PlexItemFor(kind string, tmdbID, tvdbID int) (*PlexItem, error) {
	it := &PlexItem{Kind: kind}
	var tmdb, tvdb sql.NullInt64
	err := s.db.QueryRow(`
		SELECT rating_key, section_id, tmdb_id, tvdb_id FROM plex_items
		WHERE kind = ?
		  AND ((? > 0 AND tvdb_id = ?) OR (? > 0 AND tmdb_id = ?))
		ORDER BY (? > 0 AND tvdb_id = ?) DESC
		LIMIT 1`,
		kind, tvdbID, tvdbID, tmdbID, tmdbID, tvdbID, tvdbID).
		Scan(&it.RatingKey, &it.SectionID, &tmdb, &tvdb)
	if errors.Is(err, sql.ErrNoRows) {
		return s.plexItemByImdb(kind, tmdbID, tvdbID)
	}
	if err != nil {
		return nil, fmt.Errorf("plex item: %w", err)
	}
	it.TmdbID, it.TvdbID = int(tmdb.Int64), int(tvdb.Int64)
	return it, nil
}

// plexItemByImdb is the second attempt: reely's own row for this title
// knows its IMDb id, and Plex may hold the same film under that when the
// two disagree about TMDB — or when Plex has no TMDB id for it at all.
//
// It resolves reely's title first and then the item, rather than
// carrying IMDb on the entitlement: an entitlement names a title, and
// how many ways that title can be recognised is not its business.
func (s *Store) plexItemByImdb(kind string, tmdbID, tvdbID int) (*PlexItem, error) {
	table := "movies"
	if kind == "show" {
		table = "shows"
	}
	//nolint:gosec // table is one of two literals chosen above, never input
	q := `SELECT p.rating_key, p.section_id, p.tmdb_id, p.tvdb_id
		FROM ` + table + ` t
		JOIN plex_items p ON p.kind = ? AND p.imdb_id = t.imdb_id
		WHERE t.imdb_id <> ''
		  AND ((? > 0 AND t.tmdb_id = ?) OR (? > 0 AND t.tvdb_id = ?))
		LIMIT 1`
	args := []any{kind, tmdbID, tmdbID, tvdbID, tvdbID}
	if kind == "movie" {
		// movies carry no tvdb id, so asking for one matches nothing
		q = `SELECT p.rating_key, p.section_id, p.tmdb_id, p.tvdb_id
			FROM movies t
			JOIN plex_items p ON p.kind = ? AND p.imdb_id = t.imdb_id
			WHERE t.imdb_id <> '' AND ? > 0 AND t.tmdb_id = ?
			LIMIT 1`
		args = []any{kind, tmdbID, tmdbID}
	}
	it := &PlexItem{Kind: kind}
	var tmdb, tvdb sql.NullInt64
	err := s.db.QueryRow(q, args...).Scan(&it.RatingKey, &it.SectionID, &tmdb, &tvdb)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil // Plex has not seen it yet; try again next pass
	}
	if err != nil {
		return nil, fmt.Errorf("plex item by imdb: %w", err)
	}
	it.TmdbID, it.TvdbID = int(tmdb.Int64), int(tvdb.Int64)
	return it, nil
}

// EntitledTitle is one row the reconcile pass has to project onto Plex.
type EntitledTitle struct {
	Kind   string
	TmdbID int
	TvdbID int
	Title  string
	// OnDisk is whether reely holds a file for this title. It decides
	// what an unresolved title MEANS: without a file it is simply not
	// downloaded yet, and with one the media server is holding the same
	// film under a different identity — a mismatch to fix rather than a
	// download to wait for. Reporting both as "waiting" tells somebody
	// to be patient about a thing patience will never fix.
	OnDisk bool
}

// EntitledTitles is every distinct title anybody is entitled to. The
// pass walks these rather than the entitlements, because a title granted
// to three groups is still one item to write.
func (s *Store) EntitledTitles() ([]EntitledTitle, error) {
	// The name is resolved rather than trusted. entitlements.title is a
	// copy taken when the grant was made, and a grant can be made from a
	// source that had no name to copy — seeding entitles what is only in
	// Plex — so an empty one falls through to whichever table does know.
	// MIN over the raw column would also pick '' over a real title when
	// two rows disagree, which is the same bug from the other side.
	rows, err := s.db.Query(`
		SELECT e.kind, COALESCE(e.tmdb_id, 0), COALESCE(e.tvdb_id, 0),
		  COALESCE(
		    MIN(NULLIF(e.title, '')),
		    (SELECT m.title FROM movies m
		      WHERE e.kind = 'movie' AND m.tmdb_id = e.tmdb_id LIMIT 1),
		    (SELECT sh.title FROM shows sh
		      WHERE e.kind = 'show'
		        AND ((e.tvdb_id IS NOT NULL AND sh.tvdb_id = e.tvdb_id)
		          OR (e.tmdb_id IS NOT NULL AND sh.tmdb_id = e.tmdb_id))
		      LIMIT 1),
		    (SELECT NULLIF(p.title, '') FROM plex_items p
		      WHERE p.kind = e.kind
		        AND ((e.tmdb_id IS NOT NULL AND p.tmdb_id = e.tmdb_id)
		          OR (e.tvdb_id IS NOT NULL AND p.tvdb_id = e.tvdb_id))
		      LIMIT 1),
		    ''),
		  MAX(CASE
		    WHEN e.kind = 'movie' AND EXISTS (
		      SELECT 1 FROM movies m
		      WHERE m.tmdb_id = e.tmdb_id AND COALESCE(m.file_path, '') <> ''
		    ) THEN 1
		    WHEN e.kind = 'show' AND EXISTS (
		      SELECT 1 FROM shows sh JOIN episodes ep ON ep.show_id = sh.id
		      WHERE ((e.tvdb_id > 0 AND sh.tvdb_id = e.tvdb_id)
		          OR (e.tmdb_id > 0 AND sh.tmdb_id = e.tmdb_id))
		        AND COALESCE(ep.file_path, '') <> ''
		    ) THEN 1
		    ELSE 0 END)
		FROM entitlements e
		GROUP BY e.kind, e.tmdb_id, e.tvdb_id
		ORDER BY e.kind, 4 COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("entitled titles: %w", err)
	}
	defer rows.Close()
	out := []EntitledTitle{}
	for rows.Next() {
		var e EntitledTitle
		if err := rows.Scan(&e.Kind, &e.TmdbID, &e.TvdbID, &e.Title, &e.OnDisk); err != nil {
			return nil, fmt.Errorf("entitled titles: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// ShareState is whether reely writes somebody's Plex share, and what it
// last wrote there.
type ShareState struct {
	UserID    int64  `json:"userId"`
	Managed   bool   `json:"managed"`
	Movies    string `json:"movies"`
	Shows     string `json:"shows"`
	WrittenAt string `json:"writtenAt,omitempty"`
	// ShareAccountID is the Plex account this person watches on when it
	// is not the one they sign in with, and 0 when it is. PlexAccountID
	// is that sign-in account, so the picker can say which one "their
	// own" actually means rather than offering an unlabelled default.
	ShareAccountID int64 `json:"shareAccountId,omitempty"`
	PlexAccountID  int64 `json:"plexAccountId,omitempty"`
	// Groups is what this person belongs to, and which of them their own
	// requests land in.
	Groups []GroupRef `json:"groups,omitempty"`
}

// SetManaged turns reely's control of one person's Plex share on or off.
//
// It defaults off for everybody, including accounts that already exist.
// Turning it on NARROWS what that person can see, which is not a thing
// to do to a household at once because a migration ran — so each is
// moved across deliberately.
func (s *Store) SetManaged(userID int64, managed bool) error {
	_, err := s.db.Exec(`INSERT INTO plex_share_state (user_id, managed)
		VALUES (?, ?) ON CONFLICT (user_id) DO UPDATE SET managed = excluded.managed`,
		userID, managed)
	if err != nil {
		return fmt.Errorf("set managed: %w", err)
	}
	return nil
}

// ShareStateOf reads one person's share state; a row that was never
// written reads as unmanaged, which is the safe default.
func (s *Store) ShareStateOf(userID int64) (*ShareState, error) {
	st := &ShareState{UserID: userID}
	var written sql.NullString
	err := s.db.QueryRow(`SELECT managed, written_movies, written_shows, written_at
		FROM plex_share_state WHERE user_id = ?`, userID).
		Scan(&st.Managed, &st.Movies, &st.Shows, &written)
	if errors.Is(err, sql.ErrNoRows) {
		return st, nil
	}
	if err != nil {
		return nil, fmt.Errorf("share state: %w", err)
	}
	st.WrittenAt = written.String
	return st, nil
}

// ManagedUsers is every account reely writes the Plex share for.
//
// An account with no Plex identity at all is skipped rather than
// reported: a local-only login has no share to write, and that is a
// normal state rather than a fault.
func (s *Store) ManagedUsers() ([]int64, error) {
	rows, err := s.db.Query(`SELECT p.user_id FROM plex_share_state p
		JOIN users u ON u.id = p.user_id
		WHERE p.managed = 1 AND u.active = 1
		  AND COALESCE(u.plex_share_account_id, u.plex_account_id) IS NOT NULL
		ORDER BY p.user_id`)
	if err != nil {
		return nil, fmt.Errorf("managed users: %w", err)
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("managed users: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// RecordWrite remembers what was last sent to somebody's share. A stored
// value that no longer matches what Plex reports means a human edited it
// by hand, which is worth reporting rather than silently overwriting.
func (s *Store) RecordWrite(userID int64, movies, shows string) error {
	_, err := s.db.Exec(`INSERT INTO plex_share_state
		(user_id, managed, written_movies, written_shows, written_at)
		VALUES (?, 1, ?, ?, datetime('now'))
		ON CONFLICT (user_id) DO UPDATE SET
			written_movies = excluded.written_movies,
			written_shows = excluded.written_shows,
			written_at = excluded.written_at`, userID, movies, shows)
	if err != nil {
		return fmt.Errorf("record write: %w", err)
	}
	return nil
}

// ShareAccount is the Plex account whose share reely writes for a reely
// user: their own, unless they watch on a different one.
//
// The Plex owner is why this is not simply plex_account_id. An owner is
// absent from their own sharing list and cannot be restricted, so an
// owner who wants their own view watches on a second account while the
// owner account keeps the token. Both are the same person here.
func (s *Store) ShareAccount(userID int64) (int64, error) {
	var share, signin sql.NullInt64
	err := s.db.QueryRow(`SELECT plex_share_account_id, plex_account_id
		FROM users WHERE id = ?`, userID).Scan(&share, &signin)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, errors.New("no such account")
	}
	if err != nil {
		return 0, fmt.Errorf("share account: %w", err)
	}
	if share.Valid && share.Int64 > 0 {
		return share.Int64, nil
	}
	return signin.Int64, nil
}

// SetShareAccount points a reely account at the Plex account it watches
// on. Zero clears it, which puts them back on the account they sign in
// with.
func (s *Store) SetShareAccount(userID, plexAccountID int64) error {
	_, err := s.db.Exec(`UPDATE users SET plex_share_account_id = ? WHERE id = ?`,
		nullID(plexAccountID), userID)
	if err != nil {
		return fmt.Errorf("set share account: %w", err)
	}
	return nil
}

// HeldTitles is everything the seed has to cover: what reely imported,
// AND what Plex holds.
//
// Both, because they are not the same set. reely knows the titles it
// added; a Plex server has usually been running for years and holds
// whatever was put there before reely existed. Seeding from reely alone
// would leave those unlabelled, so the first person switched to managed
// would lose the larger part of what they can see today — the exact
// failure the seed exists to prevent.
//
// The Plex side reads the item cache, so a seed is only as complete as
// the last reconcile pass; the handler refreshes it first.
func (s *Store) HeldTitles() ([]EntitledTitle, error) {
	rows, err := s.db.Query(`
		SELECT kind, tmdb_id, tvdb_id, MAX(title) FROM (
			SELECT 'movie' AS kind, COALESCE(tmdb_id, 0) AS tmdb_id,
			       0 AS tvdb_id, title FROM movies
			UNION ALL
			SELECT 'show', COALESCE(tmdb_id, 0), COALESCE(tvdb_id, 0), title FROM shows
			UNION ALL
			SELECT kind, COALESCE(tmdb_id, 0), COALESCE(tvdb_id, 0), title FROM plex_items
		)
		GROUP BY kind, tmdb_id, tvdb_id`)
	if err != nil {
		return nil, fmt.Errorf("held titles: %w", err)
	}
	defer rows.Close()
	out := []EntitledTitle{}
	for rows.Next() {
		var e EntitledTitle
		if err := rows.Scan(&e.Kind, &e.TmdbID, &e.TvdbID, &e.Title); err != nil {
			return nil, fmt.Errorf("held titles: %w", err)
		}
		if e.TmdbID <= 0 && e.TvdbID <= 0 {
			continue // nothing to match it to in Plex
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// BackfillGroup is the group holding what was already in Plex, creating
// it the first time with everybody in it.
//
// The owner does not assemble this one. Its membership is "everybody who
// can already see the library", its contents are "everything that was
// already there", and neither is a judgement call — offering it as a
// group to build would only be a way to build it wrong.
func (s *Store) BackfillGroup(name string) (*Group, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM share_groups WHERE backfill = 1`).Scan(&id)
	if err == nil {
		return s.Group(id)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("backfill group: %w", err)
	}
	label, err := s.freeLabel(name, 0)
	if err != nil {
		return nil, err
	}
	res, err := s.db.Exec(`INSERT INTO share_groups (name, label, backfill)
		VALUES (?, ?, 1)`, name, label)
	if err != nil {
		return nil, fmt.Errorf("backfill group: %w", err)
	}
	id, err = res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("backfill group: %w", err)
	}
	return s.Group(id)
}

// SetEveryoneGroup marks which group is the household, or clears it.
//
// One per install: "everyone" is singular, and two of them would mean
// two answers to who the house is. Marking a second moves the flag
// rather than refusing, because the owner saying "this one" is a
// clearer instruction than an error telling them to unset the other.
//
// A personal group can never be it — that one is somebody's own corner,
// and the whole point of this flag is the opposite.
func (s *Store) SetEveryoneGroup(groupID int64, on bool) error {
	g, err := s.Group(groupID)
	if err != nil {
		return err
	}
	if on && g.Personal {
		return errors.New("a personal group cannot be the everyone group")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("everyone group: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed
	if on {
		if _, err := tx.Exec(`UPDATE share_groups SET everyone = 0 WHERE everyone = 1`); err != nil {
			return fmt.Errorf("everyone group: %w", err)
		}
	}
	if _, err := tx.Exec(`UPDATE share_groups SET everyone = ? WHERE id = ?`,
		boolInt(on), groupID); err != nil {
		return fmt.Errorf("everyone group: %w", err)
	}
	return tx.Commit()
}

// EveryoneGroupID is the household group, or 0 where the owner has not
// named one — which is every install that has not asked for it.
func (s *Store) EveryoneGroupID() (int64, error) {
	var id int64
	err := s.db.QueryRow(`SELECT id FROM share_groups WHERE everyone = 1`).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("everyone group: %w", err)
	}
	return id, nil
}

// JoinEveryone puts one account in the household group, if there is one.
//
// Called as an account is created, which is the difference between this
// and the backfill group's frozen membership. A latecomer never had the
// old library, so backfill deliberately leaves them out; the household
// is the people the owner shares Plex with, and somebody who just got an
// account is one of them.
func (s *Store) JoinEveryone(userID int64) error {
	id, err := s.EveryoneGroupID()
	if err != nil || id == 0 {
		return err
	}
	return s.AddMember(id, userID)
}

// SyncEveryoneMembers puts every active account in the household group,
// for the owner marking an existing group as everyone after the fact.
func (s *Store) SyncEveryoneMembers(groupID int64) (int, error) {
	res, err := s.db.Exec(`INSERT INTO share_group_members (group_id, user_id)
		SELECT ?, id FROM users WHERE active = 1
		ON CONFLICT (group_id, user_id) DO NOTHING`, groupID)
	if err != nil {
		return 0, fmt.Errorf("everyone members: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// SyncBackfillMembers puts every active account in the backfill group.
//
// Run at seed time and whenever the owner asks, never on its own: a
// person who joins later never had access to the old library, so there
// is nothing of theirs to preserve, and handing them the lot because
// they signed in would be a decision reely has no business making.
func (s *Store) SyncBackfillMembers(groupID int64) (int, error) {
	res, err := s.db.Exec(`INSERT INTO share_group_members (group_id, user_id)
		SELECT ?, id FROM users WHERE active = 1
		ON CONFLICT (group_id, user_id) DO NOTHING`, groupID)
	if err != nil {
		return 0, fmt.Errorf("backfill members: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

// GrantHeld entitles a group to everything the install already holds,
// and reports how many rows that added. Idempotent, so running it twice
// is the same as running it once.
func (s *Store) GrantHeld(groupID, by int64) (int, error) {
	titles, err := s.HeldTitles()
	if err != nil {
		return 0, err
	}
	before, err := s.Group(groupID)
	if err != nil {
		return 0, err
	}
	for _, t := range titles {
		if err := s.Grant(groupID, t.Kind, t.TmdbID, t.TvdbID, t.Title, by); err != nil {
			return 0, err
		}
	}
	after, err := s.Group(groupID)
	if err != nil {
		return 0, err
	}
	return after.Titles - before.Titles, nil
}

// SetTitleGroups replaces which groups may see one title. This is the
// per-title editor: an admin fixing a mistake without re-adding
// anything, or sharing something with somebody after the fact.
func (s *Store) SetTitleGroups(kind string, tmdbID, tvdbID int, title string, groupIDs []int64, by int64) error {
	if kind != "movie" && kind != "show" {
		return errors.New("kind must be movie or show")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("set title groups: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed
	if _, err := tx.Exec(`DELETE FROM entitlements WHERE kind = ?
		AND ((? > 0 AND tmdb_id = ?) OR (? > 0 AND tvdb_id = ?))`,
		kind, tmdbID, tmdbID, tvdbID, tvdbID); err != nil {
		return fmt.Errorf("set title groups: %w", err)
	}
	for _, gid := range groupIDs {
		if _, err := tx.Exec(`INSERT INTO entitlements
			(group_id, kind, tmdb_id, tvdb_id, title, granted_by)
			VALUES (?, ?, ?, ?, ?, ?)`,
			gid, kind, nullID(int64(tmdbID)), nullID(int64(tvdbID)), title,
			nullID(by)); err != nil {
			return fmt.Errorf("set title groups: %w", err)
		}
	}
	return tx.Commit()
}

// GroupMemberIDs is who is in a group.
func (s *Store) GroupMemberIDs(groupID int64) ([]int64, error) {
	rows, err := s.db.Query(`SELECT user_id FROM share_group_members
		WHERE group_id = ? ORDER BY user_id`, groupID)
	if err != nil {
		return nil, fmt.Errorf("group members: %w", err)
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("group members: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// SetGroupMembers replaces a group's membership, because the UI edits it
// as a set of ticks rather than one person at a time.
//
// A personal group is refused outright: it belongs to its account, and
// an account with nowhere to put its own titles has no valid state.
func (s *Store) SetGroupMembers(groupID int64, userIDs []int64) error {
	var owner sql.NullInt64
	if err := s.db.QueryRow(`SELECT owner_user_id FROM share_groups WHERE id = ?`,
		groupID).Scan(&owner); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return errors.New("no such group")
		}
		return fmt.Errorf("set members: %w", err)
	}
	if owner.Valid {
		return errors.New("a personal group belongs to its account")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("set members: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed
	if _, err := tx.Exec(`DELETE FROM share_group_members WHERE group_id = ?`,
		groupID); err != nil {
		return fmt.Errorf("set members: %w", err)
	}
	for _, uid := range userIDs {
		if _, err := tx.Exec(`INSERT INTO share_group_members (group_id, user_id)
			VALUES (?, ?) ON CONFLICT (group_id, user_id) DO NOTHING`,
			groupID, uid); err != nil {
			return fmt.Errorf("set members: %w", err)
		}
	}
	return tx.Commit()
}

// TitleGroupIDs is which groups may see one title, for the editor to
// tick.
func (s *Store) TitleGroupIDs(kind string, tmdbID, tvdbID int) ([]int64, error) {
	rows, err := s.db.Query(`SELECT group_id FROM entitlements
		WHERE kind = ? AND ((? > 0 AND tmdb_id = ?) OR (? > 0 AND tvdb_id = ?))
		ORDER BY group_id`, kind, tmdbID, tmdbID, tvdbID, tvdbID)
	if err != nil {
		return nil, fmt.Errorf("title groups: %w", err)
	}
	defer rows.Close()
	out := []int64{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("title groups: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// ShareStates is every account's share state, for the UI to render the
// switches without a call each. An account with no row reads as
// unmanaged, which is the safe default and the one most will have.
func (s *Store) ShareStates() ([]ShareState, error) {
	rows, err := s.db.Query(`
		SELECT u.id, COALESCE(p.managed, 0), COALESCE(p.written_movies, ''),
		       COALESCE(p.written_shows, ''), p.written_at,
		       COALESCE(u.plex_share_account_id, 0), COALESCE(u.plex_account_id, 0)
		FROM users u LEFT JOIN plex_share_state p ON p.user_id = u.id
		WHERE u.active = 1
		ORDER BY u.username COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("share states: %w", err)
	}
	defer rows.Close()
	out := []ShareState{}
	for rows.Next() {
		var st ShareState
		var written sql.NullString
		if err := rows.Scan(&st.UserID, &st.Managed, &st.Movies, &st.Shows, &written,
			&st.ShareAccountID, &st.PlexAccountID); err != nil {
			return nil, fmt.Errorf("share states: %w", err)
		}
		st.WrittenAt = written.String
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("share states: %w", err)
	}
	for i := range out {
		groups, err := s.GroupRefsOf(out[i].UserID)
		if err != nil {
			return nil, err
		}
		out[i].Groups = groups
	}
	return out, nil
}

// RequestTargets is which groups a fulfilled request is granted to.
//
// audience nil means every group the asker is in except the backfill: a
// household member asking for a film usually means the household should
// get it. The backfill has to be excluded — everybody is in that one, so
// including it would send every new request to everybody and there would
// be no split at all. A named
// set narrows it — somebody in two households choosing one of them, or
// an empty set keeping it to themselves, which is the case the choice
// exists for. With a shared group every request is otherwise a public
// act, and somebody asking for a gift has no way to keep it quiet.
//
// Their own group is always included and any group they do not belong to
// is dropped. A request the asker cannot then watch is nobody's intent,
// and a named group is a claim from the requesting side, so it is
// checked here rather than trusted.
func (s *Store) RequestTargets(userID int64, audience []int64) ([]int64, error) {
	mine, err := s.GroupRefsOf(userID)
	if err != nil {
		return nil, err
	}
	if len(mine) == 0 {
		return nil, errors.New("this account has no group")
	}
	want := map[int64]bool{}
	for _, g := range mine {
		if g.Personal {
			want[g.ID] = true // never droppable
		}
	}
	// The back catalogue holds what was already in Plex before the split
	// and everybody is in it, so granting a title there shares it with
	// the whole install. That is the owner's call to make and nobody
	// else's — and never something that just happens, so it takes an
	// owner who named it outright.
	//
	// Whose account it is decides this, not which door they came in by.
	// An owner asking for a film from the portal while away from home is
	// still the owner; the portal is where they are, not who they are.
	//
	// Skipping it only when no audience was named left the rule to
	// whichever client built the list, and a client that ticks every
	// group a person belongs to named it every time.
	owner, err := s.isAdmin(userID)
	if err != nil {
		return nil, err
	}
	asked := map[int64]bool{}
	for _, id := range audience {
		asked[id] = true
	}
	for _, g := range mine {
		named := audience != nil && asked[g.ID]
		// Neither house-wide group is ever a request default. The
		// backfill group holds what was already in Plex; an everyone
		// group is the household itself. Granting a title in either
		// shares it with the whole install, which is the owner's call to
		// make and nobody else's — and never something that just happens,
		// so it takes an owner who named it outright.
		if (g.Backfill || g.Everyone) && (!owner || !named) {
			continue
		}
		if audience == nil || named {
			want[g.ID] = true
		}
	}
	out := make([]int64, 0, len(want))
	for _, g := range mine {
		if want[g.ID] {
			out = append(out, g.ID)
		}
	}
	// An owner may name the back catalogue whether or not they belong to
	// it. Membership says what somebody SEES; this says what they may
	// share into, and that is the role. Deriving it from membership made
	// the capability depend on whether a sync had happened to include
	// them — so an owner could be refused the one group only they may
	// use, for a reason nothing on screen would explain.
	if owner && audience != nil {
		bf, err := s.BackfillGroupRef()
		if err != nil {
			return nil, err
		}
		if bf != nil && asked[bf.ID] && !want[bf.ID] {
			out = append(out, bf.ID)
		}
	}
	return out, nil
}

// GroupRef is one of a person's groups. Personal marks their own — the
// one a request always reaches, and the one they cannot opt out of.
// Backfill marks the group holding what was already in Plex before the
// split — everybody is in it, and nothing new ever goes to it.
type GroupRef struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Personal bool   `json:"personal"`
	Backfill bool   `json:"backfill"`
	Everyone bool   `json:"everyone"`
}

// GroupRefsOf is every group a person belongs to. Safe to show that
// person: they already know which households they are in, and it is not
// the install-wide list.
func (s *Store) GroupRefsOf(userID int64) ([]GroupRef, error) {
	rows, err := s.db.Query(`
		SELECT g.id, g.name, g.owner_user_id IS NOT NULL, g.backfill, g.everyone
		FROM share_groups g JOIN share_group_members m ON m.group_id = g.id
		WHERE m.user_id = ?
		ORDER BY g.owner_user_id IS NOT NULL, g.name COLLATE NOCASE`, userID)
	if err != nil {
		return nil, fmt.Errorf("groups of: %w", err)
	}
	defer rows.Close()
	out := []GroupRef{}
	for rows.Next() {
		var g GroupRef
		if err := rows.Scan(&g.ID, &g.Name, &g.Personal, &g.Backfill, &g.Everyone); err != nil {
			return nil, fmt.Errorf("groups of: %w", err)
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// ShareMode is what a bulk change does to the groups a title already has.
type ShareMode string

const (
	// ShareAdd leaves what a title already has and adds to it. The
	// ordinary case: forty films picked out of a grid for one household
	// were not picked to have their other audiences revoked.
	ShareAdd ShareMode = "add"
	// ShareRemove takes those groups away and leaves the rest.
	ShareRemove ShareMode = "remove"
	// ShareReplace makes the named groups the whole audience.
	ShareReplace ShareMode = "replace"
)

// BulkShare changes who can see many titles at once, in one transaction.
//
// One transaction rather than a loop over the single-title path because
// that path schedules a reconcile per call: forty titles would mean
// forty passes over the library, all but the last of them wasted.
func (s *Store) BulkShare(mode ShareMode, groupIDs []int64, titles []EntitledTitle, by int64) (int, error) {
	switch mode {
	case ShareAdd, ShareRemove, ShareReplace:
	default:
		return 0, errors.New("mode must be add, remove or replace")
	}
	if len(groupIDs) == 0 && mode != ShareReplace {
		return 0, errors.New("pick at least one group")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("bulk share: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	changed := 0
	for _, t := range titles {
		if t.Kind != "movie" && t.Kind != "show" {
			return 0, errors.New("kind must be movie or show")
		}
		if t.TmdbID <= 0 && t.TvdbID <= 0 {
			continue // nothing to match it to in Plex
		}
		if mode == ShareReplace {
			if _, err := tx.Exec(`DELETE FROM entitlements WHERE kind = ?
				AND ((? > 0 AND tmdb_id = ?) OR (? > 0 AND tvdb_id = ?))`,
				t.Kind, t.TmdbID, t.TmdbID, t.TvdbID, t.TvdbID); err != nil {
				return 0, fmt.Errorf("bulk share: %w", err)
			}
		}
		for _, gid := range groupIDs {
			if mode == ShareRemove {
				if _, err := tx.Exec(`DELETE FROM entitlements
					WHERE group_id = ? AND kind = ?
					  AND ((? > 0 AND tmdb_id = ?) OR (? > 0 AND tvdb_id = ?))`,
					gid, t.Kind, t.TmdbID, t.TmdbID, t.TvdbID, t.TvdbID); err != nil {
					return 0, fmt.Errorf("bulk share: %w", err)
				}
				continue
			}
			// a title already shared with a group stays shared with it
			// once, so picking a run that overlaps costs nothing
			if _, err := tx.Exec(`INSERT INTO entitlements
				(group_id, kind, tmdb_id, tvdb_id, title, granted_by)
				VALUES (?, ?, ?, ?, ?, ?)
				ON CONFLICT DO NOTHING`,
				gid, t.Kind, nullID(int64(t.TmdbID)), nullID(int64(t.TvdbID)),
				t.Title, nullID(by)); err != nil {
				return 0, fmt.Errorf("bulk share: %w", err)
			}
		}
		changed++
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("bulk share: %w", err)
	}
	return changed, nil
}

// A title carries two ids, and a grant may name either. Which one it
// names is not a choice anybody made: seeding entitles what the media
// server holds, and Plex records whichever id the agent that matched the
// item supplied — so a show reely knows by both ids is routinely granted
// by only one of them.
//
// Collapsing that to a single key, as this did, means a grant naming the
// TMDB id never matches a show whose row also has a TVDB id. The title
// simply vanishes from the portal while remaining perfectly watchable in
// Plex, and its owner sees it shared with nobody. Everything else that
// matches a grant to a title — Revoke, SetTitleGroups, PlexItemFor —
// already accepts either id, so this is the odd one out rather than a
// convention.
//
// The two spaces stay apart because a TMDB id and a TVDB id are
// unrelated numbers that can coincide, and one flat set would let a
// title through on a collision that means nothing.

// TitleGroups says which groups are entitled to a title.
type TitleGroups struct {
	Tmdb map[int][]int64
	Tvdb map[int][]int64
}

// For is every group entitled to a title carrying these ids, by either
// of them, without repeating a group that is named by both.
func (g TitleGroups) For(tmdbID, tvdbID int) []int64 {
	var out []int64
	seen := map[int64]bool{}
	for _, id := range [][2]int{{tvdbID, 1}, {tmdbID, 0}} {
		if id[0] <= 0 {
			continue
		}
		from := g.Tmdb
		if id[1] == 1 {
			from = g.Tvdb
		}
		for _, gid := range from[id[0]] {
			if !seen[gid] {
				seen[gid] = true
				out = append(out, gid)
			}
		}
	}
	return out
}

// GroupsByTitle is every entitlement of a kind, indexed by both ids, so
// the owner's listing can say who each title is shared with.
func (s *Store) GroupsByTitle(kind string) (TitleGroups, error) {
	g := TitleGroups{Tmdb: map[int][]int64{}, Tvdb: map[int][]int64{}}
	rows, err := s.db.Query(`
		SELECT COALESCE(tmdb_id, 0), COALESCE(tvdb_id, 0), group_id
		FROM entitlements WHERE kind = ?`, kind)
	if err != nil {
		return g, fmt.Errorf("groups by title: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var tmdb, tvdb int
		var group int64
		if err := rows.Scan(&tmdb, &tvdb, &group); err != nil {
			return g, fmt.Errorf("groups by title: %w", err)
		}
		if tmdb > 0 {
			g.Tmdb[tmdb] = append(g.Tmdb[tmdb], group)
		}
		if tvdb > 0 {
			g.Tvdb[tvdb] = append(g.Tvdb[tvdb], group)
		}
	}
	return g, rows.Err()
}

// Visible is the set of titles one account may see, by either id.
type Visible struct {
	Tmdb map[int]bool
	Tvdb map[int]bool
}

// Has reports whether a title carrying these ids is entitled.
func (v Visible) Has(tmdbID, tvdbID int) bool {
	return (tvdbID > 0 && v.Tvdb[tvdbID]) || (tmdbID > 0 && v.Tmdb[tmdbID])
}

// VisibleTitles is what one account may see of a kind: everything their
// groups hold.
//
// Used to scope the portal's own listing, so reely stops advertising
// titles somebody cannot play — and stops showing them what other
// households have.
func (s *Store) VisibleTitles(userID int64, kind string) (Visible, error) {
	v := Visible{Tmdb: map[int]bool{}, Tvdb: map[int]bool{}}
	rows, err := s.db.Query(`
		SELECT DISTINCT COALESCE(e.tmdb_id, 0), COALESCE(e.tvdb_id, 0)
		FROM entitlements e
		JOIN share_group_members m ON m.group_id = e.group_id
		WHERE m.user_id = ? AND e.kind = ?`, userID, kind)
	if err != nil {
		return v, fmt.Errorf("visible titles: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var tmdb, tvdb int
		if err := rows.Scan(&tmdb, &tvdb); err != nil {
			return v, fmt.Errorf("visible titles: %w", err)
		}
		if tmdb > 0 {
			v.Tmdb[tmdb] = true
		}
		if tvdb > 0 {
			v.Tvdb[tvdb] = true
		}
	}
	return v, rows.Err()
}

// StaleEntitlement is a grant pointing at a title nothing on this
// install has any more.
type StaleEntitlement struct {
	Kind   string
	TmdbID int
	TvdbID int
	Title  string // whatever name it had, for the log
}

// SweepStale ages out entitlements that can never resolve, and reports
// the ones it dropped.
//
// A grant is a candidate when reely has no row for the title AND the
// media server has no item for it. Seeding is where they come from: it
// entitles what the media server holds, so a server holding a wrong
// match produces a grant keyed on that wrong id — and correcting the
// match over there leaves the grant pointing at nothing.
//
// Two things are NOT this, however alike they look in the waiting list:
//
//   - A title somebody asked for and has not downloaded. A request
//     creates the row in movies or shows that reely searches on, and the
//     presence of that row is the whole difference.
//   - Anything at all, on a pass where a section failed to scan. An
//     empty section makes every grant for it indistinguishable from an
//     orphan, so a single observation is never enough.
//
// Hence the clock. missing_since is set the first time a grant fails to
// resolve and cleared the moment it resolves again, so a transient
// failure costs a timestamp and nothing else. Only a grant that stays
// unresolvable for minAge across healthy passes is dropped.
//
// The caller must only run this after a sweep that actually read the
// server, and one that did not report errors.
func (s *Store) SweepStale(minAge time.Duration) ([]StaleEntitlement, error) {
	var cached int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM plex_items`).Scan(&cached); err != nil {
		return nil, fmt.Errorf("sweep stale: %w", err)
	}
	if cached == 0 {
		return nil, nil // nothing was read; nothing is provably anything
	}

	// A kind whose cache is empty is a section that did not answer, and
	// nothing of that kind is judged on a pass that could not see it.
	// This is the guard the clock alone does not give: a section broken
	// for longer than minAge would otherwise age its grants out.
	const unheld = `
		EXISTS (SELECT 1 FROM plex_items pk WHERE pk.kind = e.kind)
		AND NOT EXISTS (SELECT 1 FROM movies m
			WHERE e.kind = 'movie' AND m.tmdb_id = e.tmdb_id)
		AND NOT EXISTS (SELECT 1 FROM shows sh
			WHERE e.kind = 'show'
			  AND ((e.tvdb_id IS NOT NULL AND sh.tvdb_id = e.tvdb_id)
			    OR (e.tmdb_id IS NOT NULL AND sh.tmdb_id = e.tmdb_id)))
		AND NOT EXISTS (SELECT 1 FROM plex_items p
			WHERE p.kind = e.kind
			  AND ((e.tmdb_id IS NOT NULL AND p.tmdb_id = e.tmdb_id)
			    OR (e.tvdb_id IS NOT NULL AND p.tvdb_id = e.tvdb_id)))`

	tx, err := s.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("sweep stale: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	// anything holding the title again is not missing, whatever it was
	// last pass — this is what makes a transient failure harmless
	if _, err := tx.Exec(`UPDATE entitlements AS e
		SET missing_since = NULL
		WHERE missing_since IS NOT NULL AND NOT (` + unheld + `)`); err != nil {
		return nil, fmt.Errorf("sweep stale: %w", err)
	}
	if _, err := tx.Exec(`UPDATE entitlements AS e
		SET missing_since = datetime('now')
		WHERE missing_since IS NULL AND ` + unheld); err != nil {
		return nil, fmt.Errorf("sweep stale: %w", err)
	}

	cutoff := fmt.Sprintf("-%d seconds", int(minAge.Seconds()))
	const old = `missing_since IS NOT NULL
		AND missing_since <= datetime('now', ?)`

	rows, err := tx.Query(`SELECT DISTINCT e.kind,
		COALESCE(e.tmdb_id, 0), COALESCE(e.tvdb_id, 0), MAX(e.title)
		FROM entitlements e WHERE `+old+` AND `+unheld+`
		GROUP BY e.kind, e.tmdb_id, e.tvdb_id`, cutoff)
	if err != nil {
		return nil, fmt.Errorf("sweep stale: %w", err)
	}
	out := []StaleEntitlement{}
	for rows.Next() {
		var st StaleEntitlement
		if err := rows.Scan(&st.Kind, &st.TmdbID, &st.TvdbID, &st.Title); err != nil {
			rows.Close()
			return nil, fmt.Errorf("sweep stale: %w", err)
		}
		out = append(out, st)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("sweep stale: %w", err)
	}
	rows.Close()

	if len(out) > 0 {
		if _, err := tx.Exec(`DELETE FROM entitlements AS e
			WHERE `+old+` AND `+unheld, cutoff); err != nil {
			return nil, fmt.Errorf("sweep stale: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("sweep stale: %w", err)
	}
	return out, nil
}

// MoveEntitlements follows a title that changed identity.
//
// Re-matching replaces the ids on a title. The grants naming it are keyed
// on those ids, so without this they keep pointing at the identity that
// was wrong: the title is never labelled under its new id, and everybody
// entitled to it quietly loses it on the media server. The grant is about
// the title, not the number that happened to name it.
//
// A group already entitled to the new identity keeps the one grant it
// has. That is the ordinary case after seeding from a server that held
// the same file under a different match: the group holds both sides, and
// re-matching makes them the same title, so the leftover is dropped
// rather than duplicated.
func (s *Store) MoveEntitlements(kind string, oldTmdb, oldTvdb, newTmdb, newTvdb int) (int, error) {
	if oldTmdb == newTmdb && oldTvdb == newTvdb {
		return 0, nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("move entitlements: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // no-op once committed

	moved := 0
	// OR IGNORE leaves behind any row whose group already holds the new
	// identity; the delete that follows clears those, so the group keeps
	// the one grant it has rather than gaining a duplicate.
	move := func(update, del string, old, id int) error {
		if old <= 0 {
			return nil
		}
		res, err := tx.Exec(update, nullID(int64(id)), kind, old)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		moved += int(n)
		_, err = tx.Exec(del, kind, old)
		return err
	}
	if err := move(
		`UPDATE OR IGNORE entitlements SET tmdb_id = ?, missing_since = NULL
			WHERE kind = ? AND tmdb_id = ?`,
		`DELETE FROM entitlements WHERE kind = ? AND tmdb_id = ?`,
		oldTmdb, newTmdb); err != nil {
		return 0, fmt.Errorf("move entitlements: %w", err)
	}
	if err := move(
		`UPDATE OR IGNORE entitlements SET tvdb_id = ?, missing_since = NULL
			WHERE kind = ? AND tvdb_id = ?`,
		`DELETE FROM entitlements WHERE kind = ? AND tvdb_id = ?`,
		oldTvdb, newTvdb); err != nil {
		return 0, fmt.Errorf("move entitlements: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("move entitlements: %w", err)
	}
	return moved, nil
}

// isAdmin reports whether an account may do the things only an owner may.
func (s *Store) isAdmin(userID int64) (bool, error) {
	var role string
	err := s.db.QueryRow(`SELECT role FROM users WHERE id = ?`, userID).Scan(&role)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("role: %w", err)
	}
	return role == "admin", nil
}

// BackfillGroupRef is the back catalogue as a picker row, or nil when
// this install has none. Separate from GroupRefsOf because belonging to
// a group and being allowed to share into it are different things: an
// owner may do the second without the first.
func (s *Store) BackfillGroupRef() (*GroupRef, error) {
	g := &GroupRef{Backfill: true}
	err := s.db.QueryRow(`SELECT id, name FROM share_groups WHERE backfill = 1`).
		Scan(&g.ID, &g.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("backfill group: %w", err)
	}
	return g, nil
}
