# Getting Started

## Install (Docker / unraid)

```
docker run -d --name reely \
  --restart=unless-stopped \
  -p 8788:8788 \
  -e PUID=99 -e PGID=100 -e UMASK=022 \
  -v /mnt/user/appdata/reely:/config \
  -v /mnt/user/data:/data \
  ghcr.io/getreely/reely:latest
```

Three things about this command matter more than they look:

- **Keep SABnzbd's completed folder and every library root under the same
  `/data` mount.** That's what makes imports instant renames and lets the
  same movie live in five collections while occupying disk once —
  hardlinks only work within one filesystem. If downloads live on a
  different volume, imports silently fall back to copying.
- **`--restart=unless-stopped` is load-bearing.** Restoring a backup works
  by staging the file and exiting; Docker bringing the container back is
  what applies it.
- On unraid: Docker tab → Add Container → same repository, port, paths and
  variables. PUID 99 / PGID 100 are the unraid defaults.

### Environment

| Variable | Default | Purpose |
| --- | --- | --- |
| `REELY_PORT` | `8788` | HTTP port |
| `REELY_CONFIG_DIR` | `/config` | Database, secret key, backups |
| `REELY_SECRET_KEY` | — | 64-hex-char key sealing stored credentials; auto-generated to `/config/secret.key` when unset. Supplying it via environment keeps the key out of `/config` entirely (`openssl rand -hex 32`). |
| `PUID` / `PGID` / `UMASK` | `99` / `100` / `022` | File ownership for imports |

## First run

Open `http://<server>:8788`. The setup wizard walks through everything in
order — only the admin account is required, every other step can be done
later in Settings:

1. **Admin account** — the first account is always an admin; creating it
   locks the API behind login.
2. **Libraries** — one per media folder, each holding movies *or* shows.
3. **TMDB key** — a free API key from themoviedb.org powers all metadata.
   Optionally add a **TVDB key** too (free at thetvdb.com/api-information)
   to source shows from TheTVDB — the numbering release groups follow;
   see [Metadata](Metadata.md#tvdb-for-shows-optional).
4. **Quality profile** — resolutions, upgrade cutoff, HDR policy, and the
   runtime-scaled size band.
5. **Prowlarr** — URL + API key; release searches fan out through it.
6. **SABnzbd** — URL + API key + categories; grabs land there, imports
   watch it.
7. **Watched lists** — optional; see [Watched Lists](Watched-Lists.md).

## Accounts

Two roles. **Admins** own the install: settings, libraries, users,
activity, backups. **Users** get the libraries an admin assigns them —
their own collections, their own watched lists, nothing else visible.
Details in [Libraries and Collections](Libraries-and-Collections.md).

## Remote access

Reely binds plain HTTP. On a LAN that's fine; for remote use put it
behind Tailscale (nothing to configure) or any HTTPS reverse proxy —
cookies upgrade to `Secure` automatically when requests arrive over
HTTPS (or with `X-Forwarded-Proto: https`).
