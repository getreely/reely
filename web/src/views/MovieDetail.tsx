import { useState } from "react"
import { api } from "@/api"
import { useApi } from "@/hooks/use-api"
import { AddToLibraryAction, CastRow, ImportPathAction, DetailHero, IconAction, ImdbLink, ProfilePicker, Tag, TmdbLink, UploadAction, fmtQuality, fmtRuntime, fmtSize } from "@/components/detail"
import { SharedWith } from "@/components/SharedWith"
import { RematchDialog } from "@/components/RematchDialog"
import { ReleaseDialog } from "@/components/Releases"
import { SimilarRow } from "@/components/rows"
import { DeleteTitleDialog, type DeleteMode } from "@/components/SelectionBar"
import { Switch } from "@/components/ui/switch"
import { Pencil, RefreshCw, Search, Trash2, Zap } from "lucide-react"
import { toast } from "sonner"
import { toastSearchResult } from "@/lib/search-toast"
import { Overview } from "@/components/Overview"
import { TitleStreams } from "@/components/Streams"

export function MovieDetailView({ movieId, onBack, onOpenPerson, onOpenPreview }: {
  movieId: number; onBack: () => void
  onOpenPerson?: (tmdbId: number) => void
  onOpenPreview?: (kind: "movie" | "show", tmdbId: number) => void
}) {
  const { data, error, loading, reload } = useApi(() => api.movie(movieId), 15_000)
  const profilesQuery = useApi(() => api.profiles())
  const [searchOpen, setSearchOpen] = useState(false)

  const toggleMonitor = async (v: boolean) => {
    try {
      await api.setMovieMonitored(movieId, v)
      reload({ quiet: true })
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }
  const setProfile = async (profileId: number) => {
    try {
      await api.setMovieProfile(movieId, profileId)
      reload({ quiet: true })
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }
  const setAvailability = async (v: "released" | "announced") => {
    try {
      await api.setMovieAvailability(movieId, v)
      toast.success(v === "announced"
        ? "Automation may grab this any time — watch for fakes before release"
        : "Automation waits until this movie is actually out")
      reload({ quiet: true })
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }
  const [uploading, setUploading] = useState(false)
  const upload = async (files: FileList) => {
    setUploading(true)
    try {
      await api.uploadMovie(movieId, files)
      toast.success("File imported")
      reload({ quiet: true })
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setUploading(false) }
  }
  const refreshMeta = async () => {
    try {
      await api.refreshTitle("movie", movieId)
      toast.success("Metadata refreshed from TMDB")
      reload({ quiet: true })
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }
  const searchNow = async () => {
    try {
      toastSearchResult(await api.searchMovieNow(movieId))
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }
  const [delOpen, setDelOpen] = useState(false)
  const [rematchOpen, setRematchOpen] = useState(false)
  const remove = async (title: string, mode: DeleteMode) => {
    try {
      // deleting just the file keeps the title — stay on its page and let
      // it read "missing" while the replacement search runs
      if (mode === "filesOnly") {
        const r = await api.deleteMovieFile(movieId)
        toast(r.filesDeleted === 0
          ? "Nothing to delete — no file on disk"
          : r.queued > 0
            ? "File deleted — searching for a replacement"
            : "File deleted — the movie stays in the library")
        reload({ quiet: true })
        return
      }
      await api.deleteMovie(movieId, mode === "files")
      toast(`${title} removed`)
      onBack()
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }

  if (loading) return <p className="mono-label py-4 text-faint">loading…</p>
  if (error || !data) {
    return (
      <section>
        <BackButton onBack={onBack} />
        <p className="mono-label py-4 text-want">{error ?? "movie not found"}</p>
      </section>
    )
  }
  const { movie: m, imageBase } = data

  return (
    <section>
      <BackButton onBack={onBack} />
      <DetailHero imageBase={imageBase} backdrop={m.backdrop} poster={m.poster} title={m.title}
        actions={<>
          <IconAction title="Auto search" onClick={() => void searchNow()}><Zap className="h-4 w-4" /></IconAction>
          <IconAction title="Refresh metadata from TMDB" onClick={() => void refreshMeta()}><RefreshCw className="h-4 w-4" /></IconAction>
          <IconAction title="Re-match — this is not the right film"
            onClick={() => setRematchOpen(true)}><Pencil className="h-4 w-4" /></IconAction>
          <IconAction title="Manual search" onClick={() => setSearchOpen(true)}><Search className="h-4 w-4" /></IconAction>
          <ImportPathAction title="Import from server path"
            onImport={async path => { await api.importMoviePath(movieId, path); reload({ quiet: true }); return "Imported" }} />
          <UploadAction title={uploading ? "Importing…" : "Import a file"} busy={uploading}
            onFiles={f => void upload(f)} />
          <AddToLibraryAction kind="movies" tmdbId={m.tmdbId} title={m.title} />
          <IconAction title="Delete" danger onClick={() => setDelOpen(true)}>
            <Trash2 className="h-4 w-4" />
          </IconAction>
        </>}>
        <h1 className="font-display text-[32px] font-bold leading-tight tracking-tight text-balance">{m.title}</h1>
        <div className="mono-label mt-1 text-muted-foreground">
          {[m.year || null, fmtRuntime(m.runtime), m.genres?.join(" · ")].filter(Boolean).join("  ·  ")}
        </div>
        <div className="mt-3 flex flex-wrap items-center gap-2">
          {m.filePath
            ? <Tag kind="good">On disk{m.quality ? ` · ${fmtQuality(m.quality, m.source)}` : ""}</Tag>
            : m.downloading ? <Tag kind="info">Downloading</Tag>
              : m.monitored ? <Tag kind="want">Missing</Tag> : <Tag kind="dim">Not monitored</Tag>}
          {m.fileSize > 0 && <span className="text-xs text-muted-foreground">{fmtSize(m.fileSize)}</span>}
          <ImdbLink imdbId={m.imdbId} />
          <TmdbLink tmdbId={m.tmdbId} kind="movie" />
        </div>
        <Overview text={m.overview || "No overview cached — rescan the library to refresh metadata."} />
        <div className="mt-5 flex flex-wrap items-center gap-x-6 gap-y-3">
          <div className="flex items-center gap-2.5">
            <Switch checked={m.monitored} onCheckedChange={toggleMonitor} aria-label={`Monitor ${m.title}`} />
            <span className="text-[12.5px] text-muted-foreground">Monitored</span>
          </div>
          <ProfilePicker value={m.qualityProfileId} profiles={profilesQuery.data?.profiles ?? null}
            onChange={id => void setProfile(id)} />
          <label className="flex items-center gap-2" title="With 'After release', automatic searches and RSS wait until the movie is actually out — releases appearing earlier are almost always mislabeled or fake. Manual grabs are never blocked. 'Any time' lifts the gate for a title you expect early.">
            <span className="text-[12.5px] text-muted-foreground">Auto-grab</span>
            <select value={m.minAvailability || "released"}
              onChange={e => void setAvailability(e.target.value as "released" | "announced")}
              className="mono-label rounded border border-linesoft bg-surface2 px-1.5 py-0.5">
              <option value="released">After release</option>
              <option value="announced">Any time</option>
            </select>
          </label>
        </div>
        {m.filePath && (
          <div className="mono-label mt-4 truncate text-faint" title={m.filePath}>{m.filePath}</div>
        )}
        {m.filePath && <TitleStreams kind="movie" id={m.id} />}
      </DetailHero>
      <RematchDialog open={rematchOpen} onOpenChange={setRematchOpen}
        kind="movie" id={movieId} title={m.title} onDone={() => reload({ quiet: true })} />
      <div className="mt-4">
        <SharedWith kind="movie" id={m.tmdbId} title={m.title} />
      </div>
      <CastRow cast={m.cast} imageBase={imageBase} onOpenPerson={onOpenPerson} />
      <SimilarRow kind="movie" tmdbId={m.tmdbId} onOpenPreview={onOpenPreview} />
      <ReleaseDialog open={searchOpen} onOpenChange={setSearchOpen}
        title={`Releases — ${m.title}`} fetchReleases={() => api.movieReleases(movieId)}
        onGrab={r => api.grabMovie(movieId, r)} />
      <DeleteTitleDialog open={delOpen} onOpenChange={setDelOpen} what={`"${m.title}"`}
        onConfirm={mode => void remove(m.title, mode)} />
    </section>
  )
}

function BackButton({ onBack }: { onBack: () => void }) {
  return (
    <button onClick={onBack} className="mono-label mb-5 flex items-center gap-1.5 text-muted-foreground hover:text-brass">
      ← Movies
    </button>
  )
}
