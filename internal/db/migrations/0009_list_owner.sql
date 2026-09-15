-- Lists remember who created them: an mdblist list syncs with its
-- creator's personal api key when they have one, falling back to the
-- install-wide key. Deleting the user keeps the list running on the
-- fallback.
ALTER TABLE lists ADD COLUMN created_by INTEGER REFERENCES users(id) ON DELETE SET NULL;
