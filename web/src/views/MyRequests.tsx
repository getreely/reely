import { api } from "@/api"
import type { ApiRequest } from "@/api"
import { useApi } from "@/hooks/use-api"
import { RequestsView } from "@/views/Requests"
import { useAccess } from "@/lib/access"
import { requestOpensAt } from "@/lib/request-open"
import { cn, img } from "@/lib/utils"

// The Requests tab out on the portal, which is two pages wearing one
// name — because "requests" means something different depending on who
// is looking, and both meanings belong on the tab called Requests.
//
// To an owner it is a queue: things waiting on a decision only they can
// make. Deciding used to need a machine at home, which is the wrong way
// round — somebody asks while you are out, and the ask waits for your
// commute rather than for you.
//
// To everybody else it is what is still waiting on a decision. Only
// that: an approved title stops being a request and becomes something
// on its way, which the Movies and Shows tabs already say better than a
// list of receipts would. A denied one is not here either — denying
// returns a title to askable rather than leaving a refusal to explain,
// which is the rule everywhere else and would be odd to break here.
//
// No names on it. Whose ask it is has never been anybody else's
// business on this surface.
export function PortalRequestsView({ onOpenPreview }: {
  onOpenPreview?: (kind: "movie" | "show", id: number, src?: "tvdb") => void
}) {
  const { user } = useAccess()
  // the account's role, not isAdmin — that is false out here by design,
  // since admin routes are not served on this listener. Deciding is one
  // of the few that is.
  const owns = user === null || user.role === "admin"
  if (owns) return <RequestsView onOpenPreview={onOpenPreview} />
  return <AskedForView onOpenPreview={onOpenPreview} />
}

function AskedForView({ onOpenPreview }: {
  onOpenPreview?: (kind: "movie" | "show", id: number, src?: "tvdb") => void
}) {
  // mine: the wide listing carries everybody's asks in the libraries
  // this account can see, which is what makes Explore say "already
  // requested" — right there, wrong on a page about what I asked for.
  const { data, loading } = useApi(() => api.requests(true), 15_000)
  // it carries approved ones too, for the same reason. Also right
  // there, and also not this page.
  const waiting = (data?.requests ?? []).filter(r => r.status === "pending")

  return (
    <section>
      <h1 className="mb-1 font-display text-[26px] font-bold tracking-tight">Requests</h1>
      <p className="mb-4 text-[12.5px] text-muted-foreground">
        What you have asked for and the owner has not decided yet. Anything
        approved is on its way and shows up under Movies or Shows once it lands.
      </p>
      {loading && waiting.length === 0 && (
        <p className="text-[13px] text-muted-foreground">Loading…</p>
      )}
      {!loading && waiting.length === 0 && (
        <p className="text-[13px] text-muted-foreground">
          Nothing waiting on a decision. Find something and hit Request.
        </p>
      )}
      <Group rows={waiting} onOpenPreview={onOpenPreview} />
    </section>
  )
}

function Group({ rows, onOpenPreview }: {
  rows: ApiRequest[]
  onOpenPreview?: (kind: "movie" | "show", id: number, src?: "tvdb") => void
}) {
  if (rows.length === 0) return null
  return (
    <div className="mb-6">
      <div className="flex flex-col gap-2">
        {rows.map(r => (
          <button key={r.id} disabled={!onOpenPreview || !requestOpensAt(r)}
            onClick={() => {
              const at = requestOpensAt(r)
              if (at) onOpenPreview?.(at.kind, at.id, at.src)
            }}
            className={cn("flex items-center gap-3 rounded-xl border border-linesoft bg-surface p-2 text-left",
              onOpenPreview && requestOpensAt(r) && "hover:border-brass/50")}>
            <div className="h-[63px] w-[42px] shrink-0 overflow-hidden rounded bg-surface2">
              {r.poster && (
                <img src={img("https://image.tmdb.org/t/p", "w154", r.poster)} alt=""
                  loading="lazy" className="h-full w-full object-cover" />
              )}
            </div>
            <div className="min-w-0 flex-1">
              <div className="truncate text-[13px] font-semibold">{r.title}</div>
              <div className="mono-label text-faint">
                {[r.year || null, r.kind === "movie" ? "film" : "series"]
                  .filter(Boolean).join("  ·  ")}
              </div>
            </div>
            <span className="mono-label shrink-0 rounded-lg bg-want/15 px-2 py-1 text-want">
              waiting
            </span>
          </button>
        ))}
      </div>
    </div>
  )
}
