import { createContext, useContext } from "react"
import type { ApiUser } from "@/api"

// Who is signed in and what the UI should offer them.
//
// The server is the actual gate — every rule here has a matching check in the
// Go handlers, and this only decides what to render. Hiding a control the API
// would refuse is the point: a plain user should not be shown an "Edit
// metadata" button that answers 403.
//
// A null user means no account exists yet (the API is open for the setup
// wizard), which the server also treats as admin — so the two agree.

export interface Access {
  user: ApiUser | null
  isAdmin: boolean
  /** Libraries this account may work in. Empty for admins, who reach all. */
  libraryIds: number[]
  /**
   * Whether this account adds titles itself. A requester browses the same
   * libraries and asks instead, so every Add becomes a Request.
   */
  mayAdd: boolean
  /**
   * Where this account's requests land, or 0 when they hold more than one
   * library and haven't chosen — the only case worth asking about.
   */
  defaultLibraryId: number
  /**
   * True when this page came from the internet-facing listener, where
   * admin routes don't exist. An admin signing in from outside is shown
   * the requesting side only — the settings they'd otherwise reach for
   * answer 404 there, which reads as a broken install rather than as a
   * boundary doing its job.
   */
  external: boolean
}

const AccessContext = createContext<Access>({
  user: null, isAdmin: true, libraryIds: [], mayAdd: true, defaultLibraryId: 0,
  external: false,
})

export const AccessProvider = AccessContext.Provider

export function accessFor(user: ApiUser | null, external = false): Access {
  return {
    user,
    external,
    // an admin is still an admin; they just cannot administer from out
    // there, because those routes are not served on that listener
    isAdmin: !external && (user === null || user.role === "admin"),
    libraryIds: user?.libraryIds ?? [],
    // an open install and admins always may; everyone else as stored
    // Nobody adds from the portal, whatever their account says: the add
    // routes are not served out there, so an Add button would answer 404.
    // Everyone asks instead, which is what the portal is for.
    mayAdd: !external && (user === null || user.role === "admin" || user.mayAdd),
    defaultLibraryId: user?.defaultLibraryId ?? 0,
  }
}

export function useAccess(): Access {
  return useContext(AccessContext)
}

/** Shorthand for the common "should this control render at all" check. */
export function useIsAdmin(): boolean {
  return useContext(AccessContext).isAdmin
}

/** True when this account asks for titles rather than adding them. */
export function useIsRequester(): boolean {
  return !useContext(AccessContext).mayAdd
}
