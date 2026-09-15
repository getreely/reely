-- The TVDB id, carried along from TMDB's external ids. It is the key most
-- usenet indexers accept for TV searches (newznab tvsearch tvdbid=), so
-- storing it lets the search paths ask by id the way Sonarr does. Filled
-- on add and refreshed with the rest of the metadata; 0 = not known yet.
ALTER TABLE shows ADD COLUMN tvdb_id INTEGER NOT NULL DEFAULT 0;
