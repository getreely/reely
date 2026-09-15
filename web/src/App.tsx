import { useEffect, useState } from "react"
import { api, auth } from "@/api"
import type { ApiLibrary, ApiPersonCredit, ApiUser } from "@/api"
import {
  DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuLabel, DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu"
import { useApi } from "@/hooks/use-api"
import { AccessProvider, accessFor } from "@/lib/access"
import { LoginView } from "@/views/Login"
import { WizardView } from "@/views/Wizard"
import { ActivityView } from "@/views/Activity"
import { RequestsView } from "@/views/Requests"
import { PortalRequestsView } from "@/views/MyRequests"
import { CalendarView } from "@/views/Calendar"
import { MineView } from "@/views/Mine"
import { LibraryView } from "@/views/Library"
import { HomeView } from "@/views/Home"
import { ExploreView } from "@/views/Explore"
import { MovieDetailView } from "@/views/MovieDetail"
import { ShowDetailView } from "@/views/ShowDetail"
import { PersonView } from "@/views/Person"
import { PreviewView } from "@/views/Preview"
import { EpisodeView } from "@/views/Episode"
import { AddSearchDialog } from "@/components/AddSearch"
import { SettingsView } from "@/views/Settings"
import { Toaster } from "sonner"
import { cn } from "@/lib/utils"
import { CalendarDays, Clapperboard, Compass, Film, House, Inbox, ListChecks, LogOut, PanelLeftClose, PanelLeftOpen, Search, Settings as SettingsIcon, Tv } from "lucide-react"
import { useAccess, useIsAdmin } from "@/lib/access"

type NavKey = "home" | "explore" | "movies" | "shows" | "calendar" | "activity" | "requests" | "settings"
type Detail = {
  kind: "movie" | "show" | "person" | "episode" | "previewMovie" | "previewShow"
  id: number
  // preview routes only: the id is a TVDB series id (TVDB-sourced search)
  src?: "tvdb"
}

export default function App() {
  const [checked, setChecked] = useState(false)
  const [authRequired, setAuthRequired] = useState(false)
  const [user, setUser] = useState<ApiUser | null>(null)
  // served by the internet-facing listener, where the admin routes are
  // absent rather than refused
  const [external, setExternal] = useState(false)
  const [nav, setNav] = useState<NavKey>("home")
  // outside, the landing view is the thing the portal is for
  useEffect(() => { if (external) setNav("explore") }, [external])
  // a library sub-entry narrows the movies/shows tab to one collection;
  // clicking the tab itself shows every library the user may see
  const [libFilter, setLibFilter] = useState<ApiLibrary | null>(null)
  // the global add-search — the pill in the sidebar, or "/" from anywhere
  const [searchOpen, setSearchOpen] = useState(false)
  // the rail collapses to icons; the choice sticks across sessions
  const [railOpen, setRailOpen] = useState(() => localStorage.getItem("reely.rail") !== "closed")
  const toggleRail = () => setRailOpen(open => {
    localStorage.setItem("reely.rail", open ? "closed" : "open")
    return !open
  })
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      const t = e.target as HTMLElement | null
      if (e.key === "/" && t?.tagName !== "INPUT" && t?.tagName !== "TEXTAREA" && !t?.isContentEditable) {
        e.preventDefault()
        setSearchOpen(true)
      }
    }
    window.addEventListener("keydown", onKey)
    return () => window.removeEventListener("keydown", onKey)
  }, [])
  // details stack on top of the movies/shows tabs (title → person → title…);
  // back pops one layer, switching tabs clears the whole stack. Every push
  // also lays down a history entry so the phone's back gesture (and the
  // browser's back button) pops the stack instead of leaving the app.
  const [stack, setStack] = useState<Detail[]>([])
  const push = (d: Detail) => {
    history.pushState({ depth: stack.length + 1 }, "")
    setStack(s => [...s, d])
  }
  // back buttons go through history so gesture, browser button, and UI
  // button all take the same path; the popstate handler does the popping
  const pop = () => history.back()
  useEffect(() => {
    const onPop = (e: PopStateEvent) => {
      const depth = typeof e.state?.depth === "number" ? e.state.depth : 0
      setStack(s => {
        if (depth >= s.length) return s
        const next = s.slice(0, depth)
        // popping back across a cross-tab jump (person → their movie while
        // on the shows tab) re-selects the tab the revealed title lives under
        const t = next.length > 0 ? next[next.length - 1] : null
        if (t?.kind === "movie") setNav("movies")
        else if (t?.kind === "show") setNav("shows")
        return next
      })
    }
    window.addEventListener("popstate", onPop)
    return () => window.removeEventListener("popstate", onPop)
  }, [])
  // adding from a preview swaps the preview for the real title page —
  // same depth, so history needs no entry, and the tab stays put (home
  // and the kind tabs all render title pages)
  const addedFromPreview = (kind: "movie" | "show", id: number) => {
    setStack(s => [...s.slice(0, -1), { kind, id }])
  }
  // tab switches clear the stack, so drop its history entries too — the
  // popstate this triggers finds an already-empty stack and does nothing
  const clearStack = () => {
    if (stack.length > 0) history.go(-stack.length)
    setStack([])
  }
  const go = (key: NavKey) => { setNav(key); clearStack(); setLibFilter(null) }
  const goLibrary = (lib: ApiLibrary) => {
    setNav(lib.kind === "movies" ? "movies" : "shows")
    clearStack()
    setLibFilter(lib)
  }
  const top = stack.length > 0 ? stack[stack.length - 1] : null
  // the server already scopes this list to what the user may see; the
  // mount-time fetch runs before login, so re-fetch once a session exists
  const libsQuery = useApi(() => api.libraries(), user ? 60_000 : undefined)
  const reloadLibs = libsQuery.reload
  useEffect(() => {
    if (user) reloadLibs({ quiet: true })
  }, [user, reloadLibs])
  const libraries = libsQuery.data?.libraries ?? []
  const movieLibs = libraries.filter(l => l.kind === "movies")
  const showLibs = libraries.filter(l => l.kind === "shows")
  // a filmography card opens its library title under the matching tab
  const openCredit = (c: ApiPersonCredit) => {
    if (!c.localId) return
    setNav(c.kind === "movie" ? "movies" : "shows")
    push({ kind: c.kind, id: c.localId })
  }

  useEffect(() => {
    auth.me()
      .then(r => {
        setAuthRequired(r.authRequired)
        setUser(r.user ?? null)
        setExternal(r.external ?? false)
      })
      .catch(() => setAuthRequired(false))
      .finally(() => setChecked(true))
  }, [])

  if (!checked) return null
  // no accounts yet: the API is wide open, so the wizard runs before
  // anything else and its first step closes that door
  if (!authRequired && !user) {
    return (
      <>
        <WizardView onDone={u => { setAuthRequired(true); setUser(u) }} />
        <Toaster position="bottom-right" theme="dark" />
      </>
    )
  }
  if (authRequired && !user) {
    return <LoginView onLoggedIn={u => setUser(u)} />
  }

  return (
    <AccessProvider value={accessFor(user, external)}>
      <div className="flex min-h-screen">
        {/* the rail stays put while the library scrolls: sticky and exactly
            one viewport tall, so mt-auto pins Settings to the bottom of the
            SCREEN rather than the bottom of a very long grid */}
        <nav className={cn(
          "sticky top-0 flex h-screen shrink-0 flex-col gap-0.5 overflow-y-auto border-r border-linesoft bg-background p-3 transition-[width] max-md:hidden",
          railOpen ? "w-[210px]" : "w-[60px]"
        )}>
          {/* open: logo, name, and the collapse control on the right.
              Collapsed: one button in their place — there is no room for
              two icons in a 60px rail, and expanding is the only thing
              worth clicking up here. */}
          <div className={cn("mb-4 flex items-center gap-2 pt-1", railOpen ? "px-2" : "justify-center")}>
            {railOpen ? (
              <>
                <Clapperboard className="h-5 w-5 shrink-0 text-brass" />
                <span className="font-display text-lg font-bold">Reely</span>
                <button onClick={toggleRail} title="Collapse the sidebar"
                  aria-label="Collapse the sidebar"
                  className="ml-auto shrink-0 text-faint hover:text-brass">
                  <PanelLeftClose className="h-4 w-4" />
                </button>
              </>
            ) : (
              <button onClick={toggleRail} title="Expand the sidebar"
                aria-label="Expand the sidebar"
                className="shrink-0 text-brass hover:text-brass/80">
                <PanelLeftOpen className="h-5 w-5" />
              </button>
            )}
          </div>
          {/* Outside, this is a portal for finding something, asking for
              it, and watching what you have. Movies and Shows are served
              out there and scoped to what the account may see; the
              per-library entries under them are not, because a
              requester's view is decided by their groups and not by
              which folders a title happens to live in. */}
          {!external && (
            <NavButton open={railOpen} active={nav === "home"} onClick={() => go("home")} icon={<House className="h-4 w-4" />} label="Home" />
          )}
          <NavButton open={railOpen} active={nav === "explore"} onClick={() => go("explore")} icon={<Compass className="h-4 w-4" />} label={external ? "Find" : "Explore"} />
          {external && <>
            <NavButton open={railOpen} active={nav === "movies"} onClick={() => go("movies")} icon={<Film className="h-4 w-4" />} label="Movies" />
            <NavButton open={railOpen} active={nav === "shows"} onClick={() => go("shows")} icon={<Tv className="h-4 w-4" />} label="Shows" />
          </>}
          {!external && <>
            <NavButton open={railOpen} active={nav === "movies" && !libFilter} onClick={() => go("movies")} icon={<Film className="h-4 w-4" />} label="Movies" />
            {railOpen && movieLibs.length > 1 && movieLibs.map(l => (
              <LibNavButton key={l.id} library={l} active={libFilter?.id === l.id} onClick={() => goLibrary(l)} />
            ))}
            <NavButton open={railOpen} active={nav === "shows" && !libFilter} onClick={() => go("shows")} icon={<Tv className="h-4 w-4" />} label="Shows" />
            {railOpen && showLibs.length > 1 && showLibs.map(l => (
              <LibNavButton key={l.id} library={l} active={libFilter?.id === l.id} onClick={() => goLibrary(l)} />
            ))}
          </>}
          {/* what is coming is read-only and scoped to the libraries this
              account reaches, so it belongs out there too */}
          <NavButton open={railOpen} active={nav === "calendar"} onClick={() => go("calendar")} icon={<CalendarDays className="h-4 w-4" />} label="Calendar" />
          {/* Out here it is everybody's: a queue to the owner, a list of
              what you asked for to anybody else. */}
          {external && (
            <NavButton open={railOpen} active={nav === "requests"} onClick={() => go("requests")}
              icon={<Inbox className="h-4 w-4" />} label="Requests" />
          )}
          {!external && <>
            <ActivityNav open={railOpen} active={nav === "activity"} onClick={() => go("activity")} />
            <RequestsNav open={railOpen} active={nav === "requests"} onClick={() => go("requests")} />
          </>}
          <div className="mt-auto">
            <NavButton open={railOpen} active={nav === "settings"} onClick={() => go("settings")} icon={<SettingsIcon className="h-4 w-4" />} label="Settings" />
          </div>
        </nav>
        <div className="relative min-w-0 flex-1">
          {/* floating top bar — the search's permanent home on every page */}
          <div className="sticky top-[calc(env(safe-area-inset-top)+0.75rem)] z-30 mx-6 mt-3 flex items-center gap-3 rounded-2xl border border-linesoft bg-surface py-2 pl-3 pr-3 shadow-[0_8px_28px_rgba(0,0,0,0.25)] max-md:mx-4">
            <button onClick={() => setSearchOpen(true)}
              className="flex w-full max-w-[440px] items-center gap-2.5 rounded-[10px] border border-linesoft bg-background px-3 py-2 text-left text-[13px] text-faint hover:border-brass/60">
              <Search className="h-3.5 w-3.5" />
              Search movies &amp; shows…
            </button>
            {/* Inside the bar rather than pinned beside it. Positioned
                against the page, it scrolled away while the bar it
                appeared to belong to stayed — so on a phone, where the
                bar IS the header, signing out meant scrolling back to
                the top to find the circle again. It travels with its
                bar now, at the far right on a wide screen because the
                search stops at 440px. */}
            {user && (
              <div className="ml-auto shrink-0">
                <AccountMenu user={user} />
              </div>
            )}
          </div>
        <main className="min-w-0 p-6 max-md:p-4 max-md:pb-[calc(env(safe-area-inset-bottom)+4.5rem)]">
          {top?.kind === "person" && (
            <PersonView key={`p${top.id}`} tmdbId={top.id} onBack={pop} onOpenCredit={openCredit} />
          )}
          {top?.kind === "episode" && (
            <EpisodeView key={`e${top.id}`} episodeId={top.id} onBack={pop}
              onOpenShow={id => {
                // an episode page renders on any tab, but show detail only
                // lives under home/shows — reached from the calendar tab,
                // the pushed show matched nothing and the page went blank
                setNav("shows")
                push({ kind: "show", id })
              }} />
          )}
          {(top?.kind === "previewMovie" || top?.kind === "previewShow") && (
            <PreviewView key={`v${top.kind}${top.id}`} tmdbId={top.id} src={top.src}
              kind={top.kind === "previewMovie" ? "movie" : "show"}
              onBack={pop} onAdded={addedFromPreview}
              onOpenPerson={id => push({ kind: "person", id })}
              onOpenPreview={(kind, tmdbId) => push({ kind: kind === "movie" ? "previewMovie" : "previewShow", id: tmdbId })} />
          )}
          {top?.kind !== "person" && top?.kind !== "episode" && top?.kind !== "previewMovie" && top?.kind !== "previewShow" && nav === "home" && (
            top?.kind === "movie"
              ? <MovieDetailView key={top.id} movieId={top.id} onBack={pop}
                  onOpenPerson={id => push({ kind: "person", id })}
                  onOpenPreview={(kind, tmdbId) => push({ kind: kind === "movie" ? "previewMovie" : "previewShow", id: tmdbId })} />
              : top?.kind === "show"
                ? <ShowDetailView key={top.id} showId={top.id} onBack={pop}
                    onOpenPerson={id => push({ kind: "person", id })}
                    onOpenEpisode={id => push({ kind: "episode", id })}
                    onOpenPreview={(kind, tmdbId) => push({ kind: kind === "movie" ? "previewMovie" : "previewShow", id: tmdbId })} />
                : <HomeView
                    onOpenMovie={id => push({ kind: "movie", id })}
                    onOpenShow={id => push({ kind: "show", id })}
                    onOpenEpisode={id => push({ kind: "episode", id })}
                    onExplore={() => go("explore")} />
          )}
          {top?.kind !== "person" && top?.kind !== "episode" && top?.kind !== "previewMovie" && top?.kind !== "previewShow" && nav === "explore" && (
            <ExploreView onOpenPreview={(kind, tmdbId) => push({ kind: kind === "movie" ? "previewMovie" : "previewShow", id: tmdbId })} />
          )}
          {top?.kind !== "person" && top?.kind !== "episode" && top?.kind !== "previewMovie" && top?.kind !== "previewShow" && nav === "movies" && (
            top?.kind === "movie"
              ? <MovieDetailView key={top.id} movieId={top.id} onBack={pop}
                  onOpenPerson={id => push({ kind: "person", id })}
                  onOpenPreview={(kind, tmdbId) => push({ kind: kind === "movie" ? "previewMovie" : "previewShow", id: tmdbId })} />
              : external
                ? <MineView kind="movies"
                    onOpenPreview={(k, tmdbId) => push({ kind: k === "movie" ? "previewMovie" : "previewShow", id: tmdbId })} />
                : <LibraryView kind="movies" library={libFilter ?? undefined}
                    onOpenMovie={m => push({ kind: "movie", id: m.id })}
                    onOpenShow={() => {}} />
          )}
          {top?.kind !== "person" && top?.kind !== "episode" && top?.kind !== "previewMovie" && top?.kind !== "previewShow" && nav === "shows" && (
            top?.kind === "show"
              ? <ShowDetailView key={top.id} showId={top.id} onBack={pop}
                  onOpenPerson={id => push({ kind: "person", id })}
                  onOpenEpisode={id => push({ kind: "episode", id })}
                  onOpenPreview={(kind, tmdbId) => push({ kind: kind === "movie" ? "previewMovie" : "previewShow", id: tmdbId })} />
              : external
                ? <MineView kind="shows"
                    onOpenPreview={(k, tmdbId, src) => push({
                      kind: k === "movie" ? "previewMovie" : "previewShow",
                      id: tmdbId, src,
                    })} />
                : <LibraryView kind="shows" library={libFilter ?? undefined}
                    onOpenMovie={() => {}}
                    onOpenShow={s => push({ kind: "show", id: s.id })} />
          )}
          {nav === "calendar" && !top && <CalendarView
            onOpenEpisode={id => push({ kind: "episode", id })}
            onOpenMovie={id => { setNav("movies"); push({ kind: "movie", id }) }}
            onOpenPreview={(kind, id, src) => push({
              kind: kind === "movie" ? "previewMovie" : "previewShow", id, src,
            })} />}
          {nav === "activity" && <ActivityView />}
          {nav === "requests" && (
            external
              ? <PortalRequestsView onOpenPreview={(kind, id, src) =>
                  push({ kind: kind === "movie" ? "previewMovie" : "previewShow", id, src })} />
              : <RequestsView onOpenPreview={(kind, id, src) =>
                  push({ kind: kind === "movie" ? "previewMovie" : "previewShow", id, src })} />
          )}
          {nav === "settings" && <SettingsView />}
        </main>
        </div>
      </div>
      <BottomNav nav={nav} onGo={go} />
      <AddSearchDialog open={searchOpen} onOpenChange={setSearchOpen}
        onOpenPreview={(kind, id, src) => {
          setNav(kind === "movie" ? "movies" : "shows")
          push({ kind: kind === "movie" ? "previewMovie" : "previewShow", id, src })
        }}
        onAdded={(kind, id) => {
          setNav(kind === "movie" ? "movies" : "shows")
          push({ kind, id })
        }} />
      <Toaster position="bottom-right" theme="dark" />
    </AccessProvider>
  )
}

