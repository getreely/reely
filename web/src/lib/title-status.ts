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

// What a browse card says about a title, as opposed to the strip above:
// a word in the corner, for the rows that show things you may not have.
//
// The states are ordered by what somebody looking at a poster wants to
// know, and only one can be true at a time:
//
//   Downloading  a client is working on it right now
//   In library   it is there and playable
//   Partial      a show with some of what it should have, not all
//   Requested    asked for, or added and still empty
//
// "In library" used to cover everything reely held, so a title added
// seconds ago — nothing on disk, nothing yet downloading — read as ready
// to watch. Being in the catalogue is not the same as being playable.
export type TitleBadge = "Downloading" | "In library" | "Partial" | "Requested"

// A movie is binary: it has its file or it does not.
export function movieBadge(m: ApiMovie | undefined, requested: boolean): TitleBadge | undefined {
  if (!m) return requested ? "Requested" : undefined
  if (m.downloading) return "Downloading"
  if (m.filePath) return "In library"
  // held but empty — somebody asked for this and it has not arrived,
  // which is what the person waiting for it means by "requested"
  return "Requested"
}

// A show is a count, so it has a middle state a movie cannot have.
//
// Complete is showStatus's rule rather than a second one: everything
// monitored and aired is here. Judging against every aired episode
// instead would leave a show with a deliberate gap reading Partial for
// good, and the two badges on the same card would disagree.
export function showBadge(sh: ApiShow | undefined, requested: boolean): TitleBadge | undefined {
  if (!sh) return requested ? "Requested" : undefined
  if (sh.downloading) return "Downloading"
  if (sh.onDisk === 0) return "Requested"
  return showStatus(sh).strip === "good" ? "In library" : "Partial"
}

// badgeTone maps a state onto the colour language the app already uses:
// green for here, amber for wanted, blue for partly here, and the brass
// the download progress bar is drawn in for something actively arriving.
export function badgeTone(badge: TitleBadge | undefined): "good" | "want" | "info" | "brand" {
  switch (badge) {
    case "In library": return "good"
    case "Partial": return "info"
    case "Downloading": return "brand"
    default: return "want"
  }
}
