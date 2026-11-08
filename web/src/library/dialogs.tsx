import { useCallback, useEffect, useState } from 'react'
import { api, ApiError, type Node, type Space } from '../api'

// The library's modal surfaces. They own only their own form state; every
// mutation goes straight to the API and calls back so the caller can refresh.

export function NewSpaceDialog({ zh, onClose, onCreated }: { zh: boolean; onClose: () => void; onCreated: (space: Space) => void }) {
  const [name, setName] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const submit = async () => {
    if (!name.trim()) return
    setBusy(true)
    setError(null)
    try {
      // Visibility is not asked for: a team space is "members" by definition,
      // and who those members are is decided in the settings panel. Leaving it
      // private would only be a label — access is decided by grants.
      const { space } = await api.createSpace(name.trim(), 'members')
      onCreated(space)
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="nz-modal open" onClick={onClose}>
      <div className="nz-dialog" onClick={(event) => event.stopPropagation()}>
        <h3>{zh ? '新建空间' : 'New space'}</h3>
        <p>{zh ? '建好后在「空间设置」里添加成员。' : 'Add members from Space settings once it exists.'}</p>
        <label htmlFor="nz-name">{zh ? '空间名称' : 'Name'}</label>
        <input
          id="nz-name"
          type="text"
          value={name}
          onChange={(event) => setName(event.target.value)}
          autoFocus
          placeholder={zh ? '例如：产品知识库' : 'e.g. Product library'}
          onKeyDown={(event) => event.key === 'Enter' && void submit()}
        />
        {error ? <p className="error small">{error}</p> : null}
        <div className="nz-actions">
          <button className="nz-btn" onClick={onClose}>{zh ? '取消' : 'Cancel'}</button>
          <button className="nz-btn primary" onClick={() => void submit()} disabled={busy || !name.trim()}>
            {zh ? '创建' : 'Create'}
          </button>
        </div>
      </div>
    </div>
  )
}

export function PromptDialog({
  zh,
  title,
  label,
  placeholder,
  confirm,
  onClose,
  onSubmit,
}: {
  zh: boolean
  title: string
  label: string
  placeholder?: string
  confirm: string
  onClose: () => void
  onSubmit: (value: string) => void
}) {
  const [value, setValue] = useState('')
  return (
    <div className="nz-modal open" onClick={onClose}>
      <div className="nz-dialog" onClick={(event) => event.stopPropagation()}>
        <h3>{title}</h3>
        <label htmlFor="nz-prompt">{label}</label>
        <input
          id="nz-prompt"
          type="text"
          value={value}
          autoFocus
          placeholder={placeholder}
          onChange={(event) => setValue(event.target.value)}
          onKeyDown={(event) => event.key === 'Enter' && value.trim() && onSubmit(value)}
        />
        <div className="nz-actions">
          <button className="nz-btn" onClick={onClose}>{zh ? '取消' : 'Cancel'}</button>
          <button className="nz-btn primary" onClick={() => onSubmit(value)} disabled={!value.trim()}>{confirm}</button>
        </div>
      </div>
    </div>
  )
}

type Member = { user_id: string; name: string; role: string }
type FoundUser = { user_id: string; display_name: string; email: string }

// One panel for the space itself: rename, members, delete. The gear next to the
// space switcher opens it, and members are added by searching the directory —
// there is no invitation step, because every account here is already inside the
// platform.
export function SpaceSettings({
  zh,
  space,
  onClose,
  onChanged,
  onDeleted,
}: {
  zh: boolean
  space: Space
  onClose: () => void
  onChanged: () => void
  onDeleted: () => void
}) {
  const [name, setName] = useState(space.name)
  const [members, setMembers] = useState<Member[]>([])
  const [filter, setFilter] = useState('')
  const [query, setQuery] = useState('')
  const [found, setFound] = useState<FoundUser[]>([])
  const [role, setRole] = useState<'viewer' | 'editor'>('viewer')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const load = useCallback(() => {
    api
      .space(space.id)
      .then((data) => setMembers(data.members ?? []))
      .catch((err) => setError(err instanceof ApiError ? err.message : String(err)))
  }, [space.id])

  useEffect(() => {
    load()
  }, [load])

  // The directory search is debounced: a member picker that fires a request per
  // keystroke would hammer the bus for nothing.
  useEffect(() => {
    const q = query.trim()
    if (!q) {
      setFound([])
      return
    }
    const timer = window.setTimeout(() => {
      api
        .searchUsers(q)
        .then((data) => setFound(data.users))
        .catch(() => setFound([]))
    }, 250)
    return () => window.clearTimeout(timer)
  }, [query])

  const rename = async () => {
    if (!name.trim() || name.trim() === space.name) return
    setBusy(true)
    setError(null)
    try {
      await api.updateSpace(space.id, { name: name.trim() })
      onChanged()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  const add = async (user: FoundUser) => {
    setError(null)
    try {
      await api.addMemberByID(space.id, user.user_id, role)
      setQuery('')
      setFound([])
      load()
      onChanged()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err))
    }
  }

  const setMemberRole = async (member: Member, next: 'viewer' | 'editor') => {
    setError(null)
    try {
      await api.addMemberByID(space.id, member.user_id, next)
      load()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err))
    }
  }

  const removeMember = async (member: Member) => {
    setError(null)
    try {
      await api.removeMember(space.id, member.user_id)
      load()
      onChanged()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err))
    }
  }

  const removeSpace = async () => {
    if (!window.confirm(zh ? `删除「${space.name}」？其下的目录与文档会一起消失。` : `Delete "${space.name}"? Its folders and documents go with it.`)) return
    setBusy(true)
    try {
      await api.deleteSpace(space.id)
      onDeleted()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err))
      setBusy(false)
    }
  }

  const q = filter.trim().toLowerCase()
  const shown = q ? members.filter((m) => m.name.toLowerCase().includes(q)) : members
  const isTeam = space.kind === 'team'

  return (
    <div className="nz-modal open" onClick={onClose}>
      <div className="nz-dialog" onClick={(event) => event.stopPropagation()}>
        <h3>{zh ? '空间设置' : 'Space settings'}</h3>
        <p>{zh ? '空间级授权：可编辑能增删节点，只读只能看。' : 'Space-wide: editors change the tree, viewers only read.'}</p>

        <label htmlFor="nz-space-name">{zh ? '空间名称' : 'Name'}</label>
        <div className="nz-invite">
          <input
            id="nz-space-name"
            type="text"
            value={name}
            onChange={(event) => setName(event.target.value)}
            onKeyDown={(event) => event.key === 'Enter' && void rename()}
          />
          <button className="nz-btn" onClick={() => void rename()} disabled={busy || !name.trim()}>
            {zh ? '保存' : 'Save'}
          </button>
        </div>

        <label htmlFor="nz-member-search">{zh ? `成员（${members.length}）` : `Members (${members.length})`}</label>
        <input
          id="nz-member-search"
          type="text"
          value={filter}
          onChange={(event) => setFilter(event.target.value)}
          placeholder={zh ? '按姓名筛选' : 'Filter by name'}
        />
        <ul className="nz-members">
          {shown.map((member) => (
            <li key={member.user_id}>
              <span className="nz-member-avatar">{initials(member.name || member.user_id)}</span>
              <span className="nz-member-name">{member.name || member.user_id}</span>
              {member.role === 'owner' ? (
                <span className="small muted">{zh ? '所有者' : 'owner'}</span>
              ) : (
                <>
                  <select
                    value={member.role}
                    onChange={(event) => void setMemberRole(member, event.target.value as 'viewer' | 'editor')}
                    aria-label={zh ? '角色' : 'Role'}
                  >
                    <option value="editor">{zh ? '可编辑' : 'Editor'}</option>
                    <option value="viewer">{zh ? '只读' : 'Viewer'}</option>
                  </select>
                  <button className="nz-link" onClick={() => void removeMember(member)}>
                    {zh ? '移除' : 'Remove'}
                  </button>
                </>
              )}
            </li>
          ))}
          {shown.length === 0 ? <li className="small muted">{zh ? '没有匹配的成员' : 'No matching members'}</li> : null}
        </ul>

        <label htmlFor="nz-invite">{zh ? '添加成员' : 'Add a member'}</label>
        <div className="nz-invite">
          <input
            id="nz-invite"
            type="text"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={zh ? '输入姓名或邮箱搜索…' : 'Search by name or e-mail…'}
          />
          <select value={role} onChange={(event) => setRole(event.target.value as 'viewer' | 'editor')} aria-label={zh ? '角色' : 'Role'}>
            <option value="viewer">{zh ? '只读' : 'Viewer'}</option>
            <option value="editor">{zh ? '可编辑' : 'Editor'}</option>
          </select>
        </div>
        {found.length ? (
          <ul className="nz-members nz-found">
            {found.map((user) => (
              <li key={user.user_id}>
                <span className="nz-member-avatar">{initials(user.display_name || user.email)}</span>
                <span className="nz-member-name">
                  {user.display_name || user.email}
                  <span className="small muted"> · {user.email}</span>
                </span>
                <button className="nz-link" onClick={() => void add(user)}>{zh ? '添加' : 'Add'}</button>
              </li>
            ))}
          </ul>
        ) : query.trim() ? (
          <p className="small muted">{zh ? '没有找到匹配的账号' : 'No matching account'}</p>
        ) : null}

        {error ? <p className="error small">{error}</p> : null}

        <div className="nz-actions">
          {isTeam && space.role === 'owner' ? (
            <button className="nz-btn danger" onClick={() => void removeSpace()} disabled={busy}>
              {zh ? '删除空间' : 'Delete space'}
            </button>
          ) : null}
          <span className="grow" />
          <button className="nz-btn" onClick={onClose}>{zh ? '完成' : 'Done'}</button>
        </div>
      </div>
    </div>
  )
}

