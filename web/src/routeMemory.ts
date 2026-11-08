// The library remembers the place its owner last had open.
//
// Two surfaces show the same library — the portal's workbench panels (which
// share one route, see plugin/routeStore) and the standalone page (which routes
// on the URL hash) — so they read and write the same key. The key carries the
// account: a browser a second user signs into must not resume the first user's
// space, and a stale id would only produce the "space does not exist" card.

import { parseRoute, readStoredString, writeStored, type Readable, type Writable } from './lib'

export const ROUTE_MEMORY_KEY = 'nw.na.route'
export const ROUTE_MEMORY_VERSION = 1

/** The part of Storage this module needs. Null means "no storage available". */
export type RouteStorage = (Readable & Writable) | null

type StoredRoute = { v: number; r: string }

/** routeMemoryKey keeps one place per account on a shared browser. */
export function routeMemoryKey(owner: string): string {
  return `${ROUTE_MEMORY_KEY}.${encodeURIComponent(owner)}`
}

/**
 * rememberable says whether a hash names a place worth resuming. A bare "#/" is
 * the list — an action, not a place — and must never overwrite the memory; the
 * personal-space redirect that follows it stores a real space anyway.
 */
export function rememberable(hash: string): boolean {
  const route = parseRoute(hash)
  return route.name === 'space' || route.name === 'work'
}

/** loadRoute returns the remembered hash for this account, or null. */
export function loadRoute(storage: RouteStorage, owner: string): string | null {
  const raw = readStoredString(storage, routeMemoryKey(owner), '')
  if (!raw) return null
  let parsed: StoredRoute
  try {
    parsed = JSON.parse(raw) as StoredRoute
  } catch {
    return null
  }
  if (!parsed || parsed.v !== ROUTE_MEMORY_VERSION) return null
  if (typeof parsed.r !== 'string' || !rememberable(parsed.r)) return null
  return parsed.r
}

/** saveRoute remembers a place. Everything else is ignored rather than stored. */
export function saveRoute(storage: RouteStorage, owner: string, hash: string): void {
  if (!rememberable(hash)) return
  const value: StoredRoute = { v: ROUTE_MEMORY_VERSION, r: hash }
  writeStored(storage, routeMemoryKey(owner), JSON.stringify(value))
}
