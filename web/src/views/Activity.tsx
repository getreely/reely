import { useEffect, useMemo, useState } from "react"
import { api } from "@/api"
import type {
  ApiBlocklistEntry, ApiHistoryEntry, ApiImportProblem, ApiQueueItem, ApiQueuedSearch,
} from "@/api"
import { useApi } from "@/hooks/use-api"
import { Tag, fmtSize } from "@/components/detail"
import { Button } from "@/components/ui/button"
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select"
import { Ban, Pause, Play } from "lucide-react"
import { toast } from "sonner"
import { cn } from "@/lib/utils"

// Activity: what's downloading, what's waiting to be searched for, what's
// stuck (with the tools to unstick it), what reely has done lately, and
// which releases are banned from automatic re-grabs.
//
// Downloads and searches are separate tabs because they are separate
// things: a download is a file arriving, a search is reely still looking.
// Both page server-side, both filter across the WHOLE queue rather than
// the visible page, and both select and cancel in bulk — so "narrow it,
// select all, cancel" works on the thing you meant, not on page one.
type ActivityTab = "queue" | "searches" | "history" | "blocklist"

const PAGE = 50

export function ActivityView() {
  const [histPage, setHistPage] = useState(0)
  const [blockPage, setBlockPage] = useState(0)
  const [queuePage, setQueuePage] = useState(0)
  const [searchPage, setSearchPage] = useState(0)
  const [queueFilter, setQueueFilter] = useState("")
  const [searchFilter, setSearchFilter] = useState("")
  const { data, loading, reload } = useApi(
    () => api.activity(PAGE,
      { history: histPage * PAGE, queue: queuePage * PAGE, searches: searchPage * PAGE },
      { queue: queueFilter, searches: searchFilter }),
    5_000)
  const blockQuery = useApi(() => api.blocklist(PAGE, blockPage * PAGE), 30_000)
  // the hook reads the freshest closure, so a page flip or a filter edit
  // just re-fetches
  useEffect(() => { reload({ quiet: true }) }, [histPage, queuePage, searchPage, queueFilter, searchFilter, reload])
  useEffect(() => { blockQuery.reload({ quiet: true }) }, [blockPage]) // eslint-disable-line react-hooks/exhaustive-deps
  const [tab, setTab] = useState<ActivityTab>("queue")
  const queue = data?.queue ?? []
  const queueTotal = data?.queueTotal ?? queue.length
  const problems = data?.problems ?? []
  const history = data?.history ?? []
  const historyTotal = data?.historyTotal ?? history.length
  const searches = data?.searches ?? []
  const searchesTotal = data?.searchesTotal ?? searches.length
  const blocklist = blockQuery.data?.blocklist ?? []
  const blockTotal = blockQuery.data?.total ?? blocklist.length

  // a filter that empties the last page would otherwise strand the user on
  // a page number that no longer exists
  useEffect(() => { setQueuePage(0) }, [queueFilter])
  useEffect(() => { setSearchPage(0) }, [searchFilter])

  const tabs: { id: ActivityTab; label: string; count: number }[] = [
    { id: "queue", label: "Downloads", count: queueTotal + problems.length },
    { id: "searches", label: "Searches", count: searchesTotal },
    { id: "history", label: "History", count: historyTotal },
    { id: "blocklist", label: "Blocklist", count: blockTotal },
  ]

  return (
    <section className="max-w-[760px]">
      <h1 className="font-display text-[28px] font-bold tracking-tight">Activity</h1>
      <p className="mono-label mb-4 mt-0.5 text-faint">
        {loading ? "loading…" : `${queueTotal} downloading · ${problems.length} need attention` +
          (searchesTotal > 0 ? ` · ${searchesTotal} searches queued` : "")}
      </p>

      <nav className="mb-6 flex w-full flex-row flex-wrap gap-x-1 border-b">
        {tabs.map(t => (
          <button key={t.id} onClick={() => setTab(t.id)}
            className={cn(
              "-mb-px flex items-center gap-1.5 border-b-2 border-transparent px-3 py-2.5 text-[13px] font-medium text-muted-foreground hover:text-foreground",
              tab === t.id && "border-brass text-brass"
            )}>
            {t.label} {t.count > 0 && <span className="text-faint">{t.count}</span>}
          </button>
        ))}
      </nav>

      {tab === "queue" && (
        <>
          <QueuePanel items={queue} total={queueTotal} page={queuePage} onPage={setQueuePage}
            filter={queueFilter} onFilter={setQueueFilter} onChanged={() => reload({ quiet: true })} />
          {problems.length > 0 && (
            <Section title="Needs attention" empty={false} emptyText="">
              {problems.map(p => <ProblemRow key={p.nzoId} problem={p} onChanged={() => reload({ quiet: true })} />)}
            </Section>
          )}
        </>
      )}

      {tab === "searches" && (
        <SearchPanel items={searches} total={searchesTotal} page={searchPage} onPage={setSearchPage}
          filter={searchFilter} onFilter={setSearchFilter} onChanged={() => reload({ quiet: true })} />
      )}

      {tab === "history" && (
        <Section title="History" empty={history.length === 0} emptyText="nothing yet — grab something">
          {history.map(hist => (
            <HistoryRow key={hist.id} entry={hist}
              onChanged={() => { reload({ quiet: true }); blockQuery.reload({ quiet: true }) }} />
          ))}
          <Pager page={histPage} total={historyTotal} onPage={setHistPage} />
        </Section>
      )}

      {tab === "blocklist" && (
        <>
          <BlocklistPanel entries={blocklist} onChanged={() => blockQuery.reload({ quiet: true })} />
          <Pager page={blockPage} total={blockTotal} onPage={setBlockPage} />
        </>
      )}
    </section>
  )
}

