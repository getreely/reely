-- People reely learned about from TVDB rather than TMDB.
--
-- The table was keyed on tmdb_id alone, which was true while every cast
-- list came from TMDB. A show TMDB does not carry got no cast at all,
-- and TVDB's own characters could not be stored: with no TMDB id they
-- would all have landed on tmdb_id = 0 and collided with each other on
-- the unique index.
--
-- Most TVDB people do carry a TMDB id in their remoteIds and resolve
-- onto the existing row, which is what keeps one actor from becoming two
-- people. This column is for the rest — a real credited actor TMDB has
-- never heard of, who is better stored without a filmography than left
-- off the cast list.
-- Unique but not partial, matching how tmdb_id is declared on the table.
-- SQLite treats NULLs as distinct in a unique index, so every person
-- without a TVDB id still stores; and an upsert's ON CONFLICT target has
-- to match the index it names, which a partial index would have made it
-- carry the WHERE clause too.
ALTER TABLE people ADD COLUMN tvdb_id INTEGER;
CREATE UNIQUE INDEX idx_people_tvdb ON people(tvdb_id);
