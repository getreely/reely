-- Per-movie minimum availability, Radarr-style. 'released' (the default)
-- keeps the automatic paths from grabbing releases before the movie is
-- actually out — the window where every "WEB-DL" is mislabeled or fake.
-- 'announced' lifts the gate for a title you expect early. Manual grabs
-- are never gated.
ALTER TABLE movies ADD COLUMN min_availability TEXT NOT NULL DEFAULT 'released';
