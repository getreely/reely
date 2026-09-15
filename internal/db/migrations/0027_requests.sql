-- Requests: a Plex user asks for a title, the owner approves, and the add
-- that already exists runs. Everything here is additive — no table
-- rebuild, which matters because rebuilding users would take sessions,
-- user_libraries and every history reference with it.
--
-- Permission rides on its own column rather than a new role. SQLite
-- cannot alter the CHECK constraint on users.role in place, and a
-- rebuild to add one value is a poor trade when may_add composes with
-- the two roles already there: an admin is an admin, a 'user' who may
-- add is what every account is today, and a 'user' who may not is a
-- requester.
ALTER TABLE users ADD COLUMN auth_provider TEXT NOT NULL DEFAULT 'local';
ALTER TABLE users ADD COLUMN plex_account_id INTEGER;
ALTER TABLE users ADD COLUMN plex_username TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN may_add INTEGER NOT NULL DEFAULT 1;
ALTER TABLE users ADD COLUMN active INTEGER NOT NULL DEFAULT 1;
ALTER TABLE users ADD COLUMN auto_approve_movies INTEGER NOT NULL DEFAULT 0;
ALTER TABLE users ADD COLUMN auto_approve_shows INTEGER NOT NULL DEFAULT 0;
-- NULL is no limit, which is what every existing account gets
ALTER TABLE users ADD COLUMN quota_movies_week INTEGER;
ALTER TABLE users ADD COLUMN quota_shows_week INTEGER;

-- Partial, so the many local accounts with no Plex id don't collide on
-- NULL. The id is the only thing an account is matched on at login:
-- emails change hands, and matching on one would hand an account to
-- whoever registered that address at Plex.
CREATE UNIQUE INDEX idx_users_plex ON users(plex_account_id)
    WHERE plex_account_id IS NOT NULL;

-- Where a person's requests land when they can reach more than one
-- library. Theirs to set, not the owner's — with one library there is no
-- question to ask and no setting to show.
ALTER TABLE user_libraries ADD COLUMN is_default INTEGER NOT NULL DEFAULT 0;

-- title/year/poster are copied rather than looked up: a denied request
-- still has to read as something, and the title it named may never be
-- added to this install at all.
CREATE TABLE requests (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    library_id  INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL CHECK (kind IN ('movie', 'show')),
    tmdb_id     INTEGER,
    tvdb_id     INTEGER,
    title       TEXT NOT NULL,
    year        INTEGER,
    poster_path TEXT NOT NULL DEFAULT '',
    -- JSON array of season numbers; NULL means the whole show
    seasons     TEXT,
    status      TEXT NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'approved', 'denied')),
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    decided_at  TEXT,
    decided_by  INTEGER REFERENCES users(id) ON DELETE SET NULL
);

-- One open request per title per library, so the second person to ask
-- sees it as already requested instead of opening a duplicate. Denied
-- rows fall out of the index, which is the whole implementation of
-- "a denied title can be asked for again".
CREATE UNIQUE INDEX idx_requests_open ON requests(library_id, kind, tmdb_id)
    WHERE status IN ('pending', 'approved');

CREATE INDEX idx_requests_user ON requests(user_id, created_at);
CREATE INDEX idx_requests_status ON requests(status, created_at);
