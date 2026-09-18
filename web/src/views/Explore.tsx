import { api } from "@/api"
import type { ApiSearchResult } from "@/api"
import { useApi } from "@/hooks/use-api"
import { PosterRow, TrendCard } from "@/components/rows"
import { useIsRequester } from "@/lib/access"
import { useRequested } from "@/hooks/use-requests"
import { useLibraryBadges } from "@/hooks/use-library-badges"

// Explore is what's out there, as opposed to Home's what's yours. Every
// row here comes from TMDB and opens a preview rather than a title you
// own — so adding is always one deliberate step, never a side effect of
// browsing. Titles already in a library you can see are badged.
export function ExploreView({ onOpenPreview }: {
  onOpenPreview: (kind: "movie" | "show", tmdbId: number) => void
}) {
  const exploreQuery = useApi(() => api.explore())
  // what the install holds, joined against TMDB's rows to say whether a
  // title is here, partly here, on its way, or only asked for
  const badges = useLibraryBadges()
  // Requested is a requester-side mark only. The owner browses as before;
  // what is waiting on them lives in the queue, not scattered here.
  const isRequester = useIsRequester()
  const requested = useRequested(isRequester)

  const d = exploreQuery.data
  const imageBase = d?.imageBase ?? ""

  // every row is the same shape; listing them keeps the markup honest
  const rows: { key: string; title: string; kind: "movie" | "show"; rows: ApiSearchResult[] }[] = [
    { key: "tm", title: "Trending — Movies", kind: "movie", rows: d?.movies ?? [] },
    { key: "ts", title: "Trending — Shows", kind: "show", rows: d?.shows ?? [] },
    { key: "pm", title: "Popular — Movies", kind: "movie", rows: d?.popularMovies ?? [] },
    { key: "ps", title: "Popular — Shows", kind: "show", rows: d?.popularShows ?? [] },
    { key: "rm", title: "Top rated — Movies", kind: "movie", rows: d?.topMovies ?? [] },
    { key: "rs", title: "Top rated — Shows", kind: "show", rows: d?.topShows ?? [] },
    ...(d?.providers ?? []).map(p => ({
      key: `sv${p.key}`,
      title: `${p.name} — ${p.kind === "movie" ? "Movies" : "Shows"}`,
      kind: p.kind,
      rows: p.results ?? [],
    })),
  ]

  return (
    <section>
      <h1 className="font-display text-[28px] font-bold tracking-tight">Explore</h1>
      <p className="mono-label mb-6 mt-0.5 text-faint">
        {exploreQuery.loading ? "asking TMDB…" : "trending, popular, and what's on the big services"}
      </p>

      {rows.map(r => r.rows.length > 0 && (
        <PosterRow key={r.key} title={r.title}>
          {r.rows.map(item => (
            <TrendCard key={`${r.key}${item.tmdbId}`} imageBase={imageBase} result={item}
              badge={badges.badgeFor(r.kind, item.tmdbId,
                isRequester && requested.has(r.kind, item.tmdbId))}
              onOpen={() => onOpenPreview(r.kind, item.tmdbId)} />
          ))}
        </PosterRow>
      ))}

      {exploreQuery.error && (
        <p className="mono-label py-2 text-want">discovery is off — {exploreQuery.error}</p>
      )}
      {!exploreQuery.loading && !exploreQuery.error && rows.every(r => r.rows.length === 0) && (
        <p className="mono-label py-2 text-faint">TMDB returned nothing to show</p>
      )}
    </section>
  )
}
