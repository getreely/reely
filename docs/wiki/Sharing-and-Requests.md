# Sharing and Requests

Reely can be the thing your household asks for titles through, and the
thing that keeps Plex showing each person only what they should see. That
replaces two services most setups run alongside the *arrs: a request
portal, and whatever keeps Plex's labels honest.

None of it is required. An install nobody else touches can ignore this
page entirely.

## Linking Plex

Settings → Plex. Linking does three things:

- **Identifies your server** so reely can read its libraries and set
  labels on them.
- **Ties your Plex account to your admin account**, and turns that
  account's password off — so the install has one way in rather than
  two.
- **Lets the people you share Plex with sign in**, which is what makes
  requests possible.

If Plex is ever the thing that breaks, start the container once with
`REELY_RESTORE_PASSWORD_LOGIN=1` and every admin gets its password back.
Unset it afterwards.

## Groups

A **group** is a set of the people you share Plex with. Sharing is done
by group rather than person, because "the kids", "the house" and "my
sister" are the units you actually think in, and a person joining a group
should inherit what that group already has.

Each group maps to a Plex label. Share a title with a group and reely puts
that group's label on the Plex item; unshare and it takes the label off.

**Reely only ever removes labels it put there.** A library you label by
hand keeps those labels — reely diffs against the ones it owns and leaves
everything else alone. That is what makes it safe to point at a server
that already has a labelling scheme.

### The everyone group

A group everybody is in is useful for the things the whole house should
see, but you rarely want one person's request landing in front of
everyone. So the **Everyone** group is never a request default: every
account joins it as it is created, and only the owner puts a title in
front of the whole house.

An install that grew out of an existing Plex library also gets a
**backfill** group holding what was already shared before reely split
things up. It works the same way — never a request default — but its
membership is frozen at the point it was seeded, because somebody who
joined later never had access to that old library.

## Requests

People who sign in on the requesting side can search and ask for titles.
What happens next is up to you:

- **Approve or deny from the queue.** Admin → Requests. Approving adds
  the title and shares it with the asker.
- **Auto-approve, per person.** Someone you trust gets what they ask for
  without waiting for you — set separately for movies and shows, so you
  can trust somebody with films and still vet the forty-hour series.
- **Weekly quotas, per person**, again separate for movies and shows. A
  denied request still counts against the quota, so denying is not a way
  to farm extra asks.

A request that is approved shares the title with whoever asked, which
means the Plex label goes on and they see it. The loop closes without
anybody touching Plex directly.

## The requesting side, published

`REELY_EXTERNAL_PORT` starts a second listener carrying the requesting
side and nothing else. Every admin route is **absent** from it rather
than refused — settings, users, libraries, indexers and backups are not
served there at all.

```bash
docker run -d --name reely \
  -p 8788:8788 \
  -p 8789:8789 -e REELY_EXTERNAL_PORT=8789 \
  ...
```

Publish `8789`; keep `8788` on the LAN. The set of routes is derived from
the level each one already declares, so a route registered as admin is
unreachable from outside by construction rather than because somebody
maintains an allowlist.

[Tailscale Funnel](https://tailscale.com/kb/1223/funnel) suits this well,
since it publishes a single local port:

```bash
tailscale funnel 8789
```

People reach exactly the libraries Plex says they reach. Unshare somebody
in Plex and they are signed out of reely.

## Keeping Plex in step

Labels go on Plex **items**, so a title reely has just downloaded cannot
be labelled until Plex has scanned it and has an item to label. Two
things close that gap:

- **Reely tells Plex to scan** the folder it just placed a file in,
  rather than waiting for Plex's own sweep to come round.
- **Plex tells reely when it has finished.** Settings → Plex generates a
  webhook URL to paste into Plex. Plex sends no credentials, so the token
  in the URL is the whole guard — it is generated rather than chosen, and
  can be rotated or turned off from the same place.

The webhook needs **Plex Pass**, which is a real limitation. Without it
everything still works; sharing just waits for the next scheduled pass
rather than happening the moment the scan finishes. The periodic pass
runs either way, so nothing depends on the webhook being there.

A burst of scans — a season pack landing is one webhook per episode —
is collapsed into a single pass rather than one per event.
