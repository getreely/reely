import { useEffect, useState } from "react"
import { api } from "@/api"
import type { ApiRenameOutlook, ApiSearchResult } from "@/api"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { Input } from "@/components/ui/input"
import { toast } from "sonner"
import { cn, img } from "@/lib/utils"

// Re-matching: this file is not the film reely thinks it is.
//
// It happens when a filename is ambiguous or reely read it badly — a
// "Part Three" filed as Part One, a 2025 release matched to the 2020 one
// of the same name. The file was always right; only the identity was
// wrong.
//
// So this searches the way adding does, with posters and years, because
// choosing between two films of the same name is exactly the case that
// needs to be seen rather than read.
export function RematchDialog({ open, onOpenChange, kind, id, title, onDone }: {
  open: boolean
  onOpenChange: (v: boolean) => void
  kind: "movie" | "show"
  id: number
  title: string
  onDone: () => void
}) {
  const [q, setQ] = useState(title)
  const [results, setResults] = useState<ApiSearchResult[]>([])
  const [imageBase, setImageBase] = useState("")
  const [busy, setBusy] = useState(false)
  const [searching, setSearching] = useState(false)
  const [files, setFiles] = useState<ApiRenameOutlook | null>(null)
  const [rename, setRename] = useState(false)

  useEffect(() => { if (open) setQ(title) }, [open, title])

  // What renaming would touch. reely used to work this out for itself —
  // a file where the templates would have put it was one it had filed,
  // anything else was somebody's own layout — and got the common case
  // wrong: a library imported at its release names is neither. So the
  // files are named and the choice is the owner's; `placed` only decides
  // where the box starts.
  useEffect(() => {
    if (!open) return
    setFiles(null)
    api.rematchFiles(kind, id)
      .then(o => { setFiles(o); setRename(o.files > 0) })
      .catch(() => { setFiles(null); setRename(false) })
  }, [open, kind, id])

  // debounced, so typing a title does not fire a search per keystroke
  useEffect(() => {
    if (!open || !q.trim()) { setResults([]); return }
    const t = setTimeout(() => {
      setSearching(true)
      api.search(q.trim(), kind)
        .then(r => { setResults(r.results ?? []); setImageBase(r.imageBase) })
        .catch(() => setResults([]))
        .finally(() => setSearching(false))
    }, 300)
    return () => clearTimeout(t)
  }, [q, open, kind])

  async function pick(r: ApiSearchResult) {
    setBusy(true)
    try {
      // a TVDB-sourced result is addressed by its TVDB id, the same way
      // adding one is
      const res = await (kind === "movie"
        ? api.rematchMovie(id, r.tmdbId, rename)
        : api.rematchShow(id, r.tmdbId, r.tvdbId, rename))
      toast.success(`Now matched to ${r.title}${r.year ? ` (${r.year})` : ""}`, {
        description: res.renamed > 0
          ? `${res.renamed} file${res.renamed === 1 ? "" : "s"} renamed to match.`
          : rename
            ? "Nothing moved — the files were already where the naming puts them."
            : "The files were left where they are.",
      })
      onOpenChange(false)
      onDone()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-lg rounded-xl">
        <DialogHeader>
          <DialogTitle className="font-display text-lg font-bold">Re-match</DialogTitle>
        </DialogHeader>
        <p className="text-[12.5px] text-muted-foreground">
          Pick what this actually is. The files, the library, the profile and the history
          all stay — only what reely thinks it is changes, along with the links and
          artwork that follow from it.
          {kind === "show" && " Its episodes are rebuilt to match the series you pick."}
        </p>

        <label className={cn("flex items-start gap-2.5 rounded-lg border border-linesoft p-2.5",
          (files?.files ?? 0) === 0 && "opacity-60")}>
          <input type="checkbox" className="mt-0.5 accent-brass"
            disabled={(files?.files ?? 0) === 0}
            checked={rename} onChange={e => setRename(e.target.checked)} />
          <span className="block min-w-0 flex-1">
            <span className="block text-[13px] font-semibold">Also rename the files</span>
            {files === null ? (
              <span className="mono-label text-faint">checking…</span>
            ) : files.files === 0 ? (
              <span className="mono-label text-faint">nothing on disk yet</span>
            ) : (
              <>
                <span className="mono-label block truncate text-faint" title={files.path}>
                  {files.files} file{files.files === 1 ? "" : "s"}  ·  {files.path}
                </span>
                {!files.placed && (
                  <span className="mt-1 block text-[12px] text-muted-foreground">
                    These are not laid out by reely&rsquo;s naming yet — renaming moves them
                    into it.
                  </span>
                )}
              </>
            )}
          </span>
        </label>
        <Input autoFocus value={q} onChange={e => setQ(e.target.value)}
          placeholder={kind === "movie" ? "Search films…" : "Search series…"} />

        <div className="max-h-[46vh] space-y-1.5 overflow-y-auto">
          {searching && results.length === 0 && (
            <p className="py-2 text-[13px] text-muted-foreground">Searching…</p>
          )}
          {!searching && q.trim() && results.length === 0 && (
            <p className="py-2 text-[13px] text-muted-foreground">Nothing found.</p>
          )}
          {results.map(r => (
            <button key={`${r.tmdbId}-${r.tvdbId ?? 0}`} disabled={busy} onClick={() => void pick(r)}
              className={cn("flex w-full items-center gap-3 rounded-lg p-1.5 text-left",
                "hover:bg-surface2 disabled:opacity-60")}>
              <div className="h-[63px] w-[42px] shrink-0 overflow-hidden rounded bg-surface2">
                {r.poster && (
                  <img src={img(imageBase, "w154", r.poster)} alt="" loading="lazy"
                    className="h-full w-full object-cover" />
                )}
              </div>
              <div className="min-w-0">
                <div className="truncate text-[13px] font-semibold">{r.title}</div>
                <div className="mono-label text-faint">
                  {[r.year || null,
                    r.tmdbId ? `tmdb ${r.tmdbId}` : null,
                    r.tvdbId ? `tvdb ${r.tvdbId}` : null].filter(Boolean).join("  ·  ")}
                </div>
              </div>
            </button>
          ))}
        </div>

        <div className="flex justify-end">
          <Button variant="ghost" onClick={() => onOpenChange(false)}>Cancel</Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
