import { useMemo, useRef } from "react"
import { api } from "@/api"
import type { ApiSearchResult } from "@/api"
import { useApi } from "@/hooks/use-api"
import { ChevronLeft, ChevronRight, Tv } from "lucide-react"
import { cn, img } from "@/lib/utils"

// The poster rows shared by Home and Explore: one sideways strip per
// list, scrolling by swipe on touch and by arrow button where a mouse is
// likelier than a finger.

export function PosterRow({ title, children }: { title: string; children: React.ReactNode }) {
  const ref = useRef<HTMLDivElement>(null)
  const nudge = (dir: 1 | -1) => {
    const el = ref.current
    if (el) el.scrollBy({ left: dir * el.clientWidth * 0.85, behavior: "smooth" })
  }
  return (
    <div className="mb-8">
      <div className="mb-2.5 flex items-center justify-between">
        <h2 className="font-display text-lg font-bold">{title}</h2>
        <div className="flex gap-1 max-md:hidden">
          <button onClick={() => nudge(-1)} aria-label={`Scroll ${title} left`}
            className="flex h-7 w-7 items-center justify-center rounded-lg border border-linesoft text-muted-foreground hover:border-brass/60 hover:text-brass">
            <ChevronLeft className="h-4 w-4" />
          </button>
          <button onClick={() => nudge(1)} aria-label={`Scroll ${title} right`}
            className="flex h-7 w-7 items-center justify-center rounded-lg border border-linesoft text-muted-foreground hover:border-brass/60 hover:text-brass">
            <ChevronRight className="h-4 w-4" />
          </button>
        </div>
      </div>
      <div ref={ref}
        className="flex snap-x gap-3.5 overflow-x-auto pb-1.5 [-ms-overflow-style:none] [scrollbar-width:none] [&::-webkit-scrollbar]:hidden">
        {children}
      </div>
    </div>
  )
}

export function RowCard({ imageBase, poster, title, subtitle, strip, progress, count, badge, badgeKind = "good", fill, onOpen }: {
  imageBase: string; poster: string; title: string; subtitle: string
  strip?: "good" | "want" | "dim"
  // For a show the strip becomes a bar — green for what is here over an
  // amber track for what is not — so a glance reads 8/10 without
  // covering any artwork. Same treatment the owner's grid uses, because
  // a watcher is asking the same question about the same title.
  progress?: number
  // the bar's number — "6/10" — on the artwork where the bar is, the
  // same badge the owner's grid carries
  count?: string
  badge?: string
  // fill lets the card take its container's width instead of the fixed
  // one the scrolling rows need. In a grid the fixed width leaves every
  // poster hugging the left of a wider cell, which reads as a page that
  // failed to line up rather than as a deliberate size.
  fill?: boolean
  // "In library" is green; "Requested" is the same badge in the amber the
  // rest of the app already uses for a thing that is wanted and not here
  badgeKind?: "good" | "want"
  onOpen: () => void
}) {
  return (
    // flex-col matters: a plain button centres its content, so in a row
    // stretched to its tallest card a one-line subtitle pushed the poster
    // and title down while a wrapped one stayed put. Column layout pins
    // every card's content to the top, so posters and titles line up
    // whatever the subtitles do.
    <button onClick={onOpen} className={cn("group flex snap-start flex-col text-left",
      fill ? "w-full" : "w-[118px] shrink-0")}>
      <div className="relative aspect-2/3 overflow-hidden rounded-[10px] bg-surface2 poster-shadow transition-transform group-hover:scale-[1.025]">
        {poster
          ? <img src={img(imageBase, "w342", poster)} alt="" loading="lazy" className="absolute inset-0 h-full w-full object-cover" />
          : <div className="flex h-full items-center justify-center p-3 text-center"><span className="font-display text-[13px] font-bold leading-tight">{title}</span></div>}
        {strip && (
          <span className="absolute inset-x-0 bottom-0 h-[4px]">
            {progress !== undefined ? (
              <>
                <span className="absolute inset-0 bg-want/45" />
                <span className="absolute inset-y-0 left-0 bg-good"
                  style={{ width: `${Math.round(Math.min(1, Math.max(0, progress)) * 100)}%` }} />
              </>
            ) : (
              <span className={cn("absolute inset-0",
                strip === "good" && "bg-good", strip === "want" && "bg-want", strip === "dim" && "bg-white/25")} />
            )}
          </span>
        )}
        {count && (
          <span className="font-label absolute bottom-1.5 left-1.5 flex items-center gap-1 rounded-lg bg-black/70 px-1.5 py-0.5 text-[9.5px] font-bold text-white/90 backdrop-blur-xs">
            <Tv className="h-3 w-3" /> {count}
          </span>
        )}
        {badge && (
          <span className={cn("absolute left-1.5 top-1.5 rounded-lg bg-black/70 px-1.5 py-0.5 text-[9.5px] font-bold backdrop-blur-xs",
            badgeKind === "want" ? "text-want" : "text-good")}>{badge}</span>
        )}
      </div>
      <div className="mt-1.5 truncate text-[12.5px] font-semibold leading-tight group-hover:text-brass">{title}</div>
      <div className="text-[11.5px] text-muted-foreground">{subtitle}</div>
    </button>
  )
}

export function TrendCard({ imageBase, result, owned, requested, onOpen }: {
  imageBase: string; result: ApiSearchResult; owned: boolean
  // requested only ever reaches here on the requester side; the owner's
  // browse stays as it was, with pending work living in the queue
  requested?: boolean
  onOpen: () => void
}) {
  const badge = owned ? "In library" : requested ? "Requested" : undefined
  return (
    <RowCard imageBase={imageBase} poster={result.poster} title={result.title}
      subtitle={String(result.year || "—")} badge={badge}
      badgeKind={owned ? "good" : "want"} onOpen={onOpen} />
  )
}

// SimilarRow is a title's "more like this": TMDB's recommendations for it,
// opening previews rather than titles you own. It renders nothing at all
// when TMDB has no suggestions or the call fails — a dead row on a detail
// page is worse than no row. Suggestions you already have are badged the
// same way Explore badges them, so the row never invites you to re-add
// something sitting in your library.
export function SimilarRow({ kind, tmdbId, onOpenPreview }: {
  kind: "movie" | "show"
  tmdbId: number
  onOpenPreview?: (kind: "movie" | "show", tmdbId: number) => void
}) {
  const query = useApi(() => api.similar(kind === "movie" ? "movie" : "tv", tmdbId))
  // just the TMDB ids of what you already have, so a suggestion you own is
  // badged rather than offered as new
  const mine = useApi(async () => kind === "movie"
    ? (await api.movies()).movies?.map(m => m.tmdbId) ?? []
    : (await api.shows()).shows?.map(s => s.tmdbId) ?? [])
  const owned = useMemo(() => new Set(mine.data ?? []), [mine.data])

  const results = query.data?.results ?? []
  if (!tmdbId || !onOpenPreview || results.length === 0) return null
  return (
    <div className="mt-8">
      <PosterRow title="More like this">
        {results.map(r => (
          <TrendCard key={`sim${r.tmdbId}`} imageBase={query.data?.imageBase ?? ""} result={r}
            owned={owned.has(r.tmdbId)} onOpen={() => onOpenPreview(kind, r.tmdbId)} />
        ))}
      </PosterRow>
    </div>
  )
}
