# Watched Lists

A watched list feeds a library automatically: on every sync, new entries
are added monitored, instantly hard-linked when a sibling collection
already has the file, and queued for a paced search when not. Lists are
**self-service** — anyone may create, sync, or delete lists feeding a
library they have access to; nothing about them needs an admin.

They live in Settings → Watched Lists (the one Settings tab regular
users have).

## Sources

| Source | What to enter | Needs |
| --- | --- | --- |
| TMDB chart | Trending / Popular / Top rated / Upcoming / On the air | nothing beyond the install's TMDB key |
| TMDB list | A list's URL or numeric id | list must be public |
| Trakt chart | Trending / Popular | admin-configured Trakt client id |
| Trakt list | `trakt.tv/users/<user>/lists/<slug>` URL | public list + the client id |
| mdblist | `mdblist.com/lists/<user>/<slug>` URL or numeric id | **your own** mdblist API key |

**The no-subscription watchlist recipe:** keep a public TMDB list as your
personal watchlist (TMDB accounts are free, and their apps make adding
from your phone easy), point a "TMDB list" source at your library, and
everything you save shows up at home monitored.

**Trakt:** public lists and charts work with just an application client
id, which an admin sets once in Settings → Watched Lists. Creating a
Trakt API application requires their paid VIP tier — that's Trakt's
gate, not reely's — so the card says so plainly and everything else
works without it.

**mdblist keys are strictly per user.** Each person pastes their own free
key (mdblist.com → Preferences → API Access); it's sealed at rest and
scoped to their account. A list only ever syncs with **its creator's**
key — someone without a key gets a clear sync error, never a borrowed
key or someone else's rate limit.

## Behavior

- **Item limit** — each list takes the first N entries (default 20, 0 =
  everything). Charts churn; the limit keeps a trending list from slowly
  importing the entire zeitgeist.
- **Mixed lists** — a list holding movies and shows feeds only entries
  matching its library's kind. Point two lists at two libraries to take
  both halves.
- **Cadence** — every account owns its own sync timer covering all its
  lists (default every 30 minutes, 15-minute floor), set right on the
  lists card; one person's pace never touches another's lists. Plus a
  **Sync now** button per list for the impatient.
- **Removals are ignored.** A list is a tap, not a mirror: dropping an
  entry from the list never deletes anything from a library.
