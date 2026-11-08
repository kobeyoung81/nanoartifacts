import type { ReactNode } from 'react'
import { formatDate, type DropMode } from '../lib'
import type { Node } from '../api'
import { nodeKindLabel } from './model'

// The tree: one row per node, rendered recursively. Kept free of data fetching so
// the page and the panel can both use it with whatever state they own.

export function TreeIcon({ kind }: { kind: string }) {
  if (kind === 'folder') {
    return (
      <svg className="nz-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" aria-hidden="true">
        <path d="M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2Z" />
      </svg>
    )
  }
  if (kind === 'app') {
    return (
      <svg className="nz-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" aria-hidden="true">
        <rect x="3" y="4" width="18" height="14" rx="2" />
        <path d="M8 21h8" />
        <path d="M12 18v3" />
      </svg>
    )
  }
  return (
    <svg className="nz-ico" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" aria-hidden="true">
      <path d="M6 3h8l4 4v14H6Z" />
      <path d="M14 3v4h4" />
      <path d="M9 12h6M9 16h6" />
    </svg>
  )
}

export type TreeCtx = {
  selected: string
  collapsed: Set<string>
  draggingID: string | null
  drop: { id: string; mode: DropMode } | null
  onSelect: (id: string) => void
  onToggle: (id: string) => void
  onMenu: (node: Node, trigger: HTMLElement) => void
  onPress: (event: React.PointerEvent, id: string) => void
  onPressEnd: () => void
}

export function renderTree(nodes: Node[], parent: string, depth: number, ctx: TreeCtx): ReactNode {
  const children = nodes
    .filter((n) => (n.parent_id || '') === parent)
    .sort((a, b) => a.position - b.position || a.title.localeCompare(b.title))
  if (children.length === 0) return null
  return (
    <ul style={{ paddingLeft: depth === 0 ? 0 : 12 }}>
      {children.map((node) => {
        const hasKids = nodes.some((n) => n.parent_id === node.id)
        const open = !ctx.collapsed.has(node.id)
        const mark = ctx.drop && ctx.drop.id === node.id ? ` drop-${ctx.drop.mode}` : ''
        return (
          <li key={node.id}>
            <div
              data-node-id={node.id}
              className={`nz-node${ctx.selected === node.id ? ' active' : ''}${ctx.draggingID === node.id ? ' dragging' : ''}${mark}`}
              onClick={() => ctx.onSelect(node.id)}
              onPointerDown={(event) => ctx.onPress(event, node.id)}
              onPointerUp={ctx.onPressEnd}
              onPointerCancel={ctx.onPressEnd}
            >
              <span
                className={`nz-caret${hasKids ? '' : ' hidden'}${open ? ' open' : ''}`}
                onClick={(event) => {
                  event.stopPropagation()
                  ctx.onToggle(node.id)
                }}
              >
                ▸
              </span>
              <TreeIcon kind={node.kind} />
              <span className="nz-label" title={node.title}>{node.title}</span>
              <button
                className="nz-rowmenu"
                data-rowmenu
                title="⋯"
                onClick={(event) => {
                  event.stopPropagation()
                  ctx.onMenu(node, event.currentTarget)
                }}
                onPointerDown={(event) => event.stopPropagation()}
              >
                ⋯
              </button>
            </div>
            {hasKids && open ? renderTree(nodes, node.id, depth + 1, ctx) : null}
          </li>
        )
      })}
    </ul>
  )
}

// A text glyph (⟳) at 13px blurred into a blob: the ring and the arrow ran into
// each other. This is the same stroke vocabulary as the other icons.
export function RefreshIcon() {
  return (
    <svg
      viewBox="0 0 24 24"
      width="15"
      height="15"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.8"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M20 11a8 8 0 1 0-2.3 6.3" />
      <path d="M20 4v7h-7" />
    </svg>
  )
}

export function GearIcon() {
  return (
    <svg viewBox="0 0 24 24" width="16" height="16" fill="none" stroke="currentColor" strokeWidth="1.8" aria-hidden="true">
      <circle cx="12" cy="12" r="3" />
      <path d="M19.4 15a1.7 1.7 0 0 0 .34 1.87l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.7 1.7 0 0 0-1.87-.34 1.7 1.7 0 0 0-1.03 1.56V21a2 2 0 1 1-4 0v-.09A1.7 1.7 0 0 0 8.9 19.3a1.7 1.7 0 0 0-1.87.34l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.7 1.7 0 0 0 4.7 15a1.7 1.7 0 0 0-1.56-1.03H3a2 2 0 1 1 0-4h.09A1.7 1.7 0 0 0 4.7 8.9a1.7 1.7 0 0 0-.34-1.87l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.7 1.7 0 0 0 9 4.7a1.7 1.7 0 0 0 1.03-1.56V3a2 2 0 1 1 4 0v.09A1.7 1.7 0 0 0 15 4.7a1.7 1.7 0 0 0 1.87-.34l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.7 1.7 0 0 0 19.3 9c.25.6.83 1 1.48 1.03H21a2 2 0 1 1 0 4h-.09a1.7 1.7 0 0 0-1.51 1Z" />
    </svg>
  )
}

export function FolderBody({
  children,
  emptyTitle,
  emptyHint,
  zh,
  onOpen,
  onCreate,
  createLabel,
}: {
  children: Node[]
  emptyTitle: string
  emptyHint: string
  zh: boolean
  onOpen: (id: string) => void
  onCreate?: () => void
  createLabel: string
}) {
  if (children.length === 0) {
    return (
      <div className="nz-empty">
        <div>{emptyTitle}</div>
        <div className="small">{emptyHint}</div>
        {onCreate ? (
          <button className="nz-btn primary" onClick={onCreate}>{createLabel}</button>
        ) : null}
      </div>
    )
  }
  return (
    <ul className="nz-items">
      {children.map((node) => (
        <li key={node.id}>
          <button onClick={() => onOpen(node.id)}>
            <TreeIcon kind={node.kind} />
            <span className="nz-items-title">{node.title}</span>
            <span className="small muted">{nodeKindLabel(node, zh)}</span>
            <span className="small muted">{formatDate(node.updated_at, zh ? 'zh-CN' : 'en')}</span>
          </button>
        </li>
      ))}
    </ul>
  )
}
