import { useState } from "react"
import type { ReactNode } from "react"
import { api } from "@/api"
import type { ApiGroup, ApiLibrary, ApiMyGroup } from "@/api"
import { useApi } from "@/hooks/use-api"
import { useAccess, useIsAdmin } from "@/lib/access"
import { Button } from "@/components/ui/button"
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel,
  DropdownMenuSeparator, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { ChevronDown } from "lucide-react"
import { cn } from "@/lib/utils"

// The button that puts a title somewhere: Add on the owner's side, Request
// on a requester's. One component because they are the same gesture with
// different words — the person picks a title and says where it goes — and
// two implementations drift.
//
// It acts on a click rather than opening a menu. With one library there is
// nothing to choose and the button is the whole control; with several, the
// button still does the obvious thing and a picker sits beside it for the
// times that isn't what you meant. Making the button itself open a menu
// spends a click on a question most people don't have.
export function LibraryAction({
  label, icon, libraries, current, busy, compact, onRun,
}: {
  label: string
  icon?: ReactNode
  /** The libraries this account may put the title in, already filtered. */
  libraries: ApiLibrary[]
  /** Where a plain click sends it — their default, or the only one. */
  current: ApiLibrary
  busy?: boolean
  /** compact is the search-result row; the default is the detail hero. */
  compact?: boolean
  onRun: (lib: ApiLibrary, groupIds?: number[]) => void
}) {
  const height = compact ? "h-8" : "h-9"
  const text = compact ? "" : "text-[13px]"
  // Who this is for. Undefined is not "nobody" — it is "do the usual
  // thing", which on both sides means you and the households you are in.
  // Only a deliberate change makes it a list.
  const [shareWith, setShareWith] = useState<number[] | undefined>(undefined)
  // An owner picks from every group on the install, because adding a
  // title for whoever asked them is half of what the picker is for. A
  // requester picks only from their own, since the install-wide list
  // names every household here.
  //
  // Either way the personal group is absent: a title you cannot then
  // watch is nobody's intent, so the server keeps yours regardless.
  const isAdmin = useIsAdmin()
  const { user } = useAccess()
  const groupsQuery = useApi<ApiGroup[] | ApiMyGroup[]>(() =>
    isAdmin ? api.groups() : api.myGroups())
  // The back catalogue holds what was already in Plex before the split,
  // and everybody is in it. Sharing something into it reaches the whole
  // install, so it is the owner's call and nobody else's — a requester
  // is not offered it, and the server refuses it for them whatever a
  // client sends.
  //
  // Whose account it is decides that, not which door they came in by:
  // isAdmin is false out on the portal by design, because admin routes
  // are not served there, but an owner asking for a film while away from
  // home is still the owner.
  const owns = user === null || user.role === "admin"
  const houseWide = (g: { backfill: boolean; everyone?: boolean }) =>
    g.backfill || g.everyone === true
  const shared = (groupsQuery.data ?? [])
    .filter(g => !g.personal && (owns || !houseWide(g)))
  // The households that are yours start ticked, whichever side you are
  // on: adding or asking for a film while in a household usually means
  // the household should get it. An owner's other groups start unticked
  // — those are for adding on somebody else's behalf.
  //
  // Never a house-wide group, which an owner still sees here: putting a
  // title in front of everybody is what those are for, but it takes a
  // deliberate tick and is never an assumption. That covers the back
  // catalogue and the household group alike — a requester belongs to the
  // household, so without this every ask they made would go to the whole
  // install, which is the thing an "Everyone" group is most likely to be
  // created for and least likely to be wanted for. The server holds the
  // same line, so a client cannot slip one in by naming it.
  const mine = shared
    .filter(g => !houseWide(g) && (!("mine" in g) || g.mine))
    .map(g => g.id)
  const canChoose = libraries.length > 1 || shared.length > 0

  // Keeping it to yourself is a state rather than the absence of every
  // other tick. Unticking three households one at a time says the same
  // thing, but only once you have checked that you got them all — and
  // "did I miss one" is a poor thing to wonder about a request you
  // wanted private.
  const [justMe, setJustMe] = useState(false)
  const ticked = new Set(shareWith ?? mine)
  const audience = justMe ? [] : [...ticked]

  function toggleGroup(id: number) {
    if (justMe) return // the households are inert while this is on
    setShareWith(() => {
      const next = new Set(ticked)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return [...next]
    })
  }

  if (!canChoose) {
    return (
      <Button variant={compact ? "outline" : "default"}
        className={cn(height, text, "shrink-0 gap-1")}
        disabled={busy} onClick={() => onRun(current, audience)}>
        {icon}{label}
      </Button>
    )
  }
  return (
    <div className="flex shrink-0">
      <Button variant={compact ? "outline" : "default"}
        className={cn(height, text, "gap-1 rounded-r-none pr-2.5")}
        disabled={busy} onClick={() => onRun(current, audience)}>
        {icon}{label}
      </Button>
      <DropdownMenu>
        <DropdownMenuTrigger asChild>
          <Button variant={compact ? "outline" : "default"}
            aria-label={`Choose where ${label.toLowerCase()} sends this, and who it is for`}
            className={cn(height, "rounded-l-none border-l border-black/15 px-2")}
            disabled={busy}>
            <ChevronDown className="h-3.5 w-3.5" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" className="rounded-xl">
          {libraries.length > 1 && (
            <>
              <DropdownMenuLabel className="mono-label text-faint">
                {label} to which library?
              </DropdownMenuLabel>
              {libraries.map(l => (
                <DropdownMenuItem key={l.id} onClick={() => onRun(l, audience)}>
                  {/* the one a plain click would use, so the button is never a
                      mystery about where a title just went */}
                  {l.id === current.id ? "✓ " : "   "}{l.name}
                </DropdownMenuItem>
              ))}
            </>
          )}
          {shared.length > 0 && (
            <>
              {libraries.length > 1 && <DropdownMenuSeparator />}
              <DropdownMenuLabel className="mono-label text-faint">
                {isAdmin ? "Who is it for?" : "Share it with"}
              </DropdownMenuLabel>
              {/* these toggle rather than act, so the menu has to stay
                  open — picking two people is one gesture, not two trips */}
              {/* Keeping it to yourself is a state rather than the
                  absence of every other tick, and it belongs to whoever
                  is looking — an owner is in households too. */}
              <DropdownMenuItem
                onSelect={e => { e.preventDefault(); setJustMe(v => !v) }}
              >
                {justMe ? "✓ " : "   "}Just for me
              </DropdownMenuItem>
              {shared.map(g => (
                <DropdownMenuItem
                  key={g.id}
                  disabled={justMe}
                  onSelect={e => { e.preventDefault(); toggleGroup(g.id) }}
                >
                  {ticked.has(g.id) && !justMe ? "✓ " : "   "}{g.name}
                </DropdownMenuItem>
              ))}
              <DropdownMenuItem onClick={() => onRun(current, audience)}>
                <span className="font-semibold">{label}</span>
              </DropdownMenuItem>
            </>
          )}
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  )
}

/**
 * Where a plain click should send a title: the library this account chose
 * as its default when that is one of the candidates, else the first.
 */
export function preferredLibrary(libraries: ApiLibrary[], defaultLibraryId: number): ApiLibrary | undefined {
  return libraries.find(l => l.id === defaultLibraryId) ?? libraries[0]
}
