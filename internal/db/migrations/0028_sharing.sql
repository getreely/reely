-- Sharing: which people may see which titles in Plex.
--
-- Plex enforces this with two mechanisms, both confirmed against a live
-- server before this was written. A share carries a per-kind restriction
-- string ("label=a%2Cb%2Cc", the values comma-separated and percent-
-- encoded), and every item carries labels. A user sees an item when the
-- item holds one of the labels their restriction names — the labels OR
-- together, and a restriction naming a label nothing carries shows
-- nothing rather than everything, which is the only reason it is safe to
-- derive these strings from a table that could be empty.
--
-- Entitlement hangs off a GROUP rather than a user. A household of three
-- is one group with three members; one person's own titles are a group
-- with one member. That collapses what would otherwise be two parallel
-- code paths into one, and it means adding somebody to a household costs
-- a single restriction string rather than a re-tag of every title in it.

-- A group's label is what Plex sees. The name is what the owner sees.
-- owner_user_id set marks a personal group — the one somebody's own
-- requests land in — and NULL marks a shared one.
--
-- The label is derived from the name so that somebody looking at a film
-- in Plex can tell what "Reely.jolene_family" means without coming back
-- here. It moves when the group is renamed, which costs nothing: the
-- reconcile pass computes the whole reely-owned label set for an item
-- and writes that, so the old label falls off the next time it runs.
--
-- The label collates NOCASE because Plex title-cases tags on write: a
-- label sent as "reely.jolene_family" reads back as
-- "Reely.jolene_family". Every comparison against a label has to survive
-- that, and doing it in the collation means no caller can forget.
CREATE TABLE share_groups (
    id            INTEGER PRIMARY KEY,
    name          TEXT NOT NULL,
    label         TEXT NOT NULL COLLATE NOCASE,
    owner_user_id INTEGER REFERENCES users(id) ON DELETE CASCADE,
    created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE UNIQUE INDEX idx_share_groups_label ON share_groups(label);
CREATE UNIQUE INDEX idx_share_groups_owner ON share_groups(owner_user_id)
    WHERE owner_user_id IS NOT NULL;

-- is_default is where a member's own requests land when they belong to
-- more than one group, mirroring user_libraries.is_default. Theirs to
-- set, like that one.
CREATE TABLE share_group_members (
    group_id   INTEGER NOT NULL REFERENCES share_groups(id) ON DELETE CASCADE,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    is_default INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX idx_share_group_members_user ON share_group_members(user_id);

-- One group may see one title. Both ids are carried because a show that
-- came from TheTVDB may have no TMDB id at all (see 0022_show_source),
-- and resolving a show to its Plex item prefers tvdb_id for that reason;
-- movies are TMDB-only. title is copied so a revoked or not-yet-held
-- entitlement still reads as something in the UI, the same reason
-- requests copies it.
CREATE TABLE entitlements (
    id         INTEGER PRIMARY KEY,
    group_id   INTEGER NOT NULL REFERENCES share_groups(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('movie', 'show')),
    tmdb_id    INTEGER,
    tvdb_id    INTEGER,
    title      TEXT NOT NULL DEFAULT '',
    granted_at TEXT NOT NULL DEFAULT (datetime('now')),
    granted_by INTEGER REFERENCES users(id) ON DELETE SET NULL,
    CHECK (tmdb_id IS NOT NULL OR tvdb_id IS NOT NULL)
);
CREATE UNIQUE INDEX idx_entitlements_tmdb ON entitlements(group_id, kind, tmdb_id)
    WHERE tmdb_id IS NOT NULL;
CREATE UNIQUE INDEX idx_entitlements_tvdb ON entitlements(group_id, kind, tvdb_id)
    WHERE tvdb_id IS NOT NULL;

-- Cache of what Plex holds, keyed by the ids reely already knows. Pure
-- projection of the media server — droppable and rebuildable at any
-- time, so it can never be the thing that breaks. An entitlement with no
-- row here is simply not labelled yet: the file may have landed seconds
-- ago and Plex may not have scanned it, which is why nothing waits on
-- this and the reconcile pass just tries again.
CREATE TABLE plex_items (
    kind       TEXT NOT NULL CHECK (kind IN ('movie', 'show')),
    rating_key INTEGER NOT NULL,
    section_id INTEGER NOT NULL,
    tmdb_id    INTEGER,
    tvdb_id    INTEGER,
    seen_at    TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (kind, rating_key)
);
CREATE INDEX idx_plex_items_tmdb ON plex_items(kind, tmdb_id) WHERE tmdb_id IS NOT NULL;
CREATE INDEX idx_plex_items_tvdb ON plex_items(kind, tvdb_id) WHERE tvdb_id IS NOT NULL;

-- What was last written to a person's Plex share, and whether reely is
-- allowed to write it at all.
--
-- managed defaults to 0 for everybody who already exists. An install
-- whose shares are unrestricted today keeps them that way until each
-- person is moved across deliberately — turning this on for somebody
-- narrows what they can see, and that is not a thing to do to six people
-- at once because a migration ran.
--
-- The written_* columns are the last strings reely sent. Plex accepts an
-- update it then ignores and still answers 200, so a write is only
-- believed after reading it back; and a stored value that no longer
-- matches what Plex reports means a human edited the share by hand,
-- which is worth reporting rather than silently overwriting.
CREATE TABLE plex_share_state (
    user_id          INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    managed          INTEGER NOT NULL DEFAULT 0,
    written_movies   TEXT NOT NULL DEFAULT '',
    written_shows    TEXT NOT NULL DEFAULT '',
    written_at       TEXT
);

-- Everybody who already exists gets their personal group, so no code
-- path has to cope with a member who has nowhere to put a title. The
-- label follows the username, which is already unique — so these cannot
-- collide with each other, and no group exists yet to collide with.
INSERT INTO share_groups (name, label, owner_user_id)
    SELECT username, 'reely.' || replace(lower(trim(username)), ' ', '_'), id
    FROM users;
INSERT INTO share_group_members (group_id, user_id, is_default)
    SELECT id, owner_user_id, 1 FROM share_groups WHERE owner_user_id IS NOT NULL;
