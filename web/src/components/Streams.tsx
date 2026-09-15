import { api } from "@/api"
import type { ApiStream } from "@/api"
import { useApi } from "@/hooks/use-api"

// What is actually inside the file, read from Plex rather than from the
// release name. The two disagree more often than people expect: a title
// that arrived as "x264" and was re-encoded since still says x264 in its
// filename, and the only way to know is to look at the track.
//
// It is also what decides whether a client can play a title unaided.
// Fire TV handles h264 and HEVC but not DTS or TrueHD, so the audio row
// here is the difference between direct play and a server working hard.

// A channel count is how everyone says it out loud, not a number.
function fmtChannels(n?: number): string {
  if (!n) return ""
  return { 1: "Mono", 2: "Stereo", 6: "5.1", 8: "7.1" }[n] ?? `${n}ch`
}

// Plex names a track's language in full ("English"), and leaves it out
// on a track that never declared one.
function label(s: ApiStream): string {
  const parts = [
    s.kind === "video"
      ? [s.codec.toUpperCase(), s.height ? `${s.height}p` : ""].filter(Boolean).join(" ")
      : [s.language || "Unknown", s.codec.toUpperCase(), fmtChannels(s.channels)]
        .filter(Boolean).join(" "),
  ]
  if (s.sdh) parts.push("SDH")
  if (s.forced) parts.push("Forced")
  if (s.external) parts.push("external")
  return parts.join(" · ")
}

function Row({ name, streams }: { name: string; streams: ApiStream[] }) {
  if (streams.length === 0) return null
  return (
    <div className="flex flex-wrap items-baseline gap-x-2 gap-y-1">
      <span className="mono-label w-16 shrink-0 text-faint">{name}</span>
      {streams.map((s, i) => (
        <span key={i}
          className="rounded-sm border border-border/70 px-1.5 py-0.5 text-[11px] text-muted-foreground">
          {label(s)}
        </span>
      ))}
    </div>
  )
}

// TitleStreams draws the track list for one file, and nothing at all
// when there is no answer — no Plex, a title it has not scanned, a
// server that would not say. This is extra detail about a title, so an
// absent answer is a quieter page rather than an error on it.
export function TitleStreams({ kind, id }: { kind: "movie" | "episode"; id: number }) {
  const { data } = useApi(() => api.titleStreams(kind, id))
  const streams = data?.streams ?? []
  if (streams.length === 0) return null
  return (
    <div className="mt-4 flex flex-col gap-1.5">
      <Row name="video" streams={streams.filter(s => s.kind === "video")} />
      <Row name="audio" streams={streams.filter(s => s.kind === "audio")} />
      <Row name="subs" streams={streams.filter(s => s.kind === "subtitle")} />
    </div>
  )
}
