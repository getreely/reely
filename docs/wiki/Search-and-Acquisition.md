# Search and Acquisition

Reely searches through **Prowlarr** (which fans out to every indexer it
knows) and downloads through **SABnzbd** for usenet, **qBittorrent** for
torrents. Either on its own is a complete setup; both together is fine
too.

## Usenet, torrents, or both

What can be grabbed follows from which clients you have set up. A torrent
cannot be downloaded without a torrent client, and no setting changes
that — so an install running one client needs no configuration here at
all.

Settings → Download client adds a **Usenet and torrents** choice for the
install that runs both:

| Choice | What it does |
| --- | --- |
| Both, prefer usenet | *(default)* Both available; usenet wins a tie |
| Both, prefer torrents | Both available; a torrent wins a tie |
| Usenet only | Nothing is sent to qBittorrent, which stays connected |
| Torrents only | Nothing is sent to SABnzbd, which stays connected |

**A preference only breaks a tie.** Two equally good releases, one of
each — the preference decides. A *better* release still wins on its
merits, so preferring torrents never means taking a worse copy. This is
the order Sonarr and Radarr rank by too: quality, then format score,
then protocol.

Search results label every release **USENET** or **TORRENT**, and a
release that cannot be grabbed says why rather than losing its button —
a missing Grab button and an indexer with nothing look identical and
have very different fixes.

### What a torrent does differently

A usenet job is finished when it finishes: reely moves the file into
your library and what is left in the job folder is residue.

A torrent is not finished. It seeds, and it seeds the very file it just
handed over, so reely **hard-links** it into the library instead of
moving it. The library gets a name for the data, the torrent keeps its
own, and the bytes are stored once. Moving it would stop the seeding and
leave qBittorrent pointed at files that are no longer there.

This makes the single-mount rule below matter more, not less: where a
hard link cannot be made, reely copies, and a copy means the file is on
disk twice for as long as the torrent seeds.

Once imported, a torrent is **retired** — moved to a category reely does
not sweep (`reely-seeding` by default). It keeps seeding and stops being
picked up again, which it otherwise would be on every pass, since unlike
SAB history a torrent never leaves the client's list on its own. Seed
limits are qBittorrent's to enforce.

## Quality profiles

A profile says what a library wants:

- **Qualities** — the allowed resolutions (480p / 720p / 1080p / 2160p),
  plus optional **Unknown**: releases whose name carries no resolution at
  all, which usenet's obfuscated posts often don't. Off by default, since
  an unlabeled release could be anything and the size band is then the
  only thing judging it. Unknown always ranks below every real resolution
  and can never be the cutoff.
- **Sources** — optionally restrict where encodes come from (DVD / HDTV /
  WEB / Blu-ray / Remux). Nothing picked means any source; with a
  restricted list, releases whose names carry no recognizable source are
  rejected too — unless the profile allows **Unknown** quality, which is
  the opt-in for unclassifiable names on both axes: a profile that takes
  releases whose names say nothing about resolution takes ones that say
  nothing about source, with the size band as the judge. A recognized
  source that's simply not on the list is always rejected.
- **Cutoff + upgrades** — with upgrades on, a title whose file sits below
  the cutoff stays wanted: better releases keep getting grabbed, the
  lesser file is replaced on import and deleted once nothing references
  it. At or above the cutoff, hunting stops. A **source cutoff** extends
  this within the cutoff resolution: with cutoff *1080p · Blu-ray*, a WEB
  1080p file keeps hunting until a Blu-ray (or Remux) 1080p lands — a
  higher resolution always satisfies the cutoff outright. Files whose
  source was never recognized count as below a source cutoff; a library
  scan backfills the source for any file whose name still carries it.
- **HDR policy** — allow either, require HDR, or block it (the right call
  when the screen it plays on washes HDR out).
- **Release terms** — rules judged against the raw release name
  (case-insensitive): *must contain* terms all have to appear, any *must
  not contain* term rejects, and *preferred* terms carry a score added to
  the ranking — positive prefers, negative deprioritizes without
  blocking. A resolution step is worth 1000 and a source step 100, so
  scores in the tens re-rank within a tier and hundreds jump tiers.
- **Minimum format score** — the sum of a release's matched terms and
  custom formats can be turned into a hard floor: below it, rejected.
  0 means no floor.
