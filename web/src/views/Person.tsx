import { useState } from "react"
import { api } from "@/api"
import type { ApiPersonCredit } from "@/api"
import { useApi } from "@/hooks/use-api"
import { cn, img } from "@/lib/utils"

// The person page: who they are and everything they've been in, with the
// titles already in the library lighting up as links back into it.
export function PersonView({ tmdbId, onBack, onOpenCredit }: {
  tmdbId: number
  onBack: () => void
  onOpenCredit: (credit: ApiPersonCredit) => void
}) {
  const { data, error, loading } = useApi(() => api.person(tmdbId))
  const [bioOpen, setBioOpen] = useState(false)

  if (loading) return <p className="mono-label py-4 text-faint">loading…</p>
  if (error || !data) {
    return (
      <section>
        <BackButton onBack={onBack} />
        <p className="mono-label py-4 text-want">{error ?? "person not found"}</p>
      </section>
    )
  }
  const { person: p, imageBase } = data
  const credits = data.credits ?? []
  const inLibrary = credits.filter(c => c.localId).length
  const facts = [
    p.knownFor && p.knownFor !== "Acting" ? p.knownFor : null,
    lifespan(p.birthday, p.deathday),
    p.placeOfBirth || null,
  ].filter(Boolean).join("  ·  ")

  return (
    <section>
      <BackButton onBack={onBack} />
      <div className="flex flex-col gap-6 sm:flex-row">
        <div className="w-[150px] shrink-0 sm:w-[180px]">
          <div className="relative aspect-2/3 overflow-hidden rounded-[10px] bg-surface2 poster-shadow">
            {p.photo
              ? <img src={img(imageBase, "w342", p.photo)} alt="" className="absolute inset-0 h-full w-full object-cover" />
              : <div className="flex h-full items-center justify-center p-3 text-center"><span className="font-display text-[15px] font-bold leading-tight">{p.name}</span></div>}
          </div>
        </div>
        <div className="min-w-0 flex-1">
          <h1 className="font-display text-[32px] font-bold leading-tight tracking-tight text-balance">{p.name}</h1>
          {facts && <div className="mono-label mt-1 text-muted-foreground">{facts}</div>}
          {p.biography ? (
            <>
              <p className={cn("mt-4 max-w-[68ch] whitespace-pre-line text-[13.5px] leading-relaxed text-muted-foreground",
                !bioOpen && "line-clamp-6")}>
                {p.biography}
              </p>
              {p.biography.length > 420 && (
                <button onClick={() => setBioOpen(o => !o)} className="mono-label mt-1.5 text-faint hover:text-brass">
                  {bioOpen ? "less ↑" : "more ↓"}
                </button>
              )}
            </>
          ) : (
            <p className="mono-label mt-4 text-faint">
              no biography — add a TMDB API key in Settings for the full picture
            </p>
          )}
        </div>
      </div>

      <div className="mt-8">
        <div className="mono-label mb-2.5 text-faint">
          Filmography · {credits.length} title{credits.length === 1 ? "" : "s"}
          {inLibrary > 0 && ` · ${inLibrary} in your library`}
        </div>
        {credits.length === 0 && <p className="mono-label border-t py-3 text-faint">nothing on record</p>}
        <div className="grid grid-cols-[repeat(auto-fill,minmax(118px,1fr))] gap-x-3.5 gap-y-5">
          {credits.map(c => <CreditCell key={`${c.kind}-${c.tmdbId}`} credit={c} imageBase={imageBase}
            onOpen={c.localId ? () => onOpenCredit(c) : undefined} />)}
        </div>
      </div>
    </section>
  )
}

function CreditCell({ credit: c, imageBase, onOpen }: {
  credit: ApiPersonCredit; imageBase: string; onOpen?: () => void
}) {
  const Cell = onOpen ? "button" : "div"
  return (
    <Cell onClick={onOpen} className={cn("block w-full text-left", onOpen && "group cursor-pointer")}>
      <div className="relative aspect-2/3 overflow-hidden rounded-[10px] bg-surface2 poster-shadow transition-transform group-hover:scale-[1.025]">
        {c.poster
          ? <img src={img(imageBase, "w342", c.poster)} alt="" loading="lazy" className="absolute inset-0 h-full w-full object-cover" />
          : <div className="flex h-full items-center justify-center p-3 text-center"><span className="font-display text-[13px] font-bold leading-tight">{c.title}</span></div>}
        {c.localId && (
          <span className={cn(
            "absolute -top-1 right-[9%] h-[27%] w-[15px] drop-shadow-sm [clip-path:polygon(0_0,100%_0,100%_100%,50%_82%,0_100%)]",
            c.onDisk ? "bg-good" : "bg-want")} />
        )}
      </div>
      <div className={cn("mt-1.5 text-[12.5px] font-semibold leading-tight", onOpen && "group-hover:text-brass")}>{c.title}</div>
      <div className="truncate text-[11.5px] text-muted-foreground" title={c.character}>
        {[c.year || null, c.character].filter(Boolean).join(" · ")}
      </div>
    </Cell>
  )
}

function lifespan(birthday: string, deathday: string): string | null {
  if (!birthday) return null
  const born = birthday.slice(0, 4)
  return deathday ? `${born}–${deathday.slice(0, 4)}` : `b. ${born}`
}

function BackButton({ onBack }: { onBack: () => void }) {
  return (
    <button onClick={onBack} className="mono-label mb-5 flex items-center gap-1.5 text-muted-foreground hover:text-brass">
      ← Back
    </button>
  )
}
