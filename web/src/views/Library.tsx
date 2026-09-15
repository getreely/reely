import { useMemo, useState } from "react"
import { api } from "@/api"
import { movieStatus, showStatus } from "@/lib/title-status"
import type { ApiLibrary, ApiMovie, ApiOrganizeMove, ApiShow } from "@/api"
import { useApi } from "@/hooks/use-api"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { SelectionBar, DeleteTitleDialog, type DeleteMode } from "@/components/SelectionBar"
import type { Selected } from "@/components/SelectionBar"
import { ReleaseDialog } from "@/components/Releases"
import { RematchDialog } from "@/components/RematchDialog"
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog"
import { applyFilterRows, FilterRows, MOVIE_FIELDS, SHOW_FIELDS, uniq } from "@/components/FilterRows"
import type { LibraryFilter } from "@/components/FilterRows"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { ArrowDown, ArrowUp, Filter, FolderTree, MoreVertical, Pencil, RefreshCw, Search, Trash2, Tv, X, Zap } from "lucide-react"
import { toast } from "sonner"
import { toastSearchResult } from "@/lib/search-toast"
import { cn, img } from "@/lib/utils"
import { useIsAdmin } from "@/lib/access"

// The library view shows what's on disk — real stored titles with their
// on-disk state — and doubles as TMDB lookup when you search. With a library
// passed it narrows to that one collection; the full lists still load so the
// search results know which libraries already hold a title.
type SortKey = "added" | "title" | "year" | "size" | "quality"

const SORT_LABELS: Record<SortKey, string> = {
  added: "Added", title: "Title", year: "Year", size: "Size", quality: "Quality",
}
// each sort's natural first direction: dates and sizes big/new first, text A→Z
const SORT_DEFAULT_ASC: Record<SortKey, boolean> = {
  added: false, title: true, year: false, size: false, quality: false,
}
const QUALITY_RANK: Record<string, number> = { "480p": 1, "720p": 2, "1080p": 3, "2160p": 4 }

