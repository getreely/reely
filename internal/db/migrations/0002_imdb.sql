-- IMDb ids come along free with every TMDB detail fetch; storing them gives
-- detail pages their IMDb link. Existing rows backfill on the next scan
-- (UpsertMovie/UpsertShow refresh metadata in place).

ALTER TABLE movies ADD COLUMN imdb_id TEXT NOT NULL DEFAULT '';
ALTER TABLE shows  ADD COLUMN imdb_id TEXT NOT NULL DEFAULT '';
