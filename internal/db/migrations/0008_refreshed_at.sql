-- Metadata refresh bookkeeping: when a title's TMDB record was last
-- re-fetched. NULL (existing rows) means "never" — maximally due, so the
-- first refresh pass sweeps the whole catalog over its paced passes.
ALTER TABLE movies ADD COLUMN refreshed_at TEXT;
ALTER TABLE shows  ADD COLUMN refreshed_at TEXT;
