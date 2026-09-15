-- Every other name a show answers to, as a JSON string array. TVDB names
-- same-named shows with a year suffix ("Monster (2022)") and carries the
-- alternate titles releases actually use — an anthology's seasons each
-- release under their own subtitle ("Monsters: The Lyle and Erik
-- Menendez Story"). Matching needs them all.
ALTER TABLE shows ADD COLUMN aliases TEXT NOT NULL DEFAULT '[]';