- **Sources** — `Remux`, `Blu-ray`, `WEB-DL`, `WEBRip`, `HDTV`, `DVD`, best
  first. **WEB-DL and WEBRip are separate rungs**, and WEB-DL is the better
  of the two: it is the streaming service's own file, where a WEBRip is a
  re-encode of a stream. A profile can allow both and still prefer one, and
  a WEBRip on disk upgrades to a WEB-DL of the same resolution.
- **Size band** — min/max **MB per minute of runtime**, not flat caps: a
  flat 8 GB limit is bloated for a 45-minute episode and stingy for a
  three-hour movie. Season packs divide their size across the episodes
  they span before the check.

**SD-era releases infer their resolution from convention.** HD releases
carry a resolution marker in the name — scene rules make 720p/1080p tokens
mandatory — so a TV, DVD or web release *without* one is standard
definition, and reely judges it as 480p. This is the same reading Sonarr
makes when it files such releases under SDTV. A profile that should grab
them therefore allows **480p** (and likely loosens the size band's floor —
a real SD episode is a few hundred MB, under the default 8 MB/min).
The defaults are graded the way Radarr grades them: markerless rip
tokens (`BDRip`, `BRRip`) read as SD-era **480p**, a bare `BluRay` or
`HD-DVD` name reads as **720p**, and a disc image (`BD25`, `BD50`,
`BDISO`, `BR-DISK`) or a `REMUX` is the full-HD article, **1080p** — a
UHD disc carries a `UHD` token, which is a real 2160p marker, not grist
for this. Markers outside the four stored rungs map to the nearest
honest one, at token parity with the *arrs: `1080i`, `1440p`, `FHD` and
`1920x1080` are the 1080 class; `960p` the 720 class; `576p`, `540p`,
`480i`, `360p` and the SD pixel-dimension forms are SD-class, where
claiming 480p claims less rather than more. A *file on disk* named this
way is measured rather than assumed — the scan reads its pixel size, and
the convention only stands when the container can't be read.

Regardless of profile, **cam/telesync/screener releases are always
rejected** — a theater recording with "1080p" stamped on its name is not
a 1080p, and no profile setting makes it one.

## Searching by id

Manual and scheduled searches ask twice: once as free text ("Wife Swap
S02E04"), and once by id — `tvdbid` for episodes and season packs (the
key Sonarr searches by; TMDB carries every show's TVDB id and reely
stores it), `imdbid` and `tmdbid` together for movies (the pair Radarr
sends). Prowlarr forwards the id to each indexer whose capabilities
support it; indexers that don't still answer the text query, and the two
result sets merge with duplicates collapsed. Id-keyed answers are immune
to two shows sharing a title, and they surface correctly-tagged releases
whose names a text query would miss. Shows added before this existed
pick their TVDB id up on their next metadata refresh.

For **movies**, ids also work in the other direction — in both senses,
the way Radarr matches. A reported id that **agrees** identifies the
movie outright, whatever the release chose to call it (a foreign cut, a
festival title); movie names are also matched through TMDB's
**alternative and original titles**, so "Leon" finds *Léon: The
Professional*. And when a movie release's reported ids all name a
**different** movie than the one being searched, it is rejected however
perfect its name looks: two productions sharing a title and year fool a
name parser but not the ids — release groups put the IMDb link in the
NFO themselves, so for movies those mappings are first-party.

