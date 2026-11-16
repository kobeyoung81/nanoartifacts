import { describe, expect, it } from 'vitest'
import manifest from '../../public/plugin.json'
import remoteConfig from '../../vite.config.remote.ts?raw'
import page from './page.tsx?raw'
import { hashFromSearch, portalQueryFor } from './page'

// The page contribution: the library as a route the portal mounts, instead of a
// frame. What has to hold: the manifest names it, the federation build exposes
// it, the bundle lives in the shared /plugins/ namespace, and the portal URL and
// the app's fragment translate into each other.

describe('page contribution', () => {
  it('is declared in the manifest with the module the build exposes', () => {
    expect(manifest.pages.map((entry) => entry.path)).toEqual(['/artifacts'])
    expect(manifest.pages[0].mount).toEqual({
      kind: 'module',
      module: 'nanoartifacts/page',
      export: 'LibraryPage',
    })
  })

  it('is exposed by the remote build and served from the plugin namespace', () => {
    expect(remoteConfig).toContain("'./page': './src/plugin/page.tsx'")
    expect(remoteConfig).toContain("base: '/plugins/artifacts/'")
    expect(manifest.entry).toBe('/plugins/artifacts/remoteEntry.js')
  })

  it('renders the library app in page mode inside the scoped wrapper', () => {
    expect(page).toContain('className="plugin-nanoartifacts plugin-page"')
    expect(page).toContain("mode: 'library'")
    expect(page).toContain('data-nanoartifacts-page="library"')
  })

  it('carries the box the absolutely-positioned layout needs', () => {
    // `.nz` is `position: absolute; inset: 0`. With no positioned, height-carrying
    // ancestor it resolves against the viewport, which covered the portal's 40px
    // header. The wrapper below the page header has to be that ancestor.
    expect(page).toContain("position: 'relative'")
    expect(page).toContain("flex: '1 1 auto'")
    expect(page).toContain('minHeight: 0')
  })
})

describe('portal URL ↔ app fragment', () => {
  it('reads a work', () => {
    expect(hashFromSearch('?work=w%2F1')).toBe('#/w/w%2F1')
  })

  it('reads a space and its node', () => {
    expect(hashFromSearch('?space=s1')).toBe('#/s/s1')
    expect(hashFromSearch('?space=s1&node=n1')).toBe('#/s/s1/n/n1')
  })

  it('treats an empty selection as the library root', () => {
    expect(hashFromSearch('')).toBe('')
  })

  it('writes the portal query back from a fragment', () => {
    expect(portalQueryFor('#/w/w1')).toBe('?work=w1')
    expect(portalQueryFor('#/s/s1/n/n1')).toBe('?space=s1&node=n1')
    expect(portalQueryFor('#/')).toBe('')
  })

  it('round-trips a selection', () => {
    const search = '?space=s1&node=n1'
    expect(portalQueryFor(hashFromSearch(search))).toBe(search)
  })
})
