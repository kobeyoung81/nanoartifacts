import type { Detail, Node, Space } from '../api'
import { formatDate, formatSize } from '../lib'
import { nodeKindLabel, roleLabel } from './model'

// The detail column: versions, the selected node, and the space's actions. In
// the panel it is a full view (the tree is not beside it), so it carries a back
// control; on the page the corner × collapses the column instead.

export type DetailViewProps = {
  zh: boolean
  locale: string
  space: Space
  selected: Node | null
  detail: Detail | null
  seq: number | null
  setSeq: (seq: number) => void
  artifactID?: string
  isOwner: boolean
  rollback: (seq: number) => void
  onOpenSettings: () => void
  /** Panel only: leave the detail view and go back to the tree. */
  onBack?: () => void
}

export function DetailView({
  zh,
  locale,
  space,
  selected,
  detail,
  seq,
  setSeq,
  artifactID,
  isOwner,
  rollback,
  onOpenSettings,
  onBack,
}: DetailViewProps) {
  return (
    <aside className="nz-detail">
      {onBack ? (
        <button className="nz-detail-back" onClick={onBack}>
          ← {zh ? '返回作品库' : 'Back to library'}
        </button>
      ) : null}
      <h4>{zh ? '版本' : 'Versions'}</h4>
      {detail ? (
        <div className="nz-versions">
          {detail.versions.map((version) => (
            <div className={`nz-ver${version.seq === seq ? ' active' : ''}`} key={version.seq}>
              <button className="nz-link tag" onClick={() => setSeq(version.seq)}>v{version.seq}</button>
              <span className="small muted">{formatDate(version.created_at, locale)}</span>
              <span className="grow" />
              {version.seq === detail.artifact.current_version ? (
                <span className="cur">{zh ? '当前' : 'current'}</span>
              ) : detail.can_manage ? (
                <button className="nz-link" onClick={() => void rollback(version.seq)}>
                  {zh ? '回滚' : 'Roll back'}
                </button>
              ) : null}
            </div>
          ))}
        </div>
      ) : (
        <div className="nz-sec muted">
          {selected && !artifactID ? (zh ? '目录没有版本' : 'Folders have no versions') : zh ? '还没有版本' : 'No versions yet'}
        </div>
      )}

      <h4>{zh ? '此节点' : 'This node'}</h4>
      <div className="nz-sec">
        <b>{zh ? '所属空间' : 'Space'}</b>
        {space.name}
      </div>
      <div className="nz-sec">
        <b>{zh ? '类型' : 'Kind'}</b>
        {selected ? nodeKindLabel(selected, zh) : zh ? '空间首页' : 'Space home'}
      </div>
      <div className="nz-sec">
        <b>{zh ? '权限' : 'Access'}</b>
        {zh ? `空间级 · ${roleLabel(space.role, zh)}` : `Space-wide · ${roleLabel(space.role, zh)}`}
      </div>
      {detail ? (
        <>
          <div className="nz-sec">
            <b>{zh ? '版本数' : 'Versions'}</b>
            {detail.versions.length}
          </div>
          <div className="nz-sec">
            <b>{zh ? '大小' : 'Size'}</b>
            {formatSize(detail.artifact.size_bytes)}
          </div>
        </>
      ) : null}
      <div className="nz-sec">
        <b>{zh ? '操作' : 'Actions'}</b>
        {isOwner ? (
          <button className="nz-link" onClick={onOpenSettings}>{zh ? '空间设置…' : 'Space settings…'}</button>
        ) : (
          <span className="muted">{zh ? '成员由所有者管理' : 'Managed by the owner'}</span>
        )}
      </div>
    </aside>
  )
}
