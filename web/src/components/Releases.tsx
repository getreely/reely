import { useEffect, useState } from "react"
import type { ApiRelease } from "@/api"
import { Tag, fmtSize } from "@/components/detail"
import { Button } from "@/components/ui/button"
import {
  Dialog, DialogContent, DialogHeader, DialogTitle,
} from "@/components/ui/dialog"
import { Download } from "lucide-react"
import { toast } from "sonner"
import { cn } from "@/lib/utils"

// ReleaseDialog runs one interactive indexer search and shows every answer
// with the scorer's verdict — including why the rejected ones were rejected,
// because that's how you learn your profile is tuned wrong.
//
// EVERY row is grabbable, rejected ones included: the automatic paths
// obey the profile because nobody is watching them, but a person in this
// dialog is the authority — re-grabbing a corrupt file, taking a
// below-cutoff copy on purpose, overriding a ban they disagree with.
//
// That includes a release whose protocol is switched off or has no
// client. The server refuses those with a reason rather than the button
// quietly vanishing, because "there is no Grab button" and "this indexer
// had nothing" look identical and have different fixes.
export function ReleaseDialog({ open, onOpenChange, title, fetchReleases, onGrab }: {
  open: boolean
  onOpenChange: (v: boolean) => void
  title: string
  fetchReleases: () => Promise<{ releases: ApiRelease[] | null }>
  onGrab: (r: ApiRelease) => Promise<unknown>
}) {
  const [releases, setReleases] = useState<ApiRelease[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [loading, setLoading] = useState(false)
  const [grabbed, setGrabbed] = useState<Set<string>>(new Set())
  const [grabbing, setGrabbing] = useState<string | null>(null)

  useEffect(() => {
    if (!open) return
    setLoading(true)
    setError(null)
    setReleases(null)
    setGrabbed(new Set())
    fetchReleases()
      .then(r => setReleases(r.releases ?? []))
      .catch(e => setError(e instanceof Error ? e.message : String(e)))
      .finally(() => setLoading(false))
    // fetchReleases is recreated per render; open toggling is the real trigger
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  // grabbed rows are keyed by download URL, not title: two indexers often
  // carry the same release under the same name, and grabbing one must not
  // badge the other — the URL is the one thing unique to each copy
  const grab = async (r: ApiRelease) => {
    setGrabbing(r.downloadUrl)
    try {
      await onGrab(r)
      setGrabbed(prev => new Set(prev).add(r.downloadUrl))
      toast.success(`Sent to ${r.protocol === "torrent" ? "qBittorrent" : "SABnzbd"} — ${r.title}`)
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setGrabbing(null)
    }
  }

  const accepted = releases?.filter(r => r.accepted).length ?? 0

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[80vh] w-full max-w-3xl overflow-hidden rounded-2xl p-0 sm:max-w-3xl">
        <DialogHeader className="border-b border-linesoft px-5 py-4">
          <DialogTitle className="font-display text-lg font-bold">{title}</DialogTitle>
          <p className="mono-label text-faint">
            {loading ? "searching indexers…"
              : error ? "search failed"
                : releases ? `${releases.length} releases · ${accepted} acceptable` : ""}
          </p>
        </DialogHeader>
        <div className="max-h-[62vh] overflow-y-auto px-5 pb-5">
          {error && <p className="py-4 text-[13px] text-want">{error}</p>}
          {!loading && !error && releases?.length === 0 && (
            <p className="mono-label py-4 text-faint">the indexers came back empty</p>
          )}
          {releases?.map((r, i) => (
            <div key={`${r.title}-${i}`}
              className={cn("border-b border-linesoft py-2.5", !r.accepted && "opacity-60")}>
              <div className="flex items-center gap-2">
                <span className="font-label min-w-0 flex-1 truncate text-[12px]" title={r.title}>{r.title}</span>
                {r.accepted
                  ? (
                    // format score, colored by sign — negative means the
                    // formats want this release deprioritized
                    <Tag kind={r.formatScore < 0 ? "bad" : "good"}>
                      {r.formatScore !== 0 ? `${r.formatScore > 0 ? "+" : ""}${r.formatScore}` : "accepted"}
                    </Tag>
                  )
                  : <Tag kind="dim">rejected</Tag>}
                {r.downloadUrl && (
                  grabbed.has(r.downloadUrl)
                    ? <Tag kind="brand">sent</Tag>
                    : (
                      <Button variant="outline" className="h-6 px-2 text-[11px]"
                        disabled={grabbing !== null}
                        onClick={() => void grab(r)}>
                        <Download className="mr-1 h-3 w-3" /> {r.accepted ? "Grab" : "Grab anyway"}
                      </Button>
                    )
                )}
              </div>
              <div className="mt-1 flex flex-wrap items-center gap-x-2.5 gap-y-1 text-[11.5px] text-muted-foreground">
                {r.quality
                  ? <Tag kind="info" className="px-1.5 text-[10px]">{r.quality}</Tag>
                  : <Tag kind="dim" className="px-1.5 text-[10px]">Unknown</Tag>}
                {r.source && <span className="uppercase">{r.source}</span>}
                {/* audio-language tags read off the name — MULTI, FRENCH, … —
                    so a multi-language release is visible before grabbing it */}
                {(r.languages ?? []).map(l => (
                  <span key={l} className="rounded-md bg-surface2 px-1.5 py-px text-[10px] font-semibold uppercase">{l}</span>
                ))}
                {r.hdr && <span className="font-semibold text-brass">HDR</span>}
                {(r.terms ?? []).map(t => (
                  <span key={t} className="rounded-md bg-brass/15 px-1.5 py-px text-[10px] font-semibold text-brass">+{t}</span>
                ))}
                {r.proper && <span>PROPER</span>}
                {r.size > 0 && <span>{fmtSize(r.size)}</span>}
                {/* which kind of release this is — the thing that decides
                    which client takes it, and worth seeing at a glance
                    when an install runs both */}
                {r.protocol && (
                  <Tag kind={r.protocol === "torrent" ? "torrent" : "usenet"} className="px-1.5 text-[10px]">
                    {r.protocol === "torrent" ? "TORRENT" : "USENET"}
                  </Tag>
                )}
                {r.indexer && <span>{r.indexer}</span>}
                {r.publishDate && <span>{r.publishDate.slice(0, 10)}</span>}
                {!r.accepted && r.reason && <span className="text-want">{r.reason}</span>}
              </div>
            </div>
          ))}
        </div>
      </DialogContent>
    </Dialog>
  )
}
