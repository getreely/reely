-- reely:rebuild
-- The minimum format score used zero as its off switch, which made zero
-- itself unusable as a floor — and "nothing that scores negative" is the
-- most common floor there is. The column becomes nullable: NULL is "no
-- floor", zero is a real floor. Every stored zero meant "no floor" under
-- the old encoding, so zeros migrate to NULL and nothing changes behavior.
CREATE TABLE quality_profiles_new (
    id               INTEGER PRIMARY KEY,
    name             TEXT NOT NULL UNIQUE,
    qualities        TEXT NOT NULL DEFAULT '["1080p","720p"]',
    cutoff           TEXT NOT NULL DEFAULT '1080p',
    upgrades         INTEGER NOT NULL DEFAULT 1,
    hdr              TEXT NOT NULL DEFAULT 'allow' CHECK (hdr IN ('allow', 'require', 'block')),
    min_mb_per_min   INTEGER NOT NULL DEFAULT 0,
    max_mb_per_min   INTEGER NOT NULL DEFAULT 0,
    sources          TEXT NOT NULL DEFAULT '[]',
    source_cutoff    TEXT NOT NULL DEFAULT '',
    required         TEXT NOT NULL DEFAULT '[]',
    blocked          TEXT NOT NULL DEFAULT '[]',
    preferred        TEXT NOT NULL DEFAULT '[]',
    min_format_score INTEGER,
    created_at       TEXT NOT NULL DEFAULT (datetime('now'))
);
INSERT INTO quality_profiles_new (id, name, qualities, cutoff, upgrades, hdr,
        min_mb_per_min, max_mb_per_min, sources, source_cutoff,
        required, blocked, preferred, min_format_score, created_at)
    SELECT id, name, qualities, cutoff, upgrades, hdr,
        min_mb_per_min, max_mb_per_min, sources, source_cutoff,
        required, blocked, preferred, NULLIF(min_format_score, 0), created_at
    FROM quality_profiles;
DROP TABLE quality_profiles;
ALTER TABLE quality_profiles_new RENAME TO quality_profiles;
