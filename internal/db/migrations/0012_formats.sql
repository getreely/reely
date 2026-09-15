-- Custom formats: TRaSH-style scoring rules, stored once for the whole
-- install (the Settings → Custom Formats tab) and applied by every
-- profile's release ranking. Specs are judged against the release: title
-- and group regexes, the parsed source, the resolution.

CREATE TABLE custom_formats (
    id         INTEGER PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    score      INTEGER NOT NULL DEFAULT 0,
    specs      TEXT NOT NULL DEFAULT '[]', -- JSON array of {kind, value, negate, required}
    trash_id   TEXT NOT NULL DEFAULT '',   -- guides id, for re-import dedupe
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Profiles gain a floor: releases whose format score (preferred terms +
-- custom formats) lands below it are rejected outright.
ALTER TABLE quality_profiles ADD COLUMN min_format_score INTEGER NOT NULL DEFAULT 0;
