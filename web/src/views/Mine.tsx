import { useMemo, useState } from "react"
import { api } from "@/api"
import type { ApiMovie, ApiShow } from "@/api"
import { useApi } from "@/hooks/use-api"
import { RowCard } from "@/components/rows"
import { movieStatus, showStatus } from "@/lib/title-status"
import type { Strip } from "@/lib/title-status"
import { Input } from "@/components/ui/input"
import { Button } from "@/components/ui/button"
import {
  applyFilterRows, FilterRows, uniq, WATCH_FIELDS,
} from "@/components/FilterRows"
import type { LibraryFilter } from "@/components/FilterRows"
import { Filter, X } from "lucide-react"
import { cn } from "@/lib/utils"

// What somebody can actually watch, out on the portal.
//
// Deliberately not the library view. That one carries deleting, grabbing,
// organising and scanning, none of which is served out here — rendering
// it would be a page of buttons that 404. This is the read-only half:
// what you have, and a way into it.
//
// The scoping is the server's. These listings return only what the
// account is entitled to once reely manages their Plex share, so nothing
// here filters — if a title is in the response, they can watch it.
export function MineView({ kind, onOpenPreview }: {
  kind: "movies" | "shows"
  onOpenPreview: (kind: "movie" | "show", tmdbId: number, src?: "tvdb") => void
}) {
  const [q, setQ] = useState("")
  // Searching and filtering are different jobs and the box only ever did
  // the first one. A name you half remember is a search; "comedies from
  // the nineties" is a filter, and no amount of typing in a title box
  // gets you there.
  const [filters, setFilters] = useState<LibraryFilter[]>([])
  const [filtersOpen, setFiltersOpen] = useState(false)
  const active = filters.filter(f => f.value.trim() !== "").length
  const moviesQuery = useApi(() =>
    kind === "movies" ? api.movies() : Promise.resolve({ movies: [], imageBase: "" }))
  const showsQuery = useApi(() =>
    kind === "shows" ? api.shows() : Promise.resolve({ shows: [], imageBase: "" }))

  const imageBase = (kind === "movies" ? moviesQuery.data?.imageBase : showsQuery.data?.imageBase) ?? ""
  const loading = kind === "movies" ? moviesQuery.loading : showsQuery.loading

  // The fields a watcher gets, and what each one reads off a row. Genre
  // and year are the two a person actually browses by; the rest describe
  // the file and where it is filed, which is the owner's concern.
  const fieldOf = (r: ApiMovie | ApiShow, field: string): string[] => {
    switch (field) {
      case "Genre": return r.genres ?? []
      case "Year": return r.year ? [String(r.year)] : []
      default: return []
    }
  }
  const valuesFor = (field: string): string[] => uniq(
    ((kind === "movies"
      ? (moviesQuery.data?.movies ?? [])
      : (showsQuery.data?.shows ?? [])) as (ApiMovie | ApiShow)[])
      .flatMap(r => fieldOf(r, field)))

  const items = useMemo(() => {
    const needle = q.trim().toLowerCase()
    const rows: { key: string; poster: string; title: string; subtitle: string
      strip: Strip; progress?: number; count?: string; open: () => void }[] = []
    if (kind === "movies") {
      const movies = applyFilterRows(
        (moviesQuery.data?.movies ?? []) as ApiMovie[], filters, fieldOf)
      for (const m of movies) {
        if (needle && !m.title.toLowerCase().includes(needle)) continue
        rows.push({
          key: `m${m.id}`, poster: m.poster ?? "", title: m.title,
          subtitle: m.year ? String(m.year) : "",
          ...movieStatus(m),
          open: () => onOpenPreview("movie", m.tmdbId),
        })
      }
    } else {
      const shows = applyFilterRows(
        (showsQuery.data?.shows ?? []) as ApiShow[], filters, fieldOf)
      for (const sh of shows) {
        if (needle && !sh.title.toLowerCase().includes(needle)) continue
        const { strip, progress, frac } = showStatus(sh)
        rows.push({
          key: `s${sh.id}`, poster: sh.poster ?? "", title: sh.title,
          // the count earns its place here: the bar shows the proportion,
          // and this says how many episodes that actually is
          subtitle: [sh.year || null, frac ? `${frac} eps` : null]
            .filter(Boolean).join("  ·  "),
          strip, progress, count: frac,
          // A show is previewed by the source it was STORED from, which
          // with a TVDB key configured is TheTVDB — for search, for
          // adding, and so for what reely holds. Preferring the TMDB id
          // meant the portal described a title by an entry reely never
          // used, and the two do not always mean the same show: an
          // anthology is one continuing series on TheTVDB and a separate
          // entry per instalment on TMDB, so a four-season show opened
          // as a one-season one. The add flow already prefers TVDB;
          // this is the same rule, on the half that had it backwards.
          open: () => sh.tvdbId
            ? onOpenPreview("show", sh.tvdbId, "tvdb")
            : onOpenPreview("show", sh.tmdbId),
        })
      }
    }
    return rows
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kind, q, filters, moviesQuery.data, showsQuery.data, onOpenPreview])

  return (
    <section>
      <div className="mb-4 flex flex-wrap items-center gap-3">
        <h1 className="font-display text-[26px] font-bold tracking-tight">
          {kind === "movies" ? "Movies" : "Shows"}
        </h1>
        <span className="mono-label text-faint">{items.length}</span>
        <Input value={q} onChange={e => setQ(e.target.value)}
          placeholder="Search titles…" className="h-9 w-[200px] max-sm:w-full" />
        <Button variant="outline" size="sm"
          className={cn("h-9 gap-1.5", active > 0 && "border-brass/60 text-brass")}
          onClick={() => {
            setFiltersOpen(v => !v)
            if (filters.length === 0) setFilters([{ field: "Genre", op: "is", value: "" }])
          }}>
          <Filter className="h-3.5 w-3.5" />
          Filter{active > 0 ? ` (${active})` : ""}
        </Button>
        {active > 0 && (
          <button onClick={() => { setFilters([]); setFiltersOpen(false) }}
            className="flex items-center gap-1 text-[12px] text-muted-foreground hover:text-foreground">
            <X className="h-3 w-3" /> clear
          </button>
        )}
      </div>

      {filtersOpen && (
        <div className="mb-4 rounded-xl border border-linesoft bg-surface p-3">
          <FilterRows rows={filters} onChange={setFilters}
            fields={WATCH_FIELDS} valuesFor={valuesFor} />
        </div>
      )}

      {loading && items.length === 0 && (
        <p className="text-[13px] text-muted-foreground">Loading…</p>
      )}
      {!loading && items.length === 0 && (
        <p className="text-[13px] text-muted-foreground">
          {q
            ? "Nothing here matches that."
            : "Nothing yet — anything you ask for shows up here once it arrives."}
        </p>
      )}

      {/* fill matters: RowCard carries the fixed width the scrolling rows
          need, and in a grid that leaves every poster hugging the left of
          a wider cell instead of sitting in it. */}
      <div className="grid grid-cols-[repeat(auto-fill,minmax(118px,1fr))] gap-x-3.5 gap-y-5">
        {items.map(it => (
          <RowCard key={it.key} imageBase={imageBase} poster={it.poster}
            title={it.title} subtitle={it.subtitle}
            strip={it.strip} progress={it.progress} count={it.count}
            fill onOpen={it.open} />
        ))}
      </div>
    </section>
  )
}
