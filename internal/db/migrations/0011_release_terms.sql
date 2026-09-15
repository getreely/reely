-- Release terms: per-profile custom-format rules judged against the raw
-- release name. required = every term must appear; blocked = any match
-- rejects; preferred = matches add their score to the release's ranking
-- (positive prefers, negative deprioritizes without blocking).

ALTER TABLE quality_profiles ADD COLUMN required  TEXT NOT NULL DEFAULT '[]'; -- JSON array of terms
ALTER TABLE quality_profiles ADD COLUMN blocked   TEXT NOT NULL DEFAULT '[]'; -- JSON array of terms
ALTER TABLE quality_profiles ADD COLUMN preferred TEXT NOT NULL DEFAULT '[]'; -- JSON array of {term, score}
