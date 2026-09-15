-- Which provider a show's metadata comes from. 'tmdb' is the original and
-- the fallback; 'tvdb' rows (installs that configured a TVDB API key) get
-- their identity and episode tree from TheTVDB — whose numbering release
-- groups follow — while cast, discovery, and artwork fallbacks still join
-- through tmdb_id when it is known. Movies stay TMDB-only.
ALTER TABLE shows ADD COLUMN source TEXT NOT NULL DEFAULT 'tmdb';
