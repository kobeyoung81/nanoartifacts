import { describe, expect, it } from 'vitest'
import { canDeleteNode, hasChildren } from './model'
import type { Node } from '../api'

function node(partial: Partial<Node> & { id: string; kind: Node['kind'] }): Node {
  return {
    space_id: 's',
    parent_id: '',
    title: partial.id,
    artifact_id: '',
    position: 0,
    created_by: 'u1',
    created_at: '2026-11-18T00:00:00Z',
    updated_at: '2026-11-18T00:00:00Z',
    ...partial,
  } as Node
}

const tree: Node[] = [
  node({ id: 'root', kind: 'folder' }),
  node({ id: 'docs', kind: 'folder', parent_id: 'root' }),
  node({ id: 'doc', kind: 'document', parent_id: 'docs', artifact_id: 'a1' }),
  node({ id: 'empty', kind: 'folder' }),
]

describe('hasChildren', () => {
  it('finds direct children only', () => {
    expect(hasChildren(tree, 'root')).toBe(true)
    expect(hasChildren(tree, 'docs')).toBe(true)
    expect(hasChildren(tree, 'empty')).toBe(false)
    expect(hasChildren(tree, 'doc')).toBe(false)
  })
})

describe('canDeleteNode', () => {
  it('refuses a folder that still holds rows', () => {
    expect(canDeleteNode(tree, tree[0])).toBe(false)
    expect(canDeleteNode(tree, tree[1])).toBe(false)
  })

  it('allows an empty folder and any document', () => {
    expect(canDeleteNode(tree, tree[3])).toBe(true)
    expect(canDeleteNode(tree, tree[2])).toBe(true)
  })
})
