// Pure helpers, kept out of the components so they can be tested without a DOM
// (the test environment is node, matching the portal's setup).

export type Route =
  | { name: 'spaces' }
  | { name: 'space'; spaceId: string; nodeId?: string }
  | { name: 'work'; workId: string }

export function parseRoute(hash: string): Route {
  const path = hash.replace(/^#/, '')
  const node = /^\/s\/([^/?#]+)\/n\/([^/?#]+)/.exec(path)
  if (node) {
    return {
      name: 'space',
      spaceId: decodeURIComponent(node[1]),
      nodeId: decodeURIComponent(node[2]),
    }
  }
  const space = /^\/s\/([^/?#]+)/.exec(path)
  if (space) return { name: 'space', spaceId: decodeURIComponent(space[1]) }
  const work = /^\/w\/([^/?#]+)/.exec(path)
  if (work) return { name: 'work', workId: decodeURIComponent(work[1]) }
  // Links published before spaces existed used #/works/<id>.
  const legacy = /^\/works\/([^/?#]+)/.exec(path)
  if (legacy) return { name: 'work', workId: decodeURIComponent(legacy[1]) }
  return { name: 'spaces' }
}

export function workHash(id: string): string {
  return `#/works/${encodeURIComponent(id)}`
}

// portalWorkURL is the link to one work as the rest of the platform sees it:
// the portal's own route, so a new tab or a refresh keeps the portal chrome.
// The hash form above is what the framed app routes on internally.
export function portalWorkURL(id: string): string {
  return `/artifacts?work=${encodeURIComponent(id)}`
}

// previewBootstrap says how a detail view gets its credential before loading a
// body. Inside the portal frame we hold a JWT and may mint/refresh the session
// cookie; a top-level tab has no token and must not call the bearer-only mint
// endpoint — it probes the cookie it already carries instead.
export function previewBootstrap(hasToken: boolean): 'mint' | 'probe' {
  return hasToken ? 'mint' : 'probe'
}

// formatSize keeps one decimal: "5.3 MB" is what the list shows, and rounding
// to integers would print 5 MB for files whose size is the reason a preview
// feels slow.
export function formatSize(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes <= 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return unit === 0 ? `${value} B` : `${value.toFixed(1)} ${units[unit]}`
}

const LABELS: Record<string, { zh: string; en: string }> = {
  doc: { zh: '文档', en: 'Document' },
  ppt: { zh: '演示', en: 'Deck' },
  ui: { zh: '原型', en: 'Prototype' },
  pro: { zh: '交互', en: 'Interactive' },
  html: { zh: '网页', en: 'Page' },
}

export function kindLabel(kind: string, locale: string): string {
  const entry = LABELS[kind]
  if (!entry) return kind
  return locale.toLowerCase().startsWith('zh') ? entry.zh : entry.en
}

// formatDate renders an ISO timestamp as a short local date. The library is
// scanned, not audited: the exact minute is noise.
export function formatDate(iso: string, locale: string): string {
  const date = new Date(iso)
  if (Number.isNaN(date.getTime())) return iso
  return new Intl.DateTimeFormat(locale, {
    year: 'numeric',
    month: 'short',
    day: 'numeric',
    hour: '2-digit',
    minute: '2-digit',
  }).format(date)
}

// ---- workbench interactions ------------------------------------------------

export type DropMode = 'before' | 'inside' | 'after'

// dropMode reads where a row being dragged would land. Folders accept a middle
// band ("put it inside"); documents and apps only before/after, because a file
// cannot contain anything. The test pins the boundaries: 30% and 70% for a
// folder, 50% for a file.
export function dropMode(offsetY: number, height: number, isFolder: boolean): DropMode {
  if (height <= 0) return 'after'
  const ratio = offsetY / height
  if (!isFolder) return ratio < 0.5 ? 'before' : 'after'
  if (ratio < 0.3) return 'before'
  if (ratio > 0.7) return 'after'
  return 'inside'
}

// orderAfterMove is the sibling order after dropping movingID in front of
// beforeID (empty means the end). It mirrors what the server computes, so the
// UI can predict the result; the test covers both ends of the list.
export function orderAfterMove(ids: string[], movingID: string, beforeID: string): string[] {
  const rest = ids.filter((id) => id !== movingID)
  const at = beforeID ? rest.indexOf(beforeID) : -1
  const index = at < 0 ? rest.length : at
  return [...rest.slice(0, index), movingID, ...rest.slice(index)]
}

// The column widths and the two collapsed flags survive a reload. Storage can
// throw (private mode, blocked third-party storage), so every read falls back
// and every write is swallowed: a missing preference is not an error.
export type Readable = { getItem(key: string): string | null }
export type Writable = { setItem(key: string, value: string): void }

export function readStoredNumber(
  storage: Readable | null,
  key: string,
  fallback: number,
  min: number,
  max: number,
): number {
  if (!storage) return fallback
  let raw: string | null = null
  try {
    raw = storage.getItem(key)
  } catch {
    return fallback
  }
  if (raw === null || raw === '') return fallback
  const value = Number(raw)
  if (!Number.isFinite(value)) return fallback
  return Math.min(max, Math.max(min, value))
}

export function readStoredBool(storage: Readable | null, key: string, fallback: boolean): boolean {
  if (!storage) return fallback
  let raw: string | null = null
  try {
    raw = storage.getItem(key)
  } catch {
    return fallback
  }
  if (raw === null) return fallback
  return raw === '1' || raw === 'true'
}

export function readStoredString(storage: Readable | null, key: string, fallback: string): string {
  if (!storage) return fallback
  let raw: string | null = null
  try {
    raw = storage.getItem(key)
  } catch {
    return fallback
  }
  // An empty value means "nothing stored": every caller here writes a non-empty
  // payload, so '' is the same as a missing key.
  if (raw === null || raw === '') return fallback
  return raw
}

export function writeStored(storage: Writable | null, key: string, value: string): void {
  if (!storage) return
  try {
    storage.setItem(key, value)
  } catch {
    // Private mode or a blocked storage partition: forget the preference.
  }
}