export function initials(value: string): string {
  const trimmed = value.trim()
  if (!trimmed) return '?'
  return trimmed.slice(0, 2).toUpperCase()
}

export function NodeDialog({
  zh,
  state,
  onClose,
  onDelete,
  onSaved,
}: {
  zh: boolean
  state: { kind: 'rename' | 'delete'; node: Node }
  onClose: () => void
  onDelete: () => void
  onSaved: () => void
}) {
  const [title, setTitle] = useState(state.node.title)
  const [error, setError] = useState<string | null>(null)

  const save = async () => {
    setError(null)
    try {
      await api.updateNode(state.node.id, { title: title.trim() })
      onSaved()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err))
    }
  }

  return (
    <div className="nz-modal open" onClick={onClose}>
      <div className="nz-dialog" onClick={(event) => event.stopPropagation()}>
        <h3>{state.kind === 'rename' ? (zh ? '重命名' : 'Rename') : zh ? '删除' : 'Delete'}</h3>
        {state.kind === 'delete' ? (
          <p>
            {zh
              ? `「${state.node.title}」及其内容会被删除，其他人将无法再访问。`
              : `"${state.node.title}" and everything under it will be removed.`}
          </p>
        ) : (
          <>
            <label htmlFor="nz-rename">{zh ? '名称' : 'Name'}</label>
            <input
              id="nz-rename"
              type="text"
              value={title}
              autoFocus
              onChange={(event) => setTitle(event.target.value)}
              onKeyDown={(event) => event.key === 'Enter' && void save()}
            />
          </>
        )}
        {error ? <p className="error small">{error}</p> : null}
        <div className="nz-actions">
          <button className="nz-btn" onClick={onClose}>{zh ? '取消' : 'Cancel'}</button>
          {state.kind === 'delete' ? (
            <button className="nz-btn primary danger" onClick={onDelete}>{zh ? '删除' : 'Delete'}</button>
          ) : (
            <button className="nz-btn primary" onClick={() => void save()}>{zh ? '保存' : 'Save'}</button>
          )}
        </div>
      </div>
    </div>
  )
}

