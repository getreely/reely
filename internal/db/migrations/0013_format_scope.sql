-- Custom formats gain a scope: movies or shows, set automatically by which
-- guides collection they were imported from. The same name may exist once
-- per scope — the guides' movie and show versions of a format genuinely
-- differ (different release groups, different patterns) — so uniqueness
-- moves to (name, applies_to). Rows imported before scoping existed default
-- to movies, the likelier origin.

CREATE TABLE custom_formats_new (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL,
    applies_to TEXT NOT NULL DEFAULT 'movies' CHECK (applies_to IN ('movies', 'shows')),
    score      INTEGER NOT NULL DEFAULT 0,
    specs      TEXT NOT NULL DEFAULT '[]',
    trash_id   TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (name, applies_to)
);
INSERT INTO custom_formats_new (id, name, score, specs, trash_id, created_at)
    SELECT id, name, score, specs, trash_id, created_at FROM custom_formats;
DROP TABLE custom_formats;
ALTER TABLE custom_formats_new RENAME TO custom_formats;
