-- The blocklist bans a release, and a release is a name AT an indexer.
-- Indexers carry each other's posts under identical names, so banning a
-- name everywhere threw away good copies: one indexer's upload fails to
-- unpack, and reely stopped trying the same release from anywhere else.
-- Uniqueness moves to (release_title, indexer). Rows banned before the
-- indexer was recorded keep an empty indexer, which reads as "everywhere"
-- — the safe reading for a ban whose origin is unknown.

CREATE TABLE blocklist_new (
    id            INTEGER PRIMARY KEY,
    release_title TEXT NOT NULL,
    indexer       TEXT NOT NULL DEFAULT '',
    movie_id      INTEGER REFERENCES movies(id) ON DELETE SET NULL,
    show_id       INTEGER REFERENCES shows(id) ON DELETE SET NULL,
    reason        TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (release_title, indexer)
);
INSERT INTO blocklist_new (id, release_title, movie_id, show_id, reason, created_at)
    SELECT id, release_title, movie_id, show_id, reason, created_at FROM blocklist;
DROP TABLE blocklist;
ALTER TABLE blocklist_new RENAME TO blocklist;
