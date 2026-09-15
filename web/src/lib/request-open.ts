import type { ApiRequest } from "@/api"

// How a request row opens the title it is about.
//
// A request stores whichever ids the search result carried, and with a
// TVDB key configured a show search comes from TheTVDB — so a requested
// show routinely has a TVDB id and NO TMDB one. Keying the row on the
// TMDB id alone left every such row disabled: the owner could open a
// requested film to decide on it and not a requested series, which is
// the half where seeing the seasons matters most.
//
// Shows prefer their TVDB id for the same reason everything else does:
// it is the source they were stored from. A movie has one id and always
// did.
export function requestOpensAt(r: ApiRequest): {
  kind: "movie" | "show"
  id: number
  src?: "tvdb"
} | null {
  if (r.kind === "movie") {
    return r.tmdbId ? { kind: "movie", id: r.tmdbId } : null
  }
  if (r.tvdbId) return { kind: "show", id: r.tvdbId, src: "tvdb" }
  return r.tmdbId ? { kind: "show", id: r.tmdbId } : null
}