// useSelection is the select-some / select-all bookkeeping both the
// download and search panels need. Selection is keyed by a stable string
// so it survives the 5-second refresh; ids that have since left the list
// are pruned rather than counted, otherwise "cancel 3" could act on rows
// that finished while you were reading them.
function useSelection(presentIds: string[]) {
  const [selected, setSelected] = useState<Set<string>>(new Set())
  const present = useMemo(() => new Set(presentIds), [presentIds])
  const live = useMemo(() => [...selected].filter(id => present.has(id)), [selected, present])
  const allSelected = presentIds.length > 0 && live.length === presentIds.length
  const toggle = (id: string) => setSelected(prev => {
    const next = new Set(prev)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    return next
  })
  const toggleAll = () => setSelected(allSelected ? new Set() : new Set(presentIds))
  const clear = () => setSelected(new Set())
  return { selected: live, allSelected, toggle, toggleAll, clear, has: (id: string) => selected.has(id) }
}

// Toolbar is the filter box plus the select-all control and whatever bulk
// action the panel offers. The filter runs server-side across the whole
// queue, so "select all" after filtering covers the page you can see and
// the count tells you how much matched in total.
function Toolbar({ filter, onFilter, placeholder, allSelected, onToggleAll, shown, total, children }: {
  filter: string
  onFilter: (v: string) => void
  placeholder: string
  allSelected: boolean
  onToggleAll: () => void
  shown: number
  total: number
  children?: React.ReactNode
}) {
  // the box is typed into locally and settles before it reaches the
  // server: filtering is a query param on a list that already polls every
  // five seconds, so a fetch per keystroke would be a fetch per keystroke
  // times the whole queue
  const [draft, setDraft] = useState(filter)
  useEffect(() => { setDraft(filter) }, [filter])
  useEffect(() => {
    if (draft === filter) return
    const id = setTimeout(() => onFilter(draft), 250)
    return () => clearTimeout(id)
  }, [draft]) // eslint-disable-line react-hooks/exhaustive-deps

  return (
    <div className="mb-2.5 flex flex-wrap items-center gap-2.5">
      <input
        value={draft}
        onChange={e => setDraft(e.target.value)}
        placeholder={placeholder}
        aria-label={placeholder}
        className="h-7 min-w-[190px] flex-1 rounded-lg border border-linesoft bg-surface2 px-2.5 text-[12.5px] outline-none placeholder:text-faint focus:border-brass/60"
      />
      <label className="flex shrink-0 cursor-pointer items-center gap-2 text-[12.5px] text-muted-foreground">
        <input type="checkbox" checked={allSelected} onChange={onToggleAll} className="accent-brass" />
        Select all
      </label>
      {children}
      <span className="mono-label shrink-0 text-faint">
        {shown === total ? `${total}` : `${shown} of ${total}`}
        {filter ? " matching" : ""}
      </span>
    </div>
  )
}

