-- Custom-format specs converted from the guides' web SourceSpecifications
-- were stored as kind "title" — the same AND-group as the service-token
-- patterns, which OR'd them together and made every web release match
-- every streaming-service format. They belong in their own group, kind
-- "source_title". The three patterns here are exactly (and only) what the
-- importer ever emitted for web sources, so rewriting by exact value is
-- safe; negate/required flags ride along untouched.
UPDATE custom_formats SET specs = replace(specs,
  '"kind":"title","value":"\\bWEB[-_. ]?DL\\b"',
  '"kind":"source_title","value":"\\bWEB[-_. ]?DL\\b"');
UPDATE custom_formats SET specs = replace(specs,
  '"kind":"title","value":"\\bWEB[-_. ]?Rip\\b"',
  '"kind":"source_title","value":"\\bWEB[-_. ]?Rip\\b"');
UPDATE custom_formats SET specs = replace(specs,
  '"kind":"title","value":"\\bWEB[-_. ]?(DL|Rip)\\b"',
  '"kind":"source_title","value":"\\bWEB[-_. ]?(DL|Rip)\\b"');
