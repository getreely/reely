# Maintenance

Everything on this page is admin-only.

## Backups

Settings → Backup. A backup is the whole database — catalog, settings,
users, history — written with SQLite's `VACUUM INTO`, which is
consistent even while the app is running. Media files are never part of
a backup.

- **Daily, automatically** — one rotating backup a day, newest 14 kept,
  in `/config/backups/`.
- **Back up now** — before anything you might regret.
- **Download** — each backup is a plain `.db` file you can copy
  off-machine. Stored credentials stay sealed inside it: the encryption
  key lives *outside* the database (`secret.key` or the environment), so
  a downloaded backup yields no API keys.

## Restore

Restore from any listed backup, or upload a `.db` file. The incoming
file is validated first — SQLite signature, integrity check, migration
history — then **staged**: the live database is never swapped under the
running process. Reely restarts (Docker's restart policy brings it
back), applies the staged file on boot, and keeps the replaced database
as `reely.db.pre-restore` next to it.

## Health

Settings → Health, refreshed while you look at it: whether TMDB,
Prowlarr, and SABnzbd answer; which Prowlarr indexers are enabled,
failing, or benched (and until when); SAB's version, paused state, and
free disk.

## Stats

Settings → Stats: library totals (movies, episodes, disk used), the
loop's last 30 days (grabs, imports, failures), blocklist size, and a
per-library table — titles, on-disk counts, wanted counts, and bytes.
Hard-linked copies count once per library, so each row matches what that
library's folder reports.

## Stuck imports

