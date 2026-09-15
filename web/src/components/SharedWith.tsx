import { useState } from "react"
import { api } from "@/api"
import type { ApiGroup } from "@/api"
import { useApi } from "@/hooks/use-api"
import { useIsAdmin } from "@/lib/access"
import { toast } from "sonner"
import { cn } from "@/lib/utils"
import { Users } from "lucide-react"

// Who can see this title, and changing it without re-adding anything.
//
// The question an owner actually asks is not "which labels are on this",
// it is "why can Sam see this" — so the panel names the person and the
// group granting it, and a person granted it twice appears twice,
// because revoking one of those changes nothing.
//
// Admin only: this is the shape of the household, and it has no business
// on the requesting side.
export function SharedWith({ kind, id, source, title }: {
  kind: "movie" | "show"
  id: number
  // a show sourced from TheTVDB may have no TMDB id at all, so it is
  // addressed by whichever id we hold
  source?: "tvdb"
  title: string
}) {
  const isAdmin = useIsAdmin()
  const [busy, setBusy] = useState(false)
  const groupsQuery = useApi(() => (isAdmin ? api.groups() : Promise.resolve([] as ApiGroup[])))
  const shareQuery = useApi(() =>
    isAdmin ? api.titleGroups(kind, id, source)
            : Promise.resolve({ viewers: [], groupIds: [] }))

  if (!isAdmin || !id) return null

  const groups = groupsQuery.data ?? []
  const chosen = new Set(shareQuery.data?.groupIds ?? [])
  const viewers = shareQuery.data?.viewers ?? []

  async function toggle(groupId: number) {
    const next = new Set(chosen)
    if (next.has(groupId)) next.delete(groupId)
    else next.add(groupId)
    setBusy(true)
    try {
      await api.setTitleGroups(kind, id, [...next], title, source)
      shareQuery.reload()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="rounded-2xl border border-linesoft bg-surface p-4">
      <div className="mb-2 flex items-center gap-2">
        <Users className="size-3.5 text-faint" />
        <h3 className="text-[13px] font-bold">Shared with</h3>
      </div>

      {groups.length === 0 ? (
        <p className="text-[12px] text-muted-foreground">
          No groups yet — make one in Settings → Sharing.
        </p>
      ) : (
        <>
          <div className="mb-2.5 flex flex-wrap gap-1.5">
            {groups.map(g => (
              <button
                key={g.id}
                disabled={busy}
                onClick={() => void toggle(g.id)}
                title={`Plex tag ${g.label}`}
                className={cn(
                  "font-label rounded-md px-2 py-1 text-[11.5px] font-semibold transition-colors",
                  chosen.has(g.id)
                    ? "bg-brass/15 text-brass hover:bg-brass/25"
                    : "bg-surface3 text-muted-foreground hover:text-foreground",
                )}
              >
                {chosen.has(g.id) ? "✓ " : ""}{g.name}
              </button>
            ))}
          </div>

          {viewers.length === 0 ? (
            <p className="text-[11.5px] text-faint">
              Nobody yet. Only you can see this in Plex.
            </p>
          ) : (
            <p className="text-[11.5px] text-faint">
              {viewers.map(v => `${v.username} (${v.groupName})`).join(", ")}
            </p>
          )}
        </>
      )}
    </div>
  )
}
