import { useEffect, useState } from "react"
import { api, auth } from "@/api"
import type { ApiQualityProfile, ApiUser } from "@/api"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { PathInput } from "@/components/PathInput"
import {
  Select, SelectContent, SelectItem, SelectTrigger, SelectValue,
} from "@/components/ui/select"
import { Clapperboard } from "lucide-react"
import { toast } from "sonner"
import { cn } from "@/lib/utils"

const STEPS = 7 // admin → tmdb → libraries → profile → sab → prowlarr → lists

// The first-run wizard: shown while no account exists (the API is open in
// that state, which is exactly why this must run before anything else).
// Every step after the admin account is skippable — Settings can finish
// the job later.
export function WizardView({ onDone }: { onDone: (u: ApiUser) => void }) {
  const [step, setStep] = useState(0)
  const [busy, setBusy] = useState(false)
  const [user, setUser] = useState<ApiUser | null>(null)

  const finish = () => { if (user) onDone(user) }
  const next = () => step === STEPS - 1 ? finish() : setStep(step + 1)

  return (
    <div className="flex min-h-screen items-center justify-center p-6">
      <div className="w-full max-w-[440px]">
        <div className="mb-6 flex items-center justify-center gap-2.5">
          <Clapperboard className="h-7 w-7 text-brass" />
          <span className="font-display text-2xl font-bold">Reely</span>
        </div>
        <div className="rounded-2xl border border-linesoft bg-surface p-6">
          <div className="mb-5 flex items-center gap-1.5">
            {Array.from({ length: STEPS }, (_, n) => (
              <span key={n} className={cn("h-1.5 flex-1 rounded-full",
                n <= step ? "bg-brass" : "bg-surface3")} />
            ))}
          </div>
          {step === 0 && <AdminStep busy={busy} setBusy={setBusy}
            onDone={u => { setUser(u); setStep(1) }} />}
          {step === 1 && <TmdbStep busy={busy} setBusy={setBusy} onNext={next} />}
          {step === 2 && <LibrariesStep busy={busy} setBusy={setBusy} onNext={next} />}
          {step === 3 && <ProfileStep busy={busy} setBusy={setBusy} onNext={next} />}
          {step === 4 && <SabStep busy={busy} setBusy={setBusy} onNext={next} />}
          {step === 5 && <ProwlarrStep busy={busy} setBusy={setBusy} onNext={next} />}
          {step === 6 && <ListsStep busy={busy} setBusy={setBusy} onNext={next} />}
        </div>
      </div>
    </div>
  )
}

type StepProps = { busy: boolean; setBusy: (v: boolean) => void; onNext: () => void }

function StepButtons({ busy, onSave, onSkip, saveLabel = "Save & continue" }: {
  busy: boolean; onSave: () => void; onSkip: () => void; saveLabel?: string
}) {
  return (
    <div className="mt-2 flex gap-2">
      <Button className="flex-1" disabled={busy} onClick={onSave}>{saveLabel}</Button>
      <Button variant="outline" disabled={busy} onClick={onSkip}>Skip</Button>
    </div>
  )
}

