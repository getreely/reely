import { useState } from "react"
import { api } from "@/api"
import { useApi } from "@/hooks/use-api"
import { DetailHero, IconAction, Tag, fmtQuality, fmtRuntime, fmtSize } from "@/components/detail"
import { ReleaseDialog } from "@/components/Releases"
import { Switch } from "@/components/ui/switch"
import { Search, Zap } from "lucide-react"
import { toast } from "sonner"
import { toastSearchResult } from "@/lib/search-toast"
import { cn } from "@/lib/utils"
import { Overview } from "@/components/Overview"
import { TitleStreams } from "@/components/Streams"

// EpisodeView is one episode's own page: what it's about, when it aired,
// what's on disk for it, and the same monitor/search tools as its
// row in the season list.
export function EpisodeView({ episodeId, onBack, onOpenShow }: {
  episodeId: number
  onBack: () => void
  // the show this episode belongs to — reachable from its name, since
  // "back" may lead somewhere else entirely (Home, a search, the calendar)
  onOpenShow?: (showId: number) => void
}) {
  const { data, error, loading, reload } = useApi(() => api.episode(episodeId), 15_000)
  const [searchOpen, setSearchOpen] = useState(false)

  const toggle = async (v: boolean) => {
    try {
      await api.setEpisodeMonitored(episodeId, v)
      reload({ quiet: true })
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }
  const searchNow = async () => {
    if (!data) return
    try {
      toastSearchResult(await api.searchShowNow(data.show.id, data.episode.season, data.episode.episode))
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }

  if (loading) return <p className="mono-label py-4 text-faint">loading…</p>
  if (error || !data) {
    return (
      <section>
        <button onClick={onBack} className="mono-label mb-5 flex items-center gap-1.5 text-muted-foreground hover:text-brass">← Back</button>
        <p className="mono-label py-4 text-want">{error ?? "episode not found"}</p>
      </section>
    )
  }
  const e = data.episode
  const sh = data.show
  const code = `S${String(e.season).padStart(2, "0")}E${String(e.episode).padStart(2, "0")}`
  const unaired = !!e.airDate && e.airDate.slice(0, 10) > new Date().toISOString().slice(0, 10)

  return (
    <section>
      <button onClick={onBack} className="mono-label mb-5 flex items-center gap-1.5 text-muted-foreground hover:text-brass">
        ← {sh.title}
      </button>
      <DetailHero imageBase={data.imageBase} backdrop={sh.backdrop} poster={sh.poster} title={sh.title}
        actions={<>
          <IconAction title="Auto search this episode" onClick={() => void searchNow()}>
            <Zap className="h-4 w-4" />
          </IconAction>
          <IconAction title="Manual search" onClick={() => setSearchOpen(true)}>
            <Search className="h-4 w-4" />
          </IconAction>
        </>}>
        <div className="mono-label text-muted-foreground">
          <button onClick={onOpenShow ? () => onOpenShow(sh.id) : undefined} disabled={!onOpenShow}
            className={cn(onOpenShow && "hover:text-brass")}>
            {sh.title}
          </button>
          {sh.year ? ` · ${sh.year}` : ""}
        </div>
        <h1 className="font-display mt-1 text-[28px] font-bold leading-tight tracking-tight text-balance">
          {code} — {e.title || `Episode ${e.episode}`}
        </h1>
        <div className="mt-3 flex flex-wrap items-center gap-2">
          {e.filePath
            ? <Tag kind="good">On disk{e.quality ? ` · ${fmtQuality(e.quality, e.source)}` : ""}</Tag>
            : unaired ? <Tag kind="info">Unaired</Tag>
              : e.monitored ? <Tag kind="want">Missing</Tag> : <Tag kind="dim">Not monitored</Tag>}
          {e.fileSize > 0 && <span className="text-xs text-muted-foreground">{fmtSize(e.fileSize)}</span>}
          <span className="text-xs text-muted-foreground">
            {[e.airDate ? `aired ${e.airDate.slice(0, 10)}` : null, e.runtime ? fmtRuntime(e.runtime) : null]
              .filter(Boolean).join(" · ")}
          </span>
        </div>
        <Overview text={e.overview || "No episode overview on TMDB yet."} />
        <div className="mt-5 flex items-center gap-2.5">
          <Switch checked={e.monitored} onCheckedChange={v => void toggle(v)} aria-label={`Monitor ${code}`} />
          <span className="text-[12.5px] text-muted-foreground">Monitored</span>
        </div>
        {e.filePath && (
          <div className="font-label mt-4 truncate text-[11px] uppercase text-faint" title={e.filePath}>{e.filePath}</div>
        )}
        {e.filePath && <TitleStreams kind="episode" id={e.id} />}
      </DetailHero>

      <ReleaseDialog open={searchOpen} onOpenChange={setSearchOpen}
        title={`Releases — ${sh.title} ${code}`}
        fetchReleases={() => api.showReleases(sh.id, e.season, e.episode)}
        onGrab={r => api.grabShow(sh.id, r, e.season, e.episode)} />
    </section>
  )
}
