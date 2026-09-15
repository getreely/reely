import { api } from "@/api"
import type { ApiCalendarItem } from "@/api"
import { useApi } from "@/hooks/use-api"
import { Tag } from "@/components/detail"
import { Zap } from "lucide-react"
import { toast } from "sonner"
import { toastSearchResult } from "@/lib/search-toast"
import { useAccess } from "@/lib/access"
import { img } from "@/lib/utils"

// The calendar: what's coming for everything monitored, and what already
// aired but never landed on disk.
export function CalendarView({ onOpenEpisode, onOpenMovie, onOpenPreview }: {
  onOpenEpisode?: (id: number) => void
  onOpenMovie?: (id: number) => void
  // out on the portal a row opens the preview instead: reely's own
  // detail routes are not served there, so pushing one 404s
  onOpenPreview?: (kind: "movie" | "show", id: number, src?: "tvdb") => void
} = {}) {
  const { data, loading } = useApi(() => api.calendar(), 60_000)
  const { external } = useAccess()
  const upcoming = data?.upcoming ?? []
  const missing = data?.missing ?? []
  const imageBase = data?.imageBase ?? ""

  const searchFor = async (it: ApiCalendarItem) => {
    try {
      if (it.kind === "movie" && it.movieId) toastSearchResult(await api.searchMovieNow(it.movieId))
      else if (it.showId) toastSearchResult(await api.searchShowNow(it.showId, it.season ?? 0, it.episode ?? 0))
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }

  // A calendar row opens the thing itself — inside, that is the episode's
  // own page or the movie's.
  //
  // The portal has neither. Its detail routes are absent rather than
  // refused, so a row that pushed one landed on "not found"; the preview
  // is the only title page out there. An episode row opens its SHOW,
  // since an episode has no page of its own to open, and prefers the
  // TVDB id for the same reason everything else does: that is the source
  // a show was stored from.
  const openFor = (it: ApiCalendarItem): (() => void) | undefined => {
    if (external) {
      if (!onOpenPreview) return undefined
      if (it.kind === "movie") {
        return it.tmdbId ? () => onOpenPreview("movie", it.tmdbId!) : undefined
      }
      if (it.tvdbId) return () => onOpenPreview("show", it.tvdbId!, "tvdb")
      return it.tmdbId ? () => onOpenPreview("show", it.tmdbId!) : undefined
    }
    if (it.kind === "episode" && it.episodeId && onOpenEpisode) return () => onOpenEpisode(it.episodeId!)
    if (it.kind === "movie" && it.movieId && onOpenMovie) return () => onOpenMovie(it.movieId!)
    return undefined
  }

  const byDate = new Map<string, ApiCalendarItem[]>()
  for (const it of upcoming) {
    byDate.set(it.date, [...(byDate.get(it.date) ?? []), it])
  }

  return (
    <section className="max-w-[720px]">
      <h1 className="font-display text-[28px] font-bold tracking-tight">Calendar</h1>
      <p className="mono-label mb-6 mt-0.5 text-faint">
        {loading ? "loading…" : `${upcoming.length} upcoming · ${missing.length} aired, still missing`}
      </p>

      {missing.length > 0 && (
        <div className="mb-8">
          <h2 className="font-display mb-1 text-lg font-bold">Aired · still missing</h2>
          <div className="border-t">
            {missing.map((it, i) => <CalendarRow key={i} item={it} imageBase={imageBase}
              onSearch={() => void searchFor(it)} onOpen={openFor(it)} />)}
          </div>
        </div>
      )}

      <h2 className="font-display mb-1 text-lg font-bold">Upcoming</h2>
      {!loading && upcoming.length === 0 && (
        <p className="mono-label border-t py-3 text-faint">
          nothing on the horizon — monitor something with future air dates
        </p>
      )}
      {[...byDate.entries()].map(([date, items]) => (
        <div key={date} className="mb-4">
          <div className="mono-label border-t py-2 text-faint">{friendlyDate(date)}</div>
          {items.map((it, i) => <CalendarRow key={i} item={it} imageBase={imageBase}
            onOpen={openFor(it)} />)}
        </div>
      ))}
    </section>
  )
}

function CalendarRow({ item: it, imageBase, onSearch, onOpen }: {
  item: ApiCalendarItem; imageBase: string; onSearch?: () => void; onOpen?: () => void
}) {
  const what = it.kind === "episode"
    ? `${it.title} S${String(it.season ?? 0).padStart(2, "0")}E${String(it.episode ?? 0).padStart(2, "0")}`
    : it.title
  return (
    <div className="flex items-center gap-2.5 border-b border-linesoft px-1 py-2 text-[13px]">
      {/* small enough to keep the list scannable as a list, and it holds
          its space whether or not there is artwork so the rows stay in
          line. Decorative: the title is right beside it. */}
      <span className="h-[42px] w-7 shrink-0 overflow-hidden rounded-sm bg-surface2">
        {it.poster && (
          <img src={img(imageBase, "w92", it.poster)} alt="" loading="lazy"
            className="h-full w-full object-cover" />
        )}
      </span>
      <div className="min-w-0 flex-1">
        <button onClick={onOpen} disabled={!onOpen}
          className={onOpen ? "font-semibold hover:text-brass" : "font-semibold"}>{what}</button>
        {it.kind === "episode" && it.episodeTitle && (
          <span className="ml-2 text-muted-foreground">{it.episodeTitle}</span>
        )}
        {it.kind === "movie" && <span className="ml-2 text-muted-foreground">digital release</span>}
      </div>
      {it.onDisk
        ? <Tag kind="good">on disk</Tag>
        : onSearch
          ? (
            <>
              <Tag kind="want">missing</Tag>
              <button className="text-faint hover:text-brass" title="Auto search" onClick={onSearch}>
                <Zap className="h-3.5 w-3.5" />
              </button>
            </>
          )
          : <Tag kind="info">{it.date === today() ? "today" : "upcoming"}</Tag>}
    </div>
  )
}

function today(): string {
  return new Date().toISOString().slice(0, 10)
}

function friendlyDate(date: string): string {
  if (date === today()) return "Today"
  const tomorrow = new Date(Date.now() + 86400_000).toISOString().slice(0, 10)
  if (date === tomorrow) return "Tomorrow"
  return new Date(`${date}T12:00:00`).toLocaleDateString(undefined,
    { weekday: "long", month: "long", day: "numeric" })
}
