import { beforeEach, describe, expect, it } from 'vitest'
import { getPanelRoute, opensDocument, resetPanelRoute, setPanelRoute, setPanelRouteStorage } from './routeStore'
import { routeMemoryKey } from '../routeMemory'

/** A Storage stand-in that behaves like localStorage for one key space. */
function memoryStorage(initial?: Record<string, string>) {
  const data = new Map<string, string>(Object.entries(initial ?? {}))
  return {
    data,
    getItem(key: string) {
      return data.has(key) ? (data.get(key) as string) : null
    },
    setItem(key: string, value: string) {
      data.set(key, value)
    },
  }
}

describe('opensDocument', () => {
  it('accepts a work route and a node route', () => {
    expect(opensDocument('#/w/abc')).toBe(true)
    expect(opensDocument('#/s/space-1/n/node-1')).toBe(true)
  })

  it('rejects a space route and the empty route', () => {
    // A folder selection is a space route too; the document panel would show an
    // empty body, so only routes that name a node count.
    expect(opensDocument('#/s/space-1')).toBe(false)
    expect(opensDocument('#/')).toBe(false)
  })
})

describe('setPanelRoute', () => {
  beforeEach(() => resetPanelRoute())

  it('keeps the route both panels render from', () => {
    setPanelRoute('#/s/a/n/b', 'user-a')
    expect(getPanelRoute('user-a')).toBe('#/s/a/n/b')
  })

  it('writes the place the account is looking at', () => {
    const storage = memoryStorage()
    setPanelRouteStorage(storage)
    setPanelRoute('#/s/a/n/b', 'user-a')
    expect(storage.data.get(routeMemoryKey('user-a'))).toContain('#/s/a/n/b')
  })

  it('reads the remembered place on the first snapshot', () => {
    // The panels must render the memory immediately: an effect that navigated
    // afterwards would lose the race against the personal-space redirect.
    const storage = memoryStorage({
      [routeMemoryKey('user-a')]: JSON.stringify({ v: 1, r: '#/s/a/n/b' }),
    })
    setPanelRouteStorage(storage)
    expect(getPanelRoute('user-a')).toBe('#/s/a/n/b')
  })

  it('does not resume another account, and falls back to the list', () => {
    const storage = memoryStorage({
      [routeMemoryKey('someone-else')]: JSON.stringify({ v: 1, r: '#/s/a' }),
    })
    setPanelRouteStorage(storage)
    expect(getPanelRoute('user-a')).toBe('#/')
  })

  it('switches memory when the account changes without a reload', () => {
    const storage = memoryStorage()
    setPanelRouteStorage(storage)
    setPanelRoute('#/s/a', 'user-a')
    // A new account signs in on the same page: it must not inherit the route.
    expect(getPanelRoute('user-b')).toBe('#/')
    setPanelRoute('#/s/b', 'user-b')
    expect(getPanelRoute('user-a')).toBe('#/s/a')
    expect(getPanelRoute('user-b')).toBe('#/s/b')
  })

  it('does not remember the bare list', () => {
    const storage = memoryStorage()
    setPanelRouteStorage(storage)
    setPanelRoute('#/s/a', 'user-a')
    setPanelRoute('#/', 'user-a')
    expect(getPanelRoute('user-a')).toBe('#/')
    expect(storage.data.get(routeMemoryKey('user-a'))).toContain('#/s/a')
  })

  it('degrades to the list when storage is unavailable', () => {
    setPanelRouteStorage(null)
    expect(getPanelRoute('user-a')).toBe('#/')
    expect(() => setPanelRoute('#/s/a', 'user-a')).not.toThrow()
    expect(getPanelRoute('user-a')).toBe('#/s/a')
  })
})
