-- The IMDb id of what Plex holds, as a second way to recognise a title.
--
-- Matching on TMDB alone leaves two gaps, both seen on a real library.
-- An item Plex matched through an agent that supplied no TMDB id cannot
-- be joined at all — it carries an IMDb id and nothing else, so reely
-- has an entitlement it can never label. And where reely and Plex
-- matched the same file to different TMDB ids, the seed produces two
-- entitlements for one film: the one keyed on Plex's id gets labelled
-- and the one keyed on reely's waits forever.
--
-- IMDb closes both. It is the id the two agree on when they disagree
-- about everything else.
ALTER TABLE plex_items ADD COLUMN imdb_id TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_plex_items_imdb ON plex_items(kind, imdb_id) WHERE imdb_id <> '';
