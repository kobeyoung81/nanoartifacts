import { API_BASE } from './config'
import { hostToken } from './host'

export type Artifact = {
  id: string
  owner_user_id: string
  owner_name?: string
  title: string
  kind: string
  current_version: number
  visibility: string
  size_bytes: number
  created_at: string
  updated_at: string
}

export type Version = {
  seq: number
  content_hash: string
  size_bytes: number
  note?: string
  created_by: string
  created_by_name?: string
  created_at: string
}

export type Grant = { user_id: string; name: string; role: string }

export type Detail = {
  artifact: Artifact
  versions: Version[]
  grants: Grant[]
  can_manage: boolean
  space_id?: string
  node_id?: string
}

export type Space = {
  id: string
  name: string
  kind: 'personal' | 'team'
  visibility: 'private' | 'members'
  role: 'owner' | 'editor' | 'viewer'
  owner_user_id?: string
  created_at: string
  updated_at: string
}

export type Node = {
  id: string
  space_id: string
  parent_id: string
  kind: 'folder' | 'document' | 'app'
  title: string
  position: number
  artifact_id?: string
  created_by: string
  created_at: string
  updated_at: string
}

export type Member = { user_id: string; name: string; role: string }

export type ListResponse = {
  artifacts: Artifact[]
  scope: 'owned' | 'shared'
  quota: { used: number; limit: number }
}

// The public prefix of this service's API — see config.ts for why it is not the
// build's base (the remote build's base is the plugin directory).
const BASE = API_BASE

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers)
  const token = hostToken()
  if (token) headers.set('Authorization', `Bearer ${token}`)
  headers.set('Accept', 'application/json')
  if (init.body) headers.set('Content-Type', 'application/json')

  // Same-origin so the session cookie travels with the request; the token is
  // only added when this view actually has one (inside the portal frame).
  const response = await fetch(BASE + path, { ...init, headers, credentials: 'same-origin' })
  if (!response.ok) {
    const body = (await response.json().catch(() => ({}))) as { error?: string }
    throw new ApiError(response.status, body.error || `HTTP ${response.status}`)
  }
  if (response.status === 204) return undefined as T
  return (await response.json()) as T
}

