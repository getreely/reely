# Metadata

Metadata comes from [TMDB](https://www.themoviedb.org/): titles,
artwork, overviews, genres, runtimes, air dates, digital release dates,
episode lists, and people. You bring your own API key — free with a TMDB
account — pasted once in Settings → Metadata (or the wizard). The key is
sealed at rest and the API only ever reports whether it's set, never the
value. This product uses the TMDB API but is not endorsed or certified
by TMDB.

## TVDB for shows (optional)

Release groups number episodes the way [TheTVDB](https://thetvdb.com)
does, and TVDB sometimes disagrees with TMDB about where one show ends
and another begins — TVDB keeps a revival as the original's next season
(Kitchen Nightmares is one series, S1–S9), where TMDB splits it into a
second show restarting at Season 1. That split breaks searching: the
revival's episodes release under numbers its TMDB entry doesn't have.

With a **TVDB v4 API key** (free tier at thetvdb.com/api-information)
pasted in Settings → Metadata, shows switch source: the search bar's
show results, adds, refreshes, and library scans all go through TVDB —
one entry per show, native release numbering, no offsets. Movies, cast,
people pages, and the discovery rows stay on TMDB (TVDB has no
equivalent), joined through the cross-provider id TVDB supplies, so
nothing is lost. Adds that arrive with a TMDB id — Explore, watched
lists, an old preview link — are steered onto the TVDB series their
external id names.

Without a key, nothing changes: shows stay on TMDB, and the per-show
**release numbering** setting on a show's detail page (a season offset
plus a TVDB id for id-keyed searches) covers the split-show cases by
hand.

Show metadata provided by TheTVDB. Please consider adding missing
information or subscribing.

### Migrating an existing library

Migration is a button, never automatic — titles change to TVDB's form
("Kitchen Nightmares" becomes "Kitchen Nightmares (US)") and revivals
gain seasons, and that shouldn't happen behind your back. In
Settings → Metadata, **Migrate shows to TVDB** re-sources every
TMDB-sourced show with a known TVDB id in place: files and per-episode
monitor state survive, stale season offsets clear (a native tree with an
offset would break every search), and **seasons the migration reveals
arrive unmonitored** so a revival doesn't kick off a download sweep
unasked — flip on what you want. Shows with no recorded id get one
careful name-and-year lookup; anything ambiguous lands in a manual
picker right below the button. A split duplicate pointing at a series
another entry already covers is refused with an explanation — delete the
duplicate and let the surviving entry carry those seasons. Folder
renames stay [Organize](Maintenance.md)'s job, preview first.

## Scene numbering

Where release groups number a show differently than TheTVDB does, reely
searches by the numbers on the releases. The map comes from
[TheXEM](https://thexem.info), needs no key, and is refreshed for every
TVDB-sourced show on its normal metadata refresh. Displayed numbering
never changes — the catalog stays TheTVDB's, and a season whose releases
carry other numbers is labelled on the show page. See
[Scene numbering](Search-and-Acquisition.md#scene-numbering).

## The tiered refresh

Metadata does not go stale. A background pass runs hourly and re-fetches
whatever is due, on a schedule matched to how likely things are to
change:

| Tier | What | Cadence |
| --- | --- | --- |
| Fast | Continuing shows; movies still wanted (no file, or no digital release date yet) | Daily |
| Slow | Ended or canceled shows; movies with a file and a settled release | Weekly |

The point of the slow tier is the resurrection case: a show that ended
years ago and gets revived flips its status on the weekly check — which
moves it back to the daily tier, where its new season and episodes appear
in time to be grabbed. New episodes discovered by a refresh inherit their
season's monitor state, so a season you silenced stays silent when TMDB
adds stragglers, while brand-new seasons arrive monitored.

## People pages

Every cast row click-throughs to a person page: photo, biography, and
their full filmography from TMDB — with the entries that live in your
libraries linked straight to their pages. Filmography is global TMDB
data; the "in your library" cross-links respect library access like
everything else.
