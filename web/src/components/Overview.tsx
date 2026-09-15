import { useCallback, useEffect, useRef, useState } from "react"
import { cn } from "@/lib/utils"

// A description that clamps to four lines and offers to open when — and
// only when — there is more to see.
//
// This used to be four copies of the same block, each deciding whether to
// show the button from the text's LENGTH: over 260 characters, offer it.
// A character count is a guess at a line count, and it guesses against
// the viewport. On a wide screen 260 characters is two lines and the
// button appears over text that was never clamped; on a phone it is six,
// so a 200-character overview clamps to four lines with an ellipsis and
// no way to read the rest.
//
// So it measures instead: the element knows whether it overflows. That
// answer is exact, and it is right at every width without anybody
// picking a number.
export function Overview({ text, className }: { text: string; className?: string }) {
  const [open, setOpen] = useState(false)
  const [clipped, setClipped] = useState(false)
  const ref = useRef<HTMLParagraphElement>(null)

  const measure = useCallback(() => {
    const el = ref.current
    if (!el) return
    // only meaningful while clamped; open, it never overflows
    if (open) return
    setClipped(el.scrollHeight > el.clientHeight + 1)
  }, [open])

  useEffect(() => {
    measure()
    const el = ref.current
    if (!el || typeof ResizeObserver === "undefined") return
    // rotating a phone changes the answer, as does a font finishing
    // loading after the first paint
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  }, [measure, text])

  return (
    <>
      <p ref={ref} className={cn(
        "mt-4 max-w-[68ch] text-[13.5px] leading-relaxed text-muted-foreground",
        !open && "line-clamp-4", className)}>
        {text}
      </p>
      {(clipped || open) && (
        <button onClick={() => setOpen(o => !o)}
          className="mono-label mt-1.5 text-faint hover:text-brass">
          {open ? "less ↑" : "more ↓"}
        </button>
      )}
    </>
  )
}
