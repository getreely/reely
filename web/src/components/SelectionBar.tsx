import { useState } from "react"
import { api } from "@/api"
import type { ApiMovie, ApiShow } from "@/api"
import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogHeader, DialogTitle, DialogFooter } from "@/components/ui/dialog"
import { RadioGroup, RadioGroupItem } from "@/components/ui/radio-group"
import { Label } from "@/components/ui/label"
import { Eye, EyeOff, Tags, Trash2, X, Zap } from "lucide-react"
import { toast } from "sonner"
import { useApi } from "@/hooks/use-api"
import { useIsAdmin } from "@/lib/access"
import { cn } from "@/lib/utils"

// SelectionBar is the floating bulk-action bar: pick titles in the
// grid, act on all of them at once. Everything runs through the same
// per-title endpoints the detail pages use — the server's scoping rules
// apply per item, unchanged.

export type Selected =
  | { kind: "movie"; movie: ApiMovie }
  | { kind: "show"; show: ApiShow }

// runBulk runs an action over the selection, reporting an ok/fail tally.
async function runBulk(items: Selected[], label: string, fn: (s: Selected) => Promise<unknown>) {
  let ok = 0, failed = 0
  let lastError = ""
  for (const it of items) {
    try { await fn(it); ok++ } catch (e) { failed++; lastError = e instanceof Error ? e.message : String(e) }
  }
  if (failed === 0) toast.success(`${label}: ${ok} title${ok === 1 ? "" : "s"}`)
  else toast.warning(`${label}: ${ok} done, ${failed} failed${lastError ? ` — ${lastError}` : ""}`)
}

// DeleteMode is what the delete dialog resolves to: drop the entry and
// keep the files, drop both, or drop only the files and keep hunting.
export type DeleteMode = "keep" | "files" | "filesOnly"

// DeleteTitleDialog asks the delete question with real options: remove
// the entry alone, its files with it, or just the files — keeping the
// title in the library so a replacement gets searched for. Shared by the
// detail pages and the bulk bar.
export function DeleteTitleDialog({ open, onOpenChange, what, onConfirm }: {
  open: boolean; onOpenChange: (v: boolean) => void; what: string
  onConfirm: (mode: DeleteMode) => void
}) {
  const [mode, setMode] = useState<DeleteMode>("keep")
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md rounded-xl">
        <DialogHeader>
          <DialogTitle className="font-display text-lg font-bold">
            Remove {what}
          </DialogTitle>
        </DialogHeader>
        <RadioGroup value={mode} onValueChange={v => setMode(v as DeleteMode)} className="gap-3 py-1">
          <div className="flex items-start gap-2.5">
            <RadioGroupItem value="keep" id="bd-keep" className="mt-0.5" />
            <Label htmlFor="bd-keep" className="cursor-pointer text-[13px] font-normal leading-snug">
              <b>Remove from library</b><br />
              <span className="text-muted-foreground">Files on disk stay where they are.</span>
            </Label>
          </div>
          <div className="flex items-start gap-2.5">
            <RadioGroupItem value="files" id="bd-files" className="mt-0.5" />
            <Label htmlFor="bd-files" className="cursor-pointer text-[13px] font-normal leading-snug">
              <b>Remove and delete files</b><br />
              <span className="text-muted-foreground">
                Deletes this library's files on disk.
              </span>
            </Label>
          </div>
          <div className="flex items-start gap-2.5">
            <RadioGroupItem value="filesOnly" id="bd-files-only" className="mt-0.5" />
            <Label htmlFor="bd-files-only" className="cursor-pointer text-[13px] font-normal leading-snug">
              <b>Delete files, keep in library</b><br />
              <span className="text-muted-foreground">
                For a wrong or broken file: the title stays monitored and a search for a
                replacement is queued right away.
              </span>
            </Label>
          </div>
        </RadioGroup>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button variant="destructive" onClick={() => { onConfirm(mode); onOpenChange(false) }}>
            {mode === "filesOnly" ? "Delete files" : "Remove"}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}