// QueuePanel: what SAB is downloading now. Cancelling takes the partial
// files with it — a cancel that left half a release in the incomplete
// folder would just leak disk.
function QueuePanel({ items, total, page, onPage, filter, onFilter, onChanged }: {
  items: ApiQueueItem[]; total: number; page: number; onPage: (p: number) => void
  filter: string; onFilter: (v: string) => void; onChanged: () => void
}) {
  const ids = useMemo(() => items.map(i => i.nzo_id), [items])
  const sel = useSelection(ids)
  const [busy, setBusy] = useState(false)

  const cancel = async (arg: { nzoIds?: string[]; all?: boolean }, blocklist = false) => {
    const n = arg.all ? total : (arg.nzoIds?.length ?? 0)
    const what = !arg.all && n === 1
      ? `Cancel "${items.find(i => i.nzo_id === arg.nzoIds![0])?.filename ?? "this download"}"?`
      : arg.all && filter
        ? `Cancel all ${n} downloads matching “${filter}”?`
        : `Cancel ${n} downloads?`
    const consequence = blocklist
      ? "The partly downloaded files are deleted, and the release goes on the blocklist so it won't be grabbed again."
      : "The partly downloaded files are deleted too. The release stays off the blocklist — it can be grabbed again."
    if (!window.confirm(`${what}\n\n${consequence}`)) return
    setBusy(true)
    try {
      const res = await api.activityCancel({ ...arg, filter, blocklist })
      toast.success(res.cancelled === 1
        ? blocklist ? "Download cancelled and release blocklisted" : "Download cancelled"
        : blocklist ? `${res.cancelled} downloads cancelled and blocklisted` : `${res.cancelled} downloads cancelled`)
      if (res.failed > 0) toast.error(`${res.failed} couldn't be cancelled — check the logs`)
      sel.clear()
      onChanged()
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setBusy(false) }
  }

  // Pausing is per job, so the rest of the queue carries on. The row's
  // own status decides which way the button goes — SAB says "Paused" and
  // qBittorrent's paused/stopped states are worded the same on the way
  // out, so one check covers both clients.
  const togglePause = async (nzoId: string, paused: boolean) => {
    setBusy(true)
    try {
      if (paused) {
        await api.activityResume([nzoId])
        toast.success("Resumed")
      } else {
        await api.activityPause([nzoId])
        toast.success("Paused — what has downloaded is kept")
      }
      onChanged()
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setBusy(false) }
  }

  const setPriority = async (nzoId: string, priority: number) => {
    setBusy(true)
    try {
      await api.activityPriority([nzoId], priority)
      toast.success(priority === 2 ? "Forced — starting now" : "Priority changed")
      onChanged()
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setBusy(false) }
  }

  return (
    <div className="mb-8">
      <h2 className="font-display mb-1 text-lg font-bold">Downloading</h2>
      {total === 0 && !filter
        ? <div className="border-t"><p className="mono-label py-3 text-faint">nothing in the queue</p></div>
        : (
          <>
            <Toolbar filter={filter} onFilter={onFilter} placeholder="Filter by release name…"
              allSelected={sel.allSelected} onToggleAll={sel.toggleAll} shown={items.length} total={total}>
              {sel.selected.length > 0 ? (
                <>
                  <Button variant="destructive" className="h-7 px-2.5 text-[12px]" disabled={busy}
                    onClick={() => void cancel({ nzoIds: sel.selected })}>
                    Cancel {sel.selected.length}
                  </Button>
                  <Button variant="outline" className="h-7 px-2.5 text-[12px] text-want" disabled={busy}
                    title="Cancel and blocklist — these releases won't be grabbed again"
                    onClick={() => void cancel({ nzoIds: sel.selected }, true)}>
                    <Ban className="mr-1 h-3.5 w-3.5" /> Cancel + blocklist
                  </Button>
                </>
              ) : total > 0 && (
                <Button variant="outline" className="h-7 px-2.5 text-[12px]" disabled={busy}
                  onClick={() => void cancel({ all: true })}>
                  Cancel all {total}{filter ? " matching" : ""}
                </Button>
              )}
            </Toolbar>
            <div className="border-t">
              {items.length === 0
                ? <p className="mono-label py-3 text-faint">no downloads match “{filter}”</p>
                : items.map(q => (
                  <QueueRow key={q.nzo_id} item={q} selected={sel.has(q.nzo_id)}
                    onToggle={() => sel.toggle(q.nzo_id)} busy={busy}
                    onCancel={() => void cancel({ nzoIds: [q.nzo_id] })}
                    onCancelBlock={() => void cancel({ nzoIds: [q.nzo_id] }, true)}
                    onPriority={p => void setPriority(q.nzo_id, p)}
                    onTogglePause={paused => void togglePause(q.nzo_id, paused)} />
                ))}
              <Pager page={page} total={total} onPage={onPage} />
            </div>
          </>
        )}
    </div>
  )
}

