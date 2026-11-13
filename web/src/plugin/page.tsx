// The page this application contributes to the portal.
//
// Same `App` as the panels, in `library` mode: the work library as a screen of
// its own, rendered inside the portal's React tree instead of framed. The portal
// owns the route (`/artifacts`); the selection inside the library travels in the
// portal's query string, which is what keeps a refresh or a shared link on the
// same work.

import { useCallback, useState } from 'react'
import { App } from '../app'
import { ensureStyles } from './panels'

/**
 * The page contract, structurally. The portal's contract module is the source of
 * truth; this is the same shape without importing across the two builds.
 */
export type PageContext = {
  token: string
  userId: string
  locale: string
  theme: 'light' | 'dark'
  navigate: (path: string) => void
  openPanel: (panelId: string) => void
}

const decode = (raw: string) => {
  try {
    return decodeURIComponent(raw)
  } catch {
    return raw
  }
}

/** hashFromSearch turns the portal's ?space=&node=&work= into the app's fragment. */
export function hashFromSearch(search: string): string {
  const params = new URLSearchParams(search)
  const work = params.get('work')
  if (work) {
    return `#/w/${encodeURIComponent(work)}`
  }
  const space = params.get('space')
  if (space) {
    const node = params.get('node')
    return node
      ? `#/s/${encodeURIComponent(space)}/n/${encodeURIComponent(node)}`
      : `#/s/${encodeURIComponent(space)}`
  }
  return ''
}

/** portalQueryFor is its inverse: the portal keeps a path it can show and share. */
export function portalQueryFor(hash: string): string {
  const work = /^#\/w\/([^/?#]+)/.exec(hash) || /^#\/works\/([^/?#]+)/.exec(hash)
  if (work) {
    return `?work=${encodeURIComponent(decode(work[1]))}`
  }
  const space = /^#\/s\/([^/?#]+)(?:\/n\/([^/?#]+))?/.exec(hash)
  if (space) {
    const base = `?space=${encodeURIComponent(decode(space[1]))}`
    return space[2] ? `${base}&node=${encodeURIComponent(decode(space[2]))}` : base
  }
  return ''
}

export function LibraryPage({ ctx }: { ctx: PageContext }) {
  ensureStyles()
  // Read once, at mount: the page owns the library's position from here, and the
  // portal URL follows it (not the other way round).
  const [route, setRoute] = useState(() => hashFromSearch(window.location.search))
  const navigate = useCallback(
    (hash: string) => {
      setRoute(hash)
      ctx.navigate(`/artifacts${portalQueryFor(hash)}`)
    },
    [ctx],
  )

  return (
    <div className="plugin-nanoartifacts plugin-page" data-nanoartifacts-page="library">
      <App
        host={{
          token: ctx.token,
          locale: ctx.locale,
          mode: 'library',
          surface: 'page',
          route,
          navigate,
        }}
      />
    </div>
  )
}