// Activity is admin-only on the server, so plain users don't get the tab.
// BottomNav is the phone-width replacement for the sidebar: the same
// destinations as fixed tabs along the bottom edge, above the home
// indicator. Per-library narrowing stays a desktop affair.
function BottomNav({ nav, onGo }: {
  nav: NavKey
  onGo: (key: NavKey) => void
}) {
  const { isAdmin, external } = useAccess()
  // most people will use the portal on a phone, so this bar is the one
  // that matters out there — and out there it is two things
  const items: { key: NavKey; label: string; icon: React.ReactNode }[] = external ? [
    { key: "explore", label: "Find", icon: <Compass className="h-[18px] w-[18px]" /> },
    { key: "movies", label: "Movies", icon: <Film className="h-[18px] w-[18px]" /> },
    { key: "shows", label: "Shows", icon: <Tv className="h-[18px] w-[18px]" /> },
    { key: "calendar", label: "Calendar", icon: <CalendarDays className="h-[18px] w-[18px]" /> },
    { key: "requests", label: "Requests", icon: <Inbox className="h-[18px] w-[18px]" /> },
    { key: "settings", label: "Settings", icon: <SettingsIcon className="h-[18px] w-[18px]" /> },
  ] : [
    { key: "home", label: "Home", icon: <House className="h-[18px] w-[18px]" /> },
    { key: "movies", label: "Movies", icon: <Film className="h-[18px] w-[18px]" /> },
    { key: "shows", label: "Shows", icon: <Tv className="h-[18px] w-[18px]" /> },
    { key: "calendar", label: "Calendar", icon: <CalendarDays className="h-[18px] w-[18px]" /> },
    ...(isAdmin ? [
      { key: "activity" as const, label: "Activity", icon: <ListChecks className="h-[18px] w-[18px]" /> },
      { key: "requests" as const, label: "Requests", icon: <Inbox className="h-[18px] w-[18px]" /> },
    ] : []),
    { key: "settings", label: "Settings", icon: <SettingsIcon className="h-[18px] w-[18px]" /> },
  ]
  return (
    <nav className="fixed inset-x-0 bottom-0 z-40 flex border-t border-linesoft bg-surface pb-[env(safe-area-inset-bottom)] md:hidden">
      {items.map(n => (
        <button key={n.key} aria-label={n.label} onClick={() => onGo(n.key)}
          className={cn(
            "flex min-w-0 flex-1 flex-col items-center gap-0.5 py-2 text-muted-foreground",
            nav === n.key && "text-brass"
          )}>
          {n.icon}
          <span className="font-label truncate text-[9px] uppercase tracking-[0.06em]">{n.label}</span>
        </button>
      ))}
    </nav>
  )
}

