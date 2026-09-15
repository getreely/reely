import { useRef, useState, type ReactNode } from "react"
import { api } from "@/api"
import type { ApiCastMember, ApiQualityProfile } from "@/api"
import { useApi } from "@/hooks/use-api"
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select"
import { ExternalLink, FolderInput, Plus, Upload } from "lucide-react"
import { toast } from "sonner"
import { PathInput } from "@/components/PathInput"
import {
  Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle,
} from "@/components/ui/dialog"
import { Button } from "@/components/ui/button"
import { useIsAdmin, useIsRequester } from "@/lib/access"
import { cn, img } from "@/lib/utils"

// Shared furniture for the movie/show detail pages: the backdrop hero,
// status tags, the cast strip, and the little formatters.

export function Tag({ kind, children, className }: {
  kind: "good" | "info" | "want" | "bad" | "dim" | "brand"; children: ReactNode; className?: string
}) {
  const styles = {
    good: "bg-good/15 text-good",
    info: "bg-info/15 text-info",
    want: "bg-want/15 text-want",
    bad: "bg-red-500/15 text-red-400",
    dim: "bg-surface3 text-muted-foreground",
    brand: "bg-brass/15 text-brass",
  }
  return (
    <span className={cn("inline-block rounded-md px-2 py-0.5 text-[11px] font-semibold", styles[kind], className)}>
      {children}
    </span>
  )
}

// DetailHero is the page header: backdrop washed behind poster + children,
// with the title's icon actions pinned top-right.
export function DetailHero({ imageBase, backdrop, poster, title, actions, children }: {
  imageBase: string; backdrop: string; poster: string; title: string
  actions?: ReactNode; children: ReactNode
}) {
  return (
    <div className="relative overflow-hidden rounded-2xl border border-linesoft bg-surface">
      {backdrop && (
        <img src={img(imageBase, "w1280", backdrop)} alt="" aria-hidden
          className="absolute inset-0 h-full w-full object-cover object-top opacity-25" />
      )}
      <div className="absolute inset-0 bg-linear-to-r from-background/85 via-background/60 to-background/30" />
      {actions && <div className="absolute right-4 top-4 z-10 flex gap-1.5">{actions}</div>}
      <div className="relative flex flex-col gap-6 p-6 sm:flex-row">
        <div className="w-[150px] shrink-0 sm:w-[180px]">
          <div className="relative aspect-2/3 overflow-hidden rounded-[10px] bg-surface2 poster-shadow">
            {poster
              ? <img src={img(imageBase, "w342", poster)} alt="" className="absolute inset-0 h-full w-full object-cover" />
              : <div className="flex h-full items-center justify-center p-3 text-center"><span className="font-display text-[15px] font-bold leading-tight">{title}</span></div>}
          </div>
        </div>
        <div className="min-w-0 flex-1">{children}</div>
      </div>
    </div>
  )
}

export function ImdbLink({ imdbId }: { imdbId: string }) {
  if (!imdbId) return null
  return (
    <a href={`https://www.imdb.com/title/${imdbId}`} target="_blank" rel="noreferrer"
      className="mono-label inline-flex items-center gap-1 text-faint hover:text-brass">
      IMDb <ExternalLink className="h-3 w-3" />
    </a>
  )
}

// TmdbLink points at the title's TMDB page — kind is "movie" or "tv".
export function TmdbLink({ tmdbId, kind }: { tmdbId: number; kind: "movie" | "tv" }) {
  if (!tmdbId) return null
  return (
    <a href={`https://www.themoviedb.org/${kind}/${tmdbId}`} target="_blank" rel="noreferrer"
      className="mono-label inline-flex items-center gap-1 text-faint hover:text-brass">
      TMDB <ExternalLink className="h-3 w-3" />
    </a>
  )
}

// TvdbLink points at TheTVDB's dereferer, which resolves a series id to
// its page — shows only; reely carries the id along from TMDB.
export function TvdbLink({ tvdbId }: { tvdbId?: number }) {
  if (!tvdbId) return null
  return (
    <a href={`https://www.thetvdb.com/dereferrer/series/${tvdbId}`} target="_blank" rel="noreferrer"
      className="mono-label inline-flex items-center gap-1 text-faint hover:text-brass">
      TVDB <ExternalLink className="h-3 w-3" />
    </a>
  )
}