// SearchPanel: titles waiting for their paced turn at the indexers. This
// queue lives in memory, so it empties on restart — worth saying plainly
// rather than letting it look like a bug.
function SearchPanel({ items, total, page, onPage, filter, onFilter, onChanged }: {
  items: ApiQueuedSearch[]; total: number; page: number; onPage: (p: number) => void
  filter: string; onFilter: (v: string) => void; onChanged: () => void
}) {
  const ids = useMemo(() => items.map(i => i.key), [items])
  const sel = useSelection(ids)
  const [busy, setBusy] = useState(false)

  const cancel = async (arg: { keys?: string[]; all?: boolean }) => {
    const n = arg.all ? total : (arg.keys?.length ?? 0)
    if (arg.all && !window.confirm(
      `Cancel ${filter ? `all ${total} queued searches matching “${filter}”` : `all ${total} queued searches`}?` +
      `\n\nNothing is deleted — the titles stay monitored, and reely simply stops looking for them now. Anything still missing is picked up by the next scheduled pass.`)) return
    setBusy(true)
    try {
      const res = await api.searchesCancel({ ...arg, filter })
      toast.success(res.cancelled === 1 ? "Search cancelled" : `${res.cancelled} searches cancelled`)
      if (res.cancelled < n) toast(`${n - res.cancelled} had already run`)
      sel.clear()
      onChanged()
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setBusy(false) }
  }

  return (
    <div className="mb-8">
      <h2 className="font-display mb-1 text-lg font-bold">Queued searches</h2>
      <p className="mb-2 max-w-[62ch] text-[12.5px] text-muted-foreground">
        Adding or monitoring a title queues one active indexer search for it. They run a few per
        tick so a big show doesn't hammer your indexers. The queue is held in memory — a restart
        clears it, and anything still missing is picked up by the next scheduled pass.
      </p>
      {total === 0 && !filter
        ? <div className="border-t"><p className="mono-label py-3 text-faint">nothing queued — everything monitored has been looked for</p></div>
        : (
          <>
            <Toolbar filter={filter} onFilter={onFilter} placeholder="Filter by title…"
              allSelected={sel.allSelected} onToggleAll={sel.toggleAll} shown={items.length} total={total}>
              {sel.selected.length > 0 && (
                <Button variant="destructive" className="h-7 px-2.5 text-[12px]" disabled={busy}
                  onClick={() => void cancel({ keys: sel.selected })}>
                  Cancel {sel.selected.length}
                </Button>
              )}
              {sel.selected.length === 0 && total > 0 && (
                <Button variant="outline" className="h-7 px-2.5 text-[12px]" disabled={busy}
                  onClick={() => void cancel({ all: true })}>
                  Cancel all {total}{filter ? " matching" : ""}
                </Button>
              )}
            </Toolbar>
            <div className="border-t">
              {items.length === 0
                ? <p className="mono-label py-3 text-faint">no searches match “{filter}”</p>
                : items.map(q => (
                  <SearchRow key={q.key} item={q} selected={sel.has(q.key)}
                    onToggle={() => sel.toggle(q.key)} busy={busy}
                    onCancel={() => void cancel({ keys: [q.key] })} />
                ))}
              <Pager page={page} total={total} onPage={onPage} />
            </div>
          </>
        )}
    </div>
  )
}

