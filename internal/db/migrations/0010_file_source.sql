-- Source-aware quality: alongside the resolution, files remember where the
-- encode came from (dvd/hdtv/web/bluray/remux, '' = unknown), and profiles
-- can restrict sources and keep upgrading within a resolution until a
-- source cutoff is met. Existing rows start unknown; scans backfill any
-- file whose name still carries a source token.

ALTER TABLE movies   ADD COLUMN source TEXT NOT NULL DEFAULT '';
ALTER TABLE episodes ADD COLUMN source TEXT NOT NULL DEFAULT '';

ALTER TABLE quality_profiles ADD COLUMN sources TEXT NOT NULL DEFAULT '[]'; -- JSON array, empty = any
ALTER TABLE quality_profiles ADD COLUMN source_cutoff TEXT NOT NULL DEFAULT ''; -- '' = resolution only
