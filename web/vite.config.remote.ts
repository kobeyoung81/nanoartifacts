import { defineConfig } from 'vite'
import react from '@vitejs/plugin-react'
import { federation } from '@module-federation/vite'
import type { Plugin } from 'postcss'

// The remote build.
//
// Two things are different from the standalone build, and both exist because
// this bundle renders inside the portal's page rather than in a document of its
// own:
//
//   1. the stylesheet is scoped to `.plugin-nanoartifacts` (the shell's page has
//      its own rules, and a global `body`/`:root` from this app would repaint the
//      portal);
//   2. React comes from the host (`import: false` in `shared`), so the panel and
//      the shell share one copy — two Reacts would break hooks and context.
//
// Everything else is the same source as the standalone app.

const SCOPE = '.plugin-nanoartifacts'

/** scopeStyles prefixes every rule and drops this page's global resets. */
function scopeStyles(): Plugin {
  return {
    postcssPlugin: 'nanoartifacts-scope',
    Once(root) {
      root.walkRules((rule) => {
        // Keyframe steps (`0%`, `to`) are not selectors.
        if (rule.parent && rule.parent.type === 'atrule' && /keyframes$/i.test((rule.parent as { name?: string }).name ?? '')) {
          return
        }
        const kept = rule.selectors.filter((raw) => {
          const selector = raw.trim()
          // Global page furniture belongs to the shell, not to this plugin.
          if (['*', ':root', '.dark', 'html', 'body', '#root'].includes(selector)) {
            return false
          }
          // `html, body, #root` and `body > *` style rules are page-level too.
          return !/^(html|body|#root)(\s|>|,|$)|\s(html|body|#root)(\s|>|,|$)/.test(selector)
        })
        if (kept.length === 0) {
          rule.remove()
          return
        }
        rule.selectors = kept.map((selector) => `${SCOPE} ${selector.trim()}`)
      })
    },
  }
}

export default defineConfig({
  base: '/nanoartifacts/plugins/nanoartifacts/',
  plugins: [
    react(),
    federation({
      name: 'nanoartifacts',
      filename: 'remoteEntry.js',
      exposes: { './panels': './src/plugin/panels.tsx' },
      shared: {
        react: { singleton: true, import: false },
        'react-dom': { singleton: true, import: false },
      },
    }),
  ],
  css: {
    postcss: { plugins: [scopeStyles()] },
  },
  // The manifest belongs to the app's root (/nanoartifacts/plugin.json), not to
  // the plugin bundle; copying public/ here would publish a second copy.
  publicDir: false,
  build: {
    target: 'chrome103',
    outDir: 'dist/plugins/nanoartifacts',
    emptyOutDir: true,
    // A remote has no page of its own: pointing the HTML entry at an empty
    // document keeps the standalone app (and its CSS) out of this build.
    rollupOptions: { input: 'plugin.html' },
  },
})