The one imposter the ids can't catch is the **unmapped pre-release
fake**: before a movie is actually out, a "WEB-DL" with a perfect name
and no ids attached is wrong by definition, and that's the window the
fake-release farms live in. Each movie therefore carries an
**Auto-grab** setting: *After release* (the default) keeps RSS and
automatic searches from taking anything until the movie's digital date
arrives (a theatrical date more than ninety days past counts too, since
TMDB never learns a digital date for many titles; a title with no dates
at all is left ungated). *Any time* lifts the gate for a title you
expect early. Manual grabs are never gated — a pre-release row shows in
interactive search with its reason and **Grab anyway** works. Nothing
else exempts a release, not even an agreeing reported id: a movie's id
mapping comes from the release's own NFO, which the uploader writes, so
an id can say which movie a release *claims* to be, never that the
movie is actually out. This is a veto on positive evidence, never
a gate on its absence: a release with no ids attached (early releases
usually aren't mapped yet) is judged on its name exactly as before, and
a vetoed row still shows in interactive search with its reason,
grabbable by hand like any other rejection. Shows deliberately get no
such veto: TV entries merge, split and rename over the years, and
indexers map TV releases by name — a correctly-named episode carrying a
stale or foreign tvdb id is common and proves nothing.

## Scene numbering

Release groups do not always number a show the way TheTVDB does, and
where they differ, the numbers on the file are the ones an indexer can
find. TheTVDB's aired order folds the 2007 and 2008 runs of *Kitchen
Nightmares (US)* into a single 22-episode Season 1, so every release from
2010 onward carries a season number one higher than the catalog's: the
run TVDB calls S9 releases as **S10**.

[TheXEM](https://thexem.info) is the community map between the two
numberings, keyed by TVDB series id — the same map Sonarr searches with.
Reely fetches it for TVDB-sourced shows on every metadata refresh, and
stores the result per episode, because that is how the divergences
happen: an episode the scene splits or merges moves everything after it.
Nothing to configure and no key to get; a show XEM has no map for — which
is almost all of them — simply keeps its own numbers.

What the mapping changes:

- **Searches speak scene numbering.** Both the text query and the
  id-keyed one ask for S10E08, not S09E08.
- **Answers are read back through it**, so a release named S10E08 lands
  on the episode you asked for — and a release named S09E08 is correctly
  understood as a *different* episode of that show, not this one.
- **A merged season is asked for under both its numbers.** TVDB's
  Season 1 is the scene's S01 and S02, so a season pack is searched for
  and accepted either way.
- **Season pages say so.** A season whose releases carry other numbers is
  labelled "releases as S10", so a season number that looks wrong next to
  the network's own count is explained rather than mysterious.

Reely keeps displaying and storing TheTVDB's numbering throughout —
identical to Sonarr, which shows the same episode as 9x08 while searching
for S10E08. The catalog is TVDB's; only the search and match layer speaks
the scene's.

XEM's map is a snapshot, and never quite the whole show: a season still
airing is mapped as far as somebody has entered it, and a season TVDB has
only just listed is not mapped at all. Both are exactly the episodes
still being searched for, so unmapped episodes inherit the shift from the
mapping before them — inside a mapped season the run continues, and a
wholly unmapped later season inherits the last mapping's season shift.
Gaps *between* mappings are left alone rather than guessed at.

Shows still sourced from TMDB are not mapped: XEM speaks TheTVDB's
numbering, and a TMDB tree has its own season shape (a revival TMDB
restarts at S1). Those shows keep the manual **season offset** below,
which is what it has always been for.

## Two shows, one name

Reboots share their name with their originals — there is a "Wife Swap"
from 2004 and another from 2019, and `Wife.Swap.S02E04` fits both.
TheTVDB names same-named shows with the year in the title itself
("Monster (2022)"), which releases never carry: matching folds that
trailing year away, but never across two *different* years — "Monster
(2022)" can never claim a release tagged 2017. TVDB's **aliases** count
as the show's names too, because release groups use them freely — an
anthology's seasons each release under their own subtitle, and
`Monsters.The.Lyle.And.Erik.Menendez.Story.S02E01` belongs to "Monster
(2022)" only through its alias list. Beyond that, matching uses the two
signals release names actually offer.

Anthology seasons go one step further: TVDB names each season after its
story, each story's releases number themselves **S01** as their own
little show, and the subtitle in the release name is the truth. A
release spelling out a season's subtitle is placed on that season
(`Monsters.The.Lyle.And.Erik.Menendez.Story.S01E05` is the anthology's
S02E05) and refused for every other season, however its numbers happen
to line up — the Dahmer story's S01E01 can never be grabbed as the
Menendez story's. Searches for a subtitled season also ask the indexers
in that form, alongside the normal query and the id query. A **year token** in the name ("Wife.Swap.2019.
S02E04") must agree with the show's year. And when the name carries the
**episode's own title**, it must share at least one word with the
episode being matched: a release reading `Mayfield.Wasdin` is not this
show's `Floyd-Ely Vs. Clanton`, whichever quality it claims. The check
needs two words of real evidence before it refuses anything — a lone
mangled token (`HBTV`) is scene junk, not a title — and a bare
`Show.S02E04.720p` name carries no signal either way, exactly as much
as Sonarr can tell from it.

## Explore

Home is what's yours — recently added movies and recently imported
episodes. Everything drawn from TMDB lives on **Explore** instead, one
sideways row per list:

- **Trending**, **Popular** and **Top rated**, each split into movies and
  shows.
- **One row per streaming service** — Netflix, Hulu, Max, Disney+, Prime
  Video and Apple TV+ — showing what that service actually carries in your
  watch region rather than a guess. A service that carries nothing there
  is left out rather than drawn empty.

Every card opens a preview, never an add: putting something in a library
is always a separate, deliberate click. Titles you already have are badged
**In library** so a row never invites you to add the same thing twice.

The charts are cached for an hour, since TMDB's move slowly and one fetch
should serve the whole household. Each row is fetched independently, so a
row TMDB fails to answer costs only itself — the rest of the page still
draws, and the missing one is retried within a few minutes instead of
waiting out the hour.

Movie and show pages carry a **More like this** row of TMDB
recommendations, which opens previews the same way Explore does.

## Narrowing the discovery rows

TMDB's trending and popular charts are global. Settings → Metadata has two
toggles, both off by default, that narrow what reaches Explore:

- **Hide anime** — leaves out Japanese animation. The rule is animation
  **and** Japanese origin: genre alone would take Pixar and every western
  cartoon with it, origin alone would take live-action Japanese cinema.
- **English titles only** — drops Korean, Spanish, Turkish, Hindi and
  other non-English titles. It keys on the language, with stated US origin
  as an alternative, so British and Australian titles deliberately stay —
  a "top shows" row without The Crown is worse, not better. This one
  subsumes the anime filter, since anime is Japanese either way.

Both act on positive evidence only: a title TMDB gives no language or
country for is kept rather than dropped, so a missing field can never
quietly empty a row. Search is unaffected by either — looking a title up
by name is always an explicit ask.

## Upgrades and what counts as "already have it"

A release is only worth grabbing over a file you already hold if it is a
strict improvement below the cutoff. That judgement needs to know what is
on disk — so a file whose quality was never recorded is refused rather
than treated as an opportunity:

> already on disk, and its quality isn't recorded — nothing to compare a
> candidate against

This matters because an empty quality used to be indistinguishable from
**no file at all**. An episode whose filename carried no resolution read
as missing to the RSS sync, and was downloaded again on every pass. The
active upgrade sweep had always skipped such files for the same reason;
the passive path now agrees with it.

The way out of that state is not a re-download: reely reads the resolution
out of the file itself and records it (see
[Naming templates](Maintenance.md#quality-reely-can-read-from-the-file)).

**Source is the one exception, and it is deliberate.** A file's source
cannot be read from the container, and with a **source cutoff** set a file
whose source was never recorded counts as below it — so the loop keeps
hunting until a labeled encode lands. That is the intended behaviour: it
is how an install converges on the encodes it asked for, and it settles
after one grab per file.

It is also the one setting that can re-grab a library whose filenames
never carried sources. If that is not what you want, leave **cutoff
source** at *Any source*: the resolution alone then satisfies the cutoff
and nothing is re-grabbed on account of an unknown source. Nothing else
about the split re-grabs anything — a profile that allowed `web` is
migrated to allow both rungs, and its cutoff to `webrip`, the lower of
the two, so every file already on disk still satisfies it.

## Custom formats

Settings → Custom Formats holds install-wide scoring rules in the
Sonarr/Radarr shape, split into **Movies** and **Shows** columns — and
imports them straight from the [TRaSH Guides](https://trash-guides.info/):
each column's import browses the matching guides collection (the guides
publish separate movie and show versions of their formats), and picked
formats arrive with their recommended scores, editable afterwards.
Movie formats judge only movie searches and show formats only show
searches. Formats match on release-name and release-group patterns,
source, and resolution; the few guide formats that need language
tracking, indexer flags, or release-type rules are left out rather than
imported half-working. Matching formats apply in every profile's
ranking, and manual search shows each release's **format score** —
green at zero or above, red when negative — with the matched formats as
chips. The resolution/source ladder still ranks underneath, silently.

Profiles attach per title (inherited from the library on add, changeable
on any title's page). Every release is judged against the profile and
the verdict is visible: manual search shows accepted releases ranked —
quality above source above HDR above PROPER — and every rejected row
carries its reason.

**Every rejected row is still grabbable by hand.** The profile is law for
the automatic paths, because nobody is watching them — but a person in
the manual search dialog is the authority, and **Grab anyway** sends any
release to its client regardless of the verdict: re-grab a corrupt file
at the same quality, take a below-cutoff copy on purpose, override a
rejection you disagree with. The judgment column still tells you exactly
what you are overriding. One thing to know: a hand-grabbed release
imports recording whatever its name claims, so a stopgap grab is yours
to upgrade later.

A release whose protocol has no client, or has been switched off, is the
one thing **Grab anyway** cannot force — there is nowhere to send it.
The row says so rather than going quiet. Nothing about the automatic
loops changes: RSS, the sweeps and auto-search still take accepted
releases only.

## How things get grabbed

| Path | When | Cost |
| --- | --- | --- |
| Add / monitor | The moment you add or re-monitor a title | one search |
| RSS sync | Every 15 minutes (configurable), watching the indexers' newest releases | cheap, passive |
| Release-day pass | Hourly, tapering over each title's first two weeks (day 0–1 eagerly, daily through day 3, once at 7 and 14) | small |
| Wanted sweep | **Off by default** — opt-in schedule (6h floor) re-searching everything missing *or below its cutoff* | real indexer traffic |
| Search missing | The button on a library page, on demand | one search per gap |

**Search missing** is the manual version of the wanted sweep, scoped to
what you're looking at: one kind, one library if you're inside one, and
only libraries you can see. It fills gaps rather than chasing upgrades —
the button says missing, so it won't re-grab a file that's merely below
cutoff — and its count tells you up front how many searches it will
queue. Like everything else active, it goes through the paced queue.

The RSS sync deliberately has **no date gate** — an early release beats
its air date, and reely takes it. It also **never loses its place in the
feed**: each sync remembers where the last one left off, per indexer, and
pages backwards until the results reconnect with that memory (capped at
1000 releases, the same walk-back and ceiling Sonarr uses). A busy
release night that posts hundreds of releases between two polls is read
in full rather than through a one-page window. The cursor is kept per
indexer — one going quiet never costs another its place — and persists in
the database, so even a restart resumes where the last sync left off:
releases posted while reely was down are read on the first sync back.
Only when a feed churns past the ceiling can posts scroll by unseen, and
the log says so when it happens.

But RSS still judges each posting exactly once, as it appears; a release
that gets rejected at that moment (or posts while reely is down) never
comes back through the feed. The
release-day taper is the deliberate second look: instead of covering
only the day itself, it keeps hunting a new title eagerly while the
release is landing, daily through day three, then once each at days
seven and fourteen for stragglers and repacks. A title still missing
after two weeks belongs to the opt-in wanted sweep. Active searches all flow through one
paced queue (a few per tick), so monitoring a two-hundred-episode show
trickles out instead of hammering your indexers. Sibling collections
dedupe: one search serves every copy of a title, and the import's
hardlink fan-out delivers to all of them.

## Unaired episodes are not missing

An episode that hasn't been broadcast yet isn't something you're short
of, so it never counts toward a show's missing total, and neither does an
episode TMDB has announced without a date. A show mid-season reads "8 of
10 aired episodes on disk" with the rest noted as still to air, and a
show you're fully caught up on reads **Up to date** rather than being
permanently short its unaired season.

The counts use exactly the rule the backlog search uses, which is the
point: "3 missing" means three things reely will actually go looking for,
not three that TMDB has merely listed.

## Imports

A job reely grabbed itself imports onto the exact title it was grabbed
for — an obfuscated release name (a bare hash, common on usenet) can't
derail it, and a single-episode grab knows precisely which episode it
went hunting for. The release's name keeps one veto: if it parses
confidently to a **different** title (a mislabeled release), the import
stops as a visible Activity problem showing both facts — never filed
under either guess — with resolve-by-hand as the human call. Jobs reely
didn't grab (queued in SAB yourself) are matched back by name, renamed
into the library by your naming templates (`{Title} ({Year})/Season {season:00}/…`
— slashes make folders), attached, and hard-linked out to sibling
collections. Multi-episode files back every episode they span; a span
import never overwrites an episode that already holds a strictly better
file. Files using the scene's compact numbering ("Show.301" for S03E01,
common in older season packs) import too — but only with evidence: the
mapped episode must exist, episode-title words in the name must agree
with it, and a bare number that could plausibly be an absolute episode
count (the anime convention) is refused rather than guessed. Context
counts as evidence: a job whose own name declares seasons
("Show.S01-S06.DVDRIP") vouches for its files' compact numbers, and so
does resolving a stuck job onto a show by hand. The **This is
actually…** picker on a stuck import also offers season and episode
pins once a show is chosen, fed by that show's own episode list — pin a
season to place every file of the job there, or pin a single-file job
to one exact episode. Everything lands in Activity → History — upgrades tagged
`upgraded · was 720p`.

## The Activity queues

Downloads and searches are separate tabs, because they are separate
things: a download is a file on its way in, a search is reely still
looking for one.

**Downloads** lists what SABnzbd is working on right now, paged rather
than capped — the whole queue is reachable, not just the first fifty.
Cancelling a download deletes its partly downloaded data along with it; a
cancel that left half a release in the incomplete folder would only leak
disk. A plain cancel (✕) leaves the release **off** the blocklist, so it
can be grabbed again; the ban button (⊘) beside it — and the
**Cancel + blocklist** bulk action — cancels *and* bans the release, for
the download you can already tell is the wrong file. The ban carries the
same indexer scoping a failed download's would.

Each row also carries a **priority** selector — SAB's own scale, so the
change shows identically in SAB's UI: **Force** starts the download
immediately ahead of everything else, High/Normal/Low reorder the queue.

Handled jobs clean up after themselves: once a download imports (or
fails and is recorded), reely removes the job from SAB's history *and*
deletes what it left on disk — the emptied job folder and its par2/nfo
residue after an import, a failed job's partial data. The library copy
lives at its own path, so nothing you keep is touched.

**Searches** lists the titles waiting their paced turn at the indexers,
in the order they will run, each with the title it is for. Cancelling
one deletes nothing and un-monitors nothing — reely simply stops looking
*now*, and anything still missing is picked up by the next scheduled
pass. This queue is held in memory, so a restart clears it for the same
reason.

Both tabs filter across the **whole** queue rather than the page you can
see, and both select in bulk: narrow it, select all, cancel. That order
matters — "select all" after filtering acts on what matched, not on
whatever happened to be on page one.

## When downloads fail

A failed download is recorded **against the title it was grabbed for**,
cleared from SAB, and its release goes to the **blocklist** — automatic
paths will never grab that copy again. The ban is scoped to **the indexer
it came from**: indexers carry each other's posts under identical names,
and one bad upload says nothing about another indexer's copy, so the same
release elsewhere stays fair game. When the job was one reely grabbed, the
title is **re-searched immediately**: with the failed copy banned, the next
best acceptable one is grabbed without waiting for a sweep. The
Activity → Blocklist tab shows every banned release and which indexer it
covers; removing one is the pardon. A **manual grab is always an
override**: grabbing a blocklisted release by hand works, and importing it
re-earns its place. Imports that fail (unmatchable name, missing episode)
surface in Activity with their reason and Retry / Resolve actions.

Failure isn't the only road onto the blocklist. Every **grabbed** row in
Activity → History carries a ban button too — the undo for a grab that
completed but turned out to be the wrong file. Ban it there, delete the
file from the title's page with **Delete files, keep in library**, and
the replacement search that queues can't re-grab the same release.

## Auto search answers back

Auto-searching **one** title runs the search there and then, so the toast
names the release it sent to SABnzbd — the same confirmation a manual grab
gives. When nothing was sent it says why rather than claiming a search is
under way: the title isn't monitored, a download is already in flight,
nothing passed the quality profile, or every acceptable release is on the
blocklist. That last one is worth spelling out because manual search
deliberately ignores the blocklist, so those releases look perfectly
grabbable there while automatic search refuses them. Auto-searching a whole
show or season still queues its searches — that can be hundreds — and
reports how many are hunting.

## Adult content

Adult content is filtered out unconditionally, and there is no setting for
it. A toggle would be a footgun with no legitimate use on a household media
server, and Sonarr and Radarr don't offer one either.

The filtering is layered, because any one layer can be bypassed by a path
nobody thought about:

1. **Ask for less.** Every TMDB request carries `include_adult=false`, set
   at the single function all requests pass through, so no endpoint can
   forget it. TMDB defaults this to false on the endpoints that read it —
   but a default reely doesn't assert is a coincidence, not a guarantee.
2. **Drop what comes back.** Every list — search, the Explore charts and
   service rows, "more like this", watched lists, and an actor's
   filmography — is filtered on TMDB's own `adult` flag as it is converted.
   A filmography matters most here: `combined_credits` has no
   `include_adult` parameter and TMDB does not filter it, so this is the
   only thing standing between an actor's adult credits and their page.
3. **Refuse to act.** Fetching a flagged title by its TMDB id fails
   outright, so it can't be previewed, added, or refreshed even by someone
   who knows the id. Since ids are guessable, this is the layer that
   actually protects a library; the first two govern discovery.

Indexer results are filtered separately, on newznab category: searches only
ask for movies (2000) and TV (5000), and anything carrying an XXX category
(6000–6999) is dropped on the way back regardless of what it claims to be —
indexers misfile, and a category we asked for is not a category we got.

Release **names** are deliberately not pattern-matched. A keyword list
removes legitimate titles — *Shame*, *Sex Education*, *Nymphomaniac* — for
no gain over the category, which is the precise signal.