export function LibraryView({ kind, library, onOpenMovie, onOpenShow }: {
  kind: "movies" | "shows"
  library?: ApiLibrary
  onOpenMovie: (m: ApiMovie) => void
  onOpenShow: (s: ApiShow) => void
}) {
  const isAdmin = useIsAdmin()
  const libsQuery = useApi(() => api.libraries())
  // Households, for the "Shared with" filter. Only the owner has them —
  // the list is the shape of every household on the install.
  const groupsQuery = useApi(() => (isAdmin ? api.groups() : Promise.resolve([])))
  const groupName = useMemo(() => {
    const m = new Map<number, string>()
    for (const g of groupsQuery.data ?? []) m.set(g.id, g.name)
    return m
  }, [groupsQuery.data])
  const sharedWith = (ids?: number[]) =>
    (ids ?? []).map(id => groupName.get(id) ?? "").filter(Boolean)
  const libraries = useMemo(
    () => (libsQuery.data?.libraries ?? []).filter(l => l.kind === kind),
    [libsQuery.data, kind])

  const moviesQuery = useApi(() => api.movies(), kind === "movies" ? 5_000 : undefined)
  const showsQuery = useApi(() => api.shows(), kind === "shows" ? 5_000 : undefined)
  const allMovies = kind === "movies" ? (moviesQuery.data?.movies ?? []) : []
  const allShows = kind === "shows" ? (showsQuery.data?.shows ?? []) : []
  // the combined view shows each TITLE once, even when it lives in several
  // collections — the card is the copy that's furthest along (on disk wins)
  const movies = library
    ? allMovies.filter(m => m.libraryId === library.id)
    : dedupeByTmdb(allMovies, (a, b) => !!a.filePath && !b.filePath)
  const shows = library
    ? allShows.filter(s => s.libraryId === library.id)
    : dedupeByTmdb(allShows, (a, b) => a.onDisk > b.onDisk)
  const imageBase = (kind === "movies" ? moviesQuery.data?.imageBase : showsQuery.data?.imageBase) ?? ""
  const loading = kind === "movies" ? moviesQuery.loading : showsQuery.loading

  // one pick puts the grid in select mode; the bar at the bottom acts on
  // everything picked. Keyed by row id, which is unique across a kind.
  const [selection, setSelection] = useState<Map<number, Selected>>(new Map())
  const toggleSelect = (item: Selected) => {
    const id = item.kind === "movie" ? item.movie.id : item.show.id
    setSelection(prev => {
      const next = new Map(prev)
      if (next.has(id)) next.delete(id)
      else next.set(id, item)
      return next
    })
  }

  // the poster's 3-dot menu: search or delete a title without opening it
  const [menuSearch, setMenuSearch] = useState<{ kind: "movie" | "show"; id: number; title: string } | null>(null)
  const [menuDelete, setMenuDelete] = useState<{ kind: "movie" | "show"; id: number; title: string } | null>(null)
  // re-matching from the grid, so correcting a wrong match does not mean
  // opening the title that is wrong to find out what it should be
  const [menuRematch, setMenuRematch] = useState<{ kind: "movie" | "show"; id: number; title: string } | null>(null)
  const refreshTitle = async (kind: "movie" | "show", id: number, title: string, source?: string) => {
    try {
      await api.refreshTitle(kind, id)
      toast.success(`Refreshed ${title} from ${source === "tvdb" ? "TheTVDB" : "TMDB"}`)
      if (kind === "movie") moviesQuery.reload({ quiet: true })
      else showsQuery.reload({ quiet: true })
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }
  const autoSearch = async (k: "movie" | "show", id: number) => {
    try {
      toastSearchResult(k === "movie" ? await api.searchMovieNow(id) : await api.searchShowNow(id))
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }
  const [sweeping, setSweeping] = useState(false)
  // organize is preview-first: nothing moves until the list has been read
  const [organizing, setOrganizing] = useState<
    { lib: ApiLibrary; moves: ApiOrganizeMove[]; emptied: string[]; leftover: string[] } | null>(null)
  const [organizeBusy, setOrganizeBusy] = useState(false)
  const previewOrganize = async (lib: ApiLibrary) => {
    setOrganizeBusy(true)
    try {
      const r = await api.organizePreview(lib.id)
      setOrganizing({ lib, moves: r.moves ?? [], emptied: r.emptied ?? [], leftover: r.leftover ?? [] })
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setOrganizeBusy(false) }
  }
  const applyOrganize = async () => {
    if (!organizing) return
    setOrganizeBusy(true)
    try {
      const r = await api.organizeApply(organizing.lib.id)
      const errs = r.errors ?? []
      if (errs.length > 0) toast.error(`Moved ${r.moved} of ${r.planned} — ${errs[0]}`)
      else {
        const gone = (r.removed ?? []).length
        const kept = (r.kept ?? []).length
        toast.success(`Moved ${r.moved} file${r.moved === 1 ? "" : "s"}` +
          (gone > 0 ? `, cleaned up ${gone} folder${gone === 1 ? "" : "s"}` : "") +
          // the ones still standing are the only ones anybody has to
          // look at, and a library of hundreds is not worth walking to
          // find them
          (kept > 0 ? ` · ${kept} kept, still holding other files` : ""))
      }
      setOrganizing(null)
      moviesQuery.reload({ quiet: true })
      showsQuery.reload({ quiet: true })
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setOrganizeBusy(false) }
  }
  const searchMissing = async () => {
    setSweeping(true)
    try {
      const r = await api.searchMissing(kind, library?.id ?? 0)
      toast.success(r.queued === 0
        ? "Nothing missing — everything monitored is on disk"
        : `Queued ${r.queued} search${r.queued === 1 ? "" : "es"} — watch Activity`)
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setSweeping(false) }
  }
  const removeTitle = async (mode: DeleteMode) => {
    if (!menuDelete) return
    try {
      if (mode === "filesOnly") {
        if (menuDelete.kind === "movie") await api.deleteMovieFile(menuDelete.id)
        else await api.deleteShowFiles(menuDelete.id)
        toast(`${menuDelete.title}: files deleted — staying in the library`)
      } else {
        if (menuDelete.kind === "movie") await api.deleteMovie(menuDelete.id, mode === "files")
        else await api.deleteShow(menuDelete.id, mode === "files")
        toast(`${menuDelete.title} removed`)
      }
      moviesQuery.reload({ quiet: true })
      showsQuery.reload({ quiet: true })
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }

  // the library's own search + filters + sort — all client-side, narrowing
  // the library live as you type
  const [searchQ, setSearchQ] = useState("")
  const [filters, setFilters] = useState<LibraryFilter[]>([])
  const [sort, setSort] = useState<SortKey>("added")
  const [sortAsc, setSortAsc] = useState(SORT_DEFAULT_ASC.added)
  const pickSort = (v: SortKey) => {
    if (sort === v) setSortAsc(a => !a)
    else { setSort(v); setSortAsc(SORT_DEFAULT_ASC[v]) }
  }
  const active = filters.filter(f => f.value.trim() !== "")
  const libName = (id: number) => libraries.find(l => l.id === id)?.name ?? ""

  const movieStatus = (m: ApiMovie) => (m.filePath ? "On disk" : m.monitored ? "Missing" : "Unmonitored")
  // wanted = monitored, aired, no file: a show mid-season isn't missing
  // what hasn't aired, and an un-monitored gap is a choice, not a shortfall
  const showStatus = (s: ApiShow) =>
    s.aired > 0 && s.wanted === 0 ? "Complete" : s.monitored ? "Missing episodes" : "Unmonitored"
  const movieField = (m: ApiMovie, field: string): string[] => {
    switch (field) {
      case "Genre": return m.genres ?? []
      case "Year": return m.year ? [String(m.year)] : []
      case "Quality": return m.filePath && m.quality ? [m.quality] : []
      case "Source": return m.source ? [m.source] : []
      case "Status": return [movieStatus(m)]
      case "Library": return libName(m.libraryId) ? [libName(m.libraryId)] : []
      case "Shared with": return sharedWith(m.groupIds)
      default: return []
    }
  }
  const showField = (sh: ApiShow, field: string): string[] => {
    switch (field) {
      case "Genre": return sh.genres ?? []
      case "Year": return sh.year ? [String(sh.year)] : []
      case "Status": return [showStatus(sh)]
      case "Library": return libName(sh.libraryId) ? [libName(sh.libraryId)] : []
      case "Shared with": return sharedWith(sh.groupIds)
      default: return []
    }
  }
  // "Shared with" is the owner's: a requester has no group list to
  // filter by, and it is not theirs to see.
  const filterFields = useMemo(() => {
    const base = kind === "movies" ? MOVIE_FIELDS : SHOW_FIELDS
    return isAdmin ? [...base, "Shared with"] : [...base]
  }, [kind, isAdmin])
  const valuesFor = (field: string): string[] => {
    if (field === "Status") return kind === "movies"
      ? ["On disk", "Missing", "Unmonitored"]
      : ["Complete", "Missing episodes", "Unmonitored"]
    if (field === "Library") return uniq(libraries.map(l => l.name))
    if (field === "Shared with") return uniq((groupsQuery.data ?? []).map(g => g.name))
    const rows = kind === "movies" ? movies : shows
    const of = kind === "movies" ? movieField : (showField as (r: ApiMovie | ApiShow, f: string) => string[])
    return uniq(rows.flatMap(r => of(r as ApiMovie & ApiShow, field)))
  }

  function applyFilters<T>(list: T[], fieldOf: (row: T, field: string) => string[]): T[] {
    const q = searchQ.trim().toLowerCase()
    if (q) list = list.filter(r => (r as { title: string }).title.toLowerCase().includes(q))
    return applyFilterRows(list, active, fieldOf)
  }
  const dir = sortAsc ? 1 : -1
  const visibleMovies = useMemo(() => {
    const cmp: Record<SortKey, (a: ApiMovie, b: ApiMovie) => number> = {
      added: (a, b) => a.addedAt.localeCompare(b.addedAt) || a.id - b.id,
      title: (a, b) => a.title.localeCompare(b.title),
      year: (a, b) => a.year - b.year,
      size: (a, b) => a.fileSize - b.fileSize,
      quality: (a, b) => (QUALITY_RANK[a.quality] ?? 0) - (QUALITY_RANK[b.quality] ?? 0),
    }
    return [...applyFilters(movies, movieField)].sort((a, b) => dir * cmp[sort](a, b))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [movies, searchQ, filters, sort, sortAsc, libraries])
  const visibleShows = useMemo(() => {
    const cmp: Record<SortKey, (a: ApiShow, b: ApiShow) => number> = {
      added: (a, b) => a.addedAt.localeCompare(b.addedAt) || a.id - b.id,
      title: (a, b) => a.title.localeCompare(b.title),
      year: (a, b) => a.year - b.year,
      size: (a, b) => a.onDisk - b.onDisk,
      quality: (a, b) => a.onDisk / Math.max(1, a.aired) - b.onDisk / Math.max(1, b.aired),
    }
    return [...applyFilters(shows, showField)].sort((a, b) => dir * cmp[sort](a, b))
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [shows, searchQ, filters, sort, sortAsc, libraries])

  const scan = async (libraryId: number, libraryName: string) => {
    try {
      await api.scanLibrary(libraryId)
      toast.success(`Scanning ${libraryName} — the library fills as titles match`)
    } catch (err) {
      toast.error(`${err instanceof Error ? err.message : err}`)
    }
  }

  const title = library ? library.name : kind === "movies" ? "Movies" : "Shows"
  const count = kind === "movies" ? movies.length : shows.length
  const filesOnDisk = kind === "movies"
    ? movies.filter(m => m.filePath).length
    : shows.reduce((n, s) => n + s.onDisk, 0)
  // exactly what the sweep will queue: a monitored movie with no file, or
  // a monitored, aired, missing episode of a monitored show. aired-onDisk
  // is NOT that number — it counts unmonitored episodes inside monitored
  // shows, which the sweep skips; `wanted` is the per-show count built on
  // the sweep's own rule.
  const missingCount = kind === "movies"
    ? movies.filter(m => m.monitored && !m.filePath).length
    : shows.reduce((n, s) => n + (s.monitored ? s.wanted : 0), 0)

  return (
    <section>
      <div className="mb-5 flex flex-wrap items-end justify-between gap-3">
        <div>
          <h1 className="font-display text-[28px] font-bold tracking-tight">{title}</h1>
          <p className="mono-label mt-0.5 text-faint">
            {loading ? "loading…" : `${count} ${kind === "movies" ? "movie" : "show"}${count === 1 ? "" : "s"} · ${filesOnDisk} on disk`}
          </p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          {/* the library's own search — narrows the grid live, no network */}
          <div className="relative w-full max-w-[210px]">
            <Search className="absolute left-3 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-faint" />
            <Input value={searchQ} onChange={e => setSearchQ(e.target.value)}
              placeholder={`Search ${title.toLowerCase()}…`} className="h-9 pl-9 pr-7 text-[13px]" />
            {searchQ && (
              <button className="absolute right-2 top-1/2 -translate-y-1/2 text-faint hover:text-foreground"
                aria-label="Clear search" onClick={() => setSearchQ("")}>
                <X className="h-3.5 w-3.5" />
              </button>
            )}
          </div>
          <Popover>
            <PopoverTrigger asChild>
              <Button variant="outline" className={cn("h-9 gap-1.5 text-[13px]", active.length > 0 && "border-brass text-brass")}>
                <Filter className="h-3.5 w-3.5" />
                {active.length > 0 ? `Filters · ${active.length}` : "Filter"}
              </Button>
            </PopoverTrigger>
            <PopoverContent align="end" className="w-[430px] max-w-[95vw] rounded-xl p-3">
              <FilterRows rows={filters} onChange={setFilters}
                fields={filterFields} valuesFor={valuesFor} />
              {filters.length > 0 && (
                <button className="mono-label mt-2 text-faint hover:text-brass" onClick={() => setFilters([])}>
                  clear all
                </button>
              )}
            </PopoverContent>
          </Popover>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" className="h-9 gap-1.5 text-[13px]">
                {sortAsc ? <ArrowUp className="h-3.5 w-3.5" /> : <ArrowDown className="h-3.5 w-3.5" />}
                {SORT_LABELS[sort]}
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end" className="rounded-xl">
              <DropdownMenuLabel className="mono-label text-faint">Sort by</DropdownMenuLabel>
              {(Object.keys(SORT_LABELS) as SortKey[]).map(k => (
                <DropdownMenuItem key={k} onClick={() => pickSort(k)}>
                  <span className="flex-1">{SORT_LABELS[k]}</span>
                  {sort === k && (sortAsc ? <ArrowUp className="h-3.5 w-3.5" /> : <ArrowDown className="h-3.5 w-3.5" />)}
                </DropdownMenuItem>
              ))}
            </DropdownMenuContent>
          </DropdownMenu>
          {/* what select-all-then-search did, in one click. Counts what's
              actually findable, so it never promises to search unaired
              episodes. */}
          {missingCount > 0 && (
            <Button variant="outline" className="h-9 gap-1.5 text-[13px]"
              disabled={sweeping} onClick={() => void searchMissing()}>
              <Zap className="h-3.5 w-3.5" />
              Search missing · {missingCount}
            </Button>
          )}
          {isAdmin && libraries.length > 0 && (
            <>
              <OrganizeButton libraries={libraries} busy={organizeBusy} onPick={previewOrganize} />
              <ScanButton libraries={libraries} onScan={scan} />
            </>
          )}
        </div>
      </div>

      {/* library grid */}
          {!loading && count > 0 && visibleMovies.length + visibleShows.length === 0 && (
            <p className="mono-label py-4 text-faint">nothing matches the search or filters</p>
          )}
          {!loading && count === 0 && (
            <div className="max-w-[52ch] text-[13.5px] leading-relaxed text-muted-foreground">
              <p className="font-display mb-2 text-xl font-bold text-foreground">The screening room is empty.</p>
              <p>
                {libraries.length === 0
                  ? `An admin can create a ${kind === "movies" ? "movie" : "show"} library in Settings, then scan it to fill the library.`
                  : `Point a library at your ${kind === "movies" ? "movie" : "show"} folder and hit Scan — matched titles land here with their posters.`}
              </p>
            </div>
          )}
          <PosterGrid>
            {kind === "movies" && visibleMovies.map(m => (
              <MovieCell key={m.id} movie={m} imageBase={imageBase} onOpen={() => onOpenMovie(m)}
                selected={selection.has(m.id)} selectionActive={selection.size > 0}
                onToggleSelect={() => toggleSelect({ kind: "movie", movie: m })}
                menu={<TitleMenu title={m.title}
                  onAuto={() => void autoSearch("movie", m.id)}
                  onManual={() => setMenuSearch({ kind: "movie", id: m.id, title: m.title })}
                  onRefresh={() => void refreshTitle("movie", m.id, m.title)}
                  onRematch={() => setMenuRematch({ kind: "movie", id: m.id, title: m.title })}
                  onDelete={() => setMenuDelete({ kind: "movie", id: m.id, title: m.title })} />} />
            ))}
            {kind === "shows" && visibleShows.map(s => (
              <ShowCell key={s.id} show={s} imageBase={imageBase} onOpen={() => onOpenShow(s)}
                selected={selection.has(s.id)} selectionActive={selection.size > 0}
                onToggleSelect={() => toggleSelect({ kind: "show", show: s })}
                menu={<TitleMenu title={s.title}
                  onAuto={() => void autoSearch("show", s.id)}
                  onManual={() => setMenuSearch({ kind: "show", id: s.id, title: s.title })}
                  onRefresh={() => void refreshTitle("show", s.id, s.title, s.source)}
                  onRematch={() => setMenuRematch({ kind: "show", id: s.id, title: s.title })}
                  onDelete={() => setMenuDelete({ kind: "show", id: s.id, title: s.title })} />} />
            ))}
          </PosterGrid>
      <SelectionBar items={[...selection.values()]}
        onClear={() => setSelection(new Map())}
        onChanged={() => { moviesQuery.reload({ quiet: true }); showsQuery.reload({ quiet: true }) }} />
      {menuSearch && (
        <ReleaseDialog open onOpenChange={v => { if (!v) setMenuSearch(null) }}
          title={`Releases — ${menuSearch.title}`}
          fetchReleases={() => menuSearch.kind === "movie"
            ? api.movieReleases(menuSearch.id) : api.showReleases(menuSearch.id, 0)}
          onGrab={r => menuSearch.kind === "movie"
            ? api.grabMovie(menuSearch.id, r) : api.grabShow(menuSearch.id, r, 0)} />
      )}
      {organizing && (
        <OrganizeDialog plan={organizing} busy={organizeBusy}
          onCancel={() => setOrganizing(null)} onApply={() => void applyOrganize()} />
      )}
      {menuRematch && (
        <RematchDialog open onOpenChange={v => { if (!v) setMenuRematch(null) }}
          kind={menuRematch.kind} id={menuRematch.id} title={menuRematch.title}
          onDone={() => {
            if (menuRematch.kind === "movie") moviesQuery.reload({ quiet: true })
            else showsQuery.reload({ quiet: true })
          }} />
      )}
      <DeleteTitleDialog open={menuDelete !== null} onOpenChange={v => { if (!v) setMenuDelete(null) }}
        what={`"${menuDelete?.title ?? ""}"`}
        onConfirm={mode => void removeTitle(mode)} />
    </section>
  )
}

// TitleMenu is the poster's 3-dot menu — the detail page's search,
// re-match and delete actions without leaving the grid.
function TitleMenu({ title, onAuto, onManual, onRefresh, onRematch, onDelete }: {
  title: string
  onAuto: () => void
  onManual: () => void
  onRefresh: () => void
  onRematch: () => void
  onDelete: () => void
}) {
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <span role="button" tabIndex={0} aria-label={`Actions — ${title}`}
          className="flex h-6 w-6 items-center justify-center rounded-md border border-white/50 bg-black/45 text-white/90 backdrop-blur-xs hover:border-white">
          <MoreVertical className="h-3.5 w-3.5" />
        </span>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="rounded-xl">
        <DropdownMenuItem onClick={onAuto}>
          <Zap className="mr-1.5 h-3.5 w-3.5" /> Auto search
        </DropdownMenuItem>
        <DropdownMenuItem onClick={onManual}>
          <Search className="mr-1.5 h-3.5 w-3.5" /> Manual search
        </DropdownMenuItem>
        <DropdownMenuItem onClick={onRefresh}>
          <RefreshCw className="mr-1.5 h-3.5 w-3.5" /> Refresh metadata
        </DropdownMenuItem>
        <DropdownMenuItem onClick={onRematch}>
          <Pencil className="mr-1.5 h-3.5 w-3.5" /> Re-match…
        </DropdownMenuItem>
        <DropdownMenuItem onClick={onDelete} className="text-want focus:text-want">
          <Trash2 className="mr-1.5 h-3.5 w-3.5" /> Delete…
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

// OrganizeButton mirrors ScanButton: one library goes straight through,
// several ask which.
function OrganizeButton({ libraries, busy, onPick }: {
  libraries: ApiLibrary[]
  busy: boolean
  onPick: (lib: ApiLibrary) => void
}) {
  if (libraries.length === 1) {
    return (
      <Button variant="outline" className="h-9" disabled={busy} onClick={() => onPick(libraries[0])}>
        <FolderTree className="mr-1.5 h-3.5 w-3.5" /> Organize
      </Button>
    )
  }
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" className="h-9" disabled={busy}>
          <FolderTree className="mr-1.5 h-3.5 w-3.5" /> Organize
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="rounded-xl">
        <DropdownMenuLabel className="mono-label text-faint">Organize library</DropdownMenuLabel>
        {libraries.map(l => (
          <DropdownMenuItem key={l.id} onClick={() => onPick(l)}>{l.name}</DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

// OrganizeDialog is the half of the feature that earns its keep: every
// move is shown before any of them happens, so moving files is something
// the person reading agreed to rather than something the button did.
function OrganizeDialog({ plan, busy, onCancel, onApply }: {
  plan: { lib: ApiLibrary; moves: ApiOrganizeMove[]; emptied: string[]; leftover: string[] }
  busy: boolean
  onCancel: () => void
  onApply: () => void
}) {
  const strip = (p: string) => p.startsWith(plan.lib.path) ? p.slice(plan.lib.path.length).replace(/^\/+/, "") : p
  return (
    <Dialog open onOpenChange={o => { if (!o) onCancel() }}>
      <DialogContent className="max-h-[85vh] max-w-[760px] overflow-hidden rounded-xl">
        <DialogHeader>
          <DialogTitle className="font-display">Organize {plan.lib.name}</DialogTitle>
        </DialogHeader>
        {plan.moves.length === 0 ? (
          <p className="py-2 text-[13.5px] text-muted-foreground">
            Every file is already where the naming template says it belongs. Nothing to move.
          </p>
        ) : (
          <>
            <p className="mono-label text-faint">
              {plan.moves.length} file{plan.moves.length === 1 ? "" : "s"} to move
              {plan.emptied.length > 0 && ` · ${plan.emptied.length} folder${plan.emptied.length === 1 ? "" : "s"} cleaned up`}
            </p>
            {plan.leftover.length > 0 && (
              <details className="mt-1">
                <summary className="mono-label cursor-pointer text-want">
                  {plan.leftover.length} folder{plan.leftover.length === 1 ? "" : "s"} will stay — something else is in {plan.leftover.length === 1 ? "it" : "them"}
                </summary>
                <div className="mt-1 max-h-[14vh] overflow-y-auto pr-1">
                  {plan.leftover.map(d => (
                    <div key={d} className="mono-label truncate py-0.5 text-faint" title={d}>{strip(d)}</div>
                  ))}
                </div>
              </details>
            )}
            <div className="max-h-[46vh] overflow-y-auto pr-1">
              {plan.moves.map(m => (
                <div key={m.from} className="border-b border-linesoft py-2">
                  <div className="text-[13px] font-semibold">{m.title}</div>
                  <div className="font-label mt-0.5 truncate text-[11px] text-want" title={m.from}>
                    {strip(m.from)}
                  </div>
                  <div className="font-label truncate text-[11px] text-good" title={m.to}>
                    → {strip(m.to)}
                  </div>
                </div>
              ))}
            </div>
          </>
        )}
        <DialogFooter>
          <Button variant="outline" onClick={onCancel}>Cancel</Button>
          {plan.moves.length > 0 && (
            <Button disabled={busy} onClick={onApply}>
              {busy ? "Moving…" : `Move ${plan.moves.length} file${plan.moves.length === 1 ? "" : "s"}`}
            </Button>
          )}
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

function ScanButton({ libraries, onScan }: {
  libraries: { id: number; name: string }[]
  onScan: (id: number, name: string) => void
}) {
  if (libraries.length === 1) {
    return (
      <Button variant="outline" className="h-9" onClick={() => onScan(libraries[0].id, libraries[0].name)}>
        <RefreshCw className="mr-1.5 h-3.5 w-3.5" /> Scan
      </Button>
    )
  }
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="outline" className="h-9">
          <RefreshCw className="mr-1.5 h-3.5 w-3.5" /> Scan
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="rounded-xl">
        <DropdownMenuLabel className="mono-label text-faint">Scan library</DropdownMenuLabel>
        {libraries.map(l => (
          <DropdownMenuItem key={l.id} onClick={() => onScan(l.id, l.name)}>{l.name}</DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

// dedupeByTmdb keeps one row per TMDB id, replacing it when a later row is
// better (the caller says what better means). Rows without a TMDB id pass
// through untouched.
function dedupeByTmdb<T extends { tmdbId: number }>(items: T[], better: (a: T, b: T) => boolean): T[] {
  const at = new Map<number, number>()
  const out: T[] = []
  for (const it of items) {
    if (!it.tmdbId) { out.push(it); continue }
    const i = at.get(it.tmdbId)
    if (i === undefined) {
      at.set(it.tmdbId, out.length)
      out.push(it)
    } else if (better(it, out[i])) {
      out[i] = it
    }
  }
  return out
}

function PosterGrid({ children }: { children: React.ReactNode }) {
  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(118px,1fr))] gap-x-3.5 gap-y-5">
      {children}
    </div>
  )
}

// The status strip lives on the poster's bottom edge: green = on the
// disk, amber = wanted, grey = unmonitored. For shows it becomes a
// progress bar — green for what's on disk over an amber track for what's
// missing — so a glance reads 8/10 without covering any artwork. With
// selection wired, a check lives top-left: hover
// reveals the check, one pick puts the whole grid in select mode.
function Poster({ imageBase, poster, title, subtitle, strip, progress, badge, count, onOpen, selected, selectionActive, onToggleSelect, menu }: {
  imageBase: string; poster: string; title: string; subtitle: string
  strip?: "good" | "want" | "dim"; progress?: number; badge?: string; count?: string; onOpen?: () => void
  selected?: boolean; selectionActive?: boolean; onToggleSelect?: () => void
  menu?: React.ReactNode
}) {
  const Cell = onOpen ? "button" : "div"
  const click = selectionActive && onToggleSelect ? onToggleSelect : onOpen
  return (
    <Cell onClick={click} className={cn("group/cell block w-full text-left", onOpen && "group cursor-pointer")}>
      <div className={cn(
        "relative aspect-2/3 overflow-hidden rounded-[10px] bg-surface2 poster-shadow transition-transform group-hover:scale-[1.025]",
        selected && "ring-2 ring-brass ring-offset-2 ring-offset-background"
      )}>
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
          <span className="font-label absolute bottom-1.5 right-1.5 rounded-sm bg-black/75 px-1.5 py-0.5 text-[8.5px] font-bold tracking-[0.07em] text-white">{badge}</span>
        )}
        {onToggleSelect && (
          <span role="button" tabIndex={0} aria-label={selected ? `Deselect ${title}` : `Select ${title}`}
            onClick={e => { e.stopPropagation(); onToggleSelect() }}
            onKeyDown={e => { if (e.key === "Enter") { e.stopPropagation(); onToggleSelect() } }}
            className={cn(
              "absolute left-1.5 top-1.5 z-10 flex h-6 w-6 items-center justify-center rounded-md border transition-opacity",
              selected
                ? "border-brass bg-brass text-brass-ink opacity-100"
                // touch has no hover to reveal it — stay visible there, or
                // multi-select is unreachable from a phone
                : "border-white/50 bg-black/45 text-transparent opacity-0 backdrop-blur-xs hover:border-white group-hover/cell:opacity-100 [@media(hover:none)]:opacity-100",
              selectionActive && "opacity-100"
            )}>
            <svg viewBox="0 0 24 24" width="13" height="13" style={{ stroke: "currentColor", fill: "none", strokeWidth: 3 }}><path d="M5 13l4 4 10-11" /></svg>
          </span>
        )}
        {menu && !selectionActive && (
          // the menu's clicks (trigger and portaled items alike) must not
          // bubble into the cell and open the title
          <span onClick={e => e.stopPropagation()} onKeyDown={e => e.stopPropagation()}
            className="absolute right-1.5 top-1.5 z-10 opacity-0 transition-opacity focus-within:opacity-100 group-hover/cell:opacity-100 has-[[data-state=open]]:opacity-100 [@media(hover:none)]:opacity-100">
            {menu}
          </span>
        )}
      </div>
      <div className={cn("mt-1.5 text-[12.5px] font-semibold leading-tight", onOpen && "group-hover:text-brass")}>{title}</div>
      <div className="text-[11.5px] text-muted-foreground">{subtitle}</div>
    </Cell>
  )
}

function MovieCell({ movie, imageBase, onOpen, selected, selectionActive, onToggleSelect, menu }: {
  movie: ApiMovie; imageBase: string; onOpen: () => void
  selected: boolean; selectionActive: boolean; onToggleSelect: () => void
  menu?: React.ReactNode
}) {
  const { strip } = movieStatus(movie)
  // The badge describes the FILE, so it goes when the file does. A title
  // whose file was deleted kept reading "720P", which says the opposite
  // of what the strip beside it says.
  const badge = movie.filePath && movie.quality ? movie.quality.toUpperCase() : undefined
  return <Poster imageBase={imageBase} poster={movie.poster} title={movie.title}
    subtitle={String(movie.year || "—")} strip={strip}
    badge={badge} onOpen={onOpen}
    selected={selected} selectionActive={selectionActive} onToggleSelect={onToggleSelect} menu={menu} />
}

function ShowCell({ show, imageBase, onOpen, selected, selectionActive, onToggleSelect, menu }: {
  show: ApiShow; imageBase: string; onOpen: () => void
  selected: boolean; selectionActive: boolean; onToggleSelect: () => void
  menu?: React.ReactNode
}) {
  const { strip, progress, frac } = showStatus(show)
  return <Poster imageBase={imageBase} poster={show.poster} title={show.title}
    subtitle={frac ? `${frac} eps` : "no episodes yet"} strip={strip} progress={progress}
    count={frac} onOpen={onOpen}
    selected={selected} selectionActive={selectionActive} onToggleSelect={onToggleSelect} menu={menu} />
}
