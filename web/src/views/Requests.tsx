import { useState } from "react"
import { api } from "@/api"
import type { ApiRequest } from "@/api"
import { useApi } from "@/hooks/use-api"
import { requestOpensAt } from "@/lib/request-open"
import { Tag } from "@/components/detail"
import { Button } from "@/components/ui/button"
import { img } from "@/lib/utils"
import { toast } from "sonner"

// The queue: what people have asked for and nobody has decided yet.
//
// This is the one place a request names who asked. The requester's own
// side is deliberately anonymous — they are told a title has been
// requested, never by whom — and the asymmetry is the point, so it isn't
// worth collapsing the two views into one.
//
// Approving performs the add, so the row leaves the queue and the title
// appears in the library it named. Denying leaves the row for the record
// and returns the title to askable: there is no refused state to explain,
// because a denied request simply stops existing as far as asking again
// is concerned.

export function RequestsView({ onOpenPreview }: {
  /** Opens the requested title, so a decision can be made on more than a name. */
  onOpenPreview?: (kind: "movie" | "show", id: number, src?: "tvdb") => void
}) {
  const { data, loading, reload } = useApi(() => api.requestQueue(), 15_000)
  const imageBase = data?.imageBase ?? ""
  const [deciding, setDeciding] = useState(0)
  const requests = data?.requests ?? []

  const decide = async (r: ApiRequest, decision: "approve" | "deny") => {
    setDeciding(r.id)
    try {
      await api.decideRequest(r.id, decision)
      toast.success(decision === "approve"
        ? `Approved ${r.title} — searching now`
        : `Denied ${r.title}`)
      reload({ quiet: true })
    } catch (e) {
      toast.error(e instanceof Error ? e.message : "Could not decide that")
    } finally {
      setDeciding(0)
    }
  }

  return (
    <div className="mx-auto max-w-3xl px-4 py-6 md:px-8">
      <div className="mb-5 flex items-baseline justify-between gap-3 border-b border-linesoft pb-3">
        <h1 className="font-display text-2xl font-bold">Requests</h1>
        <span className="mono-label text-faint">
          {loading && requests.length === 0
            ? "loading…"
            : `${requests.length} waiting`}
        </span>
      </div>

      {!loading && requests.length === 0 && (
        <p className="px-1 py-10 text-center text-sm text-muted-foreground">
          Nothing waiting. Requests from people you've shared a library with land here.
        </p>
      )}

      <div className="flex flex-col">
        {requests.map(r => (
          <div key={r.id}
            className="flex flex-wrap items-center gap-x-3.5 gap-y-2 border-b border-linesoft py-3 last:border-b-0">
            {/* the title itself opens, because deciding on a name and a
                poster thumbnail is deciding on less than you could */}
            <button className="flex min-w-0 flex-1 items-center gap-3.5 text-left"
              disabled={!onOpenPreview || !requestOpensAt(r)}
              onClick={() => {
                const at = requestOpensAt(r)
                if (at) onOpenPreview?.(at.kind, at.id, at.src)
              }}>
              <span className="h-[54px] w-9 shrink-0 overflow-hidden rounded-sm bg-surface2">
                {r.poster && (
                  <img src={img(imageBase, "w92", r.poster)} alt="" loading="lazy"
                    className="h-full w-full object-cover" />
                )}
              </span>
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2 text-[13.5px] font-semibold">
                  <span className="truncate">{r.title}{r.year ? ` (${r.year})` : ""}</span>
                  <Tag kind="dim" className="shrink-0">
                    {r.kind === "movie"
                      ? "Film"
                      : r.seasons?.length
                        ? `Season${r.seasons.length > 1 ? "s" : ""} ${r.seasons.join(", ")}`
                        : "Series"}
                  </Tag>
                </div>
                <div className="truncate text-xs text-muted-foreground">
                  {[r.username, r.libraryName].filter(Boolean).join(" · ")}
                </div>
              </div>
            </button>
            {/* Approving is the ordinary, safe answer and reads green;
                denying is the one that throws something away, so it is
                quiet until you go for it. The brand colour is a red, so
                using it for Approve made the harmless action look like
                the alarming one. */}
            <div className="ml-auto flex shrink-0 items-center gap-2">
              <Button variant="outline" disabled={deciding === r.id}
                className="h-8 border-linesoft text-muted-foreground hover:border-want/50 hover:text-want"
                onClick={() => void decide(r, "deny")}>
                Deny
              </Button>
              <Button disabled={deciding === r.id}
                className="h-8 bg-good text-good-ink hover:bg-good/90"
                onClick={() => void decide(r, "approve")}>
                Approve
              </Button>
            </div>
          </div>
        ))}
      </div>
    </div>
  )
}
