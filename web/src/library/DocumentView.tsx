import type { Node } from '../api'
import { portalWorkURL } from '../lib'
import { FolderBody } from './tree'

// The middle column: the selected node's body. It used to live inside the page
// component together with the tree and the detail column; it is now its own
// component so the document panel can render exactly this and nothing else.

export type DocumentViewProps = {
  zh: boolean
  selected: Node | null
  artifactID?: string
  expired: boolean
  src: string | null
  children: Node[]
  canWrite: boolean
  openNode: (id: string) => void
  onCreate: () => void
}

export function DocumentView({
  zh,
  selected,
  artifactID,
  expired,
  src,
  children,
  canWrite,
  openNode,
  onCreate,
}: DocumentViewProps) {
  return (
    <section className="nz-main">
      <div className="nz-body">
        {selected && artifactID ? (
          expired ? (
            <div className="center-card">
              <h1>NanoArtifacts</h1>
              <p>{zh ? '会话已过期。' : 'Your session has expired.'}</p>
              <p className="muted small">
                {zh ? '打开门户后返回，作品库会自动获得授权。' : 'Open the portal and come back; it will authorize you.'}
              </p>
              <a className="nz-btn primary" href={portalWorkURL(artifactID)}>
                {zh ? '打开门户并返回' : 'Open the portal'}
              </a>
            </div>
          ) : src ? (
            <div className={`nz-frame-wrap${selected.kind === 'app' ? ' app' : ''}`}>
              <iframe
                className="nz-frame"
                title={selected.title}
                src={src}
                sandbox="allow-scripts"
                referrerPolicy="no-referrer"
              />
            </div>
          ) : (
            <div className="nz-loading">{zh ? '加载中…' : 'Loading…'}</div>
          )
        ) : selected && selected.kind === 'app' ? (
          <div className="nz-empty">
            <div>{zh ? '应用还没发布内容' : 'This app has no content yet'}</div>
          </div>
        ) : (
          <FolderBody
            children={children}
            emptyTitle={zh ? '这个目录还没有内容' : 'Nothing in this folder yet'}
            emptyHint={
              canWrite
                ? zh
                  ? '用左下角的「新建目录」，或让 agent 把文档发布到这里'
                  : 'Add a folder from the bottom left, or have an agent publish here'
                : zh
                  ? '等作者添加内容'
                  : 'The owner has not added anything yet'
            }
            zh={zh}
            onOpen={openNode}
            onCreate={canWrite ? onCreate : undefined}
            createLabel={zh ? '新建目录' : 'New folder'}
          />
        )}
      </div>
    </section>
  )
}

/** Re-exported for callers that pass the parent id straight through. */
export type { Node }