// SearchRow is one waiting search: what it's for, and a way to call it off.
function SearchRow({ item, selected, onToggle, busy, onCancel }: {
  item: ApiQueuedSearch; selected: boolean; onToggle: () => void; busy: boolean; onCancel: () => void
}) {
  const what = item.kind === "episode"
    ? `S${String(item.season ?? 0).padStart(2, "0")}E${String(item.episode ?? 0).padStart(2, "0")}`
    : "movie"
  return (
    <div className="flex items-center gap-2.5 border-b border-linesoft px-1 py-2.5">
      <input type="checkbox" checked={selected} onChange={onToggle} className="accent-brass"
        aria-label={`Select ${item.title}`} />
      <div className="min-w-0 flex-1">
        <div className="truncate text-[12.5px] font-semibold" title={item.title}>{item.title}</div>
        <div className="font-label truncate text-[11px] text-faint">{what}</div>
      </div>
      <Tag kind="dim" className="shrink-0">queued</Tag>
      <button className="shrink-0 text-want opacity-70 hover:opacity-100 disabled:opacity-30"
        title="Cancel this search" disabled={busy} onClick={onCancel}>
        ✕
      </button>
    </div>
  )
}

// BlocklistPanel lists banned releases with single and bulk pardons —
// deleting an entry lets the automatic grab paths fetch that release again.
function BlocklistPanel({ entries, onChanged }: {
  entries: ApiBlocklistEntry[]; onChanged: () => void
}) {
  const [selected, setSelected] = useState<Set<number>>(new Set())
  const [busy, setBusy] = useState(false)
  const allSelected = entries.length > 0 && entries.every(e => selected.has(e.id))

  const toggle = (id: number) => setSelected(prev => {
    const next = new Set(prev)
    if (next.has(id)) next.delete(id)
    else next.add(id)
    return next
  })
  const toggleAll = () => setSelected(allSelected ? new Set() : new Set(entries.map(e => e.id)))

  const remove = async (ids: number[]) => {
    setBusy(true)
    try {
      await api.blocklistRemove(ids)
      toast.success(ids.length === 1 ? "Release un-blocked" : `${ids.length} releases un-blocked`)
      setSelected(new Set())
      onChanged()
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setBusy(false) }
  }

  if (entries.length === 0) {
    return (
      <p className="mono-label border-t py-3 text-faint">
        empty — releases land here when their download fails, and stay off the automatic grab lists until removed
      </p>
    )
  }
  return (
    <div>
      <div className="mb-2 flex items-center gap-2.5">
        <label className="flex cursor-pointer items-center gap-2 text-[12.5px] text-muted-foreground">
          <input type="checkbox" checked={allSelected} onChange={toggleAll} className="accent-brass" />
          Select all
        </label>
        {selected.size > 0 && (
          <Button variant="outline" className="h-7 px-2.5 text-[12px]" disabled={busy}
            onClick={() => void remove([...selected])}>
            Remove {selected.size} from blocklist
          </Button>
        )}
      </div>
      <div className="border-t">
        {entries.map(e => (
          <div key={e.id} className="flex items-center gap-2.5 border-b border-linesoft px-1 py-2.5">
            <input type="checkbox" checked={selected.has(e.id)} onChange={() => toggle(e.id)}
              className="accent-brass" aria-label={`Select ${e.releaseTitle}`} />
            <div className="min-w-0 flex-1">
              <div className="truncate text-[12.5px] font-semibold" title={e.releaseTitle}>{e.releaseTitle}</div>
              <div className="font-label truncate text-[11px] text-faint">
                {/* the indexer matters: the ban covers that indexer's copy,
                    so the same name from another one is still grabbable */}
                {[e.forTitle, e.indexer ? `at ${e.indexer}` : "every indexer", e.reason]
                  .filter(Boolean).join(" · ")}
              </div>
            </div>
            <span className="mono-label shrink-0 text-faint">{e.createdAt.slice(0, 16).replace("T", " ")}</span>
            <button className="shrink-0 text-want opacity-70 hover:opacity-100" title="Remove from blocklist"
              disabled={busy} onClick={() => void remove([e.id])}>
              ✕
            </button>
          </div>
        ))}
      </div>
    </div>
  )
}

// Pager: prev/next over fixed pages of PAGE rows; hidden when one page holds
// everything.
function Pager({ page, total, onPage }: { page: number; total: number; onPage: (p: number) => void }) {
  const pages = Math.max(1, Math.ceil(total / PAGE))
  if (pages <= 1) return null
  return (
    <div className="flex items-center justify-center gap-3 py-2.5">
      <button className="mono-label text-faint hover:text-brass disabled:opacity-40" disabled={page === 0}
        onClick={() => onPage(page - 1)}>‹ prev</button>
      <span className="mono-label text-faint">page {page + 1} of {pages}</span>
      <button className="mono-label text-faint hover:text-brass disabled:opacity-40" disabled={page >= pages - 1}
        onClick={() => onPage(page + 1)}>next ›</button>
    </div>
  )
}

