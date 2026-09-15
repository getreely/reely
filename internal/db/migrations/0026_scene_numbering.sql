-- Scene numbering, from TheXEM: the season/episode a release carries when
-- the scene disagrees with TheTVDB. TVDB's aired order sometimes merges
-- two broadcast seasons into one ("Kitchen Nightmares (US)" folds the 2007
-- and 2008 runs into a 22-episode S1), and every release after that point
-- is numbered a season ahead of the catalog. XEM is the community map
-- between the two, and it is per EPISODE, not per season: a split or
-- merged episode moves on its own.
--
-- NULL means "no mapping" — the episode's own numbers are what its
-- releases carry, which is the overwhelming majority of episodes. The
-- show-level season_offset stays as the manual fallback for shows XEM
-- does not cover.
ALTER TABLE episodes ADD COLUMN scene_season INTEGER;
ALTER TABLE episodes ADD COLUMN scene_episode INTEGER;
