import { useMemo } from "react"
import { api } from "@/api"
import type { ApiRequest } from "@/api"
import { useApi } from "@/hooks/use-api"

// The open requests in the libraries this account can reach, as the same
// shape of lookup `owned` uses — a Set of keys the views test titles
// against. Requests are per LIBRARY, so this only ever answers for the
// libraries the server already scoped to this account: three people
// sharing one see the same requests, and somebody with their own library
// sees the title as still askable there.
//
// Nothing here names who asked. That listing carries no username at all.

export function requestKey(kind: string, tmdbId?: number, tvdbId?: number): string {
  return kind === "show" && tvdbId ? `show-tvdb-${tvdbId}` : `${kind}-${tmdbId ?? 0}`
}

export interface RequestedSet {
  /** True when this title has an open request in a library you can reach. */
  has: (kind: string, tmdbId?: number, tvdbId?: number) => boolean
  requests: ApiRequest[]
  reload: () => void
}

export function useRequested(enabled = true): RequestedSet {
  const query = useApi(() => api.requests(), enabled ? 30_000 : undefined)
  const requests = query.data?.requests ?? []
  const keys = useMemo(() => {
    const set = new Set<string>()
    for (const r of requests) {
      set.add(requestKey(r.kind, r.tmdbId, r.tvdbId))
      // a show asked for by TVDB id is still the same show by TMDB id
      if (r.kind === "show" && r.tmdbId) set.add(`show-${r.tmdbId}`)
    }
    return set
  }, [requests])
  return {
    has: (kind, tmdbId, tvdbId) =>
      keys.has(requestKey(kind, tmdbId, tvdbId)) ||
      (kind === "show" && !!tmdbId && keys.has(`show-${tmdbId}`)),
    requests,
    reload: query.reload,
  }
}
