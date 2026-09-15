import { toast } from "sonner"
import type { SearchNowResult } from "@/api"

// Every auto-search button reports the same way. Searching one title runs
// inline, so the toast can name the release that went to SABnzbd exactly
// like a manual grab does — and when nothing was sent, it says why instead
// of claiming a search is under way. A whole show is still queued, so that
// case only reports how many searches are hunting.
export function toastSearchResult(r: SearchNowResult, nothingQueued = "Nothing to search") {
  if (r.grabbed) {
    toast.success(`Sent to SABnzbd — ${r.grabbed}`)
    return
  }
  if (r.reason) {
    toast(`Nothing grabbed — ${r.reason}`)
    return
  }
  toast.success(r.queued > 0
    ? `Searching for ${r.queued} episode${r.queued === 1 ? "" : "s"} — grabs show up in Activity`
    : nothingQueued)
}
