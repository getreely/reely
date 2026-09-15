-- The group that is the whole house.
--
-- Distinct from backfill, which looks similar and is not. Backfill holds
-- what was already in Plex before the split, and its membership is
-- deliberately frozen at seed time: somebody who joins later never had
-- the old library, so handing them all of it because they signed in
-- would be a decision reely has no business making.
--
-- An everyone group is the opposite on exactly that point. It means "the
-- people I share Plex with", so a new account belongs in it the moment
-- it exists. A fresh install has no backfill and no way to say this,
-- which left the owner making an ordinary group called Everyone — and an
-- ordinary group is a request default, so the first person to ask for
-- something shared it with the entire house.
--
-- What the two share is that neither is ever a request default. Only the
-- owner puts a title in front of everybody.
ALTER TABLE share_groups ADD COLUMN everyone INTEGER NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX idx_share_groups_everyone ON share_groups(everyone)
    WHERE everyone = 1;
