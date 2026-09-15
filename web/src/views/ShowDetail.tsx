import { useEffect, useState } from "react"
import { api } from "@/api"
import type { ApiEpisode } from "@/api"
import { useApi } from "@/hooks/use-api"
import { AddToLibraryAction, CastRow, DetailHero, IconAction, ImdbLink, ImportPathAction, ProfilePicker, Tag, TmdbLink, TvdbLink, UploadAction, fmtQuality, fmtSize } from "@/components/detail"
import { SharedWith } from "@/components/SharedWith"
import { RematchDialog } from "@/components/RematchDialog"
import { ReleaseDialog } from "@/components/Releases"
import { SimilarRow } from "@/components/rows"
import { DeleteTitleDialog, type DeleteMode } from "@/components/SelectionBar"
import { Button } from "@/components/ui/button"
import { Switch } from "@/components/ui/switch"
import { ChevronDown, Pencil, RefreshCw, Search, Trash2, Zap } from "lucide-react"
import { toast } from "sonner"
import { toastSearchResult } from "@/lib/search-toast"
import { cn } from "@/lib/utils"
import { Overview } from "@/components/Overview"

// which release search is open: a whole season pack or one episode
type SearchTarget = { season: number; episode?: number; label: string } | null

export function ShowDetailView({ showId, onBack, onOpenPerson, onOpenEpisode, onOpenPreview }: {
  showId: number; onBack: () => void; onOpenPerson?: (tmdbId: number) => void
  onOpenEpisode?: (id: number) => void
  onOpenPreview?: (kind: "movie" | "show", tmdbId: number) => void
}) {
  const { data, error, loading, reload } = useApi(() => api.show(showId), 15_000)
  const profilesQuery = useApi(() => api.profiles())
  const [search, setSearch] = useState<SearchTarget>(null)
  // seasons collapse; null = the default (only the newest season open)
  const [openSeasons, setOpenSeasons] = useState<Record<number, boolean> | null>(null)

  const toggleShow = async (v: boolean) => {
    try {
      await api.setShowMonitored(showId, v)
      reload({ quiet: true })
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }
  const setProfile = async (profileId: number) => {
    try {
      await api.setShowProfile(showId, profileId)
      reload({ quiet: true })
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }
  // auto search: one episode runs inline and names what it grabbed; a whole
  // show or season queues background searches for the watcher to work
  const refreshMeta = async () => {
    try {
      await api.refreshTitle("show", showId)
      // name the source that actually served the refresh
      toast.success(`Metadata refreshed from ${data?.show.source === "tvdb" ? "TheTVDB" : "TMDB"}`)
      reload({ quiet: true })
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }
  const searchNow = async (season = 0, episode = 0) => {
    try {
      const r = await api.searchShowNow(showId, season, episode)
      toastSearchResult(r, "Nothing to search — everything wanted is on disk or unaired")
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }
  const [delOpen, setDelOpen] = useState(false)
  const [rematchOpen, setRematchOpen] = useState(false)
  const remove = async (title: string, mode: DeleteMode) => {
    try {
      // deleting just the files keeps the show — stay on its page and let
      // the episodes read "missing" while the replacement searches queue
      if (mode === "filesOnly") {
        const r = await api.deleteShowFiles(showId)
        toast(r.filesDeleted === 0
          ? "Nothing to delete — no files on disk"
          : r.queued > 0
            ? `${r.filesDeleted} file${r.filesDeleted === 1 ? "" : "s"} deleted — ${r.queued} replacement search${r.queued === 1 ? "" : "es"} queued`
            : `${r.filesDeleted} file${r.filesDeleted === 1 ? "" : "s"} deleted — the show stays in the library`)
        reload({ quiet: true })
        return
      }
      await api.deleteShow(showId, mode === "files")
      toast(`${title} removed`)
      onBack()
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }
  const [uploading, setUploading] = useState(false)
  const upload = async (files: FileList) => {
    setUploading(true)
    try {
      const r = await api.uploadShow(showId, files)
      const results = r.results ?? []
      const ok = results.filter(x => !x.error)
      const bad = results.filter(x => x.error)
      if (ok.length) toast.success(`Imported ${ok.length} file${ok.length === 1 ? "" : "s"}`)
      for (const b of bad) toast.error(`${b.file}: ${b.error}`)
      reload({ quiet: true })
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setUploading(false) }
  }
  const toggleEpisode = async (e: ApiEpisode, v: boolean) => {
    try {
      await api.setEpisodeMonitored(e.id, v)
      reload({ quiet: true })
    } catch (err) { toast.error(`${err instanceof Error ? err.message : err}`) }
  }
  const toggleSeason = async (season: number, v: boolean) => {
    try {
      await api.setSeasonMonitored(showId, season, v)
      reload({ quiet: true })
    } catch (err) { toast.error(`${err instanceof Error ? err.message : err}`) }
  }

  if (loading) return <p className="mono-label py-4 text-faint">loading…</p>
  if (error || !data) {
    return (
      <section>
        <BackButton onBack={onBack} />
        <p className="mono-label py-4 text-want">{error ?? "show not found"}</p>
      </section>
    )
  }
  const { show: sh, imageBase } = data
  // newest season first — that's the one you came for. Specials (season 0)
  // fall to the bottom, where they belong.
  const seasons = [...(sh.seasons ?? [])].sort((a, b) => b.number - a.number)

  return (
    <section>
      <BackButton onBack={onBack} />
      <DetailHero imageBase={imageBase} backdrop={sh.backdrop} poster={sh.poster} title={sh.title}
        actions={<>
          <IconAction title={`Refresh metadata from ${sh.source === "tvdb" ? "TheTVDB" : "TMDB"}`} onClick={() => void refreshMeta()}><RefreshCw className="h-4 w-4" /></IconAction>
          <IconAction title="Re-match — this is not the right series"
            onClick={() => setRematchOpen(true)}><Pencil className="h-4 w-4" /></IconAction>
          <IconAction title="Auto search all missing episodes" onClick={() => void searchNow()}>
            <Zap className="h-4 w-4" />
          </IconAction>
          <ImportPathAction title="Import from server path"
            onImport={async path => {
              const r = await api.importShowPath(showId, path)
              const results = r.results ?? []
              const ok = results.filter(x => !x.error).length
              const bad = results.filter(x => x.error)
              for (const b of bad.slice(0, 3)) toast.error(`${b.file}: ${b.error}`)
              reload({ quiet: true })
              if (ok === 0) throw new Error(bad[0]?.error ?? "nothing imported")
              return `Imported ${ok} file${ok === 1 ? "" : "s"}`
            }} />
          <UploadAction title={uploading ? "Importing…" : "Import files — each ties to its SxxEyy"}
            multiple busy={uploading} onFiles={f => void upload(f)} />
          <AddToLibraryAction kind="shows" tmdbId={sh.tmdbId} tvdbId={sh.tvdbId} title={sh.title} />
          <IconAction title="Delete" danger onClick={() => setDelOpen(true)}>
            <Trash2 className="h-4 w-4" />
          </IconAction>
        </>}>
        <h1 className="font-display text-[32px] font-bold leading-tight tracking-tight text-balance">{sh.title}</h1>
        <div className="mono-label mt-1 text-muted-foreground">
          {[sh.year || null, sh.status || null, sh.genres?.join(" · ")].filter(Boolean).join("  ·  ")}
        </div>
        <div className="mt-3 flex flex-wrap items-center gap-2">
          {/* "missing" counts only what the sweep would actually hunt:
              monitored, aired, and no file. Un-monitored gaps are choices,
              and an episode nobody has released yet can't be missing. */}
          {sh.aired > 0 && sh.wanted === 0
            ? <Tag kind="good">{sh.aired < sh.episodes ? "Up to date" : "Complete"}</Tag>
            : sh.downloading ? <Tag kind="info">Downloading</Tag>
            : sh.monitored ? <Tag kind="want">{sh.wanted} missing</Tag> : <Tag kind="dim">Not monitored</Tag>}
          <span className="text-xs text-muted-foreground">
            {sh.monitoredAired > 0
              ? `${sh.monitoredAired - sh.wanted} of ${sh.monitoredAired} monitored episodes on disk`
              : `${sh.onDisk} of ${sh.aired} aired episodes on disk`}
            {sh.episodes > sh.aired && ` · ${sh.episodes - sh.aired} still to air`}
          </span>
          <ImdbLink imdbId={sh.imdbId} />
          <TmdbLink tmdbId={sh.tmdbId} kind="tv" />
          <TvdbLink tvdbId={sh.tvdbId} />
        </div>
        <Overview text={sh.overview || "No overview cached — rescan the library to refresh metadata."} />
        <div className="mt-5 flex flex-wrap items-center gap-x-6 gap-y-3">
          <div className="flex items-center gap-2.5">
            <Switch checked={sh.monitored} onCheckedChange={toggleShow} aria-label={`Monitor ${sh.title}`} />
            <span className="text-[12.5px] text-muted-foreground">Monitored — applies to every episode</span>
          </div>
          <ProfilePicker value={sh.qualityProfileId} profiles={profilesQuery.data?.profiles ?? null}
            onChange={id => void setProfile(id)} />
        </div>
        {/* TVDB-sourced shows ARE scene numbering — the offset escape
            hatch only applies while a show still lives on TMDB */}
        {sh.source !== "tvdb" && (
          <NumberingEditor showId={showId} seasonOffset={sh.seasonOffset ?? 0} tvdbId={sh.tvdbId ?? 0}
            onSaved={() => reload({ quiet: true })} />
        )}
        {sh.source === "tvdb" && (
          <p className="mono-label mt-3 text-faint">
            Metadata provided by{" "}
            <a href={`https://thetvdb.com/dereferrer/series/${sh.tvdbId}`} target="_blank" rel="noreferrer"
              className="hover:text-brass">TheTVDB</a>
          </p>
        )}
      </DetailHero>
      <RematchDialog open={rematchOpen} onOpenChange={setRematchOpen}
        kind="show" id={showId} title={sh.title} onDone={() => reload({ quiet: true })} />
      <div className="mt-4">
        {/* a show sourced from TheTVDB may carry no TMDB id at all, so
            it is addressed by whichever one it actually has */}
        <SharedWith
          kind="show"
          id={sh.tmdbId || sh.tvdbId || 0}
          source={sh.tmdbId ? undefined : "tvdb"}
          title={sh.title}
        />
      </div>

      <CastRow cast={sh.cast} imageBase={imageBase} onOpenPerson={onOpenPerson} />
      <SimilarRow kind="show" tmdbId={sh.tmdbId} onOpenPreview={onOpenPreview} />

      {seasons.map(season => {
        const eps = season.episodes ?? []
        const onDisk = eps.filter(e => e.filePath).length
        // same rule as the show total: a season mid-run counts what has
        // aired, so "4/4" doesn't read as "4/10" all season long
        const today = new Date().toISOString().slice(0, 10)
        const aired = eps.filter(e => e.airDate && e.airDate.slice(0, 10) <= today).length
        const name = season.name || `Season ${season.number}`
        // Where TheXEM says this season's releases carry other numbers,
        // say so: a show whose S9 is the scene's S10 otherwise looks like
        // reely has the season number wrong. An episode with no mapping is
        // one the scene numbers the same way, so it counts under its own
        // number — a season the scene splits in two names both halves.
        const sceneSeasons = [...new Set(eps.map(e => e.sceneSeason || e.season))].sort((a, b) => a - b)
        const renumbered = eps.length > 0 && (sceneSeasons.length > 1 || sceneSeasons[0] !== season.number)
        // only the newest season starts open — a long-running show stays
        // one screen tall until you reach for its past
        const newest = seasons[0]?.number
        const open = openSeasons?.[season.number] ?? season.number === newest
        const toggleOpen = () => setOpenSeasons({ ...openSeasons, [season.number]: !open })
        return (
          <div key={season.number} className="mt-8">
            <div className="mb-1 flex items-baseline justify-between gap-2">
              <button onClick={toggleOpen} aria-expanded={open}
                className="group/season flex items-center gap-1.5 text-left">
                <ChevronDown className={cn("h-4 w-4 shrink-0 text-faint transition-transform group-hover/season:text-brass", !open && "-rotate-90")} />
                <h2 className="font-display text-lg font-bold group-hover/season:text-brass">{name}</h2>
                {renumbered && (
                  <span className="mono-label text-faint"
                    title="TheXEM: the numbering this season's releases carry, which reely searches by">
                    releases as {sceneSeasons.map(n => `S${String(n).padStart(2, "0")}`).join(" + ")}
                  </span>
                )}
              </button>
              <div className="flex items-center gap-3">
                <span className="mono-label text-faint">
                  {onDisk}/{aired} on disk{eps.length > aired && ` · ${eps.length - aired} to air`}
                </span>
                <button className="text-faint hover:text-brass" title={`Auto search ${name}'s missing episodes`}
                  onClick={() => void searchNow(season.number)}>
                  <Zap className="h-3.5 w-3.5" />
                </button>
                <button className="text-faint hover:text-brass" title={`Manual search — ${name} pack`}
                  onClick={() => setSearch({ season: season.number, label: `${sh.title} — ${name} pack` })}>
                  <Search className="h-3.5 w-3.5" />
                </button>
                {/* checked = every episode monitored; a mixed season reads
                    as off, and one flip makes it uniform */}
                <Switch checked={eps.length > 0 && eps.every(e => e.monitored)}
                  onCheckedChange={v => void toggleSeason(season.number, v)}
                  aria-label={`Monitor ${name}`} />
              </div>
            </div>
            {open && (
              <div className="border-t">
                {eps.map(e => (
                  <EpisodeRow key={e.id} episode={e} onToggle={toggleEpisode}
                    onOpen={onOpenEpisode ? () => onOpenEpisode(e.id) : undefined}
                    onAutoSearch={() => void searchNow(e.season, e.episode)}
                    onSearch={() => setSearch({
                      season: e.season, episode: e.episode,
                      label: `${sh.title} S${String(e.season).padStart(2, "0")}E${String(e.episode).padStart(2, "0")}`,
                    })} />
                ))}
              </div>
            )}
          </div>
        )
      })}

      <ReleaseDialog open={search !== null} onOpenChange={v => { if (!v) setSearch(null) }}
        title={search ? `Releases — ${search.label}` : ""}
        fetchReleases={() => api.showReleases(showId, search?.season ?? 0, search?.episode)}
        onGrab={r => api.grabShow(showId, r, search?.season ?? 0, search?.episode)} />
      <DeleteTitleDialog open={delOpen} onOpenChange={setDelOpen} what={`"${sh.title}"`}
        onConfirm={mode => void remove(sh.title, mode)} />
    </section>
  )
}

function EpisodeRow({ episode: e, onToggle, onSearch, onAutoSearch, onOpen }: {
  episode: ApiEpisode
  onToggle: (e: ApiEpisode, v: boolean) => void
  onSearch: () => void
  onAutoSearch: () => void
  onOpen?: () => void
}) {
  const unaired = !!e.airDate && e.airDate.slice(0, 10) > new Date().toISOString().slice(0, 10)
  // a download in flight is not missing — the stripe says so too
  const missing = !e.filePath && !unaired && e.monitored && !e.downloading
  return (
    <div className={cn(
      "grid grid-cols-[44px_1fr_auto_auto_auto_auto] items-center gap-3 border-b border-linesoft py-2 pr-1",
      // the file state reads off the row's left edge: green = on disk,
      // amber = aired and still missing; missing rows also dim
      "border-l-2 pl-2",
      e.filePath ? "border-l-good/70"
        : e.downloading ? "border-l-info/70"
        : missing ? "border-l-want/70" : "border-l-transparent"
    )}>
      <span className="font-label text-right text-xs text-faint">
        {String(e.episode).padStart(2, "0")}
      </span>
      <div className="min-w-0">
        <button onClick={onOpen} disabled={!onOpen}
          className={cn("block max-w-full truncate text-left text-[13.5px] font-semibold",
            onOpen && "hover:text-brass",
            !e.filePath && !unaired && "text-muted-foreground")}>
          {e.title || `Episode ${e.episode}`}
        </button>
        <div className="mt-0.5 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
          {e.airDate ? e.airDate.slice(0, 10) : "—"}
          {e.fileSize > 0 && <span>{fmtSize(e.fileSize)}</span>}
        </div>
      </div>
      <div className="hidden sm:block">
        {e.filePath
          ? <Tag kind="good">{e.quality ? fmtQuality(e.quality, e.source) : "On disk"}</Tag>
          : unaired ? <Tag kind="info">Unaired</Tag>
            : e.downloading ? <Tag kind="info">Downloading</Tag>
            : e.monitored ? <Tag kind="want">Missing</Tag> : <Tag kind="dim">Not monitored</Tag>}
      </div>
      <button className="text-faint hover:text-brass" title="Auto search" onClick={onAutoSearch}>
        <Zap className="h-3.5 w-3.5" />
      </button>
      <button className="text-faint hover:text-brass" title="Manual search" onClick={onSearch}>
        <Search className="h-3.5 w-3.5" />
      </button>
      <Switch checked={e.monitored} onCheckedChange={v => onToggle(e, v)}
        aria-label={`Monitor S${String(e.season).padStart(2, "0")}E${String(e.episode).padStart(2, "0")}`} />
    </div>
  )
}

// NumberingEditor is the escape hatch for shows TMDB splits differently
// than release groups number them: a revival TMDB restarts at Season 1
// while its releases continue the original's numbering (S8, S9, …). The
// offset tells reely how far apart the two are; the TVDB id feeds the
// id-keyed search when TMDB's entry has none.
function NumberingEditor({ showId, seasonOffset, tvdbId, onSaved }: {
  showId: number; seasonOffset: number; tvdbId: number; onSaved: () => void
}) {
  const [open, setOpen] = useState(false)
  const [offset, setOffset] = useState(String(seasonOffset))
  const [tvdb, setTvdb] = useState(tvdbId > 0 ? String(tvdbId) : "")
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    if (open) return // don't stomp edits mid-typing on the 15s poll
    setOffset(String(seasonOffset))
    setTvdb(tvdbId > 0 ? String(tvdbId) : "")
  }, [seasonOffset, tvdbId, open])

  const save = async () => {
    const off = Number(offset)
    const id = tvdb.trim() === "" ? 0 : Number(tvdb)
    if (!Number.isInteger(off) || !Number.isInteger(id) || id < 0) {
      toast.error("Season offset and TVDB id must be whole numbers")
      return
    }
    setBusy(true)
    try {
      await api.setShowNumbering(showId, off, id)
      toast.success(off === 0
        ? "Release numbering reset to standard"
        : `Saved — searches now ask for S${String(1 + off).padStart(2, "0")} to fill Season 1`)
      setOpen(false)
      onSaved()
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setBusy(false) }
  }

  return (
    <div className="mt-3">
      <button onClick={() => setOpen(o => !o)}
        className="mono-label text-faint hover:text-brass">
        Release numbering: {seasonOffset !== 0
          ? `offset ${seasonOffset > 0 ? "+" : ""}${seasonOffset} (S1 releases as S${1 + seasonOffset})`
          : "standard"} {open ? "↑" : "↓"}
      </button>
      {open && (
        <div className="mt-2 max-w-[62ch] rounded-lg border border-linesoft bg-surface2 p-3">
          <p className="mb-2.5 text-[12px] leading-relaxed text-muted-foreground">
            For shows TMDB lists separately from how releases number them — a revival TMDB restarts
            at Season 1 whose releases continue the original's numbering. Set the offset between
            them (revival S1 released as S8 → offset 7). The TVDB id is used for id-keyed indexer
            searches; leave it empty to use TMDB's own mapping.
          </p>
          <div className="flex flex-wrap items-center gap-2.5">
            <label className="flex items-center gap-1.5 text-[12.5px] text-muted-foreground">
              Season offset
              <input value={offset} onChange={e => setOffset(e.target.value)} inputMode="numeric"
                className="h-7 w-16 rounded-lg border border-linesoft bg-surface px-2 text-[12.5px] outline-none focus:border-brass/60" />
            </label>
            <label className="flex items-center gap-1.5 text-[12.5px] text-muted-foreground">
              TVDB id
              <input value={tvdb} onChange={e => setTvdb(e.target.value)} inputMode="numeric"
                placeholder="from TMDB"
                className="h-7 w-24 rounded-lg border border-linesoft bg-surface px-2 text-[12.5px] outline-none placeholder:text-faint focus:border-brass/60" />
            </label>
            <Button className="h-7 px-2.5 text-[12px]" disabled={busy} onClick={() => void save()}>
              Save
            </Button>
          </div>
        </div>
      )}
    </div>
  )
}

function BackButton({ onBack }: { onBack: () => void }) {
  return (
    <button onClick={onBack} className="mono-label mb-5 flex items-center gap-1.5 text-muted-foreground hover:text-brass">
      ← Shows
    </button>
  )
}
