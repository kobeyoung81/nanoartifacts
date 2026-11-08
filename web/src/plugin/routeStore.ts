// The route two panels share.
//
// The library panel and the document panel are separate workbench panels, but
// they show one library: selecting a node in one has to select it in the other.
// A module-level store is enough — both panels live in the same page, and the
// app already routes on a single hash string.
//
// The store also remembers that route, so reopening the panels (a portal
// reload, a new window) resumes the space and the document instead of starting
// over at "#/". The value lives in the same key the standalone page uses
// (routeMemory), scoped to the account that owns it.

import { useSyncExternalStore } from 'react'
import { loadRoute, saveRoute, type RouteStorage } from '../routeMemory'

let route: string | null = null
/** The account `route` was resolved for; null while nothing is resolved. */
let owner: string | null = null
/** Test seam: undefined reads the browser's storage, null means none. */
let storageOverride: RouteStorage | undefined

const listeners = new Set<() => void>()

// Storage is optional: private mode and blocked partitions throw on access,
// and a missing memory is not an error.
function storage(): RouteStorage {
  if (storageOverride !== undefined) {
    return storageOverride
  }
  try {
    return typeof window === 'undefined' ? null : window.localStorage
  } catch {
    return null
  }
}

/**
 * getPanelRoute is the hash both panels render from. The memory is read here,
 * on the first snapshot for an account, so the first render is already the
 * remembered place — restoring from an effect would render "#/" first, and the
 * personal-space redirect would overwrite the memory before it was used.
 */
export function getPanelRoute(who: string): string {
  if (route === null || owner !== who) {
    owner = who
    route = loadRoute(storage(), who) ?? '#/'
  }
  return route
}

export function setPanelRoute(next: string, who: string): void {
  if (next === getPanelRoute(who)) {
    return
  }
  route = next
  saveRoute(storage(), who, next)
  for (const listener of listeners) {
    listener()
  }
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

/** usePanelRoute re-renders a panel when either of them navigates. */
export function usePanelRoute(who: string): string {
  return useSyncExternalStore(
    subscribe,
    () => getPanelRoute(who),
    () => getPanelRoute(who),
  )
}

/** opensDocument reports whether a route names something with a body to show. */
export function opensDocument(hash: string): boolean {
  return /^#\/w\//.test(hash) || /^#\/s\/[^/]+\/n\//.test(hash)
}

/** Test seam: forget the route, its owner and any forced storage. */
export function resetPanelRoute(): void {
  route = null
  owner = null
  storageOverride = undefined
  listeners.clear()
}

/** Test seam: force the storage the store reads and writes (null = none). */
export function setPanelRouteStorage(next: RouteStorage | undefined): void {
  storageOverride = next
  route = null
  owner = null
}
