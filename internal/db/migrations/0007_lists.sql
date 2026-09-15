-- Watched lists: external lists (TMDB charts and lists, Trakt charts and
-- public lists) that feed a library. The sync adds new items monitored —
-- the same gesture as adding by hand, on a schedule.
CREATE TABLE lists (
    id          INTEGER PRIMARY KEY,
    name        TEXT NOT NULL,
    source      TEXT NOT NULL,             -- tmdb_chart | tmdb_list | trakt_chart | trakt_list
    config      TEXT NOT NULL DEFAULT '{}',-- source-specific: chart name, list id, user/slug
    library_id  INTEGER NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    item_limit  INTEGER NOT NULL DEFAULT 20, -- charts are endless; 0 = no cap
    enabled     INTEGER NOT NULL DEFAULT 1,
    last_synced TEXT,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX idx_lists_library ON lists(library_id);