export function SelectionBar({ items, onClear, onChanged }: {
  items: Selected[]
  onClear: () => void
  onChanged: () => void
}) {
  const [delOpen, setDelOpen] = useState(false)
  const [shareOpen, setShareOpen] = useState(false)
  const [busy, setBusy] = useState(false)
  const isAdmin = useIsAdmin()
  if (items.length === 0) return null

  const guard = (fn: () => Promise<void>) => async () => {
    if (busy) return
    setBusy(true)
    try { await fn() } finally { setBusy(false); onChanged(); onClear() }
  }

  const setMonitored = (monitored: boolean) => guard(() =>
    runBulk(items, monitored ? "Monitoring" : "Unmonitored", s =>
      s.kind === "movie" ? api.setMovieMonitored(s.movie.id, monitored) : api.setShowMonitored(s.show.id, monitored)))
  const doSearch = guard(() =>
    runBulk(items, "Searching", s =>
      s.kind === "movie" ? api.searchMovieNow(s.movie.id) : api.searchShowNow(s.show.id, 0, 0)))
  const doDelete = (mode: DeleteMode) => guard(() =>
    mode === "filesOnly"
      ? runBulk(items, "Files deleted", s =>
        s.kind === "movie" ? api.deleteMovieFile(s.movie.id) : api.deleteShowFiles(s.show.id))
      : runBulk(items, "Removed", s =>
        s.kind === "movie" ? api.deleteMovie(s.movie.id, mode === "files") : api.deleteShow(s.show.id, mode === "files")))()

  return (
    <>
      <div className="fixed bottom-[calc(env(safe-area-inset-bottom)+1.25rem)] left-1/2 z-50 flex -translate-x-1/2 items-center gap-1.5 rounded-2xl border border-linesoft bg-surface2 px-3 py-2 shadow-[0_12px_40px_rgba(0,0,0,0.45)] max-md:bottom-[calc(env(safe-area-inset-bottom)+4.75rem)]">
        <span className="px-2 text-[13px] font-semibold">{items.length} selected</span>
        <span className="mx-1 h-5 w-px bg-linesoft" />
        <Button variant="ghost" size="icon" className="h-9 w-9 rounded-[10px]" title="Monitor"
          disabled={busy} onClick={setMonitored(true)}>
          <Eye className="h-4 w-4" />
        </Button>
        <Button variant="ghost" size="icon" className="h-9 w-9 rounded-[10px]" title="Unmonitor"
          disabled={busy} onClick={setMonitored(false)}>
          <EyeOff className="h-4 w-4" />
        </Button>
        <Button variant="ghost" size="icon" className="h-9 w-9 rounded-[10px]" title="Auto search"
          disabled={busy} onClick={doSearch}>
          <Zap className="h-4 w-4" />
        </Button>
        {/* Sharing is the owner's, so a requester never sees this even
            where they can select titles. */}
        {isAdmin && (
          <Button variant="ghost" size="icon" className="h-9 w-9 rounded-[10px]" title="Share with…"
            disabled={busy} onClick={() => setShareOpen(true)}>
            <Tags className="h-4 w-4" />
          </Button>
        )}
        <Button variant="ghost" size="icon" className="h-9 w-9 rounded-[10px] text-want hover:text-want" title="Remove"
          disabled={busy} onClick={() => setDelOpen(true)}>
          <Trash2 className="h-4 w-4" />
        </Button>
        <span className="mx-1 h-5 w-px bg-linesoft" />
        <Button variant="ghost" size="icon" className="h-9 w-9 rounded-[10px]" title="Clear selection"
          disabled={busy} onClick={onClear}>
          <X className="h-4 w-4" />
        </Button>
      </div>
      <BulkShareDialog
        open={shareOpen}
        onOpenChange={setShareOpen}
        items={items}
        onDone={() => { onChanged(); onClear() }}
      />
      <DeleteTitleDialog open={delOpen} onOpenChange={setDelOpen}
        what={`${items.length} title${items.length === 1 ? "" : "s"}`} onConfirm={doDelete} />
    </>
  )
}