function Section({ title, empty, emptyText, children }: {
  title: string; empty: boolean; emptyText: string; children: React.ReactNode
}) {
  return (
    <div className="mb-8">
      <h2 className="font-display mb-1 text-lg font-bold">{title}</h2>
      <div className="border-t">
        {empty ? <p className="mono-label py-3 text-faint">{emptyText}</p> : children}
      </div>
    </div>
  )
}

// PRIORITIES is SAB's own scale; Force jumps the queue and starts now.
const PRIORITIES = [
  { value: 2, label: "Force" }, { value: 1, label: "High" },
  { value: 0, label: "Normal" }, { value: -1, label: "Low" },
]

function QueueRow({ item, selected, onToggle, busy, onCancel, onCancelBlock, onPriority, onTogglePause }: {
  item: ApiQueueItem; selected: boolean; onToggle: () => void; busy: boolean
  onCancel: () => void; onCancelBlock: () => void; onPriority: (p: number) => void
  onTogglePause: (paused: boolean) => void
}) {
  const torrent = item.protocol === "torrent"
  const paused = item.status === "Paused"
  const done = item.mb > 0 ? Math.round(((item.mb - item.mbleft) / item.mb) * 100) : item.percentage
  const current = PRIORITIES.find(p => p.label.toLowerCase() === (item.priority || "normal").toLowerCase())?.value ?? 0
  return (
    <div className="flex items-start gap-2.5 border-b border-linesoft px-1 py-2.5">
      <input type="checkbox" checked={selected} onChange={onToggle} className="mt-1 accent-brass"
        aria-label={`Select ${item.filename}`} />
      <div className="min-w-0 flex-1">
        <div className="flex items-baseline gap-2">
          <span className="font-label min-w-0 flex-1 truncate text-[12px]" title={item.filename}>{item.filename}</span>
          <select value={current} disabled={busy} aria-label="Download priority"
            title={torrent
              ? "Priority in qBittorrent — it moves a torrent to the top or the bottom of the queue, so Force and High both mean top, Low means bottom"
              : "Priority in SABnzbd — Force starts this download immediately"}
            onChange={e => onPriority(Number(e.target.value))}
            className="mono-label shrink-0 rounded border border-linesoft bg-surface2 px-1 py-0.5 text-faint">
            {PRIORITIES.map(p => <option key={p.value} value={p.value}>{p.label}</option>)}
          </select>
          {/* which client has this row — the queue merges both, and which
              one is working on something is the first thing worth knowing
              when an install runs the two */}
          {item.protocol && (
            <Tag kind={torrent ? "torrent" : "usenet"} className="shrink-0 px-1.5 text-[10px]">
              {torrent ? "TORRENT" : "USENET"}
            </Tag>
          )}
          <span className="mono-label text-faint">{item.status}</span>
        </div>
        <div className="mt-1.5 flex items-center gap-3">
          <div className="h-1.5 flex-1 overflow-hidden rounded-full bg-surface3">
            <div className="h-full rounded-full bg-brass" style={{ width: `${Math.min(done, 100)}%` }} />
          </div>
          <span className="mono-label w-[200px] text-right text-faint">
            {done}% · {fmtSize(Math.round(item.mbleft * (1 << 20)))} left{item.timeleft ? ` · ${item.timeleft}` : ""}
          </span>
        </div>
      </div>
      {/* one button that says what it will do: pause while it runs, play
          while it is held. Pausing keeps what has already arrived, which
          is the whole difference between this and cancelling. */}
      <button className="mt-1 shrink-0 opacity-70 hover:opacity-100 disabled:opacity-30"
        title={paused ? "Resume this download" : "Pause this download — what has arrived is kept"}
        aria-label={paused ? `Resume ${item.filename}` : `Pause ${item.filename}`}
        disabled={busy} onClick={() => onTogglePause(paused)}>
        {paused ? <Play className="h-3.5 w-3.5" /> : <Pause className="h-3.5 w-3.5" />}
      </button>
      <button className="mt-1 shrink-0 text-want opacity-70 hover:opacity-100 disabled:opacity-30"
        title="Cancel and blocklist — this release won't be grabbed again" disabled={busy} onClick={onCancelBlock}>
        <Ban className="h-3.5 w-3.5" />
      </button>
      <button className="mt-0.5 shrink-0 text-want opacity-70 hover:opacity-100 disabled:opacity-30"
        title="Cancel this download and delete its partial files" disabled={busy} onClick={onCancel}>
        ✕
      </button>
    </div>
  )
}

