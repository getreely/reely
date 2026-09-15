import { useState } from "react"
import { api } from "@/api"
import type { ApiGroup } from "@/api"
import { toast } from "sonner"
import { cn } from "@/lib/utils"

// Who gets what a list adds.
//
// The same pills as a title's "Shared with", because it is the same
// decision asked once instead of once per title: "everything Trending
// adds is for the house". A chart refreshing twice a day is not
// something anybody tags by hand.
//
// It applies from here. Nothing records which titles a given list added,
// and inferring it from a chart's current contents would overwrite
// tagging done by hand on titles that merely happen to be on it — so
// nothing already in the library moves when this changes.
export function ListAudience({ listId, groups, chosen, onChanged }: {
  listId: number
  groups: ApiGroup[]
  chosen: number[]
  onChanged: () => void
}) {
  const [busy, setBusy] = useState(false)
  if (groups.length === 0) return null
  const picked = new Set(chosen)

  const toggle = async (groupId: number) => {
    const next = new Set(picked)
    if (next.has(groupId)) next.delete(groupId)
    else next.add(groupId)
    setBusy(true)
    try {
      await api.setListGroups(listId, [...next])
      onChanged()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex flex-wrap items-center gap-1.5">
      <span className="mono-label text-faint">shares with</span>
      {groups.map(g => (
        <button key={g.id} disabled={busy} onClick={() => void toggle(g.id)}
          title={`Plex tag ${g.label}`}
          className={cn(
            "font-label rounded-md px-1.5 py-0.5 text-[11px] font-semibold transition-colors",
            picked.has(g.id)
              ? "bg-brass/15 text-brass hover:bg-brass/25"
              : "bg-surface3 text-muted-foreground hover:text-foreground",
          )}>
          {picked.has(g.id) ? "✓ " : ""}{g.name}
        </button>
      ))}
      {picked.size === 0 && (
        <span className="text-[11px] text-faint">nobody — new titles stay yours</span>
      )}
    </div>
  )
}
