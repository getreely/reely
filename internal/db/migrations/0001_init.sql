-- Reely core schema. Identity rule: every movie, show, and person converges
-- on its TMDB id; that one number is always enough to re-match.

CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    role          TEXT NOT NULL DEFAULT 'user' CHECK (role IN ('admin', 'user')),
    created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);

-- token column holds a SHA-256 of the bearer value; the raw token exists
-- only in the login cookie
CREATE TABLE sessions (
    token      TEXT PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    expires_at TEXT NOT NULL
);
CREATE INDEX idx_sessions_expiry ON sessions(expires_at);

-- single-row-per-key app settings; credential values are sealed (see
-- internal/settings)
CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);

-- A library is a folder with a kind: movie libraries hold movies, show
-- libraries hold shows. The sidebar split falls straight out of this.
CREATE TABLE libraries (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL UNIQUE,
    path        TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK (kind IN ('movies', 'shows')),
    monitor_new INTEGER NOT NULL DEFAULT 1,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Per-user library access: a row grants a 'user'-role account one library.
-- Admins see everything and have no rows here.
CREATE TABLE user_libraries (
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    library_id INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, library_id)
);
CREATE INDEX idx_user_libraries_user ON user_libraries(user_id);

CREATE TABLE movies (
    id              INTEGER PRIMARY KEY,
    tmdb_id         INTEGER UNIQUE,
    title           TEXT NOT NULL,
    year            INTEGER,
    overview        TEXT NOT NULL DEFAULT '',
    runtime         INTEGER,
    genres          TEXT NOT NULL DEFAULT '[]',
    release_date    TEXT,  -- theatrical
    digital_release TEXT,  -- when it's worth hunting
    poster_path     TEXT NOT NULL DEFAULT '',
    backdrop_path   TEXT NOT NULL DEFAULT '',
    library_id      INTEGER REFERENCES libraries(id) ON DELETE SET NULL,
    monitored       INTEGER NOT NULL DEFAULT 1,
    file_path       TEXT,
    file_size       INTEGER,
    quality         TEXT NOT NULL DEFAULT '',
    added_at        TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_movies_library ON movies(library_id);

CREATE TABLE shows (
    id            INTEGER PRIMARY KEY,
    tmdb_id       INTEGER UNIQUE,
    title         TEXT NOT NULL,
    year          INTEGER,
    overview      TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT '',  -- continuing / ended / …
    genres        TEXT NOT NULL DEFAULT '[]',
    poster_path   TEXT NOT NULL DEFAULT '',
    backdrop_path TEXT NOT NULL DEFAULT '',
    library_id    INTEGER REFERENCES libraries(id) ON DELETE SET NULL,
    monitored     INTEGER NOT NULL DEFAULT 1,
    added_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_shows_library ON shows(library_id);

CREATE TABLE seasons (
    id       INTEGER PRIMARY KEY,
    show_id  INTEGER NOT NULL REFERENCES shows(id) ON DELETE CASCADE,
    number   INTEGER NOT NULL,
    name     TEXT NOT NULL DEFAULT '',
    UNIQUE (show_id, number)
);

CREATE TABLE episodes (
    id        INTEGER PRIMARY KEY,
    show_id   INTEGER NOT NULL REFERENCES shows(id) ON DELETE CASCADE,
    season    INTEGER NOT NULL,
    episode   INTEGER NOT NULL,
    tmdb_id   INTEGER,
    title     TEXT NOT NULL DEFAULT '',
    overview  TEXT NOT NULL DEFAULT '',
    air_date  TEXT,
    monitored INTEGER NOT NULL DEFAULT 1,
    file_path TEXT,
    file_size INTEGER,
    quality   TEXT NOT NULL DEFAULT '',
    UNIQUE (show_id, season, episode)
);
CREATE INDEX idx_episodes_show ON episodes(show_id);
CREATE INDEX idx_episodes_air ON episodes(air_date);

CREATE TABLE people (
    id         INTEGER PRIMARY KEY,
    tmdb_id    INTEGER UNIQUE,
    name       TEXT NOT NULL,
    photo_path TEXT NOT NULL DEFAULT ''
);

-- one row per person-per-title; movie_id and show_id are mutually exclusive
CREATE TABLE credits (
    id        INTEGER PRIMARY KEY,
    person_id INTEGER NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    movie_id  INTEGER REFERENCES movies(id) ON DELETE CASCADE,
    show_id   INTEGER REFERENCES shows(id) ON DELETE CASCADE,
    character TEXT NOT NULL DEFAULT '',
    ord       INTEGER NOT NULL DEFAULT 0,
    CHECK ((movie_id IS NULL) != (show_id IS NULL))
);
CREATE INDEX idx_credits_person ON credits(person_id);
CREATE INDEX idx_credits_movie ON credits(movie_id);
CREATE INDEX idx_credits_show ON credits(show_id);

CREATE TABLE history (
    id         INTEGER PRIMARY KEY,
    kind       TEXT NOT NULL,  -- imported / grabbed / renamed / removed / …
    movie_id   INTEGER REFERENCES movies(id) ON DELETE SET NULL,
    show_id    INTEGER REFERENCES shows(id) ON DELETE SET NULL,
    episode_id INTEGER REFERENCES episodes(id) ON DELETE SET NULL,
    detail     TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_history_created ON history(created_at);