// CastRow lists a title's billed cast; with onOpenPerson each card clicks
// through to that person's page.
export function CastRow({ cast, imageBase, onOpenPerson }: {
  cast: ApiCastMember[] | null; imageBase: string; onOpenPerson?: (tmdbId: number) => void
}) {
  if (!cast?.length) return null
  return (
    <div className="mt-8">
      <div className="mono-label mb-2.5 text-faint">Cast</div>
      <div className="flex gap-3 overflow-x-auto pb-2">
        {cast.map(c => {
          const Cell = onOpenPerson ? "button" : "div"
          return (
            <Cell key={c.tmdbId} onClick={onOpenPerson ? () => onOpenPerson(c.tmdbId) : undefined}
              className={cn("w-[92px] shrink-0 text-left", onOpenPerson && "group cursor-pointer")}>
              <div className="relative aspect-2/3 overflow-hidden rounded-[8px] bg-surface2 transition-transform group-hover:scale-[1.03]">
                {c.photo
                  ? <img src={img(imageBase, "w185", c.photo)} alt="" loading="lazy" className="absolute inset-0 h-full w-full object-cover" />
                  : <div className="flex h-full items-center justify-center p-2 text-center"><span className="text-[11px] font-semibold leading-tight text-muted-foreground">{c.name}</span></div>}
              </div>
              <div className={cn("mt-1 truncate text-[11.5px] font-semibold", onOpenPerson && "group-hover:text-brass")}
                title={c.name}>{c.name}</div>
              <div className="truncate text-[10.5px] text-muted-foreground" title={c.character}>{c.character}</div>
            </Cell>
          )
        })}
      </div>
    </div>
  )
}

// ProfilePicker retunes one title's quality profile — the library default
// was only its starting point.
export function ProfilePicker({ value, profiles, onChange }: {
  value: number; profiles: ApiQualityProfile[] | null; onChange: (id: number) => void
}) {
  if (!profiles?.length) return null
  return (
    <div className="flex items-center gap-2">
      <span className="mono-label text-faint">Profile</span>
      <Select value={value ? String(value) : undefined} onValueChange={v => onChange(Number(v))}>
        <SelectTrigger className="h-7 w-[160px] text-[12px]">
          <SelectValue placeholder="Profile…" />
        </SelectTrigger>
        <SelectContent>
          {profiles.map(p => <SelectItem key={p.id} value={String(p.id)}>{p.name}</SelectItem>)}
        </SelectContent>
      </Select>
    </div>
  )
}

