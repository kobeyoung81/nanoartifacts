import { describe, expect, it } from 'vitest'
import {
  dropMode,
  formatSize,
  kindLabel,
  orderAfterMove,
  parseRoute,
  portalWorkURL,
  previewBootstrap,
  readStoredBool,
  readStoredNumber,
  readStoredString,
  workHash,
  writeStored,
} from './lib'

describe('parseRoute', () => {
  it('falls back to the space list', () => {
    expect(parseRoute('')).toEqual({ name: 'spaces' })
    expect(parseRoute('#/')).toEqual({ name: 'spaces' })
    expect(parseRoute('#/nope')).toEqual({ name: 'spaces' })
  })

  it('reads a space and its node', () => {
    expect(parseRoute('#/s/sp1')).toEqual({ name: 'space', spaceId: 'sp1' })
    expect(parseRoute('#/s/sp1/n/n1')).toEqual({ name: 'space', spaceId: 'sp1', nodeId: 'n1' })
  })

  it('reads a work link, old and new form', () => {
    expect(parseRoute('#/w/abc123')).toEqual({ name: 'work', workId: 'abc123' })
    expect(parseRoute('#/works/abc123')).toEqual({ name: 'work', workId: 'abc123' })
  })

  it('decodes escaped ids', () => {
    expect(parseRoute('#/s/a%2Fb')).toEqual({ name: 'space', spaceId: 'a/b' })
    expect(parseRoute('#/w/a%2Fb')).toEqual({ name: 'work', workId: 'a/b' })
  })

  it('round-trips through workHash', () => {
    expect(parseRoute(workHash('a/b c'))).toEqual({ name: 'work', workId: 'a/b c' })
  })
})

describe('portalWorkURL', () => {
  it('points at the portal route so a new tab keeps the chrome', () => {
    expect(portalWorkURL('abc123')).toBe('/artifacts?work=abc123')
  })

  it('escapes the id', () => {
    expect(portalWorkURL('a/b')).toBe('/artifacts?work=a%2Fb')
  })
})

describe('previewBootstrap', () => {
  it('mints inside the portal frame, probes in a top-level tab', () => {
    expect(previewBootstrap(true)).toBe('mint')
    expect(previewBootstrap(false)).toBe('probe')
  })
})

describe('formatSize', () => {
  it('prints bytes without a decimal', () => {
    expect(formatSize(512)).toBe('512 B')
  })

  it('keeps one decimal above a kilobyte', () => {
    expect(formatSize(5 * 1024 * 1024)).toBe('5.0 MB')
  })

  it('handles zero and junk', () => {
    expect(formatSize(0)).toBe('0 B')
    expect(formatSize(Number.NaN)).toBe('0 B')
  })
})

describe('kindLabel', () => {
  it('translates by locale prefix', () => {
    expect(kindLabel('doc', 'zh-CN')).toBe('文档')
    expect(kindLabel('doc', 'en-US')).toBe('Document')
  })

  it('passes an unknown kind through', () => {
    expect(kindLabel('xls', 'zh-CN')).toBe('xls')
  })
})

describe('dropMode', () => {
  it('splits a folder into before / inside / after', () => {
    expect(dropMode(0, 100, true)).toBe('before')
    expect(dropMode(29, 100, true)).toBe('before')
    expect(dropMode(50, 100, true)).toBe('inside')
    expect(dropMode(71, 100, true)).toBe('after')
    expect(dropMode(100, 100, true)).toBe('after')
  })

  it('never lets a file contain anything', () => {
    expect(dropMode(20, 100, false)).toBe('before')
    expect(dropMode(50, 100, false)).toBe('after')
    expect(dropMode(90, 100, false)).toBe('after')
  })

  it('falls back when a row has no height yet', () => {
    expect(dropMode(0, 0, true)).toBe('after')
  })
})

describe('orderAfterMove', () => {
  it('moves to the front', () => {
    expect(orderAfterMove(['a', 'b', 'c'], 'c', 'a')).toEqual(['c', 'a', 'b'])
  })

  it('moves between two rows', () => {
    expect(orderAfterMove(['a', 'b', 'c'], 'a', 'c')).toEqual(['b', 'a', 'c'])
  })

  it('appends when there is no before id', () => {
    expect(orderAfterMove(['a', 'b', 'c'], 'a', '')).toEqual(['b', 'c', 'a'])
  })

  it('is a no-op when the before id is gone', () => {
    expect(orderAfterMove(['a', 'b'], 'a', 'missing')).toEqual(['b', 'a'])
  })
})

describe('stored preferences', () => {
  const storage = (value: string | null) => ({ getItem: () => value })

  it('clamps a width into range', () => {
    expect(readStoredNumber(storage('120'), 'k', 250, 180, 420)).toBe(180)
    expect(readStoredNumber(storage('900'), 'k', 250, 180, 420)).toBe(420)
    expect(readStoredNumber(storage('300'), 'k', 250, 180, 420)).toBe(300)
  })

  it('ignores junk and missing values', () => {
    expect(readStoredNumber(storage('wide'), 'k', 250, 180, 420)).toBe(250)
    expect(readStoredNumber(storage(null), 'k', 250, 180, 420)).toBe(250)
    expect(readStoredBool(storage(null), 'k', true)).toBe(true)
    expect(readStoredBool(storage('0'), 'k', true)).toBe(false)
  })

  it('returns a stored string, and treats a missing or empty one as absent', () => {
    expect(readStoredString(storage('nw.na.route'), 'k', '')).toBe('nw.na.route')
    expect(readStoredString(storage(''), 'k', 'fallback')).toBe('fallback')
    expect(readStoredString(storage(null), 'k', 'fallback')).toBe('fallback')
    expect(readStoredString(null, 'k', 'fallback')).toBe('fallback')
  })

  it('survives a storage that throws', () => {
    const broken = {
      getItem: () => {
        throw new Error('blocked')
      },
    }
    expect(readStoredNumber(broken, 'k', 250, 180, 420)).toBe(250)
    expect(readStoredBool(broken, 'k', true)).toBe(true)
    expect(() => writeStored({ setItem: () => { throw new Error('blocked') } }, 'k', '1')).not.toThrow()
  })
})