Activity shows any completed download whose import keeps failing, with
the reason on the row. **Retry** re-attempts now instead of waiting out
the backoff; **Resolve** lets you point the files at the right title by
hand. Failed *downloads* are different — those are recorded, blocklisted,
and cleared automatically ([details](Search-and-Acquisition.md#when-downloads-fail)).

## Naming templates

**Settings → Naming** holds one template per kind — movies and shows —
shared by every library and editable by admins only. Slashes make folders;
the extension comes from the source file.

| | |
| --- | --- |
| Movies | `{Title}` `{Year}` `{Quality}` `{Source}` |
| Shows | `{Title}` `{Year}` `{season:00}` `{episode:00}` `{Episode Title}` `{Quality}` `{Source}` |

The defaults produce `Arrival (2016)/Arrival (2016) [1080p BluRay].mkv` and
`Severance (2022)/Season 02/Severance - S02E07 - Chikhai Bardo [1080p WEB-DL].mkv`.

**`{Quality}` and `{Source}` are not decoration.** A library scan re-derives
both by parsing the filename, so a name that omits them loses them — and a
file whose quality isn't recorded can't be compared against a candidate
release. `{Source}` renders the form the parser reads back — `WEB-DL`, `WEBRip`,
`BluRay`, `REMUX`, `HDTV`, `DVD` — so the round trip holds.

That label is a *filename* form only. Quality profiles store the canonical
vocabulary — `webdl`, `webrip`, `bluray`, `hdtv`, `dvd`, `remux` — and the
parser normalises the label back to it on the way in. A profile "corrected"
to list `web-dl` would match nothing, because nothing produces that string
internally.

Installs from before WEB-DL and WEBRip were separated are migrated: a
profile that allowed `web` allows both rungs, a cutoff of `web` becomes
`webrip` (the lower rung, so both still satisfy it, exactly as before), and
files on disk recorded as `web` become `webrip` — the conservative reading,
since nothing recorded which they were and a container cannot say.
A placeholder whose value is unknown takes its decoration with it:
`[{Quality} {Source}]` renders `[1080p]` when the source isn't known, and
disappears entirely when neither is.

Every edit is rendered against a fixed example as you type, by the same
code the importer runs, so the preview can't drift from what actually
happens. A template that can't be used says why instead of saving:

- **A missing placeholder that separates files.** A movie template without
  `{Title}`, or a show template missing `{season:00}` or `{episode:00}`,
  renders different files onto the same path — the second import
  overwrites the first. These are refused.
- **A mistyped placeholder.** `{title}` is not `{Title}`; the wrong case
  renders as those literal characters in every filename. Anything in
  braces that isn't a real placeholder is named and refused.
- **A template that renders to nothing**, or one that would climb out of
  the library root.

Changing a template does not touch files already on disk. **Organize**
below is what applies it — and because the shipped defaults now carry
`[{Quality} {Source}]`, an install that never customised its templates
will see Organize propose renaming files that were named under the older
default. That is a rename, not a re-download, and the preview lists every
move before anything happens.

### Quality reely can read from the file

A file whose *name* says nothing about resolution is not a dead end: reely
reads the pixel size straight out of the container — Matroska/WebM,
MP4/M4V/MOV and AVI — and records the matching rung. `.wmv`, `.ts`, `.mpg`
and `.mpeg` are not read; each needs a different parser and they are
counted as "still unknown" rather than guessed at. No ffmpeg is involved; it
reads a few hundred bytes of header and never decodes a frame.

This runs when a scan finds a name with no resolution in it, and on
demand from **Settings → Naming**, which shows how many files are missing
a recorded quality or source. Nothing runs implicitly at startup: repair
is deliberate, like every other bulk action here, and a file with a blank
quality is safely refused by the grab loop in the meantime rather than
re-downloaded. The pass also recovers a missing **source** where it can:
an MKV whose segment title tag carries the original release name gives
its source back through the same parser a scan uses. A tag holding a
plain human title yields nothing and nothing is guessed; a recorded
source is never overwritten by a tag.

Width decides the rung, not height: a 1080p scope film is 1920x800, and
judging it on height would file it as 720p and start hunting an upgrade
that never satisfies. **Source cannot be recovered this way** — a container
knows how many pixels it holds, never whether they came from a disc or a
stream — so a file that lost its source keeps an empty one until it is
re-grabbed or renamed.

Settings → Naming shows how many files still have no recorded quality or
source and reads them on demand.

### How a scan decides which show a file belongs to

The folder names the title, the file names the episode. A file at
`WIFE SWAP (2019)/Season 01/Wife Swap - S01E01….mkv` belongs to the 2019
reboot because its folder says so — filenames routinely drop the year,
and a year-less "Wife Swap" would otherwise match whichever show TMDB
ranks first. The organize step writes those `Title (Year)` folders
itself, so the scan trusts them: a folder with a year is authoritative, a
year-less folder only fills in when the filename carries no title at all,
and grouping folders ("Reality") never override a real filename title.

A file also has exactly **one owner**: attaching it to an episode strips
any other episode's claim on the same path, so two shows sharing a name
can never both count the same bytes as "on disk". If a shared-title
library ever got tangled this way, one **Scan** untangles it in place —
files reattach to the show whose folder they live in, and the other
show's episodes go back to honestly Missing (and back into the search
passes).

### The order to do this in

Quality and source live in the database — that is what the sweeps read
when deciding whether a release would be an upgrade. The filename is the
durable copy: it is what a **scan** reads to rebuild those columns, which
is why leaving them out of the template loses them.

So when bringing an older library up to date:

1. **Read from the files** (Settings → Naming). Organize writes
   the name from what the database holds, so a file with no recorded
   quality is renamed without one — and would need renaming again after.
2. **Organize** the library. Names are then written from the repaired
   values.

A source that was never recorded is recovered only when an MKV's title
tag carries the release name; otherwise those files are renamed without
one until a re-grab supplies it. A scan will not clear a
source it can't read: a name carrying none says nothing about the file,
which is not the same as saying it has none.

## Organize — putting files where the template says

A library grows crooked over time: a naming template changes, a title gets
renamed at TMDB, or another tool built folders to its own taste. **Organize**,
next to Scan on a library page, moves every file reely tracks to where its
naming template says it belongs.

It always runs in two steps. The first is a preview listing every move as
`from → to`, plus the folders the moves would leave empty. Nothing is
touched until you press the button on that list — moving files is not
something a click should do before you have read what it will do.

Applying performs exactly that list. Each move is independent, so one
unreadable file is reported and the rest still run rather than stranding a
library half-organized. Afterwards the catalog points at the new paths, and
folders the plan predicted would empty are removed if they really are empty.

Some deliberate limits:

- **Nothing is overwritten.** If something already occupies a destination,
  that move is refused and reported. Two rows wanting one filename is a
  question for a person, not a silent replacement of somebody's file.
- **Only files reely can see.** A row whose file is missing from disk is
  left out of the plan rather than listed as a move certain to fail; a
  library whose files moved out from under reely wants a scan, not an
  organize.
- **A rename in place is just a rename.** It does not count towards the
  folders reported as emptied.

Hardlinked copies in other libraries are unaffected: renaming one directory
entry leaves the others pointing at the same data. Each library organizes
its own copies.

**If another tool also manages the same folders**, settle that first.
Two applications with different metadata sources will disagree about a
title's name — one says *The Proof is Out There*, the other *The Proof Is
Out There (2021)* — and each will keep rebuilding its own tree.
