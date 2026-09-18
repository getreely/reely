import { useMemo } from "react"
import { api } from "@/api"
import { useApi } from "@/hooks/use-api"
import { movieBadge, showBadge } from "@/lib/title-status"
import type { TitleBadge } from "@/lib/title-status"

// The library rows a browse card needs to say what a title is doing.
//
// Browse rows come from TMDB and know nothing about this install, so the
// answer has to be joined on the client against what reely holds. Both
// listings are fetched once here rather than per view, so Explore and a
// detail page's suggestions cannot drift into badging the same title two
// different ways.
export function useLibraryBadges(pollMs = 60_000) {
  const moviesQuery = useApi(() => api.movies(), pollMs)
  const showsQuery = useApi(() => api.shows(), pollMs)
  const movies = useMemo(
    () => new Map((moviesQuery.data?.movies ?? []).map(m => [m.tmdbId, m])),
    [moviesQuery.data])
  const shows = useMemo(
    () => new Map((showsQuery.data?.shows ?? []).map(s => [s.tmdbId, s])),
    [showsQuery.data])

  return useMemo(() => ({
    // requested is the caller's business: it only applies on the
    // requester side, and only they fetch the request list.
    badgeFor: (kind: "movie" | "show", tmdbId: number, requested = false): TitleBadge | undefined =>
      kind === "movie"
        ? movieBadge(movies.get(tmdbId), requested)
        : showBadge(shows.get(tmdbId), requested),
  }), [movies, shows])
}
