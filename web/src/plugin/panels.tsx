// The two panels this application contributes to the portal's workbench.
//
// Both render the same `App`, only with a different mode: the library panel owns
// the space switcher, the tree and the detail column; the document panel owns the
// body. They share one route store, so selecting a node in the tree opens its
// document in the other panel — no postMessage, no second copy of the data.

import { useCallback } from 'react'
import { App } from '../app'
import cssText from '../styles.css?inline'
import panelText from '../panel.css?inline'
import { opensDocument, setPanelRoute, usePanelRoute } from './routeStore'

/**
 * The panel contract, structurally. The portal's contract module is the source
 * of truth; this is the same shape without importing across the two builds.
 */
export type PanelContext = {
  token: string
  userId: string
  locale: string
  theme: 'light' | 'dark'
  navigate: (path: string) => void
  openPanel: (panelId: string) => void
}

// Three names the portal does not define, mapped onto the tokens it does. The
// rest of the palette is inherited, which is the point of a panel: a theme
// change in the shell reaches this UI without a release here.
const ALIASES = `
/* The remote build drops :root/.dark, so the plugin root carries the browser's
   colour scheme itself; any native control inside a panel then follows the
   shell's theme instead of the desktop's. */
.plugin-nanoartifacts { color-scheme: light; }
html.dark .plugin-nanoartifacts { color-scheme: dark; }
.plugin-nanoartifacts {
  /* The wrapper the panel renders inside. Every rule in this bundle is written
     as ".plugin-nanoartifacts ...", so this element has to exist here and has to
     own the height the workbench gives the panel. */
  height: 100%;
  min-height: 0;
  display: flex;
  flex-direction: column;
  --primary-tint: color-mix(in oklab, var(--primary) 12%, transparent);
  --radius-xl: calc(var(--radius) + 0.25rem);
  --font: Inter, ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, 'Segoe UI', 'Noto Color Emoji', sans-serif;
}
@supports not (color: color-mix(in oklab, red, blue)) {
  .plugin-nanoartifacts { --primary-tint: rgb(21 93 252 / 12%); }
  html.dark .plugin-nanoartifacts { --primary-tint: rgb(107 162 233 / 16%); }
}
`

// The remote build has no index.html of its own, so nothing injects the
// stylesheet the way a page load would. One <style> per page, shared by both
// panels; the CSS is already scoped to this plugin at build time.
function ensureStyles(): void {
  if (typeof document === 'undefined' || document.querySelector('style[data-plugin="nanoartifacts"]')) {
    return
  }
  const style = document.createElement('style')
  style.dataset.plugin = 'nanoartifacts'
  style.textContent = `${cssText}\n${panelText}\n${ALIASES}`
  document.head.appendChild(style)
}

function usePanelNavigate(ctx: PanelContext) {
  return useCallback(
    (hash: string) => {
      setPanelRoute(hash, ctx.userId)
      // Anything with a body is worth showing in the document panel; a folder is
      // not, and opening it there would replace the document with an empty state.
      if (opensDocument(hash)) {
        ctx.openPanel('nanoartifacts.document')
      }
    },
    [ctx],
  )
}

export function LibraryPanel({ ctx }: { ctx: PanelContext }) {
  ensureStyles()
  const route = usePanelRoute(ctx.userId)
  const navigate = usePanelNavigate(ctx)
  return (
    <div className="plugin-nanoartifacts" data-nanoartifacts-panel="library">
      <App
        host={{
          token: ctx.token,
          locale: ctx.locale,
          mode: 'library',
          route,
          navigate,
        }}
      />
    </div>
  )
}

export function DocumentPanel({ ctx }: { ctx: PanelContext }) {
  ensureStyles()
  const route = usePanelRoute(ctx.userId)
  const navigate = usePanelNavigate(ctx)
  return (
    <div className="plugin-nanoartifacts" data-nanoartifacts-panel="document">
      <App
        host={{
          token: ctx.token,
          locale: ctx.locale,
          mode: 'document',
          route,
          navigate,
        }}
      />
    </div>
  )
}
