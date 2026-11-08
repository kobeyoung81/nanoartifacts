import { describe, expect, it } from 'vitest'
import {
  loadRoute,
  rememberable,
  routeMemoryKey,
  ROUTE_MEMORY_KEY,
  ROUTE_MEMORY_VERSION,
  saveRoute,
} from './routeMemory'

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

describe('rememberable', () => {
  it('accepts the routes that name a place', () => {
    expect(rememberable('#/s/space-1')).toBe(true)
    expect(rememberable('#/s/space-1/n/node-1')).toBe(true)
    expect(rememberable('#/w/work-1')).toBe(true)
    // The pre-spaces link shape still parses to a work.
    expect(rememberable('#/works/work-1')).toBe(true)
  })

  it('rejects the list and anything unparseable', () => {
    expect(rememberable('#/')).toBe(false)
    expect(rememberable('')).toBe(false)
    expect(rememberable('#/nonsense')).toBe(false)
  })
})

describe('route memory', () => {
  it('round-trips the last place per account', () => {
    const storage = memoryStorage()
    saveRoute(storage, 'user-a', '#/s/space-1/n/node-1')
    expect(loadRoute(storage, 'user-a')).toBe('#/s/space-1/n/node-1')
    expect(storage.data.get(routeMemoryKey('user-a'))).toContain(`"v":${ROUTE_MEMORY_VERSION}`)
  })

  it('keys the memory by account, not by browser', () => {
    const storage = memoryStorage()
    saveRoute(storage, 'user-a', '#/s/space-1')
    saveRoute(storage, 'user-b', '#/s/space-2')
    expect(loadRoute(storage, 'user-a')).toBe('#/s/space-1')
    expect(loadRoute(storage, 'user-b')).toBe('#/s/space-2')
    expect(loadRoute(storage, 'user-c')).toBeNull()
  })

  it('keeps the latest place and ignores the bare list', () => {
    const storage = memoryStorage()
    saveRoute(storage, 'user-a', '#/s/space-1')
    saveRoute(storage, 'user-a', '#/')
    expect(loadRoute(storage, 'user-a')).toBe('#/s/space-1')
    saveRoute(storage, 'user-a', '#/s/space-2/n/node-2')
    expect(loadRoute(storage, 'user-a')).toBe('#/s/space-2/n/node-2')
  })

  it('rejects a value it cannot trust', () => {
    const storage = memoryStorage()
    const write = (raw: string) => storage.data.set(routeMemoryKey('user-a'), raw)
    write('not json')
    expect(loadRoute(storage, 'user-a')).toBeNull()
    write(JSON.stringify({ v: 99, r: '#/s/space-1' }))
    expect(loadRoute(storage, 'user-a')).toBeNull()
    write(JSON.stringify({ v: ROUTE_MEMORY_VERSION, r: '#/' }))
    expect(loadRoute(storage, 'user-a')).toBeNull()
    write(JSON.stringify({ v: ROUTE_MEMORY_VERSION, r: 7 }))
    expect(loadRoute(storage, 'user-a')).toBeNull()
  })

  it('shares one key prefix, one entry per account', () => {
    const storage = memoryStorage()
    saveRoute(storage, 'user-a', '#/s/space-1')
    const keys = [...storage.data.keys()]
    expect(keys).toHaveLength(1)
    expect(keys[0].startsWith(`${ROUTE_MEMORY_KEY}.`)).toBe(true)
  })

  it('degrades to no memory when storage is absent or throws', () => {
    expect(loadRoute(null, 'user-a')).toBeNull()
    expect(() => saveRoute(null, 'user-a', '#/s/space-1')).not.toThrow()
    const broken = {
      getItem() {
        throw new Error('blocked')
      },
      setItem() {
        throw new Error('blocked')
      },
    }
    expect(loadRoute(broken, 'user-a')).toBeNull()
    expect(() => saveRoute(broken, 'user-a', '#/s/space-1')).not.toThrow()
  })
})
