import { describe, expect, it } from 'vitest'
import app from '../app.tsx?raw'
import panels from './panels.tsx?raw'
import styles from '../styles.css?raw'
import panelStyles from '../panel.css?raw'
import sidebar from '../library/SidebarView.tsx?raw'
import detail from '../library/DetailView.tsx?raw'
import dialogs from '../library/dialogs.tsx?raw'
import documentView from '../library/DocumentView.tsx?raw'
import spaceMenu from '../library/SpaceMenu.tsx?raw'

// The panel is assembled from files, not from one 1900-line component: the page
// shell, three views, the modal surfaces and the tree helpers. These checks pin
// the links between them, because a missing link compiles and renders — it just
// leaves the panel showing the wrong thing.

describe('panel assembly', () => {
  it('renders one view at a time, without the three-column frame', () => {
    // A panel renders one view; a page host asks for the whole library, which
    // is why the surface is explicit rather than inferred from the mode.
    expect(app).toContain("panelView={host && host.surface !== 'page' ? host.mode : undefined}")
    expect(app).toContain("useState<'tree' | 'detail'>('tree')")
    expect(app).toContain('screen === \'detail\' ?')
    expect(app).toContain('className="panel-view"')
    // The page still owns the frame; the panel must not render it.
    expect(app).toContain('className={`nz${')
  })

  it('gives the document panel the document view, not the page layout', () => {
    // The mode used to be a boolean ("is it the library panel?"), so the
    // document panel fell through to the page's three columns — sidebar, grip
    // and all.
    expect(app).toContain("panelView === 'document' ? (")
    expect(app).toContain('<DocumentView')
  })

  it('keeps the height chain from the host to the view', () => {
    // A missing link here is silent: `height: 100%` resolves against an auto
    // parent and an iframe falls back to its 150px default.
    expect(panelStyles).toMatch(/\.panel-root \{[^}]*height: 100%/)
    expect(panelStyles).toMatch(/\.panel-view \{[^}]*flex: 1 1 auto/)
    expect(panelStyles).toMatch(/\.panel-view \{[^}]*min-height: 0/)
    expect(app).toContain('panel-root')
  })

  it('keeps the panel\'s own type baseline', () => {
    // `body` and `.nz` both carry 13px; a panel renders neither.
    expect(panelStyles).toMatch(/\.panel-view \{[^}]*font-size: 13px/)
  })

  it('moves the details from a column to a view inside the panel', () => {
    expect(app).toContain('onBack={() => setScreen(\'tree\')}')
    // The row menu's details action differs per surface.
    expect(app).toContain("if (panelView === 'library') {")
    expect(app).toContain("setScreen('detail')")
    // The panel no longer needs its own detail-open key.
    expect(app).not.toContain('nw.na.panel.detail')
    // The corner × collapses a column; a panel has no column.
    expect(app).toContain('{!panelView && detailOpen ? (')
  })

  it('keeps the panel free of the page-only resize grip', () => {
    // The grip is only rendered when the page hands the view a resize handler.
    expect(sidebar).toContain('onStartResize ?')
    expect(sidebar).toContain('onMouseDown={onStartResize(\'side\')}')
    expect(panelStyles).toContain('.panel-view')
    expect(panelStyles).not.toContain('.panel-library .nz')
  })

  it('has a way back from the details', () => {
    expect(detail).toContain('onBack')
    expect(detail).toContain('返回作品库')
    expect(styles).toMatch(/\.nz-detail-back \{/)
  })
})

describe('space switcher', () => {
  it('is drawn by the app, not by the native select menu', () => {
    // A native <option> list is painted by the OS; it cannot follow the shell's
    // theme, which is why the switcher is a custom menu now.
    expect(sidebar).not.toContain('<select')
    expect(sidebar).toContain('<SpaceMenu')
    expect(spaceMenu).toContain('nz-space-menu')
    expect(spaceMenu).toContain('role="listbox"')
  })

  it('closes on an outside click and on Escape', () => {
    expect(spaceMenu).toContain("document.addEventListener('mousedown'")
    expect(spaceMenu).toContain("event.key === 'Escape'")
    expect(spaceMenu).toContain('onNewSpace')
  })

  it('tells native controls which colour scheme to use', () => {
    // The dialog selects and scrollbars follow this; the remote build drops
    // :root/.dark, so the plugin root declares it too.
    expect(styles).toContain('color-scheme: light')
    expect(styles).toContain('color-scheme: dark')
    expect(panels).toContain('html.dark .plugin-nanoartifacts')
    expect(panels).toContain('color-scheme: dark')
  })
})

describe('module boundaries', () => {
  it('keeps the modal surfaces out of the page shell', () => {
    expect(app).not.toContain('function TransferDialog')
    expect(app).not.toContain('function SpaceSettings')
    expect(dialogs).toContain('export function TransferDialog')
    expect(dialogs).toContain('export function SpaceSettings')
  })

  it('keeps the tree rendering out of the page shell', () => {
    expect(app).not.toContain('function renderTree')
    expect(app).not.toContain('function TreeIcon')
  })

  it('keeps the page shell to composition', () => {
    expect(app).toContain('<SidebarView')
    expect(app).toContain('<DocumentView')
    expect(app).toContain('<DetailView')
  })
})

describe('narrow screens', () => {
  it('hides the page columns without hiding them inside a panel', () => {
    // Below 820px the page keeps only its body column. A panel renders the tree
    // (or the details) as its whole content and has no `.nz` frame, so the rule
    // must not match there: matching it left the panel a blank rectangle with
    // nothing to click and no error to notice.
    expect(styles).toMatch(
      /@media \(max-width: 820px\) \{[\s\S]*?\.nz > \.nz-side,\s*\.nz > \.nz-detail \{\s*display: none;/,
    )
    // The panel says the opposite, and says it where a panel's styles live.
    expect(panelStyles).toMatch(
      /\.panel-view > \.nz-side,\s*\.panel-view > \.nz-detail \{[^}]*display: flex;/,
    )
  })
})

describe('route memory', () => {
  it('scopes the shared route to the account that owns it', () => {
    expect(panels).toContain('setPanelRoute(hash, ctx.userId)')
    expect(panels).toContain('usePanelRoute(ctx.userId)')
  })

  it('restores the last place once, before the personal-space redirect', () => {
    expect(app).toContain('loadRoute(browserStorage(), userId)')
    expect(app).toContain('saveRoute(browserStorage(), userId, hash)')
    // One restore per page load, and it runs before the "#/ = my space" branch:
    // two effects would race and the redirect would overwrite the memory.
    expect(app).toContain('!restoredRef.current')
    const restore = app.indexOf('restoredRef.current = true')
    const home = app.indexOf('const home = spaces.find')
    expect(restore).toBeGreaterThan(0)
    expect(home).toBeGreaterThan(restore)
  })
})

describe('document frame', () => {
  it('stays in an opaque-origin sandbox', () => {
    // The frame lives in DocumentView now; the attribute is the second gate
    // behind the raw route's CSP.
    expect(documentView).toContain('sandbox="allow-scripts"')
    expect(documentView).not.toContain('allow-same-origin')
    expect(app).not.toContain('allow-same-origin')
  })
})

describe('panel entry', () => {
  it('exports the two components the manifest names', () => {
    expect(panels).toContain('export function LibraryPanel')
    expect(panels).toContain('export function DocumentPanel')
  })

  it('mounts each panel inside the scoped wrapper', () => {
    expect(panels.match(/className="plugin-nanoartifacts"/g)?.length).toBe(2)
    expect(panels).toContain('data-nanoartifacts-panel="library"')
    expect(panels).toContain('data-nanoartifacts-panel="document"')
  })

  it('injects its stylesheet once, scoped at build time', () => {
    expect(panels).toContain('data-plugin="nanoartifacts"')
    expect(panels).toContain("from '../styles.css?inline'")
  })
})
