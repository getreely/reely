-- WEBDL and WEBRip used to collapse into one "web" source, so a WEBRip
-- satisfied a web cutoff exactly as a WEB-DL did and neither could ever
-- upgrade to the other. They are separate rungs now (webrip below webdl),
-- and everything already stored has to be moved onto the new vocabulary —
-- carefully, because a value the loop no longer recognizes ranks as
-- unknown, which reads as "below every cutoff" and would send an entire
-- library off hunting upgrades it does not need.

-- A profile that allowed "web" allowed both kinds of web release, so it
-- must go on allowing both. Quoting the value in the LIKE and the replace
-- keeps it from matching inside "webrip" or "webdl" on a re-run.
UPDATE quality_profiles
   SET sources = replace(sources, '"web"', '"webdl","webrip"')
 WHERE sources LIKE '%"web"%';

-- A cutoff of "web" meant "a web release is good enough". webrip is the
-- lower of the two rungs, so aiming at it keeps both satisfying the
-- cutoff — exactly the old behaviour. Aiming at webdl instead would put
-- every existing web file below cutoff overnight.
UPDATE quality_profiles SET source_cutoff = 'webrip' WHERE source_cutoff = 'web';

-- Files on disk: nothing recorded which kind they were, and a container
-- cannot say. webrip is the conservative reading — it claims less rather
-- than more, and it keeps files at or above a migrated 'webrip' cutoff so
-- nothing is re-grabbed on account of this migration.
UPDATE movies   SET source = 'webrip' WHERE source = 'web';
UPDATE episodes SET source = 'webrip' WHERE source = 'web';

-- A custom format matching on source = web meant "any web release", which
-- no single source value can express now. The equivalent is a title rule
-- covering both spellings, which is what the guides' own generic WEB
-- entry becomes on import.
UPDATE custom_formats
   SET specs = (
        SELECT json_group_array(
                 CASE WHEN json_extract(value, '$.kind')  = 'source'
                       AND json_extract(value, '$.value') = 'web'
                      THEN json_set(value, '$.kind', 'title',
                                           '$.value', '\bWEB[-_. ]?(DL|Rip)\b')
                      ELSE json(value) END)
          FROM json_each(custom_formats.specs))
 WHERE EXISTS (SELECT 1 FROM json_each(specs)
                WHERE json_extract(value, '$.kind')  = 'source'
                  AND json_extract(value, '$.value') = 'web');
