-- Movies get aliases too (TMDB's alternative titles plus the original
-- title), as a JSON string array: foreign and shortened release names
-- ("Leon" for "Léon: The Professional") are the same movie, and Radarr
-- matches through exactly this list.
ALTER TABLE movies ADD COLUMN aliases TEXT NOT NULL DEFAULT '[]';
