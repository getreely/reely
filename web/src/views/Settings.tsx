import { useEffect, useRef, useState } from "react"
import { api, auth } from "@/api"
import type {
  ApiCustomFormat, ApiMigrationRow, ApiPlexHealth, ApiQualityProfile, ApiSearchResult, ApiUser,
  ApiWatchedList,
} from "@/api"
import { useApi } from "@/hooks/use-api"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select"
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Film, Pencil, Trash2, Tv, X } from "lucide-react"
import { toast } from "sonner"
import { cn } from "@/lib/utils"
import { useAccess, useIsAdmin, useIsRequester } from "@/lib/access"
import { openPlexTab, runPlexSignIn } from "@/lib/plex"
import { PathInput } from "@/components/PathInput"
import { SharingCard } from "@/components/SharingCard"
import { ListAudience } from "@/components/ListAudience"

// Settings is split into panels behind a horizontal tab bar. Each panel
// mounts only while selected, so Health only polls
// while someone is actually looking at it.
type Panel = "libraries" | "profiles" | "formats" | "naming" | "metadata" | "indexers" | "client" | "lists" | "search" | "users" | "requests" | "sharing" | "plex" | "backup" | "stats" | "health"

const NAV: { id: Panel; label: string }[] = [
  { id: "libraries", label: "Libraries" },
  { id: "profiles", label: "Quality Profiles" },
  { id: "formats", label: "Custom Formats" },
  { id: "naming", label: "File Naming" },
  { id: "metadata", label: "Metadata" },
  { id: "indexers", label: "Indexers" },
  { id: "client", label: "Download Client" },
  { id: "lists", label: "Watched Lists" },
  { id: "search", label: "Search" },
  { id: "users", label: "Users" },
  { id: "requests", label: "Requests" },
  { id: "sharing", label: "Sharing" },
  { id: "plex", label: "Plex" },
  { id: "backup", label: "Backup" },
  { id: "stats", label: "Stats" },
  { id: "health", label: "Health" },
]

export function SettingsView() {
  const isAdmin = useIsAdmin()
  // profiles are shared between the profile card and the library rows
  const profilesQuery = useApi(() => api.profiles())
  const profiles = profilesQuery.data?.profiles ?? []
  // Non-admins get the self-service tabs: watched lists feeding their own
  // libraries, and — for an account that asks rather than adds — where
  // those requests land. Both, because a requester's list files requests
  // rather than adding, so lists are theirs to keep.
  const isRequester = useIsRequester()
  // Out on the portal this page is the person's own two things: where
  // their requests land, and their watchlist — which for an account that
  // asks files requests rather than adding, so it belongs there.
  const { external } = useAccess()
  // The requests panel is where an account says where ITS requests land,
  // which only means something for an account that has libraries granted
  // to it. An admin holds every library and has no grants, so there is
  // nothing there to set — they name a library per request instead.
  const nav = external
    ? NAV.filter(n => n.id === "lists" || (n.id === "requests" && !isAdmin))
    : isAdmin
      ? NAV
      : NAV.filter(n => n.id === "lists" || (n.id === "requests" && isRequester))
  const [panel, setPanel] = useState<Panel>(isAdmin && !external ? "libraries" : "lists")
  return (
    <section>
      <h1 className="font-display text-[28px] font-bold tracking-tight">Settings</h1>
      <nav className="mb-7 mt-4 flex w-full flex-row flex-wrap gap-x-1 border-b">
        {nav.map(n => (
          <button key={n.id} onClick={() => setPanel(n.id)}
            className={cn(
              "-mb-px flex items-center gap-1.5 border-b-2 border-transparent px-3 py-2.5 text-[13px] font-medium text-muted-foreground hover:text-foreground",
              panel === n.id && "border-brass text-brass"
            )}>
            {n.label}
          </button>
        ))}
      </nav>
      {/* the formats tab runs two columns side by side; everything else
          stays a single narrow stack */}
      <div className={cn("grid gap-6", panel === "formats" ? "max-w-[980px]" : "max-w-[560px]")}>
        {panel === "libraries" && <LibrariesCard profiles={profiles} />}
        {panel === "profiles" && <ProfilesCard profiles={profiles} onChanged={() => profilesQuery.reload()} />}
        {panel === "formats" && <FormatsCard />}
        {panel === "naming" && <NamingCard />}
        {panel === "metadata" && <><TmdbCard /><TvdbCard /></>}
        {panel === "indexers" && (
          <ConnectionCard name="Prowlarr" urlKey="prowlarr_url" apiKeyKey="prowlarr_api_key"
            placeholder="http://prowlarr:9696"
            blurb="Release searches go through your Prowlarr — it fans out to every indexer it knows." />
        )}
        {panel === "client" && (
          <div className="grid gap-4">
            <ConnectionCard name="SABnzbd" urlKey="sab_url" apiKeyKey="sab_api_key"
              placeholder="http://sabnzbd:8080"
              blurb="Grabbed usenet releases land in SABnzbd under the categories below; the importer watches the same ones."
              extra={<SabCategories />} />
            <QbitCard />
            <ProtocolCard />
          </div>
        )}
        {panel === "lists" && <ListsCard />}
        {panel === "search" && <div className="grid gap-4"><RssCard /><WantedSweepCard /></div>}
        {panel === "users" && <UsersCard />}
        {panel === "sharing" && <SharingCard />}
        {panel === "requests" && <MyRequestsCard />}
        {panel === "plex" && <PlexCard />}
        {panel === "backup" && <BackupCard />}
        {panel === "stats" && <StatsCard />}
        {panel === "health" && <HealthCard />}
      </div>
    </section>
  )
}

// NamingCard edits the two naming templates. They are install-wide and
// admin-only, and they are not a cosmetic preference: the organize pass
// moves real files to wherever the template says they belong, so saving a
// bad one renames a library around it.
//
// Hence the shape of this card — every keystroke is rendered by the
// server, using the same code the importer runs, and a template that
// can't be used says why instead of being quietly accepted.
function NamingCard() {
  return (
    <div className="rounded-2xl border border-linesoft bg-surface p-5">
      <h2 className="mb-1 text-[15px] font-bold">File Naming</h2>
      <p className="mb-4 text-[12.5px] text-muted-foreground">
        How imported files are named and foldered inside a library root. One template per kind,
        shared by every library. Slashes make folders; the file extension comes from the source.
      </p>
      <NamingFields />
      <QualityRepair />
    </div>
  )
}