function ActivityNav({ active, onClick, open = true }: {
  active: boolean; onClick: () => void; open?: boolean
}) {
  const isAdmin = useIsAdmin()
  if (!isAdmin) return null
  return <NavButton open={open} active={active} onClick={onClick}
    icon={<ListChecks className="h-4 w-4" />} label="Activity" />
}

// Deciding requests is the owner's, so the tab is admin-only — the same
// arrangement Activity has, and the server refuses the queue endpoint to
// anyone else regardless.
function RequestsNav({ active, onClick, open = true }: {
  active: boolean; onClick: () => void; open?: boolean
}) {
  const isAdmin = useIsAdmin()
  if (!isAdmin) return null
  return <NavButton open={open} active={active} onClick={onClick}
    icon={<Inbox className="h-4 w-4" />} label="Requests" />
}

// LibNavButton is one collection under its Movies/Shows tab — indented,
// no icon.
function LibNavButton({ library, active, onClick }: {
  library: ApiLibrary; active: boolean; onClick: () => void
}) {
  return (
    <button onClick={onClick}
      className={cn(
        "flex w-full items-center rounded-[9px] py-1 pl-9 pr-2.5 text-left text-[12.5px] text-muted-foreground hover:bg-surface2 hover:text-foreground max-md:hidden",
        active && "bg-brass/12 font-semibold text-brass hover:bg-brass/12 hover:text-brass"
      )}>
      <span className="truncate">{library.name}</span>
    </button>
  )
}

