import { useMemo, useState } from "react"
import { api } from "@/api"
import type { ApiLibrary } from "@/api"
import { useApi } from "@/hooks/use-api"
import { CastRow, DetailHero, Tag, fmtRuntime } from "@/components/detail"
import { SimilarRow } from "@/components/rows"
import { Switch } from "@/components/ui/switch"
import { ChevronDown, Plus } from "lucide-react"
import { toast } from "sonner"
import { cn } from "@/lib/utils"
import { Overview } from "@/components/Overview"
import { useAccess, useIsRequester } from "@/lib/access"
import { LibraryAction, preferredLibrary } from "@/components/LibraryAction"
import { useRequested } from "@/hooks/use-requests"

// PreviewView is the look-before-you-add page: full TMDB detail for a
// title that may not be in any library yet. It draws like a title you
// already own — same cast row, same collapsible seasons, same episode
// rows — with one difference that earns itself: each season's switch
// chooses what starts monitored when you add, rather than reporting what
// is monitored now.
export function PreviewView({ kind, tmdbId, src, onBack, onAdded, onOpenPerson, onOpenPreview }: {
  kind: "movie" | "show"
  tmdbId: number
  // "tvdb": tmdbId is actually a TVDB series id (TVDB-sourced search result)
  src?: "tvdb"
  onBack: () => void
  onAdded: (kind: "movie" | "show", id: number) => void
  onOpenPerson?: (tmdbId: number) => void
  onOpenPreview?: (kind: "movie" | "show", tmdbId: number) => void
}) {
  const { data, error, loading } = useApi(() => api.preview(kind, tmdbId, src))
  const libsQuery = useApi(() => api.libraries())
  const [adding, setAdding] = useState(false)
  // a requester asks rather than adds — the add endpoints refuse them,
  // and out on the portal they are not served to anybody
  const isRequester = useIsRequester()
  const { defaultLibraryId } = useAccess()
  const requested = useRequested(isRequester)
  const [asking, setAsking] = useState(false)
  // seasons switched off start unmonitored; everything starts on
  const [unpicked, setUnpicked] = useState<Set<number>>(new Set())
  // seasons collapse the same way they do on a title you already own
  const [openSeasons, setOpenSeasons] = useState<Record<number, boolean>>({})

  const libraries = useMemo(
    () => (libsQuery.data?.libraries ?? []).filter(l => l.kind === (kind === "movie" ? "movies" : "shows")),
    [libsQuery.data, kind])
  const inLibs = new Set(data?.inLibraries ?? [])
  const addable = libraries.filter(l => !inLibs.has(l.id))
  // a plain click uses this account's chosen library; the picker beside
  // the button covers the times that isn't what they meant
  const preferred = preferredLibrary(addable, defaultLibraryId)

  // Which seasons a show is being asked for, or added with: the ones
  // whose switch is still on. Undefined means all of them, which is what
  // an untouched page means.
  const pickedSeasons = () => {
    if (kind === "movie" || unpicked.size === 0) return undefined
    return (data?.preview.seasons ?? []).map(se => se.number).filter(n => !unpicked.has(n))
  }

  // Asking is one call. It answers 409 when somebody already asked, which
  // reads as "Requested" rather than as a failure.
  //
  // It carries the seasons for the same reason adding does: the switches
  // are right there on the page, and honouring them on one path and not
  // the other means somebody turns three seasons off, asks, and gets the
  // whole show.
  const askFor = async (lib: ApiLibrary, audience?: number[]) => {
    if (!data) return
    const p = data.preview
    setAsking(true)
    try {
      const { status } = await api.createRequest({
        kind: p.kind, tmdbId: p.tmdbId, tvdbId: p.tvdbId,
        title: p.title, year: p.year, poster: p.poster, libraryId: lib.id,
        seasons: pickedSeasons(), audience,
      })
      toast.success(status === "approved"
        ? `Adding ${p.title} to ${lib.name} now`
        : `Requested ${p.title} for ${lib.name}`)
      requested.reload()
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Could not request that")
    } finally { setAsking(false) }
  }

  const add = async (lib: ApiLibrary, groupIds?: number[]) => {
    setAdding(true)
    try {
      if (kind === "movie") {
        const r = await api.addMovie(tmdbId, lib.id, groupIds)
        toast.success(`${r.movie.title} added to ${lib.name} — searching for a release`)
        onAdded("movie", r.movie.id)
      } else {
        // a TVDB-sourced preview adds by its TVDB id; the fetched record
        // carries both ids, so either path lands on the same series
        const r = await api.addShow(data?.preview.tmdbId ?? 0, lib.id,
          pickedSeasons(), data?.preview.tvdbId, groupIds)
        toast.success(`${r.show.title} added to ${lib.name} — searching for releases`)
        onAdded("show", r.show.id)
      }
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setAdding(false) }
  }

  if (loading) return <p className="mono-label py-4 text-faint">loading…</p>
  if (error || !data) {
    return (
      <section>
        <button onClick={onBack} className="mono-label mb-5 flex items-center gap-1.5 text-muted-foreground hover:text-brass">← Back</button>
        <p className="mono-label py-4 text-want">{error ?? "not found"}</p>
      </section>
    )
  }
  const p = data.preview
  // newest first, matching a title's own page; specials (season 0) last
  const seasons = [...(p.seasons ?? [])].sort((a, b) => b.number - a.number)
  const newest = seasons[0]?.number
  // Whether this page still has an act left in it. A season's switch
  // picks what starts monitored ON ADD, so it only means something while
  // adding or asking is still possible — a title already in the library
  // has nothing left to choose, and one already asked for is waiting on
  // somebody else. The header asks the same question to decide between a
  // button and a tag, so both read it from here rather than repeating it
  // and drifting apart.
  const canAct = addable.length > 0 && !!preferred
    && !(isRequester && requested.has(p.kind, p.tmdbId, p.tvdbId))

  return (
    <section>
      <button onClick={onBack} className="mono-label mb-5 flex items-center gap-1.5 text-muted-foreground hover:text-brass">← Back</button>
      <DetailHero imageBase={data.imageBase} backdrop={p.backdrop} poster={p.poster} title={p.title}
        actions={<>
          {addable.length === 0 || !preferred
            ? <Tag kind="good">In library</Tag>
            : isRequester
              // a requester asks instead of adding, and is told a title has
              // already been asked for — never by whom
              ? !canAct
                ? <Tag kind="want">Requested</Tag>
                : <LibraryAction label="Request" libraries={addable} current={preferred}
                    busy={asking} onRun={(l, a) => void askFor(l, a)} />
              : <LibraryAction label="Add" icon={<Plus className="h-3.5 w-3.5" />}
                  libraries={addable} current={preferred}
                  busy={adding} onRun={(l, g) => void add(l, g)} />}
        </>}>
        <h1 className="font-display text-[32px] font-bold leading-tight tracking-tight text-balance">{p.title}</h1>
        <div className="mono-label mt-1 text-muted-foreground">
          {[p.year || null, p.status || null, p.runtime ? fmtRuntime(p.runtime) : null,
            p.genres?.join(" · ")].filter(Boolean).join("  ·  ")}
        </div>
        {inLibs.size > 0 && (
          <div className="mt-3"><Tag kind="good">Already in {inLibs.size === 1 ? "a library" : `${inLibs.size} libraries`}</Tag></div>
        )}
        <Overview text={p.overview || "No overview on TMDB."} />
      </DetailHero>

      <CastRow cast={p.cast ?? null} imageBase={data.imageBase} onOpenPerson={onOpenPerson} />
      <SimilarRow kind={kind} tmdbId={p.tmdbId} onOpenPreview={onOpenPreview} />

      {kind === "show" && seasons.length > 0 && (
        <>
          {canAct && (
            <p className="mono-label mb-2 mt-8 text-faint">
              seasons switched on start monitored when you {isRequester ? "ask" : "add"}
            </p>
          )}
          {seasons.map(se => {
            const eps = se.episodes ?? []
            const name = se.name || `Season ${se.number}`
            // same as a title you own: only the newest season starts open,
            // so a long-running show doesn't unroll the whole archive
            const open = openSeasons[se.number] ?? se.number === newest
            const toggleOpen = () => setOpenSeasons({ ...openSeasons, [se.number]: !open })
            return (
              <div key={se.number} className="mt-8">
                <div className="mb-1 flex items-baseline justify-between gap-2">
                  <button onClick={toggleOpen} aria-expanded={open}
                    className="group/season flex min-w-0 items-center gap-1.5 text-left">
                    <ChevronDown className={cn("h-4 w-4 shrink-0 text-faint transition-transform group-hover/season:text-brass", !open && "-rotate-90")} />
                    <h2 className="font-display truncate text-lg font-bold group-hover/season:text-brass">{name}</h2>
                  </button>
                  <div className="flex shrink-0 items-center gap-3">
                    <span className="mono-label text-faint">{eps.length} episodes</span>
                    {/* Looks like the switch a season carries once the show
                        is yours, but it is not one: it picks what starts
                        monitored on add. Offering it with nothing left to
                        add would read as the monitoring control it
                        resembles, and do nothing at all. */}
                    {canAct && (
                      <Switch checked={!unpicked.has(se.number)}
                        aria-label={`Monitor ${name} when added`}
                        onCheckedChange={() => setUnpicked(prev => {
                          const next = new Set(prev)
                          if (next.has(se.number)) next.delete(se.number)
                          else next.add(se.number)
                          return next
                        })} />
                    )}
                  </div>
                </div>
                {open && (
                  <div className="border-t">
                    {/* the same row a title in your library draws, minus the
                        controls that need an episode to exist first */}
                    {eps.map(e => {
                      const unaired = !!e.airDate && e.airDate.slice(0, 10) > new Date().toISOString().slice(0, 10)
                      return (
                        <div key={`${e.season}-${e.episode}`}
                          className="grid grid-cols-[44px_1fr_auto] items-center gap-3 border-b border-l-2 border-linesoft border-l-transparent py-2 pl-2 pr-1">
                          <span className="font-label text-right text-xs text-faint">
                            {String(e.episode).padStart(2, "0")}
                          </span>
                          <div className="min-w-0">
                            <div className="max-w-full truncate text-[13.5px] font-semibold">
                              {e.title || `Episode ${e.episode}`}
                            </div>
                            <div className="mt-0.5 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
                              {e.airDate ? e.airDate.slice(0, 10) : "—"}
                              {e.runtime > 0 && <span>{fmtRuntime(e.runtime)}</span>}
                            </div>
                          </div>
                          <div className="hidden sm:block">
                            {unaired ? <Tag kind="info">Unaired</Tag> : <Tag kind="dim">Not added</Tag>}
                          </div>
                        </div>
                      )
                    })}
                  </div>
                )}
              </div>
            )
          })}
        </>
      )}
    </section>
  )
}
