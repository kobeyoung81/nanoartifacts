import { useEffect, useRef, useState } from 'react'
import type { Space } from '../api'

// The space switcher.
//
// It used to be a native <select>. The closed control could be themed, but the
// list that opens is drawn by the browser/OS menu: with the portal in dark mode
// on a light desktop, the options came out light and looked like another
// application's menu. This draws the list with the app's own tokens instead, so
// the panel and the page agree with whatever theme the shell is in.

export type SpaceMenuProps = {
  zh: boolean
  spaces: Space[]
  spaceId: string
  /** Name of the current space when it is not in `spaces` (a just-created one). */
  spaceName: string
  onSelect: (id: string) => void
  onNewSpace: () => void
}

export function SpaceMenu({ zh, spaces, spaceId, spaceName, onSelect, onNewSpace }: SpaceMenuProps) {
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!open) return
    const onDown = (event: Event) => {
      if (rootRef.current?.contains(event.target as Node)) return
      setOpen(false)
    }
    const onKey = (event: KeyboardEvent) => {
      if (event.key === 'Escape') setOpen(false)
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('keydown', onKey)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('keydown', onKey)
    }
  }, [open])

  // The current space can be missing from the list for a moment (a space was just
  // created and the list has not been refetched); it still has to be shown.
  const entries: { id: string; name: string }[] = spaces.map((space) => ({ id: space.id, name: space.name }))
  if (!entries.some((entry) => entry.id === spaceId)) {
    entries.push({ id: spaceId, name: spaceName })
  }

  return (
    <div className="nz-space-select" ref={rootRef}>
      <button
        type="button"
        className="nz-space-button"
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={zh ? '切换空间' : 'Switch space'}
        onClick={() => setOpen((value) => !value)}
      >
        <span className="nz-space-name">{entries.find((entry) => entry.id === spaceId)?.name ?? spaceName}</span>
        <span className="nz-space-caret" aria-hidden="true">▾</span>
      </button>
      {open ? (
        <div className="nz-space-menu" role="listbox" aria-label={zh ? '空间' : 'Spaces'}>
          {entries.map((entry) => (
            <button
              key={entry.id}
              type="button"
              role="option"
              aria-selected={entry.id === spaceId}
              className={`nz-space-item${entry.id === spaceId ? ' active' : ''}`}
              onClick={() => {
                setOpen(false)
                if (entry.id !== spaceId) onSelect(entry.id)
              }}
            >
              <span className="nz-space-check" aria-hidden="true">{entry.id === spaceId ? '✓' : ''}</span>
              <span className="nz-space-item-name">{entry.name}</span>
            </button>
          ))}
          <div className="nz-space-sep" aria-hidden="true" />
          <button
            type="button"
            className="nz-space-item"
            onClick={() => {
              setOpen(false)
              onNewSpace()
            }}
          >
            <span className="nz-space-check" aria-hidden="true">＋</span>
            <span className="nz-space-item-name">{zh ? '新建空间' : 'New space'}</span>
          </button>
        </div>
      ) : null}
    </div>
  )
}