// ProblemRow is a completed download reely couldn't place: retry runs the
// matcher again; the picker hands it to the right title outright.
function ProblemRow({ problem, onChanged }: { problem: ApiImportProblem; onChanged: () => void }) {
  const moviesQuery = useApi(() => api.movies())
  const showsQuery = useApi(() => api.shows())
  const [target, setTarget] = useState("")
  const [busy, setBusy] = useState(false)
  // picking a show unlocks season/episode pins fed by that show's own
  // episode list; "auto" leaves each file's name to resolve itself
  const [seasons, setSeasons] = useState<{ number: number; episodes: number[] }[]>([])
  const [season, setSeason] = useState(0)
  const [episode, setEpisode] = useState(0)
  useEffect(() => {
    setSeason(0); setEpisode(0); setSeasons([])
    if (!target.startsWith("s")) return
    let gone = false
    api.show(Number(target.slice(1))).then(d => {
      if (gone) return
      setSeasons((d.show.seasons ?? []).map(se => ({
        number: se.number, episodes: (se.episodes ?? []).map(e => e.episode),
      })))
    }).catch(() => {})
    return () => { gone = true }
  }, [target])

  const options = useMemo(() => [
    ...(moviesQuery.data?.movies ?? []).map(m => ({ value: `m${m.id}`, label: `${m.title} (${m.year || "?"})` })),
    ...(showsQuery.data?.shows ?? []).map(s => ({ value: `s${s.id}`, label: `${s.title} — show` })),
  ], [moviesQuery.data, showsQuery.data])

  const retry = async () => {
    setBusy(true)
    try {
      await api.activityRetry(problem.nzoId)
      toast.success("Retrying on the next tick")
      onChanged()
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setBusy(false) }
  }
  const del = async () => {
    if (!window.confirm(`Delete "${problem.name}"?\n\nRemoves the downloaded files from the download folder and clears the job. The release stays off the blocklist — it can be grabbed again.`)) return
    setBusy(true)
    try {
      await api.activityDelete(problem.nzoId)
      toast.success("Download deleted")
      onChanged()
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setBusy(false) }
  }
  const resolve = async () => {
    if (!target) return
    setBusy(true)
    try {
      const id = Number(target.slice(1))
      await api.activityResolve(problem.nzoId, target.startsWith("m")
        ? { movieId: id }
        : { showId: id, season, episode })
      toast.success("Imported")
      onChanged()
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setBusy(false) }
  }

  return (
    <div className="border-b border-linesoft px-1 py-2.5">
      <div className="flex items-baseline gap-2">
        <span className="font-label min-w-0 flex-1 truncate text-[12px]" title={problem.name}>{problem.name}</span>
        <Tag kind="want">import failed</Tag>
      </div>
      <div className="mt-1 text-[12px] text-want">{problem.error}</div>
      <div className="mt-2 flex flex-wrap items-center gap-2">
        <Button variant="outline" className="h-7 px-2.5 text-[12px]" disabled={busy} onClick={() => void retry()}>
          Retry import
        </Button>
        <Select value={target || undefined} onValueChange={setTarget}>
          <SelectTrigger className="h-7 w-[240px] text-[12px]">
            <SelectValue placeholder="This is actually…" />
          </SelectTrigger>
          <SelectContent>
            {options.map(o => <SelectItem key={o.value} value={o.value}>{o.label}</SelectItem>)}
          </SelectContent>
        </Select>
        {seasons.length > 0 && (
          <select value={season} disabled={busy} aria-label="Season pin"
            title="Pin every file of this job to one season — 'auto' lets each file's name decide"
            onChange={e => { setSeason(Number(e.target.value)); setEpisode(0) }}
            className="mono-label h-7 rounded border border-linesoft bg-surface2 px-1.5 text-[12px]">
            <option value={0}>Season: auto</option>
            {seasons.map(se => <option key={se.number} value={se.number}>Season {se.number}</option>)}
          </select>
        )}
        {season > 0 && (
          <select value={episode} disabled={busy} aria-label="Episode pin"
            title="Pin a single-file job to one exact episode"
            onChange={e => setEpisode(Number(e.target.value))}
            className="mono-label h-7 rounded border border-linesoft bg-surface2 px-1.5 text-[12px]">
            <option value={0}>Episode: auto</option>
            {(seasons.find(se => se.number === season)?.episodes ?? []).map(e => (
              <option key={e} value={e}>Episode {e}</option>
            ))}
          </select>
        )}
        <Button className="h-7 px-2.5 text-[12px]" disabled={busy || !target} onClick={() => void resolve()}>
          Import as
        </Button>
        <Button variant="destructive" className="ml-auto h-7 px-2.5 text-[12px]" disabled={busy}
          onClick={() => void del()}>
          Delete
        </Button>
      </div>
    </div>
  )
}

