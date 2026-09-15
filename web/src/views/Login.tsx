import { useEffect, useRef, useState } from "react"
import { auth } from "@/api"
import type { ApiUser } from "@/api"
import { openPlexTab, runPlexSignIn } from "@/lib/plex"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Clapperboard } from "lucide-react"

// The Plex button. The flow itself lives in lib/plex — the owner's link
// card runs the same one, and having two copies is how one mistake broke
// both at once.
function usePlexSignIn(onLoggedIn: (u: ApiUser) => void) {
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const stop = useRef(false)
  useEffect(() => () => { stop.current = true }, [])

  const start = async () => {
    // opened on the click, before anything is awaited
    const tab = openPlexTab()
    setBusy(true)
    setError("")
    try {
      const res = await runPlexSignIn(tab, false, () => stop.current)
      if (res.user) onLoggedIn(res.user)
    } catch (e) {
      if (!stop.current) setError(e instanceof Error ? e.message : String(e))
    } finally {
      if (!stop.current) setBusy(false)
    }
  }
  return { start, busy, error }
}

export function LoginView({ onLoggedIn }: { onLoggedIn: (u: ApiUser) => void }) {
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [error, setError] = useState("")
  const [busy, setBusy] = useState(false)
  const plex = usePlexSignIn(onLoggedIn)

  const submit = async (e: React.FormEvent) => {
    e.preventDefault()
    setBusy(true)
    setError("")
    try {
      onLoggedIn(await auth.login(username, password))
    } catch (err) {
      setError(err instanceof Error ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="flex min-h-screen items-center justify-center p-6">
      <form onSubmit={submit} className="w-full max-w-[340px]">
        <div className="mb-6 flex items-center justify-center gap-2.5">
          <Clapperboard className="h-7 w-7 text-brass" />
          <span className="font-display text-3xl font-bold">Reely</span>
        </div>
        <div className="grid gap-3 rounded-2xl border border-linesoft bg-surface p-5">
          <div>
            <div className="mono-label mb-1.5 text-muted-foreground">Username</div>
            <Input value={username} onChange={e => setUsername(e.target.value)} autoFocus autoComplete="username" />
          </div>
          <div>
            <div className="mono-label mb-1.5 text-muted-foreground">Password</div>
            <Input type="password" value={password} onChange={e => setPassword(e.target.value)} autoComplete="current-password" />
          </div>
          {(error || plex.error) && (
            <p className="text-[12.5px] text-want">{error || plex.error}</p>
          )}
          <Button type="submit" disabled={busy || !username || !password}>Sign in</Button>

          <div className="my-1 flex items-center gap-3">
            <span className="h-px flex-1 bg-linesoft" />
            <span className="mono-label text-faint">or</span>
            <span className="h-px flex-1 bg-linesoft" />
          </div>
          <Button type="button" variant="outline" disabled={plex.busy}
            onClick={() => void plex.start()}>
            {plex.busy ? "Waiting for Plex…" : "Sign in with Plex"}
          </Button>
          {plex.busy && (
            <p className="text-center text-[12px] text-muted-foreground">
              Approve the sign-in in the Plex tab, then come back here.
            </p>
          )}
        </div>
      </form>
    </div>
  )
}
