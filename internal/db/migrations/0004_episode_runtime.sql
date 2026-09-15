-- Per-episode runtime (minutes), from TMDB's season listings. The release
-- scorer's size band is MB per minute, so without a runtime TV releases
-- would skip the size check entirely. Backfills on the next scan.

ALTER TABLE episodes ADD COLUMN runtime INTEGER;