const kindTag: Record<string, { kind: "good" | "info" | "want" | "dim" | "brand"; text: string }> = {
  grabbed:  { kind: "brand", text: "grabbed" },
  imported: { kind: "good", text: "imported" },
  failed:   { kind: "want", text: "failed" },
  scanned:  { kind: "info", text: "scanned" },
  removed:  { kind: "dim", text: "removed" },
  migrated: { kind: "info", text: "migrated" },
  added:    { kind: "brand", text: "added" },
}

function HistoryRow({ entry, onChanged }: { entry: ApiHistoryEntry; onChanged: () => void }) {
  let tag = kindTag[entry.kind] ?? { kind: "dim" as const, text: entry.kind }
  let detail: { title?: string; path?: string; error?: string; warnings?: string[]; upgraded?: boolean; upgradedFrom?: string; via?: string } = {}
  try { detail = JSON.parse(entry.detail) } catch { /* older rows may be plain text */ }
  // an import that replaced a lesser file announces itself as the upgrade it is
  if (entry.kind === "imported" && detail.upgraded) tag = { kind: "good", text: "upgraded" }
  const what = entry.title
    ? entry.season ? `${entry.title} S${String(entry.season).padStart(2, "0")}E${String(entry.episode ?? 0).padStart(2, "0")}` : entry.title
    : detail.title ?? "—"
  const [busy, setBusy] = useState(false)
  // a grab that turned out to be the wrong file can be banned right off
  // its history row, so nothing re-downloads it
  const block = async () => {
    if (!window.confirm(`Blocklist "${detail.title ?? what}"?\n\nAutomatic searches won't grab this release again. It can be pardoned later in the Blocklist tab.`)) return
    setBusy(true)
    try {
      const r = await api.historyBlocklist(entry.id)
      toast.success(`"${r.blocked}" blocklisted`)
      onChanged()
    } catch (e) { toast.error(`${e instanceof Error ? e.message : e}`) } finally { setBusy(false) }
  }
  return (
    <div className="flex items-baseline gap-2.5 border-b border-linesoft px-1 py-2">
      <Tag kind={tag.kind} className="w-[76px] text-center">{tag.text}</Tag>
      <div className="min-w-0 flex-1">
        <div className="truncate text-[13px] font-semibold">
          {what}
          {/* which mechanism decided the grab — rss, release day, backlog,
              search missing, search, manual */}
          {detail.via && <Tag kind="dim" className="ml-1.5 px-1.5 text-[10px]">via {detail.via}</Tag>}
        </div>
        <div className="font-label truncate text-[11px] text-faint"
          title={detail.warnings?.length ? detail.warnings.join("\n") : detail.title}>
          {detail.error ?? detail.title ?? ""}
          {detail.upgradedFrom ? ` · was ${detail.upgradedFrom}` : ""}
          {detail.warnings?.length ? ` · ${detail.warnings.length} warning${detail.warnings.length === 1 ? "" : "s"} (hover)` : ""}
        </div>
      </div>
      {entry.kind === "grabbed" && !!detail.title && (
        <button className="shrink-0 self-center text-want opacity-70 hover:opacity-100 disabled:opacity-30"
          title="Blocklist this release — it won't be grabbed again" disabled={busy}
          onClick={() => void block()}>
          <Ban className="h-3.5 w-3.5" />
        </button>
      )}
      <span className="mono-label shrink-0 text-faint">{entry.createdAt.slice(0, 16).replace("T", " ")}</span>
    </div>
  )
}
