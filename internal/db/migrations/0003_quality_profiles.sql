-- Quality profiles: what a library wants the grab loop to fetch. Size
-- limits are MB per minute of runtime (0 = unbounded) so one band serves a
-- 45-minute episode and a three-hour movie alike.

CREATE TABLE quality_profiles (
    id             INTEGER PRIMARY KEY,
    name           TEXT NOT NULL UNIQUE,
    qualities      TEXT NOT NULL DEFAULT '["1080p","720p"]',  -- JSON array, allowed resolutions
    cutoff         TEXT NOT NULL DEFAULT '1080p',             -- stop upgrading at/above
    upgrades       INTEGER NOT NULL DEFAULT 1,
    hdr            TEXT NOT NULL DEFAULT 'allow' CHECK (hdr IN ('allow', 'require', 'block')),
    min_mb_per_min INTEGER NOT NULL DEFAULT 0,
    max_mb_per_min INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL DEFAULT (datetime('now'))
);

-- The profile attaches per title: every movie and every show carries one
-- (episodes follow their show). The library's profile is only the default a
-- new title inherits on import.
ALTER TABLE libraries ADD COLUMN quality_profile_id INTEGER REFERENCES quality_profiles(id) ON DELETE SET NULL;
ALTER TABLE movies    ADD COLUMN quality_profile_id INTEGER REFERENCES quality_profiles(id) ON DELETE SET NULL;
ALTER TABLE shows     ADD COLUMN quality_profile_id INTEGER REFERENCES quality_profiles(id) ON DELETE SET NULL;

-- One sensible default so a fresh install (and every existing library) can
-- grab the moment the loop lands: 1080p web/bluray territory, 8–80 MB/min —
-- a 148-minute movie computes to a 1.2–11.6 GB window, a 45-minute episode
-- to 0.4–3.5 GB. The 20 GB remux never fits; neither does the 700 MB fake.
INSERT INTO quality_profiles (name, qualities, cutoff, upgrades, min_mb_per_min, max_mb_per_min)
VALUES ('Standard 1080p', '["1080p","720p"]', '1080p', 1, 8, 80);

UPDATE libraries SET quality_profile_id = (SELECT id FROM quality_profiles WHERE name = 'Standard 1080p');
UPDATE movies    SET quality_profile_id = (SELECT id FROM quality_profiles WHERE name = 'Standard 1080p');
UPDATE shows     SET quality_profile_id = (SELECT id FROM quality_profiles WHERE name = 'Standard 1080p');
