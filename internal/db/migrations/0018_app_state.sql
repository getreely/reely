-- Runtime state that must survive a restart, one row per key, overwritten
-- in place — the RSS sync's feed cursors live here. Not settings: nothing
-- in this table is a user's choice.
CREATE TABLE app_state (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL DEFAULT ''
);