function NavButton({ active, onClick, icon, label, open = true }: {
  active: boolean; onClick: () => void; icon: React.ReactNode; label: string
  // open=false is the collapsed rail: icon only, name in the tooltip
  open?: boolean
}) {
  return (
    <button onClick={onClick} title={open ? undefined : label}
      className={cn(
        "flex w-full items-center gap-2.5 rounded-[9px] py-1.5 text-[13.5px] text-muted-foreground hover:bg-surface2 hover:text-foreground max-md:justify-center",
        open ? "px-2.5" : "justify-center px-0",
        active && "bg-brass/12 font-semibold text-brass hover:bg-brass/12 hover:text-brass"
      )}>
      {icon} {open && <span className="max-md:hidden">{label}</span>}
    </button>
  )
}

// AccountMenu is who you are signed in as, and the way out.
//
// It lives in the top bar because the top bar is the one thing on every
// page at every width. Sign-out used to sit on the side rail, which is
// hidden below md — so on a phone in portrait there was no way out at
// all, while landscape was wide enough to bring the rail back. One
// control that does not move is worth more than two that do.
function AccountMenu({ user }: { user: ApiUser }) {
  const initial = (user.username.trim()[0] ?? "?").toUpperCase()
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <button
          aria-label={`Account — ${user.username}`}
          title={user.username}
          className="flex h-9 w-9 shrink-0 items-center justify-center rounded-full border border-linesoft bg-surface2 text-[13px] font-bold text-muted-foreground hover:border-brass/60 hover:text-brass"
        >
          {initial}
        </button>
      </DropdownMenuTrigger>
      <DropdownMenuContent align="end" className="rounded-xl">
        <DropdownMenuLabel className="mono-label text-faint">
          {user.username}
        </DropdownMenuLabel>
        <DropdownMenuItem
          onClick={() => { void auth.logout().then(() => location.reload()) }}
        >
          <LogOut className="mr-2 h-3.5 w-3.5" />
          Sign out
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  )
}
