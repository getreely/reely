import { useMemo } from "react"
import { api } from "@/api"
import type { ApiMovie } from "@/api"
import { useApi } from "@/hooks/use-api"
import { PosterRow, RowCard } from "@/components/rows"
import { Compass } from "lucide-react"

// Home is what's yours: what just landed in your libraries, and nothing
// else. Everything the wider world is watching lives on Explore, so this
// page stays short and answers one question — what's new for me.
export function HomeView({ onOpenMovie, onOpenShow, onOpenEpisode, onExplore }: {
  onOpenMovie: (id: number) => void
  onOpenShow: (id: number) => void
  onOpenEpisode: (id: number) => void
  onExplore: () => void
}) {
  const moviesQuery = useApi(() => api.movies(), 30_000)
  const showsQuery = useApi(() => api.shows(), 30_000)
  const recentEpsQuery = useApi(() => api.recentEpisodes(), 30_000)

  const movies = moviesQuery.data?.movies ?? []
  const imageBase = moviesQuery.data?.imageBase ?? showsQuery.data?.imageBase ?? ""

  // Newly watchable, not newly monitored: a movie tracked months before it
  // releases belongs here the day its file lands, not the day you added it.
  // Movies with no file yet are the Movies library's business, not Home's —
  // this row answers "what can I watch tonight". A file that arrived by
  // library scan carries no import event, so its add time stands in.
  const recentMovies = useMemo(
    () => movies
      .filter(m => m.filePath)
      .map(m => ({ m, at: m.importedAt || m.addedAt }))
      .sort((a, b) => b.at.localeCompare(a.at))
      .slice(0, 20)
      .map(x => x.m),
    [movies])
  const recentShows = recentEpsQuery.data?.shows ?? []
  const empty = recentMovies.length + recentShows.length === 0

  return (
    <section>
      <h1 className="font-display mb-6 text-[28px] font-bold tracking-tight">Home</h1>

      {recentMovies.length > 0 && (
        <PosterRow title="Recently added — Movies">
          {recentMovies.map(m => (
            <RowCard key={`m${m.id}`} imageBase={imageBase} poster={m.poster} title={m.title}
              subtitle={String(m.year || "—")} strip={movieStrip(m)} onOpen={() => onOpenMovie(m.id)} />
          ))}
        </PosterRow>
      )}
      {recentShows.length > 0 && (
        <PosterRow title="Recently added — Episodes">
          {recentShows.map(s => (
            <RowCard key={`s${s.showId}`}
              imageBase={recentEpsQuery.data?.imageBase ?? imageBase}
              poster={s.poster} title={s.showTitle}
              subtitle={s.count > 1
                ? `${s.count} new episodes`
                : `S${String(s.season).padStart(2, "0")}E${String(s.episode).padStart(2, "0")}${s.episodeTitle ? ` · ${s.episodeTitle}` : ""}`}
              strip="good"
              onOpen={() => (s.count > 1 ? onOpenShow(s.showId) : onOpenEpisode(s.episodeId))} />
          ))}
        </PosterRow>
      )}
      {empty && !moviesQuery.loading && !showsQuery.loading && (
        <p className="mb-6 max-w-[52ch] text-[13.5px] leading-relaxed text-muted-foreground">
          Nothing in your libraries yet. Explore has what's trending, popular and
          best-reviewed — or search from the bar above to add something by name.
        </p>
      )}

      <button onClick={onExplore}
        className="flex items-center gap-2 rounded-xl border border-linesoft px-3.5 py-2 text-[13px] text-muted-foreground hover:border-brass/60 hover:text-brass">
        <Compass className="h-4 w-4" /> Explore what's out there
      </button>
    </section>
  )
}

function movieStrip(m: ApiMovie): "good" | "want" | "dim" {
  return m.filePath ? "good" : m.monitored ? "want" : "dim"
}
