import { useMemo, useState } from "react"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "@/components/ui/select"
import { X } from "lucide-react"
import { cn } from "@/lib/utils"

// The library filter builder: each row is field · is / is not · value,
// values suggest from what the library actually holds, and matching is
// fuzzy so the grid narrows live while a value is typed.

export interface LibraryFilter {
  field: string
  op: "is" | "not"
  value: string
}

export const MOVIE_FIELDS = ["Genre", "Year", "Quality", "Source", "Status", "Library"] as const
export const SHOW_FIELDS = ["Genre", "Year", "Status", "Library"] as const

// What somebody browsing on the portal can narrow by. The rest of the
// fields describe the file and where it is filed, which is the owner's
// business: a person deciding what to watch tonight is not choosing
// between source formats.
export const WATCH_FIELDS = ["Genre", "Year"] as const

// uniq is the value list a field offers: present, non-empty, in order.
export const uniq = (vals: string[]) => [...new Set(vals.filter(Boolean))].sort()

// applyFilterRows narrows a list by the rows somebody has built.
//
// Per field: "is" values OR together, "is not" values always exclude.
// Matching is fuzzy so the grid narrows while a value is still being
// typed rather than only once it is spelled out.
//
// Shared rather than written twice: the owner's library and the portal
// offer different FIELDS, but what a filter row MEANS cannot differ
// between them without one of the two being surprising.
export function applyFilterRows<T>(
  list: T[], rows: LibraryFilter[], fieldOf: (row: T, field: string) => string[],
): T[] {
  const byField = new Map<string, { is: string[]; not: string[] }>()
  for (const f of rows) {
    if (f.value.trim() === "") continue
    const g = byField.get(f.field) ?? { is: [], not: [] }
    g[f.op === "not" ? "not" : "is"].push(f.value)
    byField.set(f.field, g)
  }
  for (const [field, g] of byField) {
    list = list.filter(r => {
      const vals = fieldOf(r, field)
      if (g.is.length > 0 && !vals.some(v => g.is.some(w => fuzzyMatch(w, v)))) return false
      if (vals.some(v => g.not.some(w => fuzzyMatch(w, v)))) return false
      return true
    })
  }
  return list
}

// fuzzyScore ranks target against query: null when the query's characters
// don't all appear in order, otherwise higher is better — a whole
// substring (earlier is better) beats scattered letters, and consecutive
// hits beat lone ones. An empty query matches everything at rank 0.
export function fuzzyScore(query: string, target: string): number | null {
  const q = query.trim().toLowerCase()
  if (!q) return 0
  const t = target.toLowerCase()
  const at = t.indexOf(q)
  if (at >= 0) return 1000 - at
  let ti = 0
  let score = 0
  for (const ch of q) {
    const found = t.indexOf(ch, ti)
    if (found < 0) return null
    score += found === ti ? 5 : 1
    ti = found + 1
  }
  return score
}

export const fuzzyMatch = (query: string, target: string) => fuzzyScore(query, target) !== null

// The value box: a free-form input with its own suggestion dropdown —
// opens on focus, fuzzy-ranks while typing, arrows/enter to pick.
function SuggestInput({ value, options, placeholder, onChange }: {
  value: string
  options: string[]
  placeholder: string
  onChange: (v: string) => void
}) {
  const [open, setOpen] = useState(false)
  const [hi, setHi] = useState(0)
  const ranked = useMemo(() => {
    const scored: { o: string; s: number }[] = []
    for (const o of options) {
      const s = fuzzyScore(value, o)
      if (s !== null) scored.push({ o, s })
    }
    scored.sort((a, b) => b.s - a.s || a.o.localeCompare(b.o))
    return scored.map(x => x.o).slice(0, 12)
  }, [options, value])
  const cursor = Math.min(hi, ranked.length - 1)
  const shown = open && ranked.length > 0
  const pick = (v: string) => { onChange(v); setOpen(false) }
  return (
    <div className="relative flex-1">
      <Input
        className="h-9 w-full text-[13px]"
        value={value}
        placeholder={placeholder}
        onChange={e => { onChange(e.target.value); setOpen(true); setHi(0) }}
        onFocus={() => setOpen(true)}
        onBlur={() => setOpen(false)}
        onKeyDown={e => {
          if (!shown) return
          if (e.key === "ArrowDown") { e.preventDefault(); setHi(Math.min(cursor + 1, ranked.length - 1)) }
          else if (e.key === "ArrowUp") { e.preventDefault(); setHi(Math.max(cursor - 1, 0)) }
          else if (e.key === "Enter") { e.preventDefault(); pick(ranked[cursor]) }
          else if (e.key === "Escape") { e.stopPropagation(); setOpen(false) }
        }}
      />
      {shown && (
        <div className="absolute left-0 right-0 top-full z-50 mt-1 max-h-[210px] overflow-y-auto rounded-xl border bg-surface p-1 shadow-md">
          {ranked.map((o, i) => (
            <button key={o} type="button"
              className={cn(
                "block w-full truncate rounded-lg px-2.5 py-1.5 text-left text-[13px]",
                i === cursor ? "bg-surface2 text-foreground" : "text-muted-foreground hover:bg-surface2/60"
              )}
              onMouseDown={e => e.preventDefault()}
              onMouseEnter={() => setHi(i)}
              onClick={() => pick(o)}>
              {o}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

// FilterRows is the builder itself, fed with the fields this kind carries
// and a valuesFor callback drawn from the real library.
export function FilterRows({ rows, onChange, fields, valuesFor }: {
  rows: LibraryFilter[]
  onChange: (rows: LibraryFilter[]) => void
  fields: readonly string[]
  valuesFor: (field: string) => string[]
}) {
  const patch = (i: number, p: Partial<LibraryFilter>) =>
    onChange(rows.map((r, j) => (j === i ? { ...r, ...p } : r)))

  return (
    <div className="flex flex-col gap-2">
      {rows.map((row, i) => (
        <div key={i} className="flex items-center gap-2">
          <Select value={row.field} onValueChange={f => patch(i, { field: f, value: "" })}>
            <SelectTrigger className="h-9 w-[104px] shrink-0"><SelectValue /></SelectTrigger>
            <SelectContent className="rounded-xl">
              {fields.map(f => <SelectItem key={f} value={f}>{f}</SelectItem>)}
            </SelectContent>
          </Select>
          <Select value={row.op} onValueChange={op => patch(i, { op: op as LibraryFilter["op"] })}>
            <SelectTrigger className="h-9 w-[84px] shrink-0"><SelectValue /></SelectTrigger>
            <SelectContent className="rounded-xl">
              <SelectItem value="is">is</SelectItem>
              <SelectItem value="not">is not</SelectItem>
            </SelectContent>
          </Select>
          <SuggestInput value={row.value} placeholder={`${row.field}…`}
            options={valuesFor(row.field)}
            onChange={v => patch(i, { value: v })} />
          <Button variant="outline" size="icon" className="h-9 w-9 shrink-0" aria-label="Remove filter"
            onClick={() => onChange(rows.filter((_, j) => j !== i))}>
            <X className="h-3.5 w-3.5" />
          </Button>
        </div>
      ))}
      <Button variant="outline" className="h-8 w-fit border-dashed"
        onClick={() => onChange([...rows, { field: fields[0], op: "is", value: "" }])}>
        + Add filter
      </Button>
    </div>
  )
}
