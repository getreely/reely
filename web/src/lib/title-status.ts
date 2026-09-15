import type { ApiMovie, ApiShow } from "@/api"

// What a card's status strip says about a title, in one place because
// two views draw it: the owner's library grid and the portal's.
//
// They had every reason to drift — the rules are subtle, and each is a
// judgement somebody made once and would not reproduce from memory. A
// watcher deciding whether a title is worth opening is asking the same
// question the owner is, so it should get the same answer.

// good = on disk, want = wanted and not here yet, dim = not monitored.
export type Strip = "good" | "want" | "dim"

export function movieStatus(m: ApiMovie): { strip: Strip } {
  return { strip: m.filePath ? "good" : m.monitored ? "want" : "dim" }
}

export function showStatus(sh: ApiShow): {
  strip: Strip
  // the share of what was asked for that is here, for the bar. Absent
  // when there is nothing partial to draw.
  progress?: number
  // "6/10" — what the bar is a picture of
  frac?: string
} {
  // complete = nothing left that the sweep would hunt. Aired-but-unmonitored
  // gaps don't keep a show amber, and unaired seasons never did.
  const complete = sh.aired > 0 && sh.wanted === 0
  const strip: Strip = complete ? "good" : sh.onDisk > 0 || sh.monitored ? "want" : "dim"
  // the fraction judges against what is MONITORED: "6/6" means "everything
  // you asked for", so a deliberate gap can't keep a card looking short.
  // onDisk can't be the numerator — a file can back an episode that was
  // since un-monitored, and 7/6 is nonsense. A show with nothing monitored
  // falls back to the factual on-disk/aired so the card still says something.
  const den = sh.monitoredAired
  const have = den - sh.wanted
  const frac = den > 0 ? `${have}/${den}` : sh.aired > 0 ? `${sh.onDisk}/${sh.aired}` : undefined
  const progress = !complete && den > 0 && have > 0 ? have / den : undefined
  return { strip, progress, frac }
}
