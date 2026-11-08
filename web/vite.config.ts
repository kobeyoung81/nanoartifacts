import { defineConfig } from 'vitest/config'
import react from '@vitejs/plugin-react'

// base is /nanoartifacts/ — deliberately not /artifacts: that path belongs to
// the portal's own route, and sharing it made hard navigations land on this
// app instead of the portal shell. The fragment carries routing, so no
// server-side rewrite is needed beyond the proxy's prefix stripping.
export default defineConfig({
  base: '/nanoartifacts/',
  plugins: [react()],
  test: {
    environment: 'node',
    include: ['src/**/*.test.ts'],
    // The panel tests assert on real stylesheet text (`?raw`); with CSS stubbed
    // those imports come back empty.
    css: true,
  },
})