// Move or copy a node into any space the caller can write to.
//
// The target list is the folder tree of the chosen space, fetched on demand: a
// dialog is not worth preloading every space's tree. When moving, the node's own
// subtree is removed from the list — the server refuses that move anyway, and
// offering it would be a dead end.
export function TransferDialog({
  zh,
  mode,
  node,
  spaces,
  onClose,
  onDone,
}: {
  zh: boolean
  mode: 'move' | 'copy'
  node: Node
  spaces: Space[]
  onClose: () => void
  onDone: () => void
}) {
  const [spaceID, setSpaceID] = useState(node.space_id)
  const [nodes, setNodes] = useState<Node[]>([])
  const [parentID, setParentID] = useState('')
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    let alive = true
    api
      .tree(spaceID)
      .then((data) => {
        if (!alive) return
        setNodes(data.nodes ?? [])
        setParentID('')
      })
      .catch((err) => alive && setError(err instanceof ApiError ? err.message : String(err)))
    return () => {
      alive = false
    }
  }, [spaceID])

  // A folder cannot be dropped into itself: hide its own subtree from the
  // destination list when moving within the same space.
  const blocked = new Set<string>()
  if (mode === 'move') {
    blocked.add(node.id)
    let grew = true
    while (grew) {
      grew = false
      for (const item of nodes) {
        if (!blocked.has(item.id) && item.parent_id && blocked.has(item.parent_id)) {
          blocked.add(item.id)
          grew = true
        }
      }
    }
  }
  const folders = nodes.filter((item) => item.kind === 'folder' && !blocked.has(item.id))

  const submit = async () => {
    setBusy(true)
    setError(null)
    try {
      if (mode === 'move') {
        await api.moveNode(node.id, parentID, '', spaceID)
      } else {
        await api.copyNode(node.id, spaceID, parentID)
      }
      onDone()
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err))
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="nz-modal open" onClick={onClose}>
      <div className="nz-dialog" onClick={(event) => event.stopPropagation()}>
        <h3>{mode === 'move' ? (zh ? '移动到…' : 'Move to…') : zh ? '复制到…' : 'Copy to…'}</h3>
        <p>
          {mode === 'move'
            ? zh
              ? '目录会连同其下的内容一起过去。'
              : 'A folder takes everything under it.'
            : zh
              ? '只复制当前版本；内容按 hash 共享，不占额外空间。'
              : 'Only the current version travels; the bytes are shared.'}
        </p>

        <label htmlFor="nz-transfer-space">{zh ? '目标空间' : 'Space'}</label>
        <select id="nz-transfer-space" value={spaceID} onChange={(event) => setSpaceID(event.target.value)}>
          {spaces.map((item) => (
            <option key={item.id} value={item.id}>{item.name}</option>
          ))}
        </select>

        <label htmlFor="nz-transfer-parent">{zh ? '目标目录' : 'Folder'}</label>
        <select id="nz-transfer-parent" value={parentID} onChange={(event) => setParentID(event.target.value)}>
          <option value="">{zh ? '（空间根目录）' : '(space root)'}</option>
          {folders.map((item) => (
            <option key={item.id} value={item.id}>{item.title}</option>
          ))}
        </select>

        {error ? <p className="error small">{error}</p> : null}
        <div className="nz-actions">
          <button className="nz-btn" onClick={onClose}>{zh ? '取消' : 'Cancel'}</button>
          <button className="nz-btn primary" onClick={() => void submit()} disabled={busy}>
            {zh ? '确认' : 'Confirm'}
          </button>
        </div>
      </div>
    </div>
  )
}
