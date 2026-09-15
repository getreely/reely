-- Keep the title Plex has for an item.
--
-- reely already read it on every sweep and threw it away, which was fine
-- until seeding started entitling things that exist only in Plex. Those
-- got an entitlement with an empty title, and the waiting list could
-- only call them "movie 424242" — reely knew the name and had discarded
-- it one line earlier.
ALTER TABLE plex_items ADD COLUMN title TEXT NOT NULL DEFAULT '';

-- Name the entitlements reely can name today. The cache repopulates on
-- the next sweep, so anything only Plex knows is named then; this is for
-- the ones reely's own library could have named all along.
UPDATE entitlements SET title = COALESCE((
    SELECT m.title FROM movies m
    WHERE entitlements.kind = 'movie' AND m.tmdb_id = entitlements.tmdb_id
    LIMIT 1
), '')
WHERE title = '' AND kind = 'movie' AND tmdb_id IS NOT NULL;

UPDATE entitlements SET title = COALESCE((
    SELECT sh.title FROM shows sh
    WHERE entitlements.kind = 'show'
      AND ((entitlements.tvdb_id IS NOT NULL AND sh.tvdb_id = entitlements.tvdb_id)
        OR (entitlements.tmdb_id IS NOT NULL AND sh.tmdb_id = entitlements.tmdb_id))
    LIMIT 1
), '')
WHERE title = '' AND kind = 'show';