export const api = {
  // --- spaces ---
  spaces() {
    return request<{ spaces: Space[] }>('/spaces')
  },
  createSpace(name: string, visibility: 'private' | 'members') {
    return request<{ space: Space }>('/spaces', {
      method: 'POST',
      body: JSON.stringify({ name, visibility }),
    })
  },
  space(id: string) {
    return request<{ space: Space; members: Member[] }>(`/spaces/${encodeURIComponent(id)}`)
  },
  updateSpace(id: string, body: { name?: string; visibility?: 'private' | 'members' }) {
    return request<{ space: Space }>(`/spaces/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      body: JSON.stringify(body),
    })
  },
  // moveNode is the tree's drag-and-drop: put this node under parent_id,
  // before before_id ("" = the end). PATCH can only append, which is not
  // enough to drop a row between two others.
  moveNode(id: string, parentID: string, beforeID: string, spaceID = '') {
    return request<{ node: Node }>(`/nodes/${encodeURIComponent(id)}/move`, {
      method: 'POST',
      body: JSON.stringify({ space_id: spaceID, parent_id: parentID, before_id: beforeID }),
    })
  },
  // copyNode duplicates a node (and its subtree) into another space. The bytes
  // are content-addressed, so the copy shares them.
  copyNode(id: string, spaceID: string, parentID: string) {
    return request<{ node: Node }>(`/nodes/${encodeURIComponent(id)}/copy`, {
      method: 'POST',
      body: JSON.stringify({ space_id: spaceID, parent_id: parentID }),
    })
  },
  // searchUsers is the member picker's directory: a fragment of a name or an
  // address, capped by the bus.
  searchUsers(q: string) {
    return request<{ users: { user_id: string; display_name: string; email: string }[] }>(
      `/users?q=${encodeURIComponent(q)}`,
    )
  },
  deleteSpace(id: string) {
    return request<void>(`/spaces/${encodeURIComponent(id)}`, { method: 'DELETE' })
  },
  tree(id: string) {
    return request<{ space: Space; nodes: Node[] }>(`/spaces/${encodeURIComponent(id)}/tree`)
  },
  createNode(spaceId: string, body: { parent_id?: string; kind: 'folder' | 'document' | 'app'; title: string }) {
    return request<{ node: Node }>(`/spaces/${encodeURIComponent(spaceId)}/nodes`, {
      method: 'POST',
      body: JSON.stringify(body),
    })
  },
  updateNode(id: string, body: { title?: string; parent_id?: string }) {
    return request<{ node: Node }>(`/nodes/${encodeURIComponent(id)}`, {
      method: 'PATCH',
      body: JSON.stringify(body),
    })
  },
  deleteNode(id: string) {
    return request<void>(`/nodes/${encodeURIComponent(id)}`, { method: 'DELETE' })
  },
  // addMemberByID is what the settings panel uses: the directory search already
  // returned the account id, and an id cannot be mistyped.
  addMemberByID(spaceId: string, userID: string, role: 'viewer' | 'editor') {
    return request<void>(`/spaces/${encodeURIComponent(spaceId)}/grants`, {
      method: 'POST',
      body: JSON.stringify({ user_id: userID, role }),
    })
  },
  addMember(spaceId: string, email: string, role: 'viewer' | 'editor') {
    return request<void>(`/spaces/${encodeURIComponent(spaceId)}/grants`, {
      method: 'POST',
      body: JSON.stringify({ email, role }),
    })
  },
  removeMember(spaceId: string, userID: string) {
    return request<void>(`/spaces/${encodeURIComponent(spaceId)}/grants/${encodeURIComponent(userID)}`, {
      method: 'DELETE',
    })
  },
  list(scope: 'owned' | 'shared', query: string, kind: string) {
    const params = new URLSearchParams({ scope })
    if (query.trim()) params.set('q', query.trim())
    if (kind) params.set('kind', kind)
    return request<ListResponse>(`/artifacts?${params.toString()}`)
  },
  detail(id: string) {
    return request<Detail>(`/artifacts/${encodeURIComponent(id)}`)
  },
  // session exchanges the JWT for the HttpOnly cookie this view carries.
  // Re-issued on every open, so a long-lived tab stays usable.
  session() {
    return request<{ expires_at: string }>('/session', { method: 'POST' })
  },
  // me asks the cookie who it belongs to. It is what a top-level tab calls when
  // it has no token to present.
  me() {
    return request<{ user_id: string; display_name: string }>('/session')
  },
  // contentURL is what the iframe and the download link point at. No signature
  // and no expiry: the cookie authorizes it, and the server re-checks the
  // artifact's permissions on every load.
  contentURL(id: string, seq: number, download = false) {
    const query = download ? '?download=1' : ''
    return `${BASE}/artifacts/${encodeURIComponent(id)}/versions/${seq}/raw${query}`
  },
  rollback(id: string, seq: number) {
    return request<{ artifact: Artifact }>(`/artifacts/${encodeURIComponent(id)}/rollback`, {
      method: 'POST',
      body: JSON.stringify({ seq }),
    })
  },
  grant(id: string, email: string) {
    return request<void>(`/artifacts/${encodeURIComponent(id)}/grants`, {
      method: 'POST',
      body: JSON.stringify({ email }),
    })
  },
  revoke(id: string, userID: string) {
    return request<void>(`/artifacts/${encodeURIComponent(id)}/grants/${encodeURIComponent(userID)}`, {
      method: 'DELETE',
    })
  },
  remove(id: string) {
    return request<void>(`/artifacts/${encodeURIComponent(id)}`, { method: 'DELETE' })
  },
  findUser(email: string) {
    return request<{ user: { user_id: string; display_name: string; email: string } }>(
      `/users?email=${encodeURIComponent(email)}`,
    )
  },
}