// AddToLibraryAction copies a title into another collection straight from
// its page — no re-searching. It only offers libraries of the right kind
// that don't hold the title yet, and hides entirely when there are none.
// A file already on disk hardlinks into the new library instantly.
//
// A requester asks for the other library instead, the way they do
// everywhere else. Offering them a control the add endpoint refuses would
// be a 403 they can do nothing about.
export function AddToLibraryAction({ kind, tmdbId, tvdbId, title }: {
  kind: "movies" | "shows"; tmdbId: number; tvdbId?: number; title: string
}) {
  const isRequester = useIsRequester()
  const libsQuery = useApi(() => api.libraries())
  const moviesQuery = useApi(() => kind === "movies" ? api.movies() : Promise.resolve({ movies: [], imageBase: "" }))
  const showsQuery = useApi(() => kind === "shows" ? api.shows() : Promise.resolve({ shows: [], imageBase: "" }))
  if (!tmdbId && !tvdbId) return null
  const owned = new Set(kind === "movies"
    ? (moviesQuery.data?.movies ?? []).filter(m => m.tmdbId === tmdbId).map(m => m.libraryId)
    : (showsQuery.data?.shows ?? []).filter(s =>
        (tmdbId > 0 && s.tmdbId === tmdbId) || (!!tvdbId && s.tvdbId === tvdbId)).map(s => s.libraryId))
  const candidates = (libsQuery.data?.libraries ?? []).filter(l => l.kind === kind && !owned.has(l.id))
  if (candidates.length === 0) return null

  const add = async (libId: number, libName: string, groupIds?: number[]) => {
    try {
      if (isRequester) {
        const { status } = await api.createRequest({
          kind: kind === "movies" ? "movie" : "show",
          tmdbId, tvdbId, title, libraryId: libId, audience: groupIds,
        })
        toast.success(status === "approved"
          ? `Adding ${title} to ${libName} now`
          : `Requested ${title} for ${libName}`)
        return
      }
      if (kind === "movies") await api.addMovie(tmdbId, libId, groupIds)
      else await api.addShow(tmdbId, libId, undefined, tvdbId, groupIds)
      toast.success(`${title} added to ${libName} — existing files link in instantly`)
      moviesQuery.reload({ quiet: true })
      showsQuery.reload({ quiet: true })
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) }
  }
  const verb = isRequester ? "Request" : "Add"

  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button title={`${verb} for another library`} aria-label={`${verb} for another library`}
          className="flex h-8 w-8 items-center justify-center rounded-lg border border-linesoft bg-surface/75 text-muted-foreground backdrop-blur-xs hover:border-brass/50 hover:text-brass">
          <Plus className="h-4 w-4" />
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="rounded-xl">
        <DropdownMenuLabel className="mono-label text-faint">{verb} to which library?</DropdownMenuLabel>
        {candidates.map(l => (
          <DropdownMenuItem key={l.id} onClick={() => void add(l.id, l.name)}>{l.name}</DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  )
}

// IconAction is one of the hero's top-right buttons: icon only, the label
// lives in the tooltip.
export function IconAction({ title, onClick, danger, busy, children }: {
  title: string; onClick: () => void; danger?: boolean; busy?: boolean; children: ReactNode
}) {
  return (
    <button title={title} aria-label={title} disabled={busy} onClick={onClick}
      className={cn(
        "flex h-8 w-8 items-center justify-center rounded-lg border border-linesoft bg-surface/75 text-muted-foreground backdrop-blur-xs disabled:opacity-50",
        danger ? "hover:border-want/50 hover:text-want" : "hover:border-brass/50 hover:text-brass"
      )}>
      {children}
    </button>
  )
}

// UploadAction is IconAction with a hidden file input behind it. The files
// go through the same import pipeline as downloads.
export function UploadAction({ title, multiple, busy, onFiles }: {
  title: string; multiple?: boolean; busy?: boolean; onFiles: (files: FileList) => void
}) {
  const input = useRef<HTMLInputElement>(null)
  return (
    <>
      <input ref={input} type="file" multiple={multiple} className="hidden"
        accept=".mkv,.mp4,.avi,.m4v,.mov,.wmv,.mpg,.mpeg,.ts"
        onChange={e => {
          if (e.target.files?.length) onFiles(e.target.files)
          e.target.value = ""
        }} />
      <IconAction title={title} busy={busy} onClick={() => input.current?.click()}>
        <Upload className="h-4 w-4" />
      </IconAction>
    </>
  )
}

// ImportPathAction imports a file or folder already on the server —
// hard-linked in, the source untouched. Admin-only, like the path
// autocomplete behind it.
export function ImportPathAction({ title, onImport }: {
  title: string
  onImport: (path: string) => Promise<string>
}) {
  const isAdmin = useIsAdmin()
  const [open, setOpen] = useState(false)
  const [path, setPath] = useState("/data/")
  const [busy, setBusy] = useState(false)
  if (!isAdmin) return null
  const run = async () => {
    setBusy(true)
    try {
      const message = await onImport(path)
      toast.success(message)
      setOpen(false)
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setBusy(false) }
  }
  return (
    <>
      <IconAction title={title} onClick={() => setOpen(true)}>
        <FolderInput className="h-4 w-4" />
      </IconAction>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent>
          <DialogHeader><DialogTitle>{title}</DialogTitle></DialogHeader>
          <p className="text-[12.5px] text-muted-foreground">
            Point at a file or folder on the server. It links into the library —
            the original stays where it is.
          </p>
          <PathInput value={path} onChange={setPath} placeholder="/data/downloads/…" autoFocus />
          <DialogFooter>
            <Button variant="outline" onClick={() => setOpen(false)}>Cancel</Button>
            <Button disabled={busy || !path} onClick={() => void run()}>Import</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </>
  )
}

// fmtQuality labels a file on disk: "WEB · 1080P", or just the resolution
// when the file's source was never recognized.
export function fmtQuality(quality: string, source?: string): string {
  const src = source ? source.toUpperCase() : ""
  const q = quality ? quality.toUpperCase() : ""
  return [src, q].filter(Boolean).join(" · ")
}

export function fmtRuntime(minutes: number): string {
  if (!minutes) return ""
  const h = Math.floor(minutes / 60), m = minutes % 60
  return h > 0 ? `${h}h ${m}m` : `${m}m`
}

export function fmtSize(bytes: number): string {
  if (!bytes) return ""
  const gb = bytes / (1 << 30)
  return gb >= 1 ? `${gb.toFixed(1)} GB` : `${Math.round(bytes / (1 << 20))} MB`
}
