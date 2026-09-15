-- reely:rebuild — runs with foreign_keys OFF on a pinned connection.
--
-- Per-user collections: the same title may now live in several libraries,
-- so movies/shows uniqueness moves from tmdb_id alone to (tmdb_id,
-- library_id). SQLite can't alter a UNIQUE constraint in place, hence the
-- rebuild. Ids are preserved, so credits/history/blocklist references
-- stay valid; child tables reference the table NAME, which comes back.

CREATE TABLE movies_new (
    id              INTEGER PRIMARY KEY,
    tmdb_id         INTEGER,
    title           TEXT NOT NULL,
    year            INTEGER,
    overview        TEXT NOT NULL DEFAULT '',
    runtime         INTEGER,
    genres          TEXT NOT NULL DEFAULT '[]',
    release_date    TEXT,
    digital_release TEXT,
    poster_path     TEXT NOT NULL DEFAULT '',
    backdrop_path   TEXT NOT NULL DEFAULT '',
    imdb_id         TEXT NOT NULL DEFAULT '',
    library_id      INTEGER REFERENCES libraries(id) ON DELETE SET NULL,
    quality_profile_id INTEGER REFERENCES quality_profiles(id) ON DELETE SET NULL,
    monitored       INTEGER NOT NULL DEFAULT 1,
    file_path       TEXT,
    file_size       INTEGER,
    quality         TEXT NOT NULL DEFAULT '',
    added_at        TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (tmdb_id, library_id)
);
INSERT INTO movies_new (id, tmdb_id, title, year, overview, runtime, genres,
    release_date, digital_release, poster_path, backdrop_path, imdb_id,
    library_id, quality_profile_id, monitored, file_path, file_size, quality, added_at)
SELECT id, tmdb_id, title, year, overview, runtime, genres,
    release_date, digital_release, poster_path, backdrop_path, imdb_id,
    library_id, quality_profile_id, monitored, file_path, file_size, quality, added_at
FROM movies;
DROP TABLE movies;
ALTER TABLE movies_new RENAME TO movies;
CREATE INDEX idx_movies_library ON movies(library_id);
CREATE INDEX idx_movies_tmdb ON movies(tmdb_id);

CREATE TABLE shows_new (
    id            INTEGER PRIMARY KEY,
    tmdb_id       INTEGER,
    title         TEXT NOT NULL,
    year          INTEGER,
    overview      TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL DEFAULT '',
    genres        TEXT NOT NULL DEFAULT '[]',
    poster_path   TEXT NOT NULL DEFAULT '',
    backdrop_path TEXT NOT NULL DEFAULT '',
    imdb_id       TEXT NOT NULL DEFAULT '',
    library_id    INTEGER REFERENCES libraries(id) ON DELETE SET NULL,
    quality_profile_id INTEGER REFERENCES quality_profiles(id) ON DELETE SET NULL,
    monitored     INTEGER NOT NULL DEFAULT 1,
    added_at      TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (tmdb_id, library_id)
);
INSERT INTO shows_new (id, tmdb_id, title, year, overview, status, genres,
    poster_path, backdrop_path, imdb_id, library_id, quality_profile_id, monitored, added_at)
SELECT id, tmdb_id, title, year, overview, status, genres,
    poster_path, backdrop_path, imdb_id, library_id, quality_profile_id, monitored, added_at
FROM shows;
DROP TABLE shows;
ALTER TABLE shows_new RENAME TO shows;
CREATE INDEX idx_shows_library ON shows(library_id);
CREATE INDEX idx_shows_tmdb ON shows(tmdb_id);
