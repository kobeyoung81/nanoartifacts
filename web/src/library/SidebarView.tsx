import type { Node, Space } from '../api'
import { GearIcon, RefreshIcon, TreeIcon, renderTree } from './tree'
import { SpaceMenu } from './SpaceMenu'
import type { DropMode } from '../lib'

// The tree column: the space switcher, the search, the rows and the drag
// gesture. It owns no data — the page's state container hands it what to show
// and what to do. In the panel it is the whole panel, so the resize grip (which
// belongs to the page's three-column frame) is not rendered.

export type SidebarViewProps = {
  zh: boolean
  spaces: Space[]
  spaceId: string
  spaceName: string
  nodes: Node[]
  nodeId?: string
  filter: string
  setFilter: (value: string) => void
  refresh: () => void
  collapsed: Set<string>
  toggleCollapsed: (id: string) => void
  draggingID: string | null
  drop: { id: string; mode: DropMode } | null
  openNode: (id: string) => void
  selectNode: (id: string) => void
  beginPress: (event: React.PointerEvent, id: string) => void
  finishPress: () => void
  openRowMenu: (node: Node, trigger: HTMLElement) => void
  onNewSpace: () => void
  /** Route change for the space switcher (the page owns hash routing). */
  navigate: (hash: string) => void
  onOpenSettings: () => void
  onNewFolder: () => void
  /** Page only: the grip starts a column resize. */
  onStartResize?: (kind: 'side' | 'detail') => (event: React.MouseEvent) => void
}

export function SidebarView({
  zh,
  spaces,
  spaceId,
  spaceName,
  nodes,
  nodeId,
  filter,
  setFilter,
  refresh,
  collapsed,
  toggleCollapsed,
  draggingID,
  drop,
  openNode,
  selectNode,
  beginPress,
  finishPress,
  openRowMenu,
  onNewSpace,
  navigate,
  onOpenSettings,
  onNewFolder,
  onStartResize,
}: SidebarViewProps) {
  const matched = filter.trim()
    ? nodes.filter((n) => n.title.toLowerCase().includes(filter.trim().toLowerCase()))
    : []

  return (
    <aside className="nz-side">
      <div className="nz-space">
        <SpaceMenu
          zh={zh}
          spaces={spaces}
          spaceId={spaceId}
          spaceName={spaceName}
          onSelect={(id) => navigate(`#/s/${id}`)}
          onNewSpace={onNewSpace}
        />
        <button
          className="nz-members-btn"
          onClick={onOpenSettings}
          title={zh ? '空间设置' : 'Space settings'}
          aria-label={zh ? '空间设置' : 'Space settings'}
        >
          <GearIcon />
        </button>
      </div>

      <div className="nz-tree">
        <div className="nz-tree-tools">
          <input
            className="nz-search"
            value={filter}
            onChange={(event) => setFilter(event.target.value)}
            placeholder={zh ? '搜索标题…' : 'Search titles…'}
            aria-label={zh ? '搜索标题' : 'Search titles'}
          />
          <button
            className="nz-icon-btn"
            onClick={refresh}
            title={zh ? '刷新目录' : 'Refresh tree'}
            aria-label={zh ? '刷新目录' : 'Refresh tree'}
          >
            <RefreshIcon />
          </button>
        </div>
        {matched.length ? (
          <ul>
            {matched.map((node) => (
              <li key={node.id}>
                <div className={`nz-node${node.id === nodeId ? ' active' : ''}`} onClick={() => openNode(node.id)}>
                  <span className="nz-caret hidden">▸</span>
                  <TreeIcon kind={node.kind} />
                  <span className="nz-label" title={node.title}>{node.title}</span>
                  <button
                    className="nz-rowmenu"
                    data-rowmenu
                    onClick={(event) => {
                      event.stopPropagation()
                      openRowMenu(node, event.currentTarget)
                    }}
                  >
                    ⋯
                  </button>
                </div>
              </li>
            ))}
          </ul>
        ) : (
          renderTree(nodes, '', 0, {
            selected: nodeId || '',
            collapsed,
            draggingID,
            drop,
            onSelect: selectNode,
            onToggle: toggleCollapsed,
            onMenu: openRowMenu,
            onPress: beginPress,
            onPressEnd: finishPress,
          })
        )}
        {nodes.length === 0 ? (
          <p className="nz-tree-empty small muted">{zh ? '这个空间还是空的' : 'This space is empty'}</p>
        ) : null}
      </div>

      <button className="nz-newspace" onClick={onNewFolder}>
        ＋ {zh ? '新建目录' : 'New folder'}
      </button>
      {onStartResize ? (
        <div
          className="nz-side-grip"
          onMouseDown={onStartResize('side')}
          title={zh ? '拖动调整宽度，点击收起' : 'Drag to resize, click to collapse'}
          aria-hidden="true"
        />
      ) : null}
    </aside>
  )
}
