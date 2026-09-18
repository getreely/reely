import { useEffect, useMemo, useRef, useState } from "react"
import { cn, img } from "@/lib/utils"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { api } from "@/api"
import type { ApiLibrary, ApiSearchResult } from "@/api"
import { useApi } from "@/hooks/use-api"
import { Plus, Search } from "lucide-react"
import { toast } from "sonner"
import { useAccess, useIsRequester } from "@/lib/access"
import { LibraryAction, preferredLibrary } from "@/components/LibraryAction"
import { useRequested } from "@/hooks/use-requests"
import { movieBadge, showBadge, badgeTextClass } from "@/lib/title-status"

// AddSearchDialog is the app's front door for new titles: open it from the
// search pill, type, and results drop in live — movies and shows together,
// most popular first. Each row adds straight into a library; clicking the
// row itself opens the full preview page (description, seasons, episode
// lists) for a look before committing.
export function AddSearchDialog({ open, onOpenChange, onOpenPreview, onAdded }: {
  open: boolean
  onOpenChange: (v: boolean) => void
  onOpenPreview: (kind: "movie" | "show", id: number, src?: "tvdb") => void
  onAdded: (kind: "movie" | "show", id: number) => void
}) {
  const [q, setQ] = useState("")
  const [results, setResults] = useState<ApiSearchResult[]>([])
  const [imageBase, setImageBase] = useState("")
  const [searching, setSearching] = useState(false)
  const [adding, setAdding] = useState(0)
  const timer = useRef<ReturnType<typeof setTimeout>>(undefined)

  // which titles are already in some library, read when this opens
  const libsQuery = useApi(() => api.libraries(), undefined, open)
  const moviesQuery = useApi(() => api.movies(), undefined, open)
  const showsQuery = useApi(() => api.shows(), undefined, open)
  const libraries = libsQuery.data?.libraries ?? []
  const isRequester = useIsRequester()
  const { defaultLibraryId } = useAccess()
  const requested = useRequested(isRequester)
  const [asking, setAsking] = useState(0)

  // Asking is one call. It answers 409 when somebody has already asked,
  // which reads as "Requested" rather than as a failure.
  const askFor = async (r: ApiSearchResult, lib: ApiLibrary, audience?: number[]) => {
    setAsking(r.tmdbId || r.tvdbId || -1)
    try {
      const { status } = await api.createRequest({
        kind: r.kind, tmdbId: r.tmdbId, tvdbId: r.tvdbId,
        title: r.title, year: r.year, poster: r.poster, libraryId: lib.id, audience,
      })
      // approved on the spot means it needed no decision — either this
      // account is trusted with the kind, or the install already holds it
      // somewhere and adding it costs nothing
      toast.success(status === "approved"
        ? `Adding ${r.title} to ${lib.name} now`
        : `Requested ${r.title} for ${lib.name}`)
      requested.reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Could not request that")
    } finally {
      setAsking(0)
    }
  }
  // What a result is doing, from the rows above: Downloading, In
  // library, Partial or Requested.
  const held = (r: ApiSearchResult) => r.kind === "movie"
    ? movieBadge(heldMovies.get(r.tmdbId), requested.has(r.kind, r.tmdbId, r.tvdbId))
    : showBadge(heldShows.get(r.tmdbId), requested.has(r.kind, r.tmdbId, r.tvdbId))

  const owned = useMemo(() => {
    const m = new Map<string, Set<number>>()
    for (const mv of moviesQuery.data?.movies ?? []) {
      const k = `movie-${mv.tmdbId}`
      if (!m.has(k)) m.set(k, new Set())
      m.get(k)!.add(mv.libraryId)
    }
    for (const sh of showsQuery.data?.shows ?? []) {
      for (const k of [`show-${sh.tmdbId}`, sh.tvdbId ? `show-tvdb-${sh.tvdbId}` : ""]) {
        if (!k) continue
        if (!m.has(k)) m.set(k, new Set())
        m.get(k)!.add(sh.libraryId)
      }
    }
    return m
  }, [moviesQuery.data, showsQuery.data])

  // the library rows themselves, so a result can say what it is actually
  // doing rather than only whether reely has heard of it
  const heldMovies = useMemo(
    () => new Map((moviesQuery.data?.movies ?? []).map(mv => [mv.tmdbId, mv])), [moviesQuery.data])
  const heldShows = useMemo(
    () => new Map((showsQuery.data?.shows ?? []).map(sh => [sh.tmdbId, sh])), [showsQuery.data])

  // debounce keystrokes so each pause = one TMDB round trip
  useEffect(() => {
    if (!open) return
    clearTimeout(timer.current)
    const query = q.trim()
    if (query.length < 3) { setResults([]); setSearching(false); return }
    let stale = false
    timer.current = setTimeout(async () => {
      setSearching(true)
      try {
        const res = await api.search(query)
        if (stale) return // a newer query superseded this one
        setResults([...(res.results ?? [])].sort((a, b) => b.popularity - a.popularity))
        setImageBase(res.imageBase)
      } catch (e) {
        if (!stale) toast.error(`Search failed: ${e instanceof Error ? e.message : e}`)
      } finally {
        if (!stale) setSearching(false)
      }
    }, 450)
    return () => { stale = true; clearTimeout(timer.current) }
  }, [q, open])

  const close = (v: boolean) => {
    onOpenChange(v)
    if (!v) { setQ(""); setResults([]) }
  }

  const add = async (r: ApiSearchResult, lib: ApiLibrary, groupIds?: number[]) => {
    setAdding(r.tmdbId || r.tvdbId || -1)
    try {
      if (r.kind === "movie") {
        const res = await api.addMovie(r.tmdbId, lib.id, groupIds)
        toast.success(`${res.movie.title} added${libraries.length > 1 ? ` to ${lib.name}` : ""} — searching for a release`)
        close(false)
        onAdded("movie", res.movie.id)
      } else {
        const res = await api.addShow(r.tmdbId, lib.id, undefined, r.tvdbId, groupIds)
        toast.success(`${res.show.title} added${libraries.length > 1 ? ` to ${lib.name}` : ""} — searching for releases`)
        close(false)
        onAdded("show", res.show.id)
      }
    } catch (e) { toast.error(`Couldn't add: ${e instanceof Error ? e.message : e}`) } finally { setAdding(0) }
  }

  const openPreview = (r: ApiSearchResult) => {
    close(false)
    // TVDB-sourced show results preview by their TVDB id
    if (r.kind === "show" && r.tvdbId) onOpenPreview(r.kind, r.tvdbId, "tvdb")
    else onOpenPreview(r.kind, r.tmdbId)
  }

  return (
    <Dialog open={open} onOpenChange={close}>
      {/* anchored near the top (not centered) so the frame stays put while
          the result count changes under it */}
      <DialogContent className="top-[8vh] max-w-xl translate-y-0 gap-0 p-0 data-[state=closed]:slide-out-to-top-0 data-[state=open]:slide-in-from-top-0">
        <DialogHeader className="border-b px-5 py-4">
          <DialogTitle className="font-display text-lg font-bold">Add to library</DialogTitle>
          <div className="relative mt-2">
            <Search className="absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-faint" />
            <Input autoFocus value={q} onChange={e => setQ(e.target.value)}
              placeholder="Movie or show title…" className="pl-9" />
          </div>
        </DialogHeader>
        <div className="h-[min(420px,60dvh)] overflow-y-auto px-2 py-2">
          {searching && <div className="mono-label px-3 py-4 text-faint">searching…</div>}
          {!searching && q.trim().length >= 3 && results.length === 0 && (
            <div className="px-3 py-8 text-center text-sm text-muted-foreground">No matches — try a fuller title.</div>
          )}
          {!searching && q.trim().length < 3 && (
            <div className="px-3 py-8 text-center text-sm text-faint">Type at least three characters.</div>
          )}
          {!searching && results.map(r => {
            const ownedLibs = (r.kind === "show" && r.tvdbId
              ? owned.get(`show-tvdb-${r.tvdbId}`) ?? owned.get(`show-${r.tmdbId}`)
              : owned.get(`${r.kind}-${r.tmdbId}`)) ?? new Set<number>()
            const addable = libraries.filter(l =>
              l.kind === (r.kind === "movie" ? "movies" : "shows") && !ownedLibs.has(l.id))
            const preferred = preferredLibrary(addable, defaultLibraryId)
            return (
              <div key={`${r.kind}-${r.tmdbId}-${r.tvdbId ?? 0}`} className="flex items-center gap-3.5 rounded-sm px-3 py-2.5 hover:bg-surface2">
                <button className="flex min-w-0 flex-1 items-center gap-3.5 text-left" onClick={() => openPreview(r)}>
                  <span className="h-[54px] w-9 shrink-0 overflow-hidden rounded-sm bg-surface2">
                    {r.poster && (
                      <img src={img(imageBase, "w92", r.poster)} alt="" loading="lazy"
                        className="h-full w-full object-cover" />
                    )}
                  </span>
                  <span className="min-w-0 flex-1">
                    <span className="flex items-center gap-2 text-[13.5px] font-semibold">
                      <span className="truncate">{r.title}</span>
                      <span className="font-label shrink-0 rounded-sm border px-1.5 text-[9px] uppercase tracking-[0.08em] text-faint">
                        {r.kind === "movie" ? "movie" : "show"}
                      </span>
                    </span>
                    <span className="block truncate text-xs text-muted-foreground">
                      {[r.year || null, r.overview].filter(Boolean).join(" · ")}
                    </span>
                  </span>
                </button>
                {ownedLibs.size > 0 && addable.length === 0
                  // nowhere left to add it, so this says what it IS.
                  // "In library" used to be the only answer here, which
                  // made a title held everywhere and downloaded nowhere
                  // read as ready to watch.
                  ? <span className={cn("mono-label shrink-0", badgeTextClass(held(r)))}>
                      {held(r) ?? "In library"}
                    </span>
                  : !preferred
                    ? <span className="mono-label shrink-0 text-faint">no library</span>
                    : isRequester
                      // a requester asks instead of adding, and is told a
                      // title has already been asked for — never by whom
                      ? requested.has(r.kind, r.tmdbId, r.tvdbId)
                        ? <span className="mono-label shrink-0 text-want">Requested</span>
                        : <LibraryAction compact label="Request"
                            libraries={addable} current={preferred}
                            busy={asking === (r.tmdbId || r.tvdbId || -1)}
                            onRun={(l, a) => void askFor(r, l, a)} />
                      : <LibraryAction compact label="Add"
                          icon={<Plus className="h-3 w-3" />}
                          libraries={addable} current={preferred}
                          busy={adding === (r.tmdbId || r.tvdbId || -1)}
                          onRun={(l, g) => void add(r, l, g)} />}
              </div>
            )
          })}
        </div>
      </DialogContent>
    </Dialog>
  )
}
