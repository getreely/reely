-- Per-show release numbering, for shows TMDB splits differently than the
-- release scene does. TVDB (whose numbering release groups follow) keeps a
-- revival as the original's next season, while TMDB starts a new show at
-- season 1 — "Kitchen Nightmares" (2023) is TMDB's own S1-S3 but releases
-- as S8-S10. season_offset is what to ADD to a catalog season number to
-- get the number its releases carry. tvdb_override supplies the TVDB id
-- for id-keyed searches when TMDB records none (a split entry usually
-- maps to nothing); unlike tvdb_id it is user-set and survives refreshes.
ALTER TABLE shows ADD COLUMN season_offset INTEGER NOT NULL DEFAULT 0;
ALTER TABLE shows ADD COLUMN tvdb_override INTEGER NOT NULL DEFAULT 0;
