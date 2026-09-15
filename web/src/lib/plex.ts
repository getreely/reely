import { auth } from "@/api"
import type { ApiUser } from "@/api"

// Signing in with Plex, from the browser's side.
//
// Plex sends the person away to approve a PIN, so this opens plex.tv in
// another tab and polls until the PIN comes back claimed. Both the login
// page and the owner's link card use it — they differ only in what they
// do with the answer, and having two copies is how one mistake broke
// both of them at once.

export interface PlexResult {
  status: "ok" | "pending" | "linked"
  user?: ApiUser
  account?: string
  servers?: { name: string; machineId: string; owned: boolean }[]
}

export class PopupBlocked extends Error {
  constructor() {
    super("Your browser blocked the Plex window — allow pop-ups for this site, then try again.")
  }
}

/**
 * Opens the tab that will show plex.tv.
 *
 * Called from the click itself, before anything is awaited: a window
 * opened from a promise callback is no longer a user gesture and gets
 * blocked. It starts blank because the URL isn't known until the server
 * hands back a PIN.
 *
 * NOT "noopener" — that makes window.open return null, which is exactly
 * what it is for, and then there is no handle to point anywhere. The
 * blank tab just sits there, which is the bug this replaced. The
 * back-reference is severed instead: about:blank is same-origin, so
 * opener can be nulled here and stays null across the navigation.
 */
export function openPlexTab(): Window | null {
  const tab = window.open("", "_blank")
  if (tab) {
    try {
      tab.opener = null
    } catch {
      // some browsers refuse the assignment; the tab is still usable
    }
  }
  return tab
}

/**
 * Drives one sign-in to its answer. `tab` comes from openPlexTab, called
 * on the click. `stopped` lets an unmounting component abandon the poll.
 *
 * Resolves only when the person has approved it; throws on a blocked
 * pop-up, an expired PIN, or a refusal from the server.
 */
export async function runPlexSignIn(
  tab: Window | null,
  link: boolean,
  stopped: () => boolean,
): Promise<PlexResult> {
  if (!tab) throw new PopupBlocked()
  try {
    const pin = await auth.plexPin()
    tab.location.href = pin.url

    // plex.tv expires a PIN in half an hour; there is no point waiting
    // longer than somebody plausibly will
    const deadline = Date.now() + Math.min(pin.expiresIn, 900) * 1000
    while (!stopped() && Date.now() < deadline) {
      await new Promise(r => setTimeout(r, 2000))
      if (stopped()) throw new Error("cancelled")
      const res = await auth.plexCheck(pin.id, link)
      // "pending" is the ordinary answer while they are still over there
      if (res.status === "ok" || res.status === "linked") return res
    }
    throw new Error("That sign-in timed out — try again.")
  } finally {
    // whatever happened, don't leave a stray tab behind
    try {
      tab.close()
    } catch {
      // already gone
    }
  }
}