// QualityRepair surfaces the files whose quality was never recorded, and
// lets someone fix them now rather than at the next restart. It reads the
// resolution out of each file — no re-download, and nothing is renamed.
function QualityRepair() {
  const status = useApi(() => api.qualityRepairStatus())
  const [busy, setBusy] = useState(false)
  const pending = status.data?.pending ?? 0
  const pendingQ = status.data?.pendingQuality ?? 0
  const pendingS = status.data?.pendingSource ?? 0

  const run = async () => {
    setBusy(true)
    try {
      const res = await api.qualityRepair()
      const parts = []
      if (res.qualityFixed > 0) parts.push(`${res.qualityFixed} quality recorded`)
      if (res.sourceFixed > 0) parts.push(`${res.sourceFixed} source${res.sourceFixed === 1 ? "" : "s"} recovered from MKV titles`)
      toast.success(parts.length ? parts.join(" · ") : "Nothing could be read")
      if (res.unknown > 0) {
        toast(`${res.unknown} file${res.unknown === 1 ? "" : "s"} still short — a container reely can't read, or a title tag with no release name in it`)
      }
      status.reload()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally { setBusy(false) }
  }

  return (
    <div className="mt-6 border-t border-linesoft pt-4">
      <div className="mono-label mb-1.5 text-faint">Recorded quality & source</div>
      <p className="mb-3 max-w-[62ch] text-[12.5px] text-muted-foreground">
        {pending === 0
          ? "Every file on disk has its quality and source recorded. Files that lose one — a naming template that left it out, a container another tool wrote — are read straight from the file, once at startup and here on demand."
          : <>
              {pendingQ > 0 && <><strong className="text-foreground">{pendingQ}</strong> file{pendingQ === 1 ? "" : "s"} missing quality (read from the video itself){pendingS > 0 ? ", " : ". "}</>}
              {pendingS > 0 && <><strong className="text-foreground">{pendingS}</strong> missing source (recovered only when an MKV's title tag carries the release name — never guessed). </>}
              Nothing is re-downloaded and nothing is renamed; this only fills in the records the sweeps
              reason with.
            </>}
      </p>
      <Button variant="outline" className="h-8 px-3 text-[12.5px]" disabled={busy || pending === 0}
        onClick={() => void run()}>
        {busy ? "Reading files…" : pending === 0 ? "Nothing to fix" : `Read from ${pending} file${pending === 1 ? "" : "s"}`}
      </Button>
    </div>
  )
}

function NamingFields() {
  const movieStored = useApi(() => api.getSetting("naming_movie"))
  const showStored = useApi(() => api.getSetting("naming_show"))
  const [movie, setMovie] = useState<string | null>(null) // null = untouched
  const [show, setShow] = useState<string | null>(null)

  const movieValue = movie ?? movieStored.data?.value ?? ""
  const showValue = show ?? showStored.data?.value ?? ""

  const [preview, setPreview] = useState<Awaited<ReturnType<typeof api.namingPreview>> | null>(null)
  // wait for both stored values before previewing, or the first render
  // asks about two empty templates and reports them as errors; after that
  // every edit is previewed, emptied fields included — an empty template
  // is exactly the case that needs to say why it can't be used
  const loaded = movieStored.data !== null && showStored.data !== null
  useEffect(() => {
    if (!loaded) return
    // a round trip per keystroke otherwise
    const id = setTimeout(() => {
      api.namingPreview(movieValue, showValue).then(setPreview).catch(() => setPreview(null))
    }, 250)
    return () => clearTimeout(id)
  }, [loaded, movieValue, showValue])

  return (
    <div className="grid gap-6">
      <NamingField
        label="Movies" kind="movie"
        value={movieValue}
        stored={movieStored.data?.value ?? ""}
        dirty={movie !== null && movie !== (movieStored.data?.value ?? "")}
        onChange={setMovie}
        onSaved={() => { setMovie(null); movieStored.reload() }}
        settingKey="naming_movie"
        tokens={preview?.tokens.movie ?? []}
        fallback={preview?.defaults.movie ?? ""}
        result={preview?.movie}
      />
      <NamingField
        label="Shows" kind="show"
        value={showValue}
        stored={showStored.data?.value ?? ""}
        dirty={show !== null && show !== (showStored.data?.value ?? "")}
        onChange={setShow}
        onSaved={() => { setShow(null); showStored.reload() }}
        settingKey="naming_show"
        tokens={preview?.tokens.show ?? []}
        fallback={preview?.defaults.show ?? ""}
        result={preview?.show}
      />
    </div>
  )
}

function NamingField({
  label, kind, value, stored, dirty, onChange, onSaved, settingKey, tokens, fallback, result,
}: {
  label: string
  kind: "movie" | "show"
  value: string
  stored: string
  dirty: boolean
  onChange: (v: string) => void
  onSaved: () => void
  settingKey: string
  tokens: string[]
  fallback: string
  result?: { path: string; error: string }
}) {
  const [busy, setBusy] = useState(false)
  const invalid = !!result?.error
  const isDefault = stored !== "" && stored === fallback

  const save = async () => {
    setBusy(true)
    try {
      await api.putSetting(settingKey, value.trim())
      toast.success(`${label} naming saved`)
      onSaved()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally { setBusy(false) }
  }

  // inserting at the caret beats making people type "{episode:00}" exactly
  const insert = (token: string) => {
    const el = document.getElementById(`naming-${kind}`) as HTMLInputElement | null
    const at = el?.selectionStart ?? value.length
    onChange(value.slice(0, at) + token + value.slice(el?.selectionEnd ?? at))
    requestAnimationFrame(() => {
      el?.focus()
      el?.setSelectionRange(at + token.length, at + token.length)
    })
  }

  return (
    <div>
      <div className="mb-1.5 flex items-center gap-2">
        <span className="mono-label text-faint">{label}</span>
        {isDefault && <span className="mono-label text-faint">· default</span>}
      </div>
      <div className="flex gap-2">
        <Input id={`naming-${kind}`} value={value} onChange={e => onChange(e.target.value)}
          spellCheck={false} autoComplete="off"
          className={cn("font-label text-[12.5px]", invalid && "border-want focus-visible:ring-want")} />
        <Button onClick={() => void save()} disabled={busy || !dirty || invalid}>Save</Button>
      </div>

      <div className="mt-2 flex flex-wrap items-center gap-1.5">
        {tokens.map(tk => (
          <button key={tk} type="button" onClick={() => insert(tk)}
            className="font-label rounded-md border border-linesoft px-1.5 py-0.5 text-[10.5px] text-muted-foreground hover:border-brass/60 hover:text-brass">
            {tk}
          </button>
        ))}
        {fallback && value.trim() !== fallback && (
          <button type="button" onClick={() => onChange(fallback)}
            className="mono-label ml-auto text-faint hover:text-brass">reset to default</button>
        )}
      </div>

      <div className="mt-2.5 rounded-lg border border-linesoft bg-surface2 px-3 py-2">
        <div className="mono-label mb-1 text-faint">{invalid ? "Can't use this" : "A file would land at"}</div>
        {invalid
          ? <div className="text-[12.5px] text-want">{result?.error}</div>
          : <div className="font-label break-all text-[12px]">
              {result?.path
                ? <><span className="text-faint">…/{label === "Movies" ? "Movies" : "Shows"}/</span>{result.path}<span className="text-faint">.mkv</span></>
                : <span className="text-faint">…</span>}
            </div>}
      </div>
    </div>
  )
}

// StatsCard is the install at a glance: what the libraries hold, what it
// costs in disk, and how the loop has been doing this month.
function StatsCard() {
  const { data } = useApi(() => api.stats(), 30_000)
  if (!data) return null
  const gb = (b: number) => b >= (1 << 30) ? `${(b / (1 << 30)).toFixed(1)} GB` : `${(b / (1 << 20)).toFixed(0)} MB`
  const tiles: [string, string][] = [
    ["Movies on disk", `${data.moviesOnDisk} / ${data.movies}`],
    ["Episodes on disk", `${data.episodesOnDisk} / ${data.episodes}`],
    ["Disk used", gb(data.totalBytes)],
    ["Grabs · 30 days", String(data.grabs30d)],
    ["Imports · 30 days", String(data.imports30d)],
    ["Failures · 30 days", String(data.failures30d)],
  ]
  return (
    <div className="rounded-2xl border border-linesoft bg-surface p-5">
      <h2 className="mb-3 text-[15px] font-bold">Stats</h2>
      <div className="grid grid-cols-2 gap-2.5 sm:grid-cols-3">
        {tiles.map(([label, value]) => (
          <div key={label} className="rounded-xl border border-linesoft bg-surface2 px-3 py-2.5">
            <div className="mono-label text-faint">{label}</div>
            <div className="mt-0.5 text-[17px] font-bold tabular-nums">{value}</div>
          </div>
        ))}
      </div>
      {data.blocklisted > 0 && (
        <p className="mt-2.5 text-[12.5px] text-muted-foreground">
          {data.blocklisted} release{data.blocklisted === 1 ? "" : "s"} on the blocklist (Activity → Blocklist)
        </p>
      )}
      <div className="mt-4 border-t">
        {data.libraries.map(l => (
          <div key={l.id} className="flex items-baseline gap-3 border-b border-linesoft px-1 py-2">
            <div className="min-w-0 flex-1">
              <div className="truncate text-[13px] font-semibold">{l.name}</div>
              <div className="mono-label mt-0.5 text-faint">{l.kind === "movies" ? `${l.titles} movies` : `${l.titles} shows`} · {l.onDisk} on disk{l.missing > 0 ? ` · ${l.missing} wanted` : ""}</div>
            </div>
            <span className="mono-label shrink-0 text-faint">{gb(l.bytes)}</span>
          </div>
        ))}
      </div>
    </div>
  )
}

// BackupCard is the database's safety net: a rotating daily backup plus
// back-up-now, download, and restore. Restore stages the snapshot and
// restarts reely — the swap happens before anything reopens the database.
// Media files on disk are never part of it; this is the catalog, settings,
// and accounts.
function BackupCard() {
  const { data, reload } = useApi(() => api.backups())
  const [busy, setBusy] = useState(false)
  const backups = data?.backups ?? []

  const run = async (label: string, fn: () => Promise<unknown>) => {
    setBusy(true)
    try {
      await fn()
      if (label) toast.success(label)
      reload({ quiet: true })
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setBusy(false) }
  }
  const restore = (name: string) => {
    if (!window.confirm(`Restore ${name}?\n\nThe current database is replaced with this snapshot and reely restarts (a few seconds under Docker). Media files are untouched.`)) return
    void run("Restore staged — reely is restarting, reload the page in a few seconds", () => api.restoreBackup(name))
  }
  const uploadRestore = (file: File) => {
    if (!window.confirm(`Restore from ${file.name}?\n\nThe current database is replaced with this file and reely restarts. Media files are untouched.`)) return
    void run("Restore staged — reely is restarting, reload the page in a few seconds", () => api.uploadRestore(file))
  }

  return (
    <div className="rounded-2xl border border-linesoft bg-surface p-5">
      <h2 className="mb-1 text-[15px] font-bold">Backup</h2>
      <p className="mb-3 text-[12.5px] text-muted-foreground">
        A backup is the whole database — catalog, settings, users — written daily and
        kept for two weeks. Download one before big changes; restore swaps everything
        back to that moment and restarts reely. Your media files are never touched.
      </p>
      <Button variant="outline" disabled={busy}
        onClick={() => void run("Backup written", () => api.createBackup())}>
        Back up now
      </Button>
      <label className="ml-2 inline-block">
        <span className={cn("inline-flex h-9 cursor-pointer items-center rounded-md border px-4 text-sm font-medium shadow-xs hover:bg-accent", busy && "pointer-events-none opacity-50")}>
          Restore from file…
        </span>
        <input type="file" accept=".db" className="hidden"
          onChange={e => { const f = e.target.files?.[0]; if (f) uploadRestore(f); e.target.value = "" }} />
      </label>
      <div className="mt-4 border-t">
        {backups.length === 0 && (
          <p className="mono-label py-3 text-faint">no backups yet — the first daily one lands within the hour</p>
        )}
        {backups.map(b => (
          <div key={b.name} className="flex items-baseline gap-3 border-b border-linesoft px-1 py-2">
            <div className="min-w-0 flex-1">
              <div className="truncate text-[13px] font-semibold">{b.name}</div>
              <div className="mono-label mt-0.5 text-faint">
                {b.createdAt.slice(0, 16).replace("T", " ")} · {(b.size / (1 << 20)).toFixed(1)} MB
              </div>
            </div>
            <a className="mono-label text-faint hover:text-brass" href={`/api/v1/backups/${encodeURIComponent(b.name)}`}
              download>download</a>
            <button className="mono-label text-faint hover:text-brass" disabled={busy}
              onClick={() => restore(b.name)}>restore</button>
            <button className="text-faint hover:text-want" title="Delete backup" disabled={busy}
              onClick={() => { if (window.confirm(`Delete ${b.name}?`)) void run("Backup deleted", () => api.deleteBackup(b.name)) }}>
              <Trash2 className="h-3.5 w-3.5" />
            </button>
          </div>
        ))}
      </div>
    </div>
  )
}

// HealthCard is the install's vital signs, refreshed while Settings is
// open: whether each companion service answers, which Prowlarr indexers
// are benched and why, and whether SAB is paused or low on disk.
function HealthCard() {
  const { data, loading } = useApi(() => api.health(), 30_000)
  if (!data && loading) return null
  if (!data) return null
  const { tmdb, prowlarr, sab, qbit } = data

  const sabNote = !sab.configured ? "not set up"
    : !sab.ok ? sab.error || "unreachable"
    : [
        sab.version ? `v${sab.version}` : null,
        sab.paused ? "PAUSED" : null,
        sab.diskFreeGb !== undefined ? `${sab.diskFreeGb.toFixed(0)} GB free` : null,
      ].filter(Boolean).join(" · ")
  const sabState: DotState = !sab.configured ? "off"
    : !sab.ok ? "bad"
    : sab.paused || (sab.diskFreeGb !== undefined && sab.diskFreeGb < 10) ? "warn" : "good"

  // qBittorrent gets its own row rather than sharing SAB's: an install
  // may run both, and which one is unwell is the first thing worth
  // knowing. A seeding torrent holds its data indefinitely, so low disk
  // is a warning here for the same reason it is for SAB, only sooner.
  const qbitNote = !qbit.configured ? "not set up"
    : !qbit.ok ? qbit.error || "unreachable"
    : [
        qbit.version ? `v${qbit.version}` : null,
        qbit.altSpeedOn ? "ALT SPEED" : null,
        qbit.torrents !== undefined ? `${qbit.torrents} torrents` : null,
        qbit.diskFreeGb !== undefined ? `${qbit.diskFreeGb.toFixed(0)} GB free` : null,
      ].filter(Boolean).join(" · ")
  const qbitState: DotState = !qbit.configured ? "off"
    : !qbit.ok ? "bad"
    : qbit.altSpeedOn || (qbit.diskFreeGb !== undefined && qbit.diskFreeGb < 10) ? "warn" : "good"

  const plex = data.plex
  // Plex is two legs. Reaching plex.tv but not the media server is the
  // ordinary mistake — sign-in works, nobody reaches a library — so it
  // reads as a warning with its own reason rather than as "connected".
  const plexState: DotState = !plex.configured ? "off"
    : !plex.ok ? "bad"
    : !plex.serverOk || plex.unmatched > 0 ? "warn" : "good"
  const plexNote = !plex.configured ? "not linked"
    : !plex.ok ? plex.error || "plex.tv unreachable"
    : !plex.serverOk ? "server unreachable"
    : `${plex.people} ${plex.people === 1 ? "person" : "people"}` +
      (plex.pending > 0 ? ` · ${plex.pending} not accepted` : "") +
      (plex.unmatched > 0 ? ` · ${plex.unmatched} unmatched` : "")

  const benched = prowlarr.indexers.filter(ix => ix.enabled && !ix.healthy)
  const prowlarrState: DotState = !prowlarr.configured ? "off"
    : !prowlarr.ok ? "bad"
    : benched.length > 0 || prowlarr.warnings.length > 0 ? "warn" : "good"
  const prowlarrNote = !prowlarr.configured ? "not set up"
    : !prowlarr.ok ? prowlarr.error || "unreachable"
    : `${prowlarr.indexers.filter(ix => ix.enabled).length} indexers` +
      (benched.length > 0 ? ` · ${benched.length} down` : " · all healthy")

  return (
    <div className="rounded-2xl border border-linesoft bg-surface p-5">
      <h2 className="mb-3 text-[15px] font-bold">Health</h2>
      <HealthRow state={tmdb.configured ? "good" : "off"} name="TMDB"
        note={tmdb.configured ? "key set" : "no API key"} />
      <HealthRow state={prowlarrState} name="Prowlarr" note={prowlarrNote} />
      {prowlarr.ok && prowlarr.indexers.map(ix => (
        <HealthRow key={ix.name} indent name={ix.name}
          state={!ix.enabled ? "off" : ix.healthy ? "good" : "warn"}
          note={!ix.enabled ? "disabled in Prowlarr"
            : ix.healthy ? "healthy"
            : `benched after failures${ix.disabledTill ? ` · retries ${fmtTill(ix.disabledTill)}` : ""}`} />
      ))}
      {prowlarr.warnings.map((wng, i) => (
        <div key={i} className="ml-6 border-b border-linesoft py-1.5 text-[12px] text-want">{wng}</div>
      ))}
      <HealthRow state={sabState} name="SABnzbd" note={sabNote} />
      <HealthRow state={qbitState} name="qBittorrent" note={qbitNote} />
      {plex.configured && <>
        <HealthRow name={plex.serverName || "Plex"} state={plexState} note={plexNote} />
        {/* the libraries are the part worth seeing: an unmatched one
            means those people sign in and reach nothing */}
        {plex.serverOk && plex.libraries.map(l => (
          <HealthRow key={l.key} indent name={l.title}
            state={l.matched ? "good" : "warn"}
            note={l.matched ? `→ ${l.matched}` : "no reely library at that folder"} />
        ))}
        {!plex.serverOk && plex.ok && (
          <div className="ml-6 border-b border-linesoft py-1.5 text-[12px] text-want">
            {plex.serverError || "the Plex server is unreachable"}
          </div>
        )}
      </>}
    </div>
  )
}

type DotState = "good" | "warn" | "bad" | "off"

function HealthRow({ state, name, note, indent }: {
  state: DotState; name: string; note: string; indent?: boolean
}) {
  const dot = {
    good: "bg-good", warn: "bg-want", bad: "bg-want", off: "bg-surface3",
  }[state]
  return (
    <div className={cn("flex items-center gap-2.5 border-b border-linesoft py-2 text-[13px]", indent && "ml-6")}>
      <span className={cn("h-2 w-2 shrink-0 rounded-full", dot, state === "bad" && "ring-2 ring-want/40")} />
      <span className="font-semibold">{name}</span>
      <span className={cn("font-label ml-auto text-right text-[11.5px]",
        state === "bad" || state === "warn" ? "text-want" : "text-faint")}>{note}</span>
    </div>
  )
}

// fmtTill renders an RFC3339 bench-expiry as a friendly "in 28 min".
function fmtTill(iso: string): string {
  const ms = new Date(iso).getTime() - Date.now()
  if (Number.isNaN(ms) || ms <= 0) return "soon"
  const min = Math.round(ms / 60_000)
  if (min < 60) return `in ${min} min`
  const h = Math.round(min / 60)
  return `in ${h}h`
}

// UsersCard manages accounts: admins run the install, plain users see only
// the libraries picked for them here.
function UsersCard() {
  const usersQuery = useApi(() => auth.users())
  const libsQuery = useApi(() => api.libraries())
  const users = usersQuery.data?.users ?? []
  const libraries = libsQuery.data?.libraries ?? []

  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [role, setRole] = useState<"admin" | "user">("user")
  const [libraryIds, setLibraryIds] = useState<number[]>([])
  const [busy, setBusy] = useState(false)
  // one open at a time: the panel is tall, and a column of them turns the
  // list of accounts into a form
  const [editing, setEditing] = useState(0)

  const toggleLib = (id: number) =>
    setLibraryIds(ids => ids.includes(id) ? ids.filter(x => x !== id) : [...ids, id])

  const create = async () => {
    setBusy(true)
    try {
      await auth.createUser(username.trim(), password, role, role === "user" ? libraryIds : undefined)
      toast.success(`${username.trim()} created`)
      setUsername(""); setPassword(""); setLibraryIds([])
      usersQuery.reload()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }
  const remove = async (id: number, name: string) => {
    if (!window.confirm(`Delete account "${name}"?`)) return
    try {
      await auth.deleteUser(id)
      toast(`${name} deleted`)
      usersQuery.reload()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    }
  }
  const changeRole = async (u: ApiUser, next: "admin" | "user") => {
    if (u.role === next) return
    try {
      await auth.setUserRole(u.id, next)
      toast.success(next === "admin"
        ? `${u.username} is an admin`
        : `${u.username} is a plain user again`)
      usersQuery.reload()
    } catch (e) {
      // the last-admin refusal lands here, and says why
      toast.error(`${e instanceof Error ? e.message : e}`)
    }
  }
  const setUserLibs = async (id: number, name: string, current: number[], libId: number) => {
    const next = current.includes(libId) ? current.filter(x => x !== libId) : [...current, libId]
    try {
      await auth.setUserLibraries(id, next)
      toast.success(`${name}'s libraries updated`)
      usersQuery.reload()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    }
  }

  return (
    <div className="rounded-2xl border border-linesoft bg-surface p-5">
      <h2 className="mb-1 text-[15px] font-bold">Users</h2>
      <p className="mb-3 text-[12.5px] text-muted-foreground">
        Admins run the install; plain users see only the libraries granted to them.
      </p>
      {users.map(u => (
        <div key={u.id} className="border-b border-linesoft py-2 text-[13px]">
        <div className="flex items-center gap-2.5">
          <span className="font-semibold">{u.username}</span>
          {/* The role is a control rather than a label. It reads the same
              either way, so promoting and demoting are the same gesture
              — and it works on a Plex account like any other, since how
              somebody signs in and what they may do are separate. */}
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <button className={cn("font-label rounded-md px-2 py-0.5 text-[11px] font-semibold",
                u.role === "admin"
                  ? "bg-brass/15 text-brass hover:bg-brass/25"
                  : "bg-surface3 text-muted-foreground hover:text-foreground")}>
                {u.role} ▾
              </button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="start" className="rounded-xl">
              <DropdownMenuLabel className="mono-label text-faint">Role</DropdownMenuLabel>
              <DropdownMenuItem onClick={() => void changeRole(u, "user")}>
                {u.role === "user" ? "✓ " : "   "}User — the libraries you grant
              </DropdownMenuItem>
              <DropdownMenuItem onClick={() => void changeRole(u, "admin")}>
                {u.role === "admin" ? "✓ " : "   "}Admin — the whole install
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          {u.role === "admin"
            ? null
            : (
              <DropdownMenu>
                <DropdownMenuTrigger asChild>
                  <button className="font-label rounded-md bg-surface3 px-2 py-0.5 text-[11px] text-muted-foreground hover:text-foreground">
                    {(u.libraryIds ?? []).length} librar{(u.libraryIds ?? []).length === 1 ? "y" : "ies"} ▾
                  </button>
                </DropdownMenuTrigger>
                <DropdownMenuContent align="start" className="rounded-xl">
                  <DropdownMenuLabel className="mono-label text-faint">May access</DropdownMenuLabel>
                  {libraries.map(l => (
                    <DropdownMenuItem key={l.id}
                      onClick={() => void setUserLibs(u.id, u.username, u.libraryIds ?? [], l.id)}>
                      {(u.libraryIds ?? []).includes(l.id) ? "✓ " : "  "}{l.name}
                    </DropdownMenuItem>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
            )}
          {u.role !== "admin" && (
            <button
              className={cn(
                "font-label rounded-md px-2 py-0.5 text-[11px]",
                u.mayAdd
                  ? "bg-surface3 text-muted-foreground hover:text-foreground"
                  : "bg-want/15 text-want"
              )}
              title="What this account may do without asking"
              onClick={() => setEditing(id => id === u.id ? 0 : u.id)}>
              {u.mayAdd ? "adds" : "asks"} ▾
            </button>
          )}
          <button className="ml-auto text-want opacity-70 hover:opacity-100" title="Delete account"
            onClick={() => void remove(u.id, u.username)}>
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
        {editing === u.id && u.role !== "admin" && (
          <div className="mt-2">
            <RequestControls user={u} onSaved={() => { setEditing(0); usersQuery.reload() }} />
          </div>
        )}
        </div>
      ))}
      <div className="mt-3 grid gap-2">
        <div className="grid gap-2 sm:grid-cols-[1fr_1fr_110px]">
          <Input value={username} onChange={e => setUsername(e.target.value)}
            placeholder="Username" autoComplete="off" />
          <Input type="password" value={password} onChange={e => setPassword(e.target.value)}
            placeholder="Password" autoComplete="new-password" />
          <Select value={role} onValueChange={v => setRole(v as "admin" | "user")}>
            <SelectTrigger><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="user">User</SelectItem>
              <SelectItem value="admin">Admin</SelectItem>
            </SelectContent>
          </Select>
        </div>
        {role === "user" && libraries.length > 0 && (
          <div className="flex flex-wrap items-center gap-1.5">
            <span className="mono-label text-faint">May access:</span>
            {libraries.map(l => (
              <button key={l.id} onClick={() => toggleLib(l.id)}
                className={cn(
                  "rounded-lg border px-2 py-0.5 text-[12px] font-semibold",
                  libraryIds.includes(l.id)
                    ? "border-brass/50 bg-brass/15 text-brass"
                    : "border-linesoft text-muted-foreground hover:text-foreground"
                )}>
                {l.name}
              </button>
            ))}
          </div>
        )}
        <Button className="justify-self-start" onClick={() => void create()}
          disabled={busy || !username.trim() || !password || (role === "user" && libraryIds.length === 0)}>
          Add user
        </Button>
      </div>
    </div>
  )
}

// PlexCard is where the owner links their Plex account, which is what
// makes signing in with Plex possible at all: reely reads the sharing
// list with the owner's token, and everyone else is checked against it.
//
// Linking is the same PIN flow everyone else uses, with one difference —
// it is the one sign-in that cannot be checked against a sharing list,
// because reading the list is what it enables. So it is only accepted
// while an admin is already signed in and asking for it, which is what
// the link flag on the request says.
function PlexCard() {
  const status = useApi(() => api.plexStatus())
  const linked = status.data?.linked ?? false
  // An install linked before accounts could be tied to Plex is linked
  // without its owner being — so the button has to still be there.
  const accountLinked = status.data?.accountLinked ?? false
  const [servers, setServers] = useState<{ name: string; machineId: string }[]>([])
  const [serverUrl, setServerUrl] = useState("")
  const [busy, setBusy] = useState(false)
  const [waiting, setWaiting] = useState(false)
  const [report, setReport] = useState<ApiPlexHealth | null>(null)
  const stop = useRef(false)
  useEffect(() => () => { stop.current = true }, [])
  useEffect(() => { setServerUrl(status.data?.serverUrl ?? "") }, [status.data?.serverUrl])

  const link = async () => {
    // opened on the click itself, before anything is awaited: a window
    // opened from a promise callback is no longer a user gesture
    const tab = openPlexTab()
    setWaiting(true)
    try {
      const res = await runPlexSignIn(tab, true, () => stop.current)
      toast.success(`Linked ${res.account ?? "your Plex account"}`)
      if ((res.servers?.length ?? 0) > 1) setServers(res.servers ?? [])
      status.reload()
    } catch (e) {
      if (!stop.current) toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      if (!stop.current) setWaiting(false)
    }
  }

  const save = async (key: string, value: string) => {
    setBusy(true)
    try {
      await api.putSetting(key, value)
      status.reload()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally { setBusy(false) }
  }

  const test = async () => {
    setBusy(true)
    try {
      setReport(await api.plexTest())
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally { setBusy(false) }
  }

  const sync = async () => {
    setBusy(true)
    try {
      const r = await api.plexSync()
      toast.success(
        `${r.accounts} account${r.accounts === 1 ? "" : "s"} in step` +
        (r.deactivated ? `, ${r.deactivated} deactivated` : "") +
        (r.pendingInvites ? `, ${r.pendingInvites} invite${r.pendingInvites === 1 ? "" : "s"} not accepted` : ""))
      if (r.unmatched > 0) {
        toast.warning(
          `${r.unmatched} ${r.unmatched === 1 ? "person reaches" : "people reach"} no reely library — ` +
          "check that a library's folder matches the Plex one")
      }
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally { setBusy(false) }
  }

  const unlink = async () => {
    if (!window.confirm(
      "Unlink Plex?\n\nEveryone who signs in with Plex is deactivated and signed out. " +
      "Their accounts and request history are kept, so re-linking picks them back up.")) return
    setBusy(true)
    try {
      await api.plexUnlink()
      toast("Plex unlinked")
      setServers([])
      setReport(null)
      status.reload()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally { setBusy(false) }
  }

  return (
    <div className="rounded-2xl border border-linesoft bg-surface p-5">
      <h2 className="mb-1 text-[15px] font-bold">Plex</h2>
      <p className="mb-3 text-[12.5px] text-muted-foreground">
        Link your Plex account and the people you've shared your server with can sign
        in here and request titles. They reach the libraries Plex says they reach, and
        nothing else — unshare someone and they're signed out.
      </p>

      {!linked ? (
        <Button disabled={waiting} onClick={() => void link()}>
          {waiting ? "Waiting for Plex…" : "Link my Plex account"}
        </Button>
      ) : (
        <div className="grid gap-3">
          <div className="flex items-center gap-2 text-[13px]">
            <span className="rounded-md bg-have/15 px-2 py-0.5 text-[11px] font-semibold text-have">
              linked
            </span>
            <span className="mono-label truncate text-faint">
              {status.data?.machineId ? `server ${status.data.machineId.slice(0, 8)}…` : "no server chosen"}
            </span>
          </div>

          {!accountLinked && (
            <div className="rounded-xl border border-want/40 bg-want/10 px-3 py-2.5">
              <p className="text-[12.5px] text-muted-foreground">
                Your reely account isn't tied to your Plex account yet, so you still sign in
                with a password. Linking it lets you sign in with Plex and turns the password
                off — set <span className="mono-label">REELY_RESTORE_PASSWORD_LOGIN</span> and
                restart once if you ever need it back.
              </p>
              <Button className="mt-2 h-8" disabled={waiting} onClick={() => void link()}>
                {waiting ? "Waiting for Plex…" : "Sign in with Plex from now on"}
              </Button>
            </div>
          )}

          {servers.length > 1 && (
            <div className="grid gap-1.5">
              <span className="mono-label text-faint">Which server is this?</span>
              {servers.map(sv => (
                <button key={sv.machineId} disabled={busy}
                  onClick={() => void save("plex_machine_id", sv.machineId)}
                  className={cn("rounded-xl border px-3 py-2 text-left text-[13px]",
                    status.data?.machineId === sv.machineId
                      ? "border-brass/50 bg-brass/10" : "border-linesoft hover:border-line")}>
                  {sv.name}
                </button>
              ))}
            </div>
          )}

          <div>
            <div className="mono-label mb-1.5 text-muted-foreground">
              Plex server address, as reely reaches it
            </div>
            <div className="flex gap-2">
              <Input value={serverUrl} onChange={e => setServerUrl(e.target.value)}
                placeholder="http://192.168.1.10:32400" />
              <Button variant="outline" disabled={busy}
                onClick={() => void save("plex_server_url", serverUrl.trim())}>
                Save
              </Button>
            </div>
            <p className="mt-1.5 text-[12px] text-muted-foreground">
              Only the server knows where its libraries sit on disk, and the folder is how a
              Plex library is matched to a reely one. Without this, people can sign in but
              reach nothing.
            </p>
          </div>

          <PlexWebhookRow />

          {report && (
            <div className="grid gap-1 rounded-xl border border-linesoft bg-surface2/50 px-3 py-2.5">
              <div className="flex items-center gap-2 text-[13px]">
                <span className={cn("h-2 w-2 shrink-0 rounded-full",
                  report.ok ? "bg-good" : "bg-want")} />
                <span className="font-semibold">plex.tv</span>
                <span className="font-label ml-auto text-[11.5px] text-faint">
                  {report.ok
                    ? `${report.people} shared${report.pending ? ` · ${report.pending} not accepted` : ""}`
                    : report.error || "unreachable"}
                </span>
              </div>
              <div className="flex items-center gap-2 text-[13px]">
                <span className={cn("h-2 w-2 shrink-0 rounded-full",
                  report.serverOk ? "bg-good" : "bg-want")} />
                {/* the name its owner gave it is what a person
                    recognises; "Your server" is the fallback for when the
                    server did not say */}
                <span className="font-semibold">{report.serverName || "Your server"}</span>
                <span className="font-label ml-auto text-right text-[11.5px] text-faint">
                  {report.serverOk ? `${report.libraries.length} libraries` : report.serverError || "unreachable"}
                </span>
              </div>
              {/* the mapping is the answer. "Connected" hides the failure
                  that actually happens: people sign in fine and reach
                  nothing, because no reely library sits at that folder. */}
              {report.libraries.map(l => (
                <div key={l.key} className="ml-4 flex items-center gap-2 text-[12.5px]">
                  <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full",
                    l.matched ? "bg-good" : "bg-want")} />
                  <span>{l.title}</span>
                  <span className={cn("font-label ml-auto text-right text-[11.5px]",
                    l.matched ? "text-faint" : "text-want")}>
                    {l.matched ? `→ ${l.matched}` : "no reely library at that folder"}
                  </span>
                </div>
              ))}
              {report.unmatched > 0 && (
                <p className="mt-1 text-[12px] text-muted-foreground">
                  An unmatched library means those people can sign in and reach nothing.
                  Check that a reely library points at the same folder Plex does.
                </p>
              )}
            </div>
          )}

          <div className="flex gap-2">
            <Button variant="outline" disabled={busy} onClick={() => void test()}>
              Test connection
            </Button>
            <Button variant="outline" disabled={busy} onClick={() => void sync()}>
              Sync people now
            </Button>
            <Button variant="ghost" className="text-want" disabled={busy}
              onClick={() => void unlink()}>
              Unlink
            </Button>
          </div>
          <p className="text-[12px] text-muted-foreground">
            Syncing is a convenience — accounts and library access are re-checked against
            Plex every time somebody signs in, so this only brings things forward early.
          </p>
        </div>
      )}
    </div>
  )
}

// RequestControls is the owner's say over one account: whether it adds
// titles itself or asks, what skips the queue, and how often it may ask.
//
// The three request rows only mean something for an account that asks —
// an account that adds directly never queues anything to auto-approve or
// to count against a limit — so they follow the first switch rather than
// sitting there as settings that quietly do nothing.
function RequestControls({ user, onSaved }: { user: ApiUser; onSaved: () => void }) {
  const [mayAdd, setMayAdd] = useState(user.mayAdd)
  const [autoMovies, setAutoMovies] = useState(user.autoApproveMovies)
  const [autoShows, setAutoShows] = useState(user.autoApproveShows)
  // blank is the no-limit case, and it has to survive a round trip: "" and
  // "0" are different answers, so these stay strings until they are sent
  const [quotaMovies, setQuotaMovies] = useState(user.quotaMoviesWeek?.toString() ?? "")
  const [quotaShows, setQuotaShows] = useState(user.quotaShowsWeek?.toString() ?? "")
  const [busy, setBusy] = useState(false)

  const limit = (v: string) => {
    const n = parseInt(v, 10)
    return Number.isFinite(n) && n >= 0 ? n : null
  }
  const save = async () => {
    setBusy(true)
    try {
      await api.setRequestSettings(user.id, {
        mayAdd, autoApproveMovies: autoMovies, autoApproveShows: autoShows,
        quotaMoviesWeek: limit(quotaMovies), quotaShowsWeek: limit(quotaShows),
      })
      toast.success(`${user.username} updated`)
      onSaved()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }

  const row = (label: string, hint: string, on: boolean, set: (v: boolean) => void, dim = false) => (
    <div className={cn("flex items-center gap-3 py-1.5", dim && "opacity-45")}>
      <div className="min-w-0 flex-1">
        <div className="text-[13px] font-semibold">{label}</div>
        <div className="text-[12px] text-muted-foreground">{hint}</div>
      </div>
      <Switch checked={on} onCheckedChange={set} disabled={dim} />
    </div>
  )

  return (
    <div className="mb-2 grid gap-1 rounded-xl border border-linesoft bg-surface2/50 px-3 py-2">
      {row("Adds titles directly", "Off means they ask and you decide.", mayAdd, setMayAdd)}
      {row("Films skip the queue", "Approved the moment they ask.", autoMovies, setAutoMovies, mayAdd)}
      {row("Series skip the queue", "A series can be a few hundred episodes.", autoShows, setAutoShows, mayAdd)}
      <div className={cn("flex items-center gap-3 py-1.5", mayAdd && "opacity-45")}>
        <div className="min-w-0 flex-1">
          <div className="text-[13px] font-semibold">Weekly limit</div>
          <div className="text-[12px] text-muted-foreground">
            Films and series counted separately. Blank is no limit.
          </div>
        </div>
        <Input className="h-8 w-16" inputMode="numeric" placeholder="films" disabled={mayAdd}
          value={quotaMovies} onChange={e => setQuotaMovies(e.target.value)} />
        <Input className="h-8 w-16" inputMode="numeric" placeholder="series" disabled={mayAdd}
          value={quotaShows} onChange={e => setQuotaShows(e.target.value)} />
      </div>
      <Button className="mt-1 h-8 justify-self-start" disabled={busy} onClick={() => void save()}>
        Save
      </Button>
    </div>
  )
}

// MyRequestsCard is the one setting a requester owns: where the things
// they ask for land. It is theirs rather than the owner's because it
// decides nothing the owner cares about — the request still goes to the
// queue, it just arrives labelled with a library.
//
// With one library there is no question. The server resolves a lone
// library as its own default without anyone picking, so this says which
// one it is and offers nothing to change.
function MyRequestsCard() {
  const { defaultLibraryId, libraryIds } = useAccess()
  const libsQuery = useApi(() => api.libraries())
  // Only libraries GRANTED to this account, which is what the server will
  // accept. An admin sees every library on the install and holds none of
  // them, so listing what they can see would offer buttons that answer
  // "not found" — the setting is about grants, not visibility.
  const libraries = (libsQuery.data?.libraries ?? [])
    .filter(l => libraryIds.includes(l.id))
  // seeded from the session and kept locally: nothing else on the page
  // reads it, and the alternative is refetching the whole session to
  // move a radio button
  const [chosen, setChosen] = useState(defaultLibraryId)
  const [busy, setBusy] = useState(false)

  const pick = async (id: number) => {
    if (id === chosen) return
    setBusy(true)
    try {
      await api.setDefaultLibrary(id)
      setChosen(id)
      toast.success(`Requests will go to ${libraries.find(l => l.id === id)?.name}`)
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="rounded-2xl border border-linesoft bg-surface p-5">
      <h2 className="mb-1 text-[15px] font-bold">Where your requests go</h2>
      <p className="mb-3 text-[12.5px] text-muted-foreground">
        {libraries.length > 1
          ? "Anything you ask for lands in this library once it's approved."
          : "Anything you ask for lands here once it's approved."}
      </p>
      {libraries.length === 0 && !libsQuery.loading && (
        <p className="text-[13px] text-muted-foreground">
          No libraries have been shared with you yet.
        </p>
      )}
      {libraries.length === 1 && (
        <p className="mb-2 text-[12.5px] text-muted-foreground">
          There's only one, so there's nothing to choose.
        </p>
      )}
      <div className="grid gap-1.5">
        {libraries.map(l => {
          // a lone library is the default whether or not anything was ever
          // picked, so it reads as chosen rather than as an open question
          const active = l.id === chosen || libraries.length === 1
          return (
            <button key={l.id} disabled={busy || libraries.length === 1}
              onClick={() => void pick(l.id)}
              className={cn(
                "flex items-center gap-2.5 rounded-xl border px-3 py-2 text-left text-[13px]",
                active
                  ? "border-brass/50 bg-brass/10"
                  : "border-linesoft hover:border-line disabled:opacity-100"
              )}>
              {l.kind === "shows"
                ? <Tv className="h-4 w-4 shrink-0 text-muted-foreground" />
                : <Film className="h-4 w-4 shrink-0 text-muted-foreground" />}
              <span className="font-semibold">{l.name}</span>
              {active && <span className="mono-label ml-auto text-brass">default</span>}
            </button>
          )
        })}
      </div>
    </div>
  )
}

// The TMDB key is the install's own (free at themoviedb.org). It's sealed at
// rest and never echoed — the card only ever knows whether one is set.
function TmdbCard() {
  const status = useApi(() => api.getSetting("tmdb_api_key"))
  const [key, setKey] = useState("")
  const [busy, setBusy] = useState(false)

  const save = async () => {
    setBusy(true)
    try {
      await api.putSetting("tmdb_api_key", key.trim())
      toast.success(key.trim() ? "TMDB key saved" : "TMDB key cleared")
      setKey("")
      status.reload()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="rounded-2xl border border-linesoft bg-surface p-5">
      <div className="mb-1 flex items-center gap-2">
        <h2 className="text-[15px] font-bold">TMDB</h2>
        {status.data?.set
          ? <span className="rounded-md bg-good/15 px-2 py-0.5 text-[11px] font-semibold text-good">Connected</span>
          : <span className="rounded-md bg-want/15 px-2 py-0.5 text-[11px] font-semibold text-want">No key</span>}
      </div>
      <p className="mb-3 text-[12.5px] text-muted-foreground">
        Metadata, artwork, and people come from TMDB using your own API key.
      </p>
      <div className="flex gap-2">
        <Input type="password" value={key} onChange={e => setKey(e.target.value)}
          placeholder={status.data?.set ? "Replace key…" : "API key"} autoComplete="off" />
        <Button onClick={() => void save()} disabled={busy || (!key.trim() && !status.data?.set)}>
          {key.trim() ? "Save" : status.data?.set ? "Clear" : "Save"}
        </Button>
      </div>
      <div className="mt-4 border-t border-linesoft pt-4">
        <div className="mono-label mb-2 text-faint">Home discovery rows</div>
        <DiscoveryToggle settingKey="hide_anime" label="Hide anime"
          blurb="Leaves Japanese animation out of the trending and popular rows." />
        <DiscoveryToggle settingKey="discover_english_only" label="English titles only"
          blurb="Drops Korean, Spanish, Turkish and other non-English titles. British and Australian ones stay." />
        <p className="mono-label mt-2 text-faint">search is unaffected either way</p>
      </div>
    </div>
  )
}

// TvdbCard: the optional show source. With a key, shows search, add, and
// refresh through TheTVDB — the numbering release groups follow, where a
// revival is one continuing series — while movies (and cast, discovery,
// people) stay on TMDB. Without a key everything works exactly as before.
// The migration panel moves the existing library over, one admin click,
// with a manual picker for whatever can't be matched confidently.
function TvdbCard() {
  const status = useApi(() => api.getSetting("tvdb_api_key"))
  const migration = useApi(() => api.tvdbMigrationStatus())
  const [key, setKey] = useState("")
  const [busy, setBusy] = useState(false)
  const [migrating, setMigrating] = useState(false)
  const [report, setReport] = useState<{
    migrated: ApiMigrationRow[]; unresolved: ApiMigrationRow[]; failed: ApiMigrationRow[]
  } | null>(null)

  const save = async () => {
    setBusy(true)
    try {
      await api.putSetting("tvdb_api_key", key.trim())
      toast.success(key.trim() ? "TVDB key saved — shows now come from TheTVDB" : "TVDB key cleared — shows fall back to TMDB")
      setKey("")
      status.reload()
      migration.reload({ quiet: true })
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setBusy(false) }
  }

  const migrate = async () => {
    if (!window.confirm("Migrate your shows to TVDB?\n\nEach show is re-fetched from TheTVDB: titles may change to TVDB's form (e.g. \"Kitchen Nightmares (US)\"), revivals gain their later seasons (added unmonitored), and files and monitor state are kept. Run Organize afterwards if you want folders renamed to match new titles.")) return
    setMigrating(true)
    try {
      const r = await api.tvdbMigrate()
      setReport({ migrated: r.migrated ?? [], unresolved: r.unresolved ?? [], failed: r.failed ?? [] })
      const n = (r.migrated ?? []).length
      toast.success(`${n} show${n === 1 ? "" : "s"} migrated to TVDB`)
      migration.reload({ quiet: true })
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setMigrating(false) }
  }

  const m = migration.data
  const unresolved = report?.unresolved ?? m?.unresolved ?? []
  const failed = report?.failed ?? []

  return (
    <div className="mt-4 rounded-2xl border border-linesoft bg-surface p-5">
      <div className="mb-1 flex items-center gap-2">
        <h2 className="text-[15px] font-bold">TVDB — shows</h2>
        {status.data?.set
          ? <span className="rounded-md bg-good/15 px-2 py-0.5 text-[11px] font-semibold text-good">Connected</span>
          : <span className="rounded-md bg-surface3 px-2 py-0.5 text-[11px] font-semibold text-faint">Optional</span>}
      </div>
      <p className="mb-3 text-[12.5px] text-muted-foreground">
        Optional. With a TVDB API key, shows search, add, and refresh through TheTVDB — the
        numbering release groups actually use, where a revival continues as one series instead
        of splitting in two. Movies stay on TMDB either way; without a key, shows do too.
      </p>
      <div className="flex gap-2">
        <Input type="password" value={key} onChange={e => setKey(e.target.value)}
          placeholder={status.data?.set ? "Replace key…" : "TVDB v4 API key (thetvdb.com/api-information)"} autoComplete="off" />
        <Button onClick={() => void save()} disabled={busy || (!key.trim() && !status.data?.set)}>
          {key.trim() ? "Save" : status.data?.set ? "Clear" : "Save"}
        </Button>
      </div>
      {/* attribution required by TheTVDB's API terms — their brand badge
          (bundled locally; nothing loads from their servers) plus the
          sample wording, linked */}
      <a href="https://thetvdb.com" target="_blank" rel="noreferrer"
        className="mt-3 flex items-center gap-3 rounded-lg border border-linesoft bg-surface2 px-3 py-2.5 hover:border-brass/40">
        <img src="/tvdb.png" alt="TheTVDB" className="h-8 w-auto shrink-0" />
        <span className="text-[12px] leading-snug text-muted-foreground">
          Show metadata provided by TheTVDB.<br />
          Please consider adding missing information or subscribing.
        </span>
      </a>

      {status.data?.set && m && (
        <div className="mt-4 border-t border-linesoft pt-4">
          <div className="mono-label mb-2 text-faint">Migrate existing shows</div>
          <p className="mb-2.5 text-[12.5px] text-muted-foreground">
            {m.tmdbShows === 0
              ? "Every show is on TVDB — nothing to migrate."
              : `${m.tmdbShows} show${m.tmdbShows === 1 ? "" : "s"} still on TMDB (${m.ready} with a known TVDB id, ready to move automatically). ${m.tvdbShows} already on TVDB.`}
          </p>
          {m.tmdbShows > 0 && (
            <Button variant="outline" className="h-8" disabled={migrating} onClick={() => void migrate()}>
              {migrating ? "Migrating…" : `Migrate ${m.tmdbShows} show${m.tmdbShows === 1 ? "" : "s"} to TVDB`}
            </Button>
          )}
          {report && report.migrated.length > 0 && (
            <p className="mt-2 text-[12px] text-good">
              Migrated: {report.migrated.map(r => r.newTitle || r.title).slice(0, 8).join(", ")}
              {report.migrated.length > 8 ? ` and ${report.migrated.length - 8} more` : ""}.
              {report.migrated.some(r => (r.newSeasons ?? 0) > 0) &&
                " New seasons arrived unmonitored — flip on the ones you want."}
            </p>
          )}
          {failed.length > 0 && failed.map(f => (
            <p key={f.id} className="mt-2 text-[12px] text-want">{f.title}: {f.reason}</p>
          ))}
          {unresolved.length > 0 && (
            <div className="mt-3">
              <div className="mono-label mb-1.5 text-faint">Needs a human — pick the right series</div>
              {unresolved.map(u => (
                <ManualMigrateRow key={u.id} row={u} onDone={() => { migration.reload({ quiet: true }); setReport(null) }} />
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

// ManualMigrateRow: one show the sweep couldn't place — search TVDB by
// name, pick the series, done.
function ManualMigrateRow({ row, onDone }: { row: ApiMigrationRow; onDone: () => void }) {
  const [q, setQ] = useState(row.title)
  const [hits, setHits] = useState<ApiSearchResult[]>([])
  const [busy, setBusy] = useState(false)

  const search = async () => {
    setBusy(true)
    try {
      const r = await api.search(q.trim(), "show")
      setHits((r.results ?? []).filter(h => h.tvdbId))
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setBusy(false) }
  }
  const pick = async (tvdbId: number) => {
    setBusy(true)
    try {
      const r = await api.migrateShowTvdb(row.id, tvdbId)
      toast.success(`${r.newTitle || row.title} migrated to TVDB`)
      onDone()
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setBusy(false) }
  }

  return (
    <div className="mb-2 rounded-lg border border-linesoft bg-surface2 p-2.5">
      <div className="mb-1.5 text-[12.5px] font-semibold">{row.title}{row.year ? ` (${row.year})` : ""}</div>
      <div className="flex gap-2">
        <Input value={q} onChange={e => setQ(e.target.value)} className="h-7 text-[12.5px]"
          onKeyDown={e => { if (e.key === "Enter") void search() }} />
        <Button variant="outline" className="h-7 px-2.5 text-[12px]" disabled={busy} onClick={() => void search()}>
          Search TVDB
        </Button>
      </div>
      {hits.map(h => (
        <div key={h.tvdbId} className="mt-1.5 flex items-center gap-2 text-[12.5px]">
          <span className="min-w-0 flex-1 truncate">{h.title}{h.year ? ` (${h.year})` : ""}</span>
          <Button className="h-6 px-2 text-[11px]" disabled={busy} onClick={() => void pick(h.tvdbId!)}>
            This one
          </Button>
        </div>
      ))}
    </div>
  )
}

// The discovery rows come from TMDB's trending and popular charts, which
// are global. These narrow them for one household without touching search
// — looking a title up by name is always a deliberate ask.
function DiscoveryToggle({ settingKey, label, blurb }: {
  settingKey: string; label: string; blurb: string
}) {
  const setting = useApi(() => api.getSetting(settingKey))
  const [busy, setBusy] = useState(false)
  const on = setting.data?.value === "true"
  const toggle = async (v: boolean) => {
    setBusy(true)
    try {
      await api.putSetting(settingKey, v ? "true" : "false")
      setting.reload({ quiet: true })
      toast.success(`${label} ${v ? "on" : "off"}`)
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setBusy(false) }
  }
  return (
    <div className="flex items-start justify-between gap-4 py-1.5">
      <div>
        <div className="text-[13px] font-semibold">{label}</div>
        <p className="mt-0.5 max-w-[48ch] text-[12.5px] text-muted-foreground">{blurb}</p>
      </div>
      <Switch checked={on} disabled={busy} onCheckedChange={v => void toggle(v)} aria-label={label} />
    </div>
  )
}

// ConnectionCard is a URL + sealed API key pair for one companion service
// (Prowlarr, SABnzbd). The key never echoes back — only whether one is set.
function ConnectionCard({ name, urlKey, apiKeyKey, placeholder, blurb, extra }: {
  name: string; urlKey: string; apiKeyKey: string; placeholder: string; blurb: string
  extra?: React.ReactNode
}) {
  const urlQuery = useApi(() => api.getSetting(urlKey))
  const keyQuery = useApi(() => api.getSetting(apiKeyKey))
  const [url, setUrl] = useState<string | null>(null) // null = untouched
  const [key, setKey] = useState("")
  const [busy, setBusy] = useState(false)

  const shownUrl = url ?? urlQuery.data?.value ?? ""
  const configured = !!(urlQuery.data?.value && keyQuery.data?.set)
  const dirty = (url !== null && url !== (urlQuery.data?.value ?? "")) || key.trim() !== ""

  const save = async () => {
    setBusy(true)
    try {
      if (url !== null && url !== (urlQuery.data?.value ?? "")) {
        await api.putSetting(urlKey, url.trim().replace(/\/+$/, ""))
      }
      if (key.trim()) await api.putSetting(apiKeyKey, key.trim())
      toast.success(`${name} settings saved`)
      setUrl(null); setKey("")
      urlQuery.reload(); keyQuery.reload()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="rounded-2xl border border-linesoft bg-surface p-5">
      <div className="mb-1 flex items-center gap-2">
        <h2 className="text-[15px] font-bold">{name}</h2>
        {configured
          ? <span className="rounded-md bg-good/15 px-2 py-0.5 text-[11px] font-semibold text-good">Connected</span>
          : <span className="rounded-md bg-want/15 px-2 py-0.5 text-[11px] font-semibold text-want">Not set up</span>}
      </div>
      <p className="mb-3 text-[12.5px] text-muted-foreground">{blurb}</p>
      <div className="grid gap-2 sm:grid-cols-[1fr_1fr_auto]">
        <Input value={shownUrl} onChange={e => setUrl(e.target.value)}
          placeholder={placeholder} autoComplete="off" />
        <Input type="password" value={key} onChange={e => setKey(e.target.value)}
          placeholder={keyQuery.data?.set ? "Replace API key…" : "API key"} autoComplete="off" />
        <Button onClick={() => void save()} disabled={busy || !dirty}>Save</Button>
      </div>
      {extra}
    </div>
  )
}


// ProtocolCard picks which kinds of release reely will take, and which
// it prefers when both are on offer.
//
// It only narrows what the configured clients already allow. A protocol
// with no client set up cannot be grabbed however this is set, which is
// a fact about the install rather than a choice — so an install running
// one client can ignore this entirely, and the wording says so.
function ProtocolCard() {
  const q = useApi(() => api.getSetting("download_protocol"))
  const value = q.data?.value || "prefer_usenet"
  const set = async (v: string) => {
    try {
      await api.putSetting("download_protocol", v)
      toast.success("Download protocol saved")
      q.reload()
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }
  return (
    <div className="rounded-2xl border border-linesoft bg-surface p-5">
      <h2 className="mb-1 text-[15px] font-bold">Usenet and torrents</h2>
      <p className="mb-3 text-[12.5px] text-muted-foreground">
        Which releases reely will take. A preference only breaks a tie between two
        equally good releases — it never picks a worse one. Turning a protocol off
        leaves its client connected but stops anything being sent to it.
      </p>
      <Select value={value} onValueChange={v => void set(v)}>
        <SelectTrigger className="w-full sm:w-[320px]"><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectItem value="prefer_usenet">Both, prefer usenet</SelectItem>
          <SelectItem value="prefer_torrent">Both, prefer torrents</SelectItem>
          <SelectItem value="usenet_only">Usenet only</SelectItem>
          <SelectItem value="torrent_only">Torrents only</SelectItem>
        </SelectContent>
      </Select>
      <p className="mt-2 text-[12px] text-faint">
        Only the clients you have set up can be used, whatever is chosen here. Search
        results label each release, and say why one cannot be grabbed.
      </p>
    </div>
  )
}

// QbitCard is qBittorrent's connection settings.
//
// It cannot reuse ConnectionCard, and the reason is the whole difference
// between the two clients: SAB authenticates with an API key, a single
// machine credential you paste once. qBittorrent has no API key. It
// signs in with the WebUI's own username and password and keeps a
// session cookie, so there are two credential fields rather than one.
//
// Both are optional, deliberately. qBittorrent can be told to bypass
// authentication for localhost or a whitelisted subnet, which is an
// ordinary LAN setup — so a URL on its own is a complete configuration,
// and demanding a password would be reely inventing a rule qBittorrent
// does not have.
function QbitCard() {
  const urlQuery = useApi(() => api.getSetting("qbit_url"))
  const userQuery = useApi(() => api.getSetting("qbit_username"))
  const passQuery = useApi(() => api.getSetting("qbit_password"))
  const [url, setUrl] = useState<string | null>(null) // null = untouched
  const [user, setUser] = useState<string | null>(null)
  const [pass, setPass] = useState("")
  const [busy, setBusy] = useState(false)

  const savedUrl = urlQuery.data?.value ?? ""
  const savedUser = userQuery.data?.value ?? ""
  const shownUrl = url ?? savedUrl
  const shownUser = user ?? savedUser
  // a URL is the whole requirement — see above
  const configured = !!savedUrl
  const dirty = (url !== null && url !== savedUrl)
    || (user !== null && user !== savedUser)
    || pass.trim() !== ""

  const save = async () => {
    setBusy(true)
    try {
      if (url !== null && url !== savedUrl) {
        await api.putSetting("qbit_url", url.trim().replace(/\/+$/, ""))
      }
      if (user !== null && user !== savedUser) {
        await api.putSetting("qbit_username", user.trim())
      }
      if (pass.trim()) await api.putSetting("qbit_password", pass.trim())
      toast.success("qBittorrent settings saved")
      setUrl(null); setUser(null); setPass("")
      urlQuery.reload(); userQuery.reload(); passQuery.reload()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="rounded-2xl border border-linesoft bg-surface p-5">
      <div className="mb-1 flex items-center gap-2">
        <h2 className="text-[15px] font-bold">qBittorrent</h2>
        {configured
          ? <span className="rounded-md bg-good/15 px-2 py-0.5 text-[11px] font-semibold text-good">Connected</span>
          : <span className="rounded-md bg-want/15 px-2 py-0.5 text-[11px] font-semibold text-want">Not set up</span>}
      </div>
      <p className="mb-3 text-[12.5px] text-muted-foreground">
        Torrent releases go to qBittorrent. It has no API key — sign in with the same
        username and password as its WebUI. Leave both blank if qBittorrent is set to
        bypass authentication for your network.
      </p>
      <div className="grid gap-2 sm:grid-cols-[1fr_auto]">
        <Input value={shownUrl} onChange={e => setUrl(e.target.value)}
          placeholder="http://qbittorrent:8080" autoComplete="off" />
        <Button onClick={() => void save()} disabled={busy || !dirty}>Save</Button>
      </div>
      <div className="mt-2 grid gap-2 sm:grid-cols-2">
        <Input value={shownUser} onChange={e => setUser(e.target.value)}
          placeholder="Username (optional)" autoComplete="off" />
        <Input type="password" value={pass} onChange={e => setPass(e.target.value)}
          placeholder={passQuery.data?.set ? "Replace password…" : "Password (optional)"}
          autoComplete="new-password" />
      </div>
      <p className="mt-2 text-[12px] text-faint">
        Check the Health panel after saving — it reports the version and free disk space,
        and names the reason if the sign-in or the address is wrong.
      </p>
    </div>
  )
}

// syncSummary says what a sync actually did. A list belonging to an
// account that asks files requests rather than adding, and some of those
// are approved on the spot — so both counts can be non-zero, and
// reporting only one of them would misdescribe either case.
function syncSummary({ added, requested }: { added: number; requested: number }) {
  const titles = (n: number) => `${n} title${n === 1 ? "" : "s"}`
  const waiting = requested - added
  if (waiting > 0 && added > 0) return `${titles(added)} added, ${waiting} waiting on approval`
  if (waiting > 0) return `${titles(waiting)} requested`
  return `${titles(added)} added`
}

// ListsCard: watched lists that feed libraries — TMDB charts and lists
// (already covered by the TMDB key) and Trakt public lists and charts
// (needs a free client id from trakt.tv/oauth/applications).
const TMDB_MOVIE_CHARTS = [
  { id: "trending", label: "Trending" }, { id: "popular", label: "Popular" },
  { id: "top_rated", label: "Top rated" }, { id: "upcoming", label: "Upcoming" },
]
const TMDB_SHOW_CHARTS = [
  { id: "trending", label: "Trending" }, { id: "popular", label: "Popular" },
  { id: "top_rated", label: "Top rated" }, { id: "on_the_air", label: "On the air" },
]

// ListCadence is each person's own sync timer — one clock covering every
// list they run.
function ListCadence() {
  const q = useApi(() => api.listsCadence())
  const value = String(q.data?.minutes ?? 30)
  const set = async (v: string) => {
    try {
      await api.putListsCadence(Number(v))
      toast.success(`Your lists sync every ${Number(v) >= 60 ? `${Number(v) / 60} hour${Number(v) > 60 ? "s" : ""}` : `${v} minutes`}`)
      q.reload()
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }
  return (
    <div className="mb-3 flex items-center gap-2.5">
      <span className="mono-label text-faint">Sync my lists every</span>
      <Select value={value} onValueChange={v => void set(v)}>
        <SelectTrigger className="w-[150px]"><SelectValue /></SelectTrigger>
        <SelectContent>
          <SelectItem value="15">15 minutes</SelectItem>
          <SelectItem value="30">30 minutes</SelectItem>
          <SelectItem value="60">Hour</SelectItem>
          <SelectItem value="360">6 hours</SelectItem>
          <SelectItem value="720">12 hours</SelectItem>
        </SelectContent>
      </Select>
    </div>
  )
}

// Sharing labels an item, so it can only act once Plex HAS the item.
// reely knows when it imported a file — it did the importing — but not
// when the scan that follows has finished, and until then a new title
// sits waiting for the next scheduled pass. A Plex webhook reports
// exactly that moment.
//
// Plex Pass only. Said plainly rather than discovered: without a
// subscription the setting this asks for does not exist in Plex, and no
// amount of pasting the URL will help.
function PlexWebhookRow() {
  const hook = useApi(() => api.plexHook())
  const [busy, setBusy] = useState(false)
  const [copied, setCopied] = useState(false)

  const generate = async () => {
    setBusy(true)
    try {
      await api.newPlexHookToken()
      hook.reload()
      toast.success("Webhook URL ready — paste it into Plex")
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setBusy(false) }
  }
  const turnOff = async () => {
    setBusy(true)
    try {
      await api.clearPlexHookToken()
      hook.reload()
      toast.success("Webhook off — remove it in Plex too")
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setBusy(false) }
  }
  const copy = async (url: string) => {
    try {
      await navigator.clipboard.writeText(url)
      setCopied(true)
      setTimeout(() => setCopied(false), 1600)
    } catch { toast.error("Could not copy — select the address instead") }
  }

  const on = hook.data?.set ?? false
  const url = hook.data?.url ?? ""

  return (
    <div>
      <div className="mono-label mb-1.5 text-muted-foreground">
        Tell reely the moment Plex sees a new item
      </div>
      {!on ? (
        <Button variant="outline" disabled={busy} onClick={() => void generate()}>
          Generate webhook URL
        </Button>
      ) : (
        <div className="grid gap-2">
          {url ? (
            <div className="flex gap-2">
              <Input readOnly value={url} onFocus={e => e.currentTarget.select()} />
              <Button variant="outline" onClick={() => void copy(url)}>
                {copied ? "Copied" : "Copy"}
              </Button>
            </div>
          ) : (
            <p className="text-[12px] text-want">
              Set reely&rsquo;s own address in Settings &rarr; General first — the webhook URL is
              built from it.
            </p>
          )}
          <div className="flex gap-2">
            <Button variant="outline" disabled={busy} onClick={() => void generate()}>
              Rotate
            </Button>
            <Button variant="outline" disabled={busy} onClick={() => void turnOff()}>
              Turn off
            </Button>
          </div>
        </div>
      )}
      <p className="mt-1.5 text-[12px] text-muted-foreground">
        Paste it into Plex under Settings &rarr; Network &rarr; Webhooks, which needs Plex Pass.
        Sharing labels an item, so it can only act once Plex has scanned the file — this is what
        reports that. Without it the scheduled pass still catches up, just later. The address is
        the only thing guarding it, so treat it as a password and rotate if it leaks.
      </p>
    </div>
  )
}

function ListsCard() {
  // a requester's list files requests rather than adding, so the card has
  // to describe what will actually happen
  const isRequester = useIsRequester()
  const { external: isRequesterPortal, user } = useAccess()
  const isAdmin = useIsAdmin()
  const listsQuery = useApi(() => api.lists())
  const libsQuery = useApi(() => api.libraries())
  // The audience picker is the owner's — and "owner" here is the role,
  // not isAdmin, which the portal forces false by design. An admin
  // setting a list's audience while away from home is still the admin;
  // the portal is where they are, not who they are. Same correction the
  // back-catalogue group needed.
  //
  // The groups ride along on the lists payload, so this works out there
  // without the install-wide sharing endpoint being published.
  const owns = user === null || user.role === "admin"
  const shareGroups = listsQuery.data?.shareGroups ?? []
  const lists = listsQuery.data?.lists ?? []
  const libraries = libsQuery.data?.libraries ?? []
  const traktConfigured = listsQuery.data?.traktConfigured ?? false
  const mdblistConfigured = listsQuery.data?.mdblistConfigured ?? false

  const [source, setSource] = useState<"tmdb_chart" | "tmdb_list" | "trakt_chart" | "trakt_list" | "mdblist">("tmdb_chart")
  const [mdblistRef, setMdblistRef] = useState("")
  const [name, setName] = useState("")
  const [chart, setChart] = useState("trending")
  const [tmdbListId, setTmdbListId] = useState("")
  const [traktUser, setTraktUser] = useState("")
  const [traktSlug, setTraktSlug] = useState("")
  const [libraryId, setLibraryId] = useState<number>(0)
  const [limit, setLimit] = useState("20")
  const [busy, setBusy] = useState(false)

  const library = libraries.find(l => l.id === libraryId)
  const chartOptions = library?.kind === "shows" ? TMDB_SHOW_CHARTS : TMDB_MOVIE_CHARTS
  const traktChartOptions = [{ id: "trending", label: "Trending" }, { id: "popular", label: "Popular" }]

  const configFor = (): object | null => {
    switch (source) {
      case "tmdb_chart": case "trakt_chart": return { chart }
      case "tmdb_list": {
        // accept a raw id or a themoviedb.org/list/<id> URL
        const m = tmdbListId.match(/(\d+)/)
        return m ? { listId: Number(m[1]) } : null
      }
      case "trakt_list": {
        // accept user + slug fields, or a pasted trakt.tv/users/<u>/lists/<slug> URL
        const m = traktUser.match(/users\/([^/]+)\/lists\/([^/?#]+)/)
        if (m) return { user: m[1], slug: m[2] }
        return traktUser && traktSlug ? { user: traktUser.trim(), slug: traktSlug.trim() } : null
      }
      case "mdblist": {
        // accept a mdblist.com/lists/<user>/<slug> URL or a bare numeric id
        const m = mdblistRef.match(/lists\/([^/]+)\/([^/?#]+)/)
        if (m) return { user: m[1], slug: m[2] }
        const id = mdblistRef.match(/^(\d+)$/)
        return id ? { listId: Number(id[1]) } : null
      }
    }
  }

  const create = async () => {
    const config = configFor()
    if (!config || !libraryId) return
    setBusy(true)
    try {
      const created = await api.createList({
        name: name.trim(), source, config, libraryId, itemLimit: Math.max(0, Number(limit) || 0),
      })
      toast.success(`${created.name} added — syncing now`)
      setName(""); setTmdbListId(""); setTraktUser(""); setTraktSlug("")
      listsQuery.reload()
      toast.success(`${created.name}: ${syncSummary(await api.syncList(created.id))}`)
      listsQuery.reload({ quiet: true })
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }
  const syncNow = async (l: ApiWatchedList) => {
    try {
      toast.success(`${l.name}: ${syncSummary(await api.syncList(l.id))}`)
      listsQuery.reload({ quiet: true })
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }
  const toggle = async (l: ApiWatchedList) => {
    try {
      await api.setListEnabled(l.id, !l.enabled)
      listsQuery.reload({ quiet: true })
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }
  const remove = async (l: ApiWatchedList) => {
    if (!window.confirm(`Remove list "${l.name}"?\nTitles it already added stay in the library.`)) return
    try {
      await api.removeList(l.id)
      toast(`${l.name} removed`)
      listsQuery.reload()
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }

  const needsTrakt = source.startsWith("trakt")
  return (
    <>
      <div className="rounded-2xl border border-linesoft bg-surface p-5">
        <h2 className="mb-1 text-[15px] font-bold">Watched Lists</h2>
        <p className="mb-3 text-[12.5px] text-muted-foreground">
          {isRequester
            ? "A list feeds a library: new items become requests, and anything you're already approved for is fetched straight away. Syncs every half hour, or on demand."
            : "A list feeds a library: new items arrive monitored and get grabbed automatically. Syncs every half hour, or on demand."}
        </p>
        {/* the cadence is one install-wide clock rather than anybody's
            own, so it isn't served on the portal and isn't offered there */}
        {!isRequesterPortal && <ListCadence />}
        {lists.map(l => (
          <div key={l.id} className="border-b border-linesoft py-2 text-[13px]">
            <div className="flex items-center gap-2.5">
              <Switch checked={l.enabled} onCheckedChange={() => void toggle(l)} aria-label={`Enable ${l.name}`} />
              <span className="font-semibold">{l.name}</span>
              <span className="font-label text-[11px] text-faint">
                {l.source.replace("_", " ")} → {libraries.find(x => x.id === l.libraryId)?.name ?? "?"}
                {l.itemLimit > 0 && ` · top ${l.itemLimit}`}
                {l.lastSynced && ` · synced ${l.lastSynced.slice(0, 16).replace("T", " ")}`}
              </span>
              <button className="ml-auto mono-label text-faint hover:text-brass" onClick={() => void syncNow(l)}>
                Sync now
              </button>
              <button className="text-want opacity-70 hover:opacity-100" title="Remove list"
                onClick={() => void remove(l)}>
                <Trash2 className="h-3.5 w-3.5" />
              </button>
            </div>
            {owns && (
              <div className="mt-1.5 pl-[52px]">
                <ListAudience listId={l.id} groups={shareGroups} chosen={l.groupIds ?? []}
                  onChanged={() => listsQuery.reload({ quiet: true })} />
              </div>
            )}
          </div>
        ))}
        <div className="mt-3 grid gap-2">
          <div className="grid gap-2 sm:grid-cols-2">
            <Input value={name} onChange={e => setName(e.target.value)} placeholder="List name" />
            <Select value={source} onValueChange={v => setSource(v as typeof source)}>
              <SelectTrigger><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="tmdb_chart">TMDB chart</SelectItem>
                <SelectItem value="tmdb_list">TMDB list (URL or id)</SelectItem>
                <SelectItem value="trakt_chart">Trakt chart</SelectItem>
                <SelectItem value="trakt_list">Trakt list (URL)</SelectItem>
                <SelectItem value="mdblist">mdblist (URL or id)</SelectItem>
              </SelectContent>
            </Select>
            {source === "mdblist" && (
              <Input value={mdblistRef} onChange={e => setMdblistRef(e.target.value)}
                placeholder="mdblist.com/lists/user/list-name or list id" />
            )}
            {(source === "tmdb_chart" || source === "trakt_chart") && (
              <Select value={chart} onValueChange={setChart}>
                <SelectTrigger><SelectValue /></SelectTrigger>
                <SelectContent>
                  {(source === "tmdb_chart" ? chartOptions : traktChartOptions).map(c => (
                    <SelectItem key={c.id} value={c.id}>{c.label}</SelectItem>
                  ))}
                </SelectContent>
              </Select>
            )}
            {source === "tmdb_list" && (
              <Input value={tmdbListId} onChange={e => setTmdbListId(e.target.value)}
                placeholder="themoviedb.org/list/8234563 or 8234563" />
            )}
            {source === "trakt_list" && (
              <>
                <Input value={traktUser} onChange={e => setTraktUser(e.target.value)}
                  placeholder="trakt.tv list URL, or username" />
                {!traktUser.includes("/") && (
                  <Input value={traktSlug} onChange={e => setTraktSlug(e.target.value)}
                    placeholder="list slug (from the URL)" />
                )}
              </>
            )}
            {/* The library is not just a destination: its kind is what
                decides whether a chart means films or series, and what a
                mixed list keeps. So the picker shows the kind rather than
                leaving somebody to infer it from the library's name. */}
            <Select value={libraryId ? String(libraryId) : undefined}
              onValueChange={v => setLibraryId(Number(v))}>
              <SelectTrigger><SelectValue placeholder="Feeds which library…" /></SelectTrigger>
              <SelectContent>
                {libraries.map(l => (
                  <SelectItem key={l.id} value={String(l.id)}>
                    <span className="flex items-center gap-2">
                      {l.kind === "shows"
                        ? <Tv className="h-3.5 w-3.5 text-muted-foreground" />
                        : <Film className="h-3.5 w-3.5 text-muted-foreground" />}
                      {l.name}
                      <span className="mono-label text-faint">
                        {l.kind === "shows" ? "series" : "films"}
                      </span>
                    </span>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            {library && (source === "tmdb_chart" || source === "trakt_chart") && (
              <p className="text-[12px] text-muted-foreground">
                A chart has no kind of its own — feeding a {library.kind === "shows" ? "shows" : "movies"} library
                makes this the {library.kind === "shows" ? "series" : "film"} version of it.
              </p>
            )}
            <div>
              <div className="flex items-center gap-2">
                <Input type="number" min={0} className="w-24" value={limit}
                  onChange={e => setLimit(e.target.value)} />
                <span className="mono-label text-faint">item cap · 0 = all</span>
              </div>
            </div>
          </div>
          <Button className="justify-self-start" onClick={() => void create()}
            disabled={busy || !name.trim() || !libraryId || !configFor() || (needsTrakt && !traktConfigured)}>
            Add list
          </Button>
          {needsTrakt && !traktConfigured && (
            <p className="mono-label text-want">Trakt sources need a client id first (an admin sets it once).</p>
          )}
          {/* Trakt's client id is one shared install setting, so an admin
              does set that one once. An mdblist key is each person's own
              — MdblistKeyFor reads strictly the list creator's, with no
              falling back to anybody else's — and the card to enter it is
              directly below this line. Saying an admin sets it sent
              people to ask for something they already had. */}
          {source === "mdblist" && !mdblistConfigured && (
            <p className="mono-label text-want">mdblist needs your own api key first — add it below.</p>
          )}
        </div>
      </div>
      <MdblistCard onSaved={() => listsQuery.reload({ quiet: true })} />
      {isAdmin && <TraktCard onSaved={() => listsQuery.reload({ quiet: true })} />}
    </>
  )
}

// MdblistCard: strictly bring-your-own — each user's free mdblist api key
// (mdblist.com → Preferences → API Access) syncs the lists THEY create,
// covering their own lists and public ones. No shared or fallback key:
// everyone's rate limit and private lists stay their own.
function MdblistCard({ onSaved }: { onSaved: () => void }) {
  const status = useApi(() => api.mdblistKey())
  const [key, setKey] = useState("")
  const [busy, setBusy] = useState(false)

  const save = async () => {
    setBusy(true)
    try {
      await api.putMdblistKey(key.trim())
      toast.success(key.trim() ? "Your mdblist key is saved" : "Your mdblist key was cleared")
      setKey("")
      status.reload()
      onSaved()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="rounded-2xl border border-linesoft bg-surface p-5">
      <div className="mb-1 flex items-center gap-2">
        <h2 className="text-[15px] font-bold">mdblist</h2>
        {status.data?.set
          ? <span className="rounded-md bg-good/15 px-2 py-0.5 text-[11px] font-semibold text-good">Connected</span>
          : <span className="rounded-md bg-want/15 px-2 py-0.5 text-[11px] font-semibold text-want">No api key</span>}
      </div>
      <p className="mb-3 text-[12.5px] text-muted-foreground">
        mdblist.com mirrors IMDb charts (Top 250, MOVIEmeter) and Trakt lists as clean
        JSON. Bring your own free key (Preferences → API Access) — the lists you create
        sync with it, and your rate limit stays yours.
      </p>
      <div className="flex gap-2">
        <Input type="password" value={key} onChange={e => setKey(e.target.value)}
          placeholder={status.data?.set ? "Replace your api key…" : "Your api key"} autoComplete="off" />
        <Button onClick={() => void save()} disabled={busy || (!key.trim() && !status.data?.set)}>
          {key.trim() ? "Save" : status.data?.set ? "Clear" : "Save"}
        </Button>
      </div>
    </div>
  )
}

// TraktCard holds the Trakt client id, which unlocks public lists and
// charts. Heads-up: Trakt now gates CREATING new API apps behind VIP —
// without one, the TMDB sources cover the same ground for free.
function TraktCard({ onSaved }: { onSaved: () => void }) {
  const status = useApi(() => api.getSetting("trakt_client_id"))
  const [key, setKey] = useState("")
  const [busy, setBusy] = useState(false)

  const save = async () => {
    setBusy(true)
    try {
      await api.putSetting("trakt_client_id", key.trim())
      toast.success(key.trim() ? "Trakt client id saved" : "Trakt client id cleared")
      setKey("")
      status.reload()
      onSaved()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="rounded-2xl border border-linesoft bg-surface p-5">
      <div className="mb-1 flex items-center gap-2">
        <h2 className="text-[15px] font-bold">Trakt</h2>
        {status.data?.set
          ? <span className="rounded-md bg-good/15 px-2 py-0.5 text-[11px] font-semibold text-good">Connected</span>
          : <span className="rounded-md bg-want/15 px-2 py-0.5 text-[11px] font-semibold text-want">No client id</span>}
      </div>
      <p className="mb-3 text-[12.5px] text-muted-foreground">
        Public Trakt lists and charts need a client id from a Trakt API app. Note:
        Trakt only lets VIP members create new API apps — the TMDB sources above do
        the same job with no extra account.
      </p>
      <div className="flex gap-2">
        <Input type="password" value={key} onChange={e => setKey(e.target.value)}
          placeholder={status.data?.set ? "Replace client id…" : "Client id"} autoComplete="off" />
        <Button onClick={() => void save()} disabled={busy || (!key.trim() && !status.data?.set)}>
          {key.trim() ? "Save" : status.data?.set ? "Clear" : "Save"}
        </Button>
      </div>
    </div>
  )
}

// The Search tab holds the two automatic hunters: the passive RSS sync
// (a feed watcher) and the active wanted sweep (scheduled re-searches).
// They live in separate cards because they are different animals — one
// reads what indexers post, the other asks them questions.
function RssCard() {
  const rssQuery = useApi(() => api.getSetting("rss_poll_seconds"))
  const [rssMin, setRssMin] = useState<string | null>(null) // null = untouched

  const storedMin = rssQuery.data?.value ? String(Math.round(Number(rssQuery.data.value) / 60)) : ""
  const shownMin = rssMin ?? storedMin

  const saveRss = async () => {
    const mins = Math.max(5, Number(rssMin) || 15)
    try {
      await api.putSetting("rss_poll_seconds", String(mins * 60))
      toast.success(`RSS sync every ${mins} minutes`)
      setRssMin(null)
      rssQuery.reload()
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }

  return (
    <div className="rounded-2xl border border-linesoft bg-surface p-5">
      <h2 className="mb-1 text-[15px] font-bold">RSS Sync</h2>
      <p className="mb-3 text-[12.5px] text-muted-foreground">
        Watches the indexers' latest releases and grabs wanted titles and quality
        upgrades on sight — cheap and passive, and it never loses its place in the
        feed between polls.
      </p>
      <div className="grid gap-2 sm:grid-cols-2">
        <div>
          <div className="mono-label mb-1.5 text-faint">RSS sync every (minutes)</div>
          <div className="flex gap-2">
            <Input type="number" min={5} value={shownMin} onChange={e => setRssMin(e.target.value)}
              placeholder="15" />
            <Button variant="outline" onClick={() => void saveRss()}
              disabled={rssMin === null || rssMin === storedMin}>Set</Button>
          </div>
        </div>
      </div>
    </div>
  )
}

// WantedSweepCard is the opt-in backlog hunter: scheduled re-searches of
// everything monitored that's missing or below its cutoff.
function WantedSweepCard() {
  const wantedQuery = useApi(() => api.getSetting("wanted_search_hours"))
  const wanted = wantedQuery.data?.value ?? "0"

  const setWanted = async (v: string) => {
    try {
      await api.putSetting("wanted_search_hours", v)
      toast.success(v === "0" ? "Wanted sweep off" : `Wanted sweep every ${v} hours`)
      wantedQuery.reload()
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }

  return (
    <div className="rounded-2xl border border-linesoft bg-surface p-5">
      <h2 className="mb-1 text-[15px] font-bold">Wanted Sweep</h2>
      <p className="mb-3 text-[12.5px] text-muted-foreground">
        Actively re-searches everything monitored that's missing or below its
        profile's upgrade cutoff, on a schedule. This is real indexer traffic —
        every pass runs searches — so it's off unless you turn it on. New releases
        don't need it: RSS and the release-day pass cover those.
      </p>
      <div className="grid gap-2 sm:grid-cols-2">
        <div>
          <div className="mono-label mb-1.5 text-faint">Re-search every</div>
          <Select value={wanted} onValueChange={v => void setWanted(v)}>
            <SelectTrigger><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="0">Off</SelectItem>
              <SelectItem value="6">Every 6 hours</SelectItem>
              <SelectItem value="12">Every 12 hours</SelectItem>
              <SelectItem value="24">Once a day</SelectItem>
              <SelectItem value="48">Every 2 days</SelectItem>
            </SelectContent>
          </Select>
        </div>
      </div>
    </div>
  )
}

// SabCategories names the SAB categories reely files downloads under.
// Whatever you use in SAB works — the defaults match a plain setup.
function SabCategories() {
  return (
    <div className="mt-3 grid gap-2 sm:grid-cols-2">
      <CategoryField label="Movies category" settingKey="sab_category_movies" fallback="movies" />
      <CategoryField label="TV category" settingKey="sab_category_tv" fallback="tvshows" />
    </div>
  )
}

function CategoryField({ label, settingKey, fallback }: {
  label: string; settingKey: string; fallback: string
}) {
  const query = useApi(() => api.getSetting(settingKey))
  const [value, setValue] = useState<string | null>(null) // null = untouched
  const stored = query.data?.value ?? ""
  const shown = value ?? stored

  const save = async () => {
    const v = (value ?? "").trim()
    try {
      await api.putSetting(settingKey, v)
      toast.success(`${label} ${v ? `set to ${v}` : `reset to ${fallback}`}`)
      setValue(null)
      query.reload()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    }
  }

  return (
    <div>
      <div className="mono-label mb-1.5 text-faint">{label}</div>
      <div className="flex gap-2">
        <Input value={shown} onChange={e => setValue(e.target.value)} placeholder={fallback} />
        <Button variant="outline" onClick={() => void save()}
          disabled={value === null || value === stored}>Set</Button>
      </div>
    </div>
  )
}

const QUALITIES = ["2160p", "1080p", "720p", "480p"] // display order, best first
// "Unknown" allows releases whose name carries no resolution at all — an
// allowance, not a rung, so it never appears in the cutoff picker
const QUALITY_UNKNOWN = "unknown"
const SOURCES = ["remux", "bluray", "webdl", "webrip", "hdtv", "dvd"] // display order, best first
// WEBRip sits below WEB-DL: a WEB-DL is the service's own file, a WEBRip a
// re-encode of a stream. The labels spell them as release names do.
const SOURCE_LABELS: Record<string, string> = {
  remux: "Remux", bluray: "Blu-ray", webdl: "WEB-DL", webrip: "WEBRip", hdtv: "HDTV", dvd: "DVD",
}

const emptyProfile: Omit<ApiQualityProfile, "id"> = {
  name: "", qualities: ["1080p", "720p"], cutoff: "1080p",
  sources: [], sourceCutoff: "",
  required: [], blocked: [], preferred: [],
  minFormatScore: null,
  upgrades: true, hdr: "allow", minMbPerMin: 8, maxMbPerMin: 80,
}

// Quality profiles decide what the grab loop fetches: allowed resolutions,
// the upgrade cutoff, HDR policy, and the size band. The band is MB per
// minute of runtime so one number works for episodes and three-hour movies.
function ProfilesCard({ profiles, onChanged }: {
  profiles: ApiQualityProfile[]; onChanged: () => void
}) {
  const [editing, setEditing] = useState<ApiQualityProfile | null>(null) // id 0 = new
  const [busy, setBusy] = useState(false)

  const save = async () => {
    if (!editing) return
    setBusy(true)
    try {
      const { id, ...body } = editing
      body.required = (body.required ?? []).filter(t => t.trim())
      body.blocked = (body.blocked ?? []).filter(t => t.trim())
      body.preferred = (body.preferred ?? []).filter(pt => pt.term.trim())
      if (id) await api.updateProfile(id, body)
      else await api.createProfile(body)
      toast.success(`${editing.name} saved`)
      setEditing(null)
      onChanged()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }
  const remove = async (p: ApiQualityProfile) => {
    if (!window.confirm(`Delete profile "${p.name}"?\nLibraries using it fall back to the default.`)) return
    try {
      await api.removeProfile(p.id)
      toast(`${p.name} deleted`)
      onChanged()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    }
  }

  return (
    <div className="rounded-2xl border border-linesoft bg-surface p-5">
      <h2 className="mb-1 text-[15px] font-bold">Quality Profiles</h2>
      <p className="mb-3 text-[12.5px] text-muted-foreground">
        What the grab loop is allowed to fetch. Size limits scale with runtime, so a
        profile rejects the 20 GB remux and the 700 MB fake alike.
      </p>
      {profiles.map(p => (
        <div key={p.id} className="flex items-center gap-2.5 border-b border-linesoft py-2 text-[13px]">
          <span className="font-semibold">{p.name}</span>
          <span className="font-label text-[11px] text-faint">{profileSummary(p)}</span>
          <button className="ml-auto text-faint hover:text-brass" title="Edit profile"
            onClick={() => setEditing({ ...p, qualities: [...p.qualities] })}>
            <Pencil className="h-3.5 w-3.5" />
          </button>
          <button className="text-want opacity-70 hover:opacity-100" title="Delete profile"
            onClick={() => void remove(p)}>
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      ))}
      {editing === null ? (
        <Button variant="outline" className="mt-3 h-8"
          onClick={() => setEditing({ id: 0, ...emptyProfile, qualities: [...emptyProfile.qualities] })}>
          Add profile
        </Button>
      ) : (
        <ProfileForm profile={editing} onChange={setEditing} onSave={() => void save()}
          onCancel={() => setEditing(null)} busy={busy} />
      )}
    </div>
  )
}

function qualityOrder(q: string): number {
  return q === QUALITY_UNKNOWN ? QUALITIES.length : QUALITIES.indexOf(q)
}

function profileSummary(p: ApiQualityProfile): string {
  const parts = [
    // best first, with Unknown last — it is not in QUALITIES, and a bare
    // indexOf would sort it ahead of 2160p
    [...p.qualities]
      .sort((a, b) => qualityOrder(a) - qualityOrder(b))
      .map(q => (q === QUALITY_UNKNOWN ? "Unknown" : q))
      .join(" "),
    p.sourceCutoff ? `cutoff ${SOURCE_LABELS[p.sourceCutoff] ?? p.sourceCutoff} ${p.cutoff}` : `cutoff ${p.cutoff}`,
  ]
  if (p.sources?.length) {
    parts.splice(1, 0, [...p.sources].sort((a, b) => SOURCES.indexOf(a) - SOURCES.indexOf(b))
      .map(s => SOURCE_LABELS[s] ?? s).join(" "))
  }
  if (p.minMbPerMin || p.maxMbPerMin) {
    parts.push(`${p.minMbPerMin || "0"}–${p.maxMbPerMin || "∞"} MB/min`)
  }
  if (p.hdr !== "allow") parts.push(`HDR ${p.hdr}`)
  if (p.required?.length) parts.push(`must have ${p.required.join(", ")}`)
  if (p.blocked?.length) parts.push(`never ${p.blocked.join(", ")}`)
  if (p.preferred?.length) parts.push(`${p.preferred.length} preferred term${p.preferred.length === 1 ? "" : "s"}`)
  if (!p.upgrades) parts.push("no upgrades")
  return parts.join(" · ")
}

function ProfileForm({ profile, onChange, onSave, onCancel, busy }: {
  profile: ApiQualityProfile
  onChange: (p: ApiQualityProfile) => void
  onSave: () => void
  onCancel: () => void
  busy: boolean
}) {
  const set = (patch: Partial<ApiQualityProfile>) => onChange({ ...profile, ...patch })
  const toggleQuality = (q: string) => {
    const has = profile.qualities.includes(q)
    const qualities = has ? profile.qualities.filter(x => x !== q) : [...profile.qualities, q]
    // the cutoff must stay inside the allowed set, and it must be a real
    // resolution — Unknown says nothing about what is on disk
    const resolutions = qualities.filter(x => x !== QUALITY_UNKNOWN)
    const cutoff = resolutions.includes(profile.cutoff) ? profile.cutoff : (resolutions[0] ?? "")
    set({ qualities, cutoff })
  }
  const toggleSource = (s: string) => {
    const has = (profile.sources ?? []).includes(s)
    const sources = has ? profile.sources.filter(x => x !== s) : [...(profile.sources ?? []), s]
    // a source cutoff outside the (non-empty) allowed set makes no sense
    const sourceCutoff = sources.length > 0 && profile.sourceCutoff && !sources.includes(profile.sourceCutoff)
      ? "" : profile.sourceCutoff
    set({ sources, sourceCutoff })
  }
  const cutoffSources = (profile.sources?.length ? profile.sources : SOURCES)
  // preview the band in concrete terms — MB/min is the right unit to store
  // but nobody thinks in it
  const gb = (mbPerMin: number, minutes: number) => (mbPerMin * minutes) / 1024
  const band = (minutes: number, label: string) => {
    if (!profile.minMbPerMin && !profile.maxMbPerMin) return null
    const lo = profile.minMbPerMin ? gb(profile.minMbPerMin, minutes).toFixed(1) : "0"
    const hi = profile.maxMbPerMin ? gb(profile.maxMbPerMin, minutes).toFixed(1) : "∞"
    return `${label}: ${lo}–${hi} GB`
  }

  return (
    <div className="mt-3 grid gap-3 rounded-xl border border-linesoft bg-surface2 p-4">
      <Input value={profile.name} onChange={e => set({ name: e.target.value })} placeholder="Profile name" />
      <div>
        <div className="mono-label mb-1.5 text-faint">Allowed qualities</div>
        <div className="flex flex-wrap gap-1.5">
          {[...QUALITIES, QUALITY_UNKNOWN].map(q => (
            <button key={q} onClick={() => toggleQuality(q)}
              title={q === QUALITY_UNKNOWN
                ? "Take releases whose name carries no resolution — the size band is the only guard left"
                : undefined}
              className={cn(
                "rounded-lg border px-2.5 py-1 text-[12px] font-semibold",
                profile.qualities.includes(q)
                  ? "border-brass/50 bg-brass/15 text-brass"
                  : "border-linesoft text-muted-foreground hover:text-foreground"
              )}>
              {q === QUALITY_UNKNOWN ? "Unknown" : q}
            </button>
          ))}
        </div>
      </div>
      <div>
        <div className="mono-label mb-1.5 text-faint">Allowed sources (none picked = any)</div>
        <div className="flex flex-wrap gap-1.5">
          {SOURCES.map(s => (
            <button key={s} onClick={() => toggleSource(s)}
              className={cn(
                "rounded-lg border px-2.5 py-1 text-[12px] font-semibold",
                (profile.sources ?? []).includes(s)
                  ? "border-brass/50 bg-brass/15 text-brass"
                  : "border-linesoft text-muted-foreground hover:text-foreground"
              )}>
              {SOURCE_LABELS[s]}
            </button>
          ))}
        </div>
      </div>
      <div className="grid gap-2 sm:grid-cols-2">
        <div>
          <div className="mono-label mb-1.5 text-faint">Upgrade until</div>
          <Select value={profile.cutoff || undefined} onValueChange={v => set({ cutoff: v })}>
            <SelectTrigger><SelectValue placeholder="Cutoff" /></SelectTrigger>
            <SelectContent>
              {profile.qualities.filter(q => q !== QUALITY_UNKNOWN)
                .map(q => <SelectItem key={q} value={q}>{q}</SelectItem>)}
            </SelectContent>
          </Select>
        </div>
        <div>
          <div className="mono-label mb-1.5 text-faint">…and source at that resolution</div>
          <Select value={profile.sourceCutoff || "any"}
            onValueChange={v => set({ sourceCutoff: v === "any" ? "" : v })}>
            <SelectTrigger><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="any">Any source</SelectItem>
              {cutoffSources.map(s => <SelectItem key={s} value={s}>{SOURCE_LABELS[s] ?? s}</SelectItem>)}
            </SelectContent>
          </Select>
        </div>
        <div>
          <div className="mono-label mb-1.5 text-faint">HDR</div>
          <Select value={profile.hdr} onValueChange={v => set({ hdr: v as ApiQualityProfile["hdr"] })}>
            <SelectTrigger><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="allow">Allow</SelectItem>
              <SelectItem value="require">Require</SelectItem>
              <SelectItem value="block">Block</SelectItem>
            </SelectContent>
          </Select>
        </div>
      </div>
      <div>
        <div className="mono-label mb-1.5 text-faint">Size band (MB per minute of runtime, 0 = no limit)</div>
        <div className="flex items-center gap-2">
          <Input type="number" min={0} className="w-24" value={profile.minMbPerMin}
            onChange={e => set({ minMbPerMin: Math.max(0, Number(e.target.value) || 0) })} />
          <span className="text-faint">–</span>
          <Input type="number" min={0} className="w-24" value={profile.maxMbPerMin}
            onChange={e => set({ maxMbPerMin: Math.max(0, Number(e.target.value) || 0) })} />
        </div>
        <div className="mono-label mt-1.5 text-faint">
          {[band(120, "2h movie"), band(45, "45-min episode")].filter(Boolean).join("  ·  ") || "any size accepted"}
        </div>
      </div>
      <div className="grid gap-2 sm:grid-cols-2">
        <TermChips label="Must contain (all of these)" terms={profile.required ?? []}
          onChange={required => set({ required })} placeholder="e.g. x265" />
        <TermChips label="Must not contain (any rejects)" terms={profile.blocked ?? []}
          onChange={blocked => set({ blocked })} placeholder="e.g. CAM" />
      </div>
      <div>
        <div className="mono-label mb-1.5 text-faint">
          Preferred terms — matches add their score to the ranking (1000 = a resolution step, 100 = a source step)
        </div>
        {(profile.preferred ?? []).map((pt, i) => (
          <div key={i} className="mb-1.5 flex items-center gap-2">
            <Input value={pt.term} placeholder="term, e.g. HDR10+"
              onChange={e => {
                const preferred = [...profile.preferred]
                preferred[i] = { ...pt, term: e.target.value }
                set({ preferred })
              }} className="max-w-[220px]" />
            <Input type="number" value={pt.score} className="w-24"
              onChange={e => {
                const preferred = [...profile.preferred]
                preferred[i] = { ...pt, score: Number(e.target.value) || 0 }
                set({ preferred })
              }} />
            <button className="text-faint hover:text-want" title="Remove term"
              onClick={() => set({ preferred: profile.preferred.filter((_, j) => j !== i) })}>
              <X className="h-3.5 w-3.5" />
            </button>
          </div>
        ))}
        <Button variant="outline" className="h-7 text-[12px]"
          onClick={() => set({ preferred: [...(profile.preferred ?? []), { term: "", score: 50 }] })}>
          Add preferred term
        </Button>
      </div>
      <div>
        <div className="mono-label mb-1.5 text-faint">Minimum format score</div>
        {/* blank = no floor. Zero is a real floor — "nothing that scores
            negative" is the most common floor there is, and using zero as
            the off switch made it unsayable. */}
        <Input type="number" className="w-28" placeholder="none"
          value={profile.minFormatScore ?? ""}
          onChange={e => set({ minFormatScore: e.target.value === "" ? null : Number(e.target.value) })} />
        <div className="mono-label mt-1.5 text-faint">
          releases whose custom-format + preferred-term score falls below this are rejected · blank = no floor
        </div>
      </div>
      <div className="flex items-center gap-2.5">
        <Switch checked={profile.upgrades} onCheckedChange={v => set({ upgrades: v })} />
        <span className="text-[12.5px] text-muted-foreground">Upgrade files already on disk until the cutoff is met</span>
      </div>
      <div className="flex gap-2">
        <Button onClick={onSave} disabled={busy || !profile.name.trim() || profile.qualities.length === 0}>
          {profile.id ? "Save" : "Create"}
        </Button>
        <Button variant="outline" onClick={onCancel}>Cancel</Button>
      </div>
    </div>
  )
}

function LibrariesCard({ profiles }: { profiles: ApiQualityProfile[] }) {
  const libsQuery = useApi(() => api.libraries())
  const libraries = libsQuery.data?.libraries ?? []
  const [name, setName] = useState("")
  const [path, setPath] = useState("")
  const [kind, setKind] = useState<"movies" | "shows">("movies")
  const [newProfile, setNewProfile] = useState<number>(0) // 0 = default profile
  const [busy, setBusy] = useState(false)

  const create = async () => {
    setBusy(true)
    try {
      const lib = await api.createLibrary(name.trim(), path.trim(), kind)
      if (newProfile > 0) await api.setLibraryProfile(lib.id, newProfile)
      toast.success(`${name.trim()} created`)
      setName(""); setPath(""); setNewProfile(0)
      libsQuery.reload()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }
  const remove = async (id: number, libName: string) => {
    if (!window.confirm(`Remove library "${libName}"?\nFiles on disk are never touched.`)) return
    try {
      await api.removeLibrary(id)
      toast(`${libName} removed`)
      libsQuery.reload()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    }
  }

  const setProfile = async (libraryId: number, profileId: number) => {
    try {
      await api.setLibraryProfile(libraryId, profileId)
      libsQuery.reload({ quiet: true })
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    }
  }

  return (
    <div className="rounded-2xl border border-linesoft bg-surface p-5">
      <h2 className="mb-1 text-[15px] font-bold">Libraries</h2>
      <p className="mb-3 text-[12.5px] text-muted-foreground">
        A library is a folder with a kind — movie libraries hold movies, show libraries hold
        shows. Its quality profile decides what gets grabbed for it.
      </p>
      {libraries.map(l => (
        <div key={l.id} className="flex items-center gap-2.5 border-b border-linesoft py-2 text-[13px]">
          {l.kind === "movies" ? <Film className="h-3.5 w-3.5 text-faint" /> : <Tv className="h-3.5 w-3.5 text-faint" />}
          <span className="font-semibold">{l.name}</span>
          <span className="font-label min-w-0 truncate text-[11px] text-faint">{l.path}</span>
          {profiles.length > 0 && (
            <Select value={l.qualityProfileId ? String(l.qualityProfileId) : undefined}
              onValueChange={v => void setProfile(l.id, Number(v))}>
              <SelectTrigger className="ml-auto h-7 w-[150px] text-[12px]">
                <SelectValue placeholder="Profile…" />
              </SelectTrigger>
              <SelectContent>
                {profiles.map(p => <SelectItem key={p.id} value={String(p.id)}>{p.name}</SelectItem>)}
              </SelectContent>
            </Select>
          )}
          <button className={cn("text-want opacity-70 hover:opacity-100", profiles.length === 0 && "ml-auto")}
            title="Remove library" onClick={() => void remove(l.id, l.name)}>
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      ))}
      <div className="mt-3 grid gap-2 sm:grid-cols-2">
        <Input value={name} onChange={e => setName(e.target.value)} placeholder="Name" />
        <PathInput value={path} onChange={setPath} placeholder="/data/movies" />
        <Select value={kind} onValueChange={v => setKind(v as "movies" | "shows")}>
          <SelectTrigger><SelectValue /></SelectTrigger>
          <SelectContent>
            <SelectItem value="movies">Movies</SelectItem>
            <SelectItem value="shows">Shows</SelectItem>
          </SelectContent>
        </Select>
        {profiles.length > 0 && (
          <Select value={newProfile ? String(newProfile) : "0"} onValueChange={v => setNewProfile(Number(v))}>
            <SelectTrigger><SelectValue placeholder="Profile" /></SelectTrigger>
            <SelectContent>
              <SelectItem value="0">Default profile</SelectItem>
              {profiles.map(p => <SelectItem key={p.id} value={String(p.id)}>{p.name}</SelectItem>)}
            </SelectContent>
          </Select>
        )}
        <Button className="justify-self-start" onClick={() => void create()}
          disabled={busy || !name.trim() || !path.trim()}>Add</Button>
      </div>
    </div>
  )
}

// TermChips is a small tag editor: type a term, Enter (or comma) adds it,
// the × on a chip removes it.
function TermChips({ label, terms, onChange, placeholder }: {
  label: string; terms: string[]; onChange: (terms: string[]) => void; placeholder?: string
}) {
  const [draft, setDraft] = useState("")
  const add = () => {
    const t = draft.trim().replace(/,+$/, "")
    if (t && !terms.includes(t)) onChange([...terms, t])
    setDraft("")
  }
  return (
    <div>
      <div className="mono-label mb-1.5 text-faint">{label}</div>
      <div className="flex flex-wrap items-center gap-1.5">
        {terms.map(t => (
          <span key={t} className="flex items-center gap-1 rounded-lg border border-linesoft bg-surface px-2 py-0.5 text-[12px] font-semibold">
            {t}
            <button className="text-faint hover:text-want" title={`Remove ${t}`}
              onClick={() => onChange(terms.filter(x => x !== t))}>
              <X className="h-3 w-3" />
            </button>
          </span>
        ))}
        <Input value={draft} placeholder={placeholder ?? "add a term"}
          onChange={e => setDraft(e.target.value)}
          onKeyDown={e => {
            if (e.key === "Enter" || e.key === ",") { e.preventDefault(); add() }
          }}
          onBlur={add}
          className="h-7 max-w-[160px] text-[12px]" />
      </div>
    </div>
  )
}

// FormatsCard is the install's custom-format library: TRaSH-style scoring
// rules applied by every profile's release ranking. Movies and shows keep
// separate columns — the guides publish separate collections per kind, and
// importing from one files the format into its column automatically. Movie
// formats judge only movie searches, show formats only show searches.
function FormatsCard() {
  const { data, reload } = useApi(() => api.formats())
  const formats = data?.formats ?? []

  return (
    <div>
      <p className="mb-4 max-w-[640px] text-[12.5px] text-muted-foreground">
        Scoring rules judged against every release — matches add their score to the
        ranking, and search results show what each release earned. Movie formats
        apply to movie searches, show formats to show searches; a profile's minimum
        format score turns the sum into a hard floor.
      </p>
      <div className="grid items-start gap-6 md:grid-cols-2">
        <FormatColumn scope="movies" formats={formats.filter(f => f.appliesTo === "movies")}
          onChanged={() => reload({ quiet: true })} />
        <FormatColumn scope="shows" formats={formats.filter(f => f.appliesTo === "shows")}
          onChanged={() => reload({ quiet: true })} />
      </div>
    </div>
  )
}

// FormatColumn is one scope's list — its rows, score inputs, and its own
// guides import wired to the matching collection.
function FormatColumn({ scope, formats, onChanged }: {
  scope: "movies" | "shows"
  formats: ApiCustomFormat[]
  onChanged: () => void
}) {
  const [importOpen, setImportOpen] = useState(false)
  const Icon = scope === "movies" ? Film : Tv

  const setScore = async (id: number, score: number) => {
    try {
      await api.setFormatScore(id, score)
      onChanged()
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }
  const remove = async (f: ApiCustomFormat) => {
    if (!window.confirm(`Delete format "${f.name}"?`)) return
    try {
      await api.deleteFormat(f.id)
      toast(`${f.name} deleted`)
      onChanged()
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }

  return (
    <div className="rounded-2xl border border-linesoft bg-surface p-5">
      <h2 className="mb-3 flex items-center gap-2 text-[15px] font-bold capitalize">
        <Icon className="h-4 w-4 text-brass" /> {scope}
        <span className="mono-label ml-auto font-normal text-faint">
          {formats.length > 0 ? `${formats.length} format${formats.length === 1 ? "" : "s"}` : ""}
        </span>
      </h2>
      {formats.map(f => (
        <div key={f.id} className="flex items-center gap-2.5 border-b border-linesoft py-2 text-[13px]">
          <div className="min-w-0 flex-1">
            <div className="truncate font-semibold">{f.name}</div>
            <div className="mono-label mt-0.5 text-faint">
              {f.specs.length} rule{f.specs.length === 1 ? "" : "s"}
              {f.trashId ? " · TRaSH" : ""}
            </div>
          </div>
          <Input type="number" className="w-24 shrink-0" defaultValue={f.score}
            key={`s${f.id}-${f.score}`}
            onBlur={e => { const v = Number(e.target.value) || 0; if (v !== f.score) void setScore(f.id, v) }} />
          <button className="shrink-0 text-want opacity-70 hover:opacity-100" title="Delete format"
            onClick={() => void remove(f)}>
            <Trash2 className="h-3.5 w-3.5" />
          </button>
        </div>
      ))}
      {formats.length === 0 && (
        <p className="mono-label py-3 text-faint">no {scope === "movies" ? "movie" : "show"} formats yet — import some below</p>
      )}
      <Button variant="outline" className="mt-3 h-8" onClick={() => setImportOpen(true)}>
        Import from TRaSH Guides
      </Button>
      <TrashImportDialog open={importOpen} onOpenChange={setImportOpen} kind={scope}
        existing={new Set(formats.map(f => f.name))} onImported={onChanged} />
    </div>
  )
}

// TrashImportDialog browses one guides collection — the movie or show one,
// fixed by the column it was opened from — and imports checked formats
// with their recommended scores, already scoped to that column.
function TrashImportDialog({ open, onOpenChange, kind, existing, onImported }: {
  open: boolean
  onOpenChange: (v: boolean) => void
  kind: "movies" | "shows"
  existing: Set<string>
  onImported: () => void
}) {
  const [rows, setRows] = useState<(Omit<ApiCustomFormat, "id"> & { trashId: string })[]>([])
  const [loading, setLoading] = useState(false)
  const [error, setError] = useState("")
  const [q, setQ] = useState("")
  const [picked, setPicked] = useState<Set<string>>(new Set())
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    if (!open) return
    setLoading(true)
    setError("")
    api.trashFormats(kind)
      .then(r => setRows(r.formats ?? []))
      .catch(e => setError(`${e instanceof Error ? e.message : e}`))
      .finally(() => setLoading(false))
  }, [open, kind])

  const filtered = rows.filter(r => r.name.toLowerCase().includes(q.trim().toLowerCase()))
  const toggle = (name: string) => setPicked(prev => {
    const next = new Set(prev)
    if (next.has(name)) next.delete(name)
    else next.add(name)
    return next
  })
  const doImport = async () => {
    const chosen = rows.filter(r => picked.has(r.name))
    if (chosen.length === 0) return
    setBusy(true)
    try {
      await api.importFormats(chosen)
      toast.success(`Imported ${chosen.length} format${chosen.length === 1 ? "" : "s"}`)
      setPicked(new Set())
      onImported()
      onOpenChange(false)
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setBusy(false) }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="top-[8vh] max-w-xl translate-y-0 gap-0 p-0">
        <DialogHeader className="border-b px-5 py-4">
          <DialogTitle className="font-display text-lg font-bold">
            Import {kind === "movies" ? "movie" : "show"} formats — TRaSH Guides
          </DialogTitle>
          <div className="mt-2 flex items-center gap-2">
            <Input value={q} onChange={e => setQ(e.target.value)} placeholder="Filter formats…"
              className="h-8 max-w-[220px] text-[13px]" />
          </div>
        </DialogHeader>
        <div className="h-[min(380px,55dvh)] overflow-y-auto px-3 py-2">
          {loading && <p className="mono-label px-2 py-4 text-faint">fetching the guides…</p>}
          {error && <p className="mono-label px-2 py-4 text-want">{error}</p>}
          {!loading && !error && filtered.map(r => (
            <label key={r.name} className="flex cursor-pointer items-center gap-2.5 rounded-sm px-2 py-1.5 hover:bg-surface2">
              <input type="checkbox" className="accent-brass"
                checked={picked.has(r.name)} onChange={() => toggle(r.name)} />
              <span className="min-w-0 flex-1 truncate text-[13px] font-semibold">{r.name}</span>
              {existing.has(r.name) && <span className="mono-label shrink-0 text-good">imported</span>}
              <span className="mono-label shrink-0 text-faint">{r.score > 0 ? `+${r.score}` : r.score}</span>
            </label>
          ))}
          {!loading && !error && filtered.length === 0 && (
            <p className="mono-label px-2 py-4 text-faint">nothing matches</p>
          )}
        </div>
        <DialogFooter className="border-t px-5 py-3">
          <Button variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button disabled={busy || picked.size === 0} onClick={() => void doImport()}>
            Import {picked.size > 0 ? picked.size : ""}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
