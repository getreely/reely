-- The blocklist: releases that failed after grabbing. Automatic grab paths
-- refuse to fetch a blocklisted release name again; deleting the row is the
-- pardon.
CREATE TABLE blocklist (
    id            INTEGER PRIMARY KEY,
    release_title TEXT NOT NULL UNIQUE,
    movie_id      INTEGER REFERENCES movies(id) ON DELETE SET NULL,
    show_id       INTEGER REFERENCES shows(id) ON DELETE SET NULL,
    reason        TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);