// BulkShareDialog shares a run of titles with a household in one
// gesture — the thing the per-title panel is tedious for.
//
// Adding is the default and the first option, because titles picked out
// of a grid for one household were not picked to have their other
// audiences revoked. Replacing is there for when that IS what you meant,
// and says so plainly rather than being what happens by accident.
// The group list is fetched when this opens, not when it mounts — which
// is what the third argument buys. Written the other way, gating the
// fetcher itself, it resolved an empty list on mount and never asked
// again: the dialog reported no groups on an install with groups, and
// bulk sharing had never worked.
function BulkShareDialog({ open, onOpenChange, items, onDone }: {
  open: boolean
  onOpenChange: (v: boolean) => void
  items: Selected[]
  onDone: () => void
}) {
  const groupsQuery = useApi(() => api.groups(), undefined, open)
  // the backfill group is everything you already had; adding to it by
  // hand would share a title with everybody, which is never what a
  // bulk pick means
  const groups = (groupsQuery.data ?? []).filter(g => !g.personal && !g.backfill)
  const [picked, setPicked] = useState<number[]>([])
  const [mode, setMode] = useState<"add" | "remove" | "replace">("add")
  const [busy, setBusy] = useState(false)

  const toggle = (id: number) =>
    setPicked(prev => prev.includes(id) ? prev.filter(x => x !== id) : [...prev, id])

  async function apply() {
    setBusy(true)
    try {
      const titles = items.map(s => s.kind === "movie"
        ? { kind: "movie" as const, tmdbId: s.movie.tmdbId, title: s.movie.title }
        : { kind: "show" as const, tmdbId: s.show.tmdbId, tvdbId: s.show.tvdbId, title: s.show.title })
      const res = await api.bulkShare(mode, picked, titles)
      toast.success(`${res.changed} title${res.changed === 1 ? "" : "s"} updated`)
      onOpenChange(false)
      setPicked([])
      onDone()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-md rounded-xl">
        <DialogHeader>
          <DialogTitle className="font-display text-lg font-bold">
            Share {items.length} title{items.length === 1 ? "" : "s"}
          </DialogTitle>
        </DialogHeader>

        {groupsQuery.loading ? (
          <p className="py-1 text-[13px] text-muted-foreground">Loading groups…</p>
        ) : groups.length === 0 ? (
          <p className="py-1 text-[13px] text-muted-foreground">
            No groups yet — make one in Settings → Sharing.
          </p>
        ) : (
          <>
            <RadioGroup value={mode} onValueChange={v => setMode(v as typeof mode)}
              className="gap-2 py-1">
              <div className="flex items-center gap-2.5">
                <RadioGroupItem value="add" id="bs-add" />
                <Label htmlFor="bs-add" className="cursor-pointer text-[13px] font-normal">
                  <b>Share with</b> — keeps who they already reach
                </Label>
              </div>
              <div className="flex items-center gap-2.5">
                <RadioGroupItem value="remove" id="bs-remove" />
                <Label htmlFor="bs-remove" className="cursor-pointer text-[13px] font-normal">
                  <b>Stop sharing with</b>
                </Label>
              </div>
              <div className="flex items-center gap-2.5">
                <RadioGroupItem value="replace" id="bs-replace" />
                <Label htmlFor="bs-replace" className="cursor-pointer text-[13px] font-normal">
                  <b>Share with only these</b> — drops every other group
                </Label>
              </div>
            </RadioGroup>

            <div className="flex flex-wrap gap-1.5 py-1">
              {groups.map(g => (
                <button
                  key={g.id}
                  onClick={() => toggle(g.id)}
                  className={cn(
                    "font-label rounded-md px-2 py-1 text-[11.5px] font-semibold transition-colors",
                    picked.includes(g.id)
                      ? "bg-brass/15 text-brass hover:bg-brass/25"
                      : "bg-surface3 text-muted-foreground hover:text-foreground",
                  )}
                >
                  {picked.includes(g.id) ? "✓ " : ""}{g.name}
                </button>
              ))}
            </div>
          </>
        )}

        <DialogFooter>
          <Button variant="ghost" onClick={() => onOpenChange(false)}>Cancel</Button>
          <Button
            disabled={busy || (picked.length === 0 && mode !== "replace")}
            onClick={() => void apply()}
          >
            Apply
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
