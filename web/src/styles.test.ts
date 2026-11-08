// The row's "⋯" menu is revealed by `.nz-node:hover`, which a phone never fires:
// on touch the menu stayed at opacity 0 on every row but the active one, so
// renaming, moving, sharing or deleting any other document was unreachable. These
// checks pin the way out — and the fact that the desktop behaviour did not change.

import { describe, expect, it } from 'vitest'
import styles from './styles.css?raw'

describe('row menu on a pointer without hover', () => {
  it('stands the menu up when there is no hover', () => {
    const at = styles.indexOf('@media (hover: none)')
    expect(at).toBeGreaterThan(-1)
    // Read the block that follows rather than matching braces: the rule is one
    // declaration and the assertion should not depend on its formatting.
    const block = styles.slice(at, at + 200)
    expect(block).toContain('.nz-rowmenu')
    expect(block).toContain('opacity: 1')
  })

  it('keeps the desktop reveal-on-hover behaviour', () => {
    expect(styles).toContain('.nz-node:hover .nz-rowmenu')
    expect(styles).toContain('.nz-node.active .nz-rowmenu')
    expect(styles).toMatch(/\.nz-rowmenu \{[^}]*opacity: 0/)
  })

  it('shows the menu when it has keyboard focus', () => {
    expect(styles).toMatch(/\.nz-rowmenu:focus-visible \{[^}]*opacity: 1/)
  })

  it('leaves the reserved gutter for the menu in place', () => {
    // The menu is absolutely positioned in this padding; without it an always
    // visible "⋯" would sit on top of the label.
    expect(styles).toMatch(/\.nz-node \{[^}]*padding: 4px 26px 4px 6px/)
  })
})
