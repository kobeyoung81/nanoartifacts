import { describe, expect, it } from 'vitest'
import { API_BASE } from './config'

describe('API_BASE', () => {
  it('is the service prefix, not the build base', () => {
    // The remote build's base is /nanoartifacts/plugins/nanoartifacts/, and
    // deriving the API from it pointed every request at a path the static
    // handler answers with index.html (the panel then showed an empty library).
    expect(API_BASE).toBe('/nanoartifacts/api/v1')
    expect(API_BASE).not.toContain('/plugins/')
  })
})
