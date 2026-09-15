# Architecture

One Go binary serving an embedded React SPA, one SQLite database, no
external services beyond the ones you point it at (TMDB, Prowlarr,
SABnzbd).

## Stack

- **Backend** — Go, `net/http`, SQLite via a pure-Go driver (no CGO), so
  the Docker image is a static binary on a slim base.
- **Frontend** — React 19 + Tailwind v4 + Vite, embedded into the binary
  at build time; installable as a PWA.
- **Migrations** — plain SQL files applied in order on boot; table
  rebuilds run FK-checked before commit.

## Data model, briefly

Libraries have a kind and a quality profile. Movies and shows are keyed
by `(tmdb_id, library_id)` — the same title in three collections is
three rows sharing hard-linked files. Shows own seasons and episodes;
files attach per movie or per episode with path, size, and parsed
quality. History records every grab, import, failure, and scan. The
blocklist remembers failed releases. Settings are key/value with the
secret ones sealed.

## Background loops

A single watcher ticks every 15 seconds and fans out on gated cadences:

| Loop | Cadence | Work |
| --- | --- | --- |
| Import sweep | every tick | completed SAB jobs → rename, attach, hardlink fan-out |
| Search queue | every tick | up to 3 paced active searches |
| RSS sync | 15 min (config) | newest releases vs. everything wanted or upgradeable |
| Release-day pass | hourly | today's/yesterday's arrivals, per-target taper |
| Wanted sweep | opt-in, 6h floor | full backlog: missing + cutoff-unmet |
| List sync | 30 min (config) | watched lists → new titles |
| Metadata refresh | hourly pass | tiered daily/weekly re-fetch from TMDB |
| Backup | daily | `VACUUM INTO` rotation, keep 14 |

## Security posture

- **Three unauthenticated endpoints**: login, an auth probe that reports
  only whether login is required, and a health probe that answers
  `{"status":"ok"}` — the version string is withheld until signed in. A
  regression test parses the route table out of the server source and
  fires an unauthenticated request at every route, so a new endpoint
  can't ship open by accident.
- **Roles** — admins configure the install; users get their assigned
  libraries. Server-side filtering everywhere; out-of-scope titles
  answer 404 so an ID confirms nothing. Settings, users, activity,
  health, stats, and backups are admin-only.
- **Credentials** (TMDB, Prowlarr, SAB, mdblist keys) are sealed with
  AES-256-GCM; the key lives outside the database. The API reports
  secrets only as `{set: true/false}`.
- **Accounts** — bcrypt passwords with a constant-time dummy compare on
  unknown users; session tokens stored as SHA-256 hashes; cookies
  HttpOnly, SameSite=Strict, Secure over HTTPS.
- Before the first account exists the API is open — that's the setup
  wizard's window; it forces the first account to be an admin and closes
  permanently once created.

## CI

Every push runs gofmt/vet, the full race-enabled test suite, the
frontend build, golangci-lint (gosec included), gitleaks over history,
govulncheck, and a Trivy scan of the built image.
