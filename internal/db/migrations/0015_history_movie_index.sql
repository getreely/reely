-- History is queried by movie far more than it is written: the pending-grab
-- gate, grab-target lookup and now the "when did this land" timestamp on the
-- movie list all filter on movie_id. Without an index each of those scans
-- the whole table, which the home page would do once per movie.
CREATE INDEX IF NOT EXISTS idx_history_movie ON history(movie_id, kind);
