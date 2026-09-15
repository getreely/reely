import { useEffect, useState } from "react"
import { api, auth } from "@/api"
import type { ApiGroup, ApiPlexAccount, ApiSharingResult, ApiUser } from "@/api"
import { useApi } from "@/hooks/use-api"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Switch } from "@/components/ui/switch"
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { toast } from "sonner"
import { cn } from "@/lib/utils"
import { Trash2, Users } from "lucide-react"

// Splitting the library so shared users see only their own titles.
//
// A group is the unit: a household is a group with several people, and
// somebody's own titles are a group with one. That is why there is no
// separate "personal" concept in this UI — a personal group is listed
// like any other, just not deletable and not editable for membership.
//
// Nothing here touches Plex until an account is switched on below. An
// install whose shares are unrestricted today keeps them that way until
// each person is moved across deliberately, because turning it on
// narrows what that person can see.
export function SharingCard() {
  const groupsQuery = useApi(() => api.groups())
  const usersQuery = useApi(() => auth.users())
  const groups = groupsQuery.data ?? []
  const users = usersQuery.data?.users ?? []
  const [name, setName] = useState("")
  const [busy, setBusy] = useState(false)
  // The last pass, kept rather than toasted: "12 waiting" is a thing to
  // look at and act on, not a thing to catch as it goes past.
  const [last, setLast] = useState<ApiSharingResult | null>(null)

  const reload = () => {
    groupsQuery.reload()
  }

  async function create() {
    if (!name.trim()) return
    setBusy(true)
    try {
      await api.createGroup(name.trim())
      setName("")
      reload()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }

  async function reconcile() {
    setBusy(true)
    try {
      const res = await api.reconcileSharing()
      setLast(res)
      const bits = [`${res.labelled} labelled`, `${res.shares} shares written`]
      if (res.pending) bits.push(`${res.pending} waiting on Plex`)
      if (res.pruned) bits.push(`${res.pruned} stale cleared`)
      toast.success(bits.join(", "))
      for (const d of res.drifted ?? []) {
        toast.warning(`${d}'s Plex share was changed outside reely`)
      }
      for (const e of (res.errors ?? []).slice(0, 3)) toast.error(e)
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="space-y-4">
      <div className="rounded-2xl border border-linesoft bg-surface p-5">
        <h2 className="mb-1 text-[15px] font-bold">Sharing</h2>
        <p className="mb-3 text-[12.5px] text-muted-foreground">
          A group is a set of people who see the same titles. reely tags each title in
          Plex and sets each person's restriction — you never edit a label by hand.
        </p>

        <div className="mb-4 flex gap-2">
          <Input
            value={name}
            placeholder="New group — e.g. Jolene's family"
            onChange={e => setName(e.target.value)}
            onKeyDown={e => { if (e.key === "Enter") void create() }}
          />
          <Button onClick={() => void create()} disabled={busy || !name.trim()}>Add</Button>
        </div>

        {groups.length === 0 && (
          <p className="text-[13px] text-muted-foreground">No groups yet.</p>
        )}
        {groups.map(g => (
          <GroupRow key={g.id} group={g} users={users} onChanged={reload} />
        ))}
      </div>

      <SeedCard groups={groups} onSeeded={reload} />
      <ManagedCard users={users} />

      <div className="rounded-2xl border border-linesoft bg-surface p-5">
        <h2 className="mb-1 text-[15px] font-bold">Apply now</h2>
        <p className="mb-3 text-[12.5px] text-muted-foreground">
          Changes reach Plex on their own within a few minutes. This runs a pass
          immediately — useful right after a change, to watch it land.
        </p>
        <Button variant="secondary" onClick={() => void reconcile()} disabled={busy}>
          Apply to Plex
        </Button>

        {last && (
          <div className="mt-3 border-t border-linesoft pt-3 text-[12px]">
            <p className="text-muted-foreground">
              {last.labelled} labelled · {last.shares}{" "}
              {last.shares === 1 ? "share" : "shares"} written
              {last.pending > 0 && ` · ${last.pending} waiting on Plex`}
            </p>
            {(last.pruned ?? 0) > 0 && (
              <p className="mt-1 text-muted-foreground">
                {last.pruned} stale {last.pruned === 1 ? "grant" : "grants"} cleared —
                nothing on this install has {last.pruned === 1 ? "that title" : "those titles"}{" "}
                any more, in reely or on Plex.
              </p>
            )}
            {last.held && (
              <p className="mt-1 text-want">
                Shares left alone this pass — labelling did not finish, so nobody's
                access was narrowed on a half-tagged library. It will try again.
              </p>
            )}
            {last.pending > 0 && (
              <div className="mt-2">
                {/* Two different problems wearing one word. Without a
                    file it is a download to be patient about. With one,
                    the two disagree about what that file IS — and which
                    of them is wrong is not something reely can decide,
                    so it says what it knows and leaves the judgement. */}
                <p className="mb-1 text-faint">
                  Waiting on Plex — reely has these and your server does not.
                  {(last.mismatched ?? 0) > 0 && (
                    <> <strong className="text-want">
                      {last.mismatched} you already have on disk
                    </strong> — reely and Plex disagree about what that file is. Check
                    the filename against both: whichever has it wrong is the one to
                    re-match.</>
                  )}
                </p>
                <ul className="max-h-40 overflow-y-auto text-muted-foreground">
                  {(last.waiting ?? []).map((w, i) => (
                    <li key={`${w.title}${i}`} className="flex items-baseline gap-2">
                      <span className="truncate">{w.title}</span>
                      <span className={cn("font-label shrink-0 text-[10.5px]",
                        w.onDisk ? "text-want" : "text-faint")}>
                        {w.onDisk ? "on disk — identified differently" : "no file yet"}
                      </span>
                    </li>
                  ))}
                </ul>
                {last.pending > (last.waiting?.length ?? 0) && (
                  <p className="mt-1 text-faint">
                    …and {last.pending - (last.waiting?.length ?? 0)} more.
                  </p>
                )}
              </div>
            )}
            {(last.errors ?? []).length > 0 && (
              <ul className="mt-2 text-danger">
                {(last.errors ?? []).slice(0, 5).map((e, i) => (
                  <li key={i} className="truncate">{e}</li>
                ))}
              </ul>
            )}
          </div>
        )}
      </div>
    </div>
  )
}

function GroupRow({ group, users, onChanged }: {
  group: ApiGroup; users: ApiUser[]; onChanged: () => void
}) {
  const [open, setOpen] = useState(false)
  const [name, setName] = useState(group.name)
  const members = new Set(group.memberIds ?? [])

  async function toggle(userId: number) {
    const next = new Set(members)
    if (next.has(userId)) next.delete(userId)
    else next.add(userId)
    try {
      await api.setGroupMembers(group.id, [...next])
      onChanged()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    }
  }

  async function rename() {
    if (name.trim() === group.name) return
    try {
      await api.renameGroup(group.id, name.trim())
      toast.success("Renamed — the Plex tag follows on the next pass")
      onChanged()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
      setName(group.name)
    }
  }

  async function setEveryone(on: boolean) {
    try {
      await api.setGroupEveryone(group.id, on)
      toast.success(on
        ? `${group.name} is now the everyone group — the Plex tags follow on the next pass`
        : `${group.name} is an ordinary group again`)
      onChanged()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    }
  }

  async function remove() {
    try {
      await api.deleteGroup(group.id)
      // Named, because the row is gone by the time this is read and a
      // bare "deleted" leaves you checking which one you clicked. The
      // tag outlives the group until the next pass, the same way a
      // rename's does — deleting is only allowed once the group is
      // empty, so nobody loses access in the meantime.
      toast.success(`${group.name} deleted — its Plex tag clears on the next pass`)
      onChanged()
    } catch (e) {
      // a group with members is refused rather than cascaded: deleting
      // it would revoke every title it grants
      toast.error(`${e instanceof Error ? e.message : e}`)
    }
  }

  return (
    <div className="border-b border-linesoft py-2.5 text-[13px] last:border-0">
      <div className="flex items-center gap-2.5">
        <button
          className="flex min-w-0 flex-1 items-center gap-2 text-left"
          onClick={() => setOpen(o => !o)}
        >
          <Users className="size-3.5 shrink-0 text-faint" />
          <span className="truncate font-semibold">{group.name}</span>
          {group.personal && (
            <span className="font-label shrink-0 rounded-md bg-surface3 px-1.5 py-0.5 text-[10.5px] text-muted-foreground">
              personal
            </span>
          )}
        </button>
        {group.backfill && (
          <span className="font-label shrink-0 rounded-md bg-surface3 px-1.5 py-0.5 text-[10.5px] text-muted-foreground">
            existing library
          </span>
        )}
        {group.everyone && (
          <span className="font-label shrink-0 rounded-md bg-brass/15 px-1.5 py-0.5 text-[10.5px] text-brass">
            everyone
          </span>
        )}
        <span className="shrink-0 text-[11.5px] text-faint">
          {group.members} {group.members === 1 ? "person" : "people"} · {group.titles} titles
        </span>
        {!group.personal && !group.backfill && (
          <button
            className="shrink-0 text-faint hover:text-danger"
            title="Delete group"
            onClick={() => void remove()}
          >
            <Trash2 className="size-3.5" />
          </button>
        )}
      </div>

      {open && (
        <div className="mt-2.5 space-y-2.5 pl-5.5">
          {/* The label is what Plex shows on the item. Worth surfacing:
              it is what you would search for over there. */}
          <p className="text-[11.5px] text-faint">
            Plex tag <code className="rounded bg-surface3 px-1 py-0.5">{group.label}</code>
          </p>

          {!group.personal && (
            <>
              <div className="flex gap-2">
                <Input
                  value={name}
                  onChange={e => setName(e.target.value)}
                  onBlur={() => void rename()}
                  onKeyDown={e => { if (e.key === "Enter") void rename() }}
                />
              </div>
              {group.backfill && (
                <p className="text-[11.5px] text-muted-foreground">
                  What your library already held. Everybody is in it, so nothing new
                  is ever added here — otherwise everything anyone asked for would
                  reach all of them and nothing would ever be split.
                </p>
              )}
              {/* An install that never had a back catalogue still needs a
                  way to say "the whole house", and an ordinary group
                  cannot be it: ordinary groups are request defaults, so
                  the first person to ask for something would share it
                  with everybody. */}
              {!group.backfill && (
                <label className="flex items-start gap-2 text-[11.5px] text-muted-foreground">
                  <input type="checkbox" className="mt-0.5" checked={group.everyone}
                    onChange={e => void setEveryone(e.target.checked)} />
                  <span>
                    <span className="font-semibold text-foreground">Everyone</span> — new
                    accounts join automatically, and requests never land here unless you
                    pick it yourself. One group can be this.
                  </span>
                </label>
              )}
              <div className="flex flex-wrap gap-1.5">
                {users.map(u => (
                  <button
                    key={u.id}
                    onClick={() => void toggle(u.id)}
                    className={cn(
                      "font-label rounded-md px-2 py-1 text-[11.5px] font-semibold transition-colors",
                      members.has(u.id)
                        ? "bg-brass/15 text-brass hover:bg-brass/25"
                        : "bg-surface3 text-muted-foreground hover:text-foreground",
                    )}
                  >
                    {members.has(u.id) ? "✓ " : ""}{u.username}
                  </button>
                ))}
              </div>
            </>
          )}
          {group.personal && (
            <p className="text-[11.5px] text-muted-foreground">
              Where this account's own titles go. It always has exactly one member.
            </p>
          )}
        </div>
      )}
    </div>
  )
}

// Most installs arrive with a Plex library already full and already
// shared with everybody. Splitting that retroactively would take away
// access people already have, so the first move is to grant what exists
// to a group holding everyone.
function SeedCard({ groups, onSeeded }: { groups: ApiGroup[]; onSeeded: () => void }) {
  const done = groups.find(g => g.backfill)
  const [busy, setBusy] = useState(false)

  async function seed() {
    setBusy(true)
    try {
      const res = await api.seedSharing()
      toast.success(res.added === 0
        ? "Already up to date — nothing new to share"
        : `Shared ${res.added} existing titles with ${res.members} people`)
      onSeeded()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="rounded-2xl border border-linesoft bg-surface p-5">
      <h2 className="mb-1 text-[15px] font-bold">Share what you already have</h2>
      <p className="mb-3 text-[12.5px] text-muted-foreground">
        Reads your whole Plex library — including everything that was there before
        reely — and shares it with everyone who can see it today, so nobody loses
        anything. Only what is added <em>after</em> this gets split.{" "}
        <strong>Do this before turning anyone on below.</strong> Safe to run twice.
      </p>
      <Button onClick={() => void seed()} disabled={busy}>
        {done ? "Update from Plex" : "Share existing titles"}
      </Button>
      {done && (
        <p className="mt-2 text-[11.5px] text-faint">
          Held by <strong>{done.name}</strong> — {done.titles} titles,{" "}
          {done.members} {done.members === 1 ? "person" : "people"}. Someone who
          joins later is not added to it: tick them into it above if you want them
          to have your back catalogue too.
        </p>
      )}
    </div>
  )
}

// Turning management on for somebody narrows what they can see in Plex,
// so it is one switch per person and off by default. Nothing about
// their share changes until it is on.
function ManagedCard({ users }: { users: ApiUser[] }) {
  const statesQuery = useApi(() => api.shareStates())
  const states = new Map((statesQuery.data ?? []).map(s => [s.userId, s]))
  // Who this server is shared with, as Plex holds it. Failing to reach
  // plex.tv is not an error here — it costs the "watches on" picker and
  // nothing else, and the switch beside it still works.
  const accountsQuery = useApi(() => api.plexAccounts())
  const accounts = accountsQuery.data?.accounts ?? []
  return (
    <div className="rounded-2xl border border-linesoft bg-surface p-5">
      <h2 className="mb-1 text-[15px] font-bold">Manage in Plex</h2>
      <p className="mb-3 text-[12.5px] text-muted-foreground">
        While this is off, reely never touches that person's Plex share and they keep
        seeing whatever they see today. Turning it on restricts them to the titles
        their groups hold — so share your existing library first.
      </p>
      {users.map(u => (
        <ManagedRow
          key={u.id}
          user={u}
          initial={states.get(u.id)?.managed ?? false}
          watchesOn={states.get(u.id)?.shareAccountId ?? 0}
          signsInAs={states.get(u.id)?.plexAccountId ?? 0}
          accounts={accounts}
          onChanged={() => statesQuery.reload({ quiet: true })}
        />
      ))}
    </div>
  )
}

function ManagedRow({ user, initial, watchesOn, signsInAs, accounts, onChanged }: {
  user: ApiUser
  initial: boolean
  watchesOn: number
  signsInAs: number
  accounts: ApiPlexAccount[]
  onChanged: () => void
}) {
  const [on, setOn] = useState(initial)
  const [busy, setBusy] = useState(false)

  // the switches render before their state arrives, so follow it in
  useEffect(() => { setOn(initial) }, [initial])

  async function change(next: boolean) {
    setBusy(true)
    setOn(next)
    try {
      await api.setManaged(user.id, next)
    } catch (e) {
      setOn(!next)
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }

  async function watch(accountId: number) {
    setBusy(true)
    try {
      await api.setManaged(user.id, on, accountId)
      onChanged()
      toast.success(accountId === 0
        ? `${user.username} is back on the account they sign in with`
        : `${user.username} watches on ${nameOf(accounts, accountId)}`)
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }

  // Somebody already claiming a share cannot be given away twice: two
  // accounts on one share would each write the whole restriction, and
  // the reconciler refuses both rather than pick. Better to not offer it.
  const own = accounts.find(a => a.accountId === signsInAs)

  return (
    <div className="border-b border-linesoft py-2 text-[13px] last:border-0">
      <div className="flex items-center justify-between gap-3">
        <span className="font-semibold">{user.username}</span>
        <div className="flex items-center gap-2">
          {accounts.length > 0 && (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <button disabled={busy}
                  className={cn("mono-label rounded-md px-2 py-1",
                    watchesOn
                      ? "bg-brass/15 text-brass hover:bg-brass/25"
                      : "bg-surface3 text-muted-foreground hover:text-foreground")}>
                  {watchesOn ? `watches as ${nameOf(accounts, watchesOn)}` : "own account"} ▾
                </button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="rounded-xl">
                <DropdownMenuLabel className="mono-label text-faint">
                  Which Plex account do they watch on?
                </DropdownMenuLabel>
                <DropdownMenuItem onClick={() => void watch(0)}>
                  {watchesOn === 0 ? "✓ " : "   "}
                  The one they sign in with{own ? ` — ${own.username}` : ""}
                </DropdownMenuItem>
                {accounts.map(a => (
                  <DropdownMenuItem key={a.accountId} onClick={() => void watch(a.accountId)}>
                    {watchesOn === a.accountId ? "✓ " : "   "}{a.username || a.email}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
          )}
          <Switch checked={on} disabled={busy} onCheckedChange={v => void change(v)} />
        </div>
      </div>
      {watchesOn > 0 && !own && signsInAs === 0 && (
        <p className="mt-1 text-[12px] text-muted-foreground">
          This account has no Plex sign-in of its own, so its titles are written to
          the share above — which is what an owner wants: they cannot be restricted
          on their own server.
        </p>
      )}
    </div>
  )
}

function nameOf(accounts: ApiPlexAccount[], id: number): string {
  const a = accounts.find(x => x.accountId === id)
  return a ? (a.username || a.email) : `account ${id}`
}