function AdminStep({ busy, setBusy, onDone }: {
  busy: boolean; setBusy: (v: boolean) => void; onDone: (u: ApiUser) => void
}) {
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [confirm, setConfirm] = useState("")

  const create = async () => {
    if (password !== confirm) {
      toast.error("The passwords don't match")
      return
    }
    setBusy(true)
    try {
      // creating the first account turns auth on; log straight in so the
      // rest of the wizard keeps its access
      await auth.createUser(username.trim(), password)
      onDone(await auth.login(username.trim(), password))
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }

  return (
    <form onSubmit={e => { e.preventDefault(); void create() }} className="grid gap-3">
      <h1 className="font-display text-xl font-bold">Welcome to the screening room</h1>
      <p className="text-[12.5px] leading-relaxed text-muted-foreground">
        First things first: an admin account. Until one exists, anyone on your
        network could walk right in — this step is the only one you can't skip.
      </p>
      <Input value={username} onChange={e => setUsername(e.target.value)}
        placeholder="Username" autoComplete="username" autoFocus />
      <Input type="password" value={password} onChange={e => setPassword(e.target.value)}
        placeholder="Password" autoComplete="new-password" />
      <Input type="password" value={confirm} onChange={e => setConfirm(e.target.value)}
        placeholder="Password, again" autoComplete="new-password" />
      <Button type="submit" disabled={busy || !username.trim() || !password}>
        Create admin account
      </Button>
    </form>
  )
}

function TmdbStep({ busy, setBusy, onNext }: StepProps) {
  const [key, setKey] = useState("")
  const [tvdbKey, setTvdbKey] = useState("")
  const save = async () => {
    setBusy(true)
    try {
      if (key.trim()) await api.putSetting("tmdb_api_key", key.trim())
      if (tvdbKey.trim()) await api.putSetting("tvdb_api_key", tvdbKey.trim())
      onNext()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="grid gap-3">
      <h1 className="font-display text-xl font-bold">Metadata — TMDB</h1>
      <p className="text-[12.5px] leading-relaxed text-muted-foreground">
        Posters, cast, episode lists — everything comes from TMDB with your own free
        API key (themoviedb.org → Settings → API). Nothing matches without it.
      </p>
      <Input type="password" value={key} onChange={e => setKey(e.target.value)}
        placeholder="TMDB API key" autoComplete="off" autoFocus />
      <p className="text-[12.5px] leading-relaxed text-muted-foreground">
        Optional: a TVDB v4 API key (thetvdb.com/api-information) sources shows from
        TheTVDB — the numbering release groups follow, where revivals stay one series.
        Skippable now, addable any time in Settings; show metadata is then provided by TheTVDB.
      </p>
      <Input type="password" value={tvdbKey} onChange={e => setTvdbKey(e.target.value)}
        placeholder="TVDB API key (optional)" autoComplete="off" />
      <StepButtons busy={busy} onSave={() => void save()} onSkip={onNext} />
    </div>
  )
}

function LibrariesStep({ busy, setBusy, onNext }: StepProps) {
  const [moviePath, setMoviePath] = useState("")
  const [showPath, setShowPath] = useState("")
  const save = async () => {
    setBusy(true)
    try {
      if (moviePath.trim()) await api.createLibrary("Movies", moviePath.trim(), "movies")
      if (showPath.trim()) await api.createLibrary("TV", showPath.trim(), "shows")
      onNext()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="grid gap-3">
      <h1 className="font-display text-xl font-bold">Your libraries</h1>
      <p className="text-[12.5px] leading-relaxed text-muted-foreground">
        A library is a folder reely owns: existing files get scanned and matched,
        grabbed downloads land there, renamed your way.
      </p>
      <div className="mono-label text-faint">Movie folder</div>
      <PathInput value={moviePath} onChange={setMoviePath}
        placeholder="/data/movies" autoFocus />
      <div className="mono-label mt-1 text-faint">TV folder</div>
      <PathInput value={showPath} onChange={setShowPath} placeholder="/data/tv" />
      <StepButtons busy={busy} onSave={() => void save()} onSkip={onNext}
        saveLabel="Create & continue" />
    </div>
  )
}

const QUALITIES = ["2160p", "1080p", "720p", "480p"]

// ProfileStep tunes the seeded default profile rather than creating a new
// one — every library and title already points at it.
function ProfileStep({ busy, setBusy, onNext }: StepProps) {
  const [profile, setProfile] = useState<ApiQualityProfile | null>(null)
  useEffect(() => {
    api.profiles().then(r => setProfile(r.profiles?.[0] ?? null)).catch(() => setProfile(null))
  }, [])

  const toggleQuality = (q: string) => {
    if (!profile) return
    const has = profile.qualities.includes(q)
    const qualities = has ? profile.qualities.filter(x => x !== q) : [...profile.qualities, q]
    const cutoff = qualities.includes(profile.cutoff) ? profile.cutoff : (qualities[0] ?? "")
    setProfile({ ...profile, qualities, cutoff })
  }
  const save = async () => {
    if (!profile) { onNext(); return }
    setBusy(true)
    try {
      const { id, ...body } = profile
      await api.updateProfile(id, body)
      onNext()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }
  const gb = (mbPerMin: number, minutes: number) => ((mbPerMin * minutes) / 1024).toFixed(1)

  return (
    <div className="grid gap-3">
      <h1 className="font-display text-xl font-bold">Quality profile</h1>
      <p className="text-[12.5px] leading-relaxed text-muted-foreground">
        What the grab loop may fetch. Size limits scale with runtime, so one band
        covers episodes and three-hour movies. More profiles live in Settings.
      </p>
      {profile && (
        <>
          <div className="flex flex-wrap gap-1.5">
            {QUALITIES.map(q => (
              <button key={q} onClick={() => toggleQuality(q)}
                className={cn("rounded-lg border px-2.5 py-1 text-[12px] font-semibold",
                  profile.qualities.includes(q)
                    ? "border-brass/50 bg-brass/15 text-brass"
                    : "border-linesoft text-muted-foreground hover:text-foreground")}>
                {q}
              </button>
            ))}
          </div>
          <div className="grid grid-cols-2 items-center gap-2">
            <div className="mono-label text-faint">Upgrade until</div>
            <Select value={profile.cutoff || undefined}
              onValueChange={v => setProfile({ ...profile, cutoff: v })}>
              <SelectTrigger><SelectValue placeholder="Cutoff" /></SelectTrigger>
              <SelectContent>
                {profile.qualities.map(q => <SelectItem key={q} value={q}>{q}</SelectItem>)}
              </SelectContent>
            </Select>
          </div>
          <div className="mono-label text-faint">Size band (MB per minute, 0 = no limit)</div>
          <div className="flex items-center gap-2">
            <Input type="number" min={0} className="w-24" value={profile.minMbPerMin}
              onChange={e => setProfile({ ...profile, minMbPerMin: Math.max(0, Number(e.target.value) || 0) })} />
            <span className="text-faint">–</span>
            <Input type="number" min={0} className="w-24" value={profile.maxMbPerMin}
              onChange={e => setProfile({ ...profile, maxMbPerMin: Math.max(0, Number(e.target.value) || 0) })} />
            <span className="mono-label text-faint">
              ≈ {gb(profile.minMbPerMin, 120)}–{profile.maxMbPerMin ? gb(profile.maxMbPerMin, 120) : "∞"} GB / 2h movie
            </span>
          </div>
        </>
      )}
      <StepButtons busy={busy} onSave={() => void save()} onSkip={onNext} />
    </div>
  )
}

// ConnectionStep is one URL + API key service, used for SAB and Prowlarr.
function ConnectionStep({ busy, setBusy, onNext, title, blurb, urlKey, apiKeyKey, placeholder }: StepProps & {
  title: string; blurb: string; urlKey: string; apiKeyKey: string; placeholder: string
}) {
  const [url, setUrl] = useState("")
  const [key, setKey] = useState("")
  const save = async () => {
    setBusy(true)
    try {
      if (url.trim()) await api.putSetting(urlKey, url.trim().replace(/\/+$/, ""))
      if (key.trim()) await api.putSetting(apiKeyKey, key.trim())
      onNext()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="grid gap-3">
      <h1 className="font-display text-xl font-bold">{title}</h1>
      <p className="text-[12.5px] leading-relaxed text-muted-foreground">{blurb}</p>
      <Input value={url} onChange={e => setUrl(e.target.value)}
        placeholder={placeholder} autoComplete="off" autoFocus />
      <Input type="password" value={key} onChange={e => setKey(e.target.value)}
        placeholder="API key" autoComplete="off" />
      <StepButtons busy={busy} onSave={() => void save()} onSkip={onNext} />
    </div>
  )
}

function SabStep(p: StepProps) {
  return <ConnectionStep {...p} title="Downloads — SABnzbd"
    blurb="Grabbed releases land in SABnzbd and import themselves the moment they finish."
    urlKey="sab_url" apiKeyKey="sab_api_key" placeholder="http://sabnzbd:8080" />
}

function ProwlarrStep(p: StepProps) {
  return <ConnectionStep {...p} title="Indexers — Prowlarr"
    blurb="One Prowlarr connection powers every search — manual, automatic, and the RSS sync."
    urlKey="prowlarr_url" apiKeyKey="prowlarr_api_key" placeholder="http://prowlarr:9696" />
}

// ListsStep stores the Trakt client id so watched lists can pull from
// Trakt; the lists themselves are set up in Settings → Watched Lists.
// TMDB charts work with no extra setup at all.
function ListsStep({ busy, setBusy, onNext }: StepProps) {
  const [key, setKey] = useState("")
  const save = async () => {
    setBusy(true)
    try {
      if (key.trim()) await api.putSetting("trakt_client_id", key.trim())
      onNext()
    } catch (e) {
      toast.error(`${e instanceof Error ? e.message : e}`)
    } finally {
      setBusy(false)
    }
  }
  return (
    <div className="grid gap-3">
      <h1 className="font-display text-xl font-bold">Watched lists</h1>
      <p className="text-[12.5px] leading-relaxed text-muted-foreground">
        Lists keep libraries fed automatically — TMDB charts and public TMDB lists work
        out of the box with your TMDB key. Trakt lists additionally need a client id
        (note: Trakt only lets VIP members create API apps now). Pick the lists later in
        Settings → Watched Lists. Last step; Skip opens the screening room.
      </p>
      <Input type="password" value={key} onChange={e => setKey(e.target.value)}
        placeholder="Trakt client id (optional, VIP only)" autoComplete="off" autoFocus />
      <StepButtons busy={busy} onSave={() => void save()} onSkip={onNext} />
    </div>
  )
}
