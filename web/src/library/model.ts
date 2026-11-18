// Small, pure pieces shared by the library views: labels a node or a role, and
// the shape of a transient toast. No React, so both the views and the page shell
// can use them without pulling a component in.

import type { Node } from '../api'

export type Toast = { id: number; text: string }

// A folder is removed only when it is empty: the same rule the server enforces.
// The row menu asks this before offering the action, so the refusal is not the
// first time the user hears about it.
export function hasChildren(nodes: Node[], id: string): boolean {
  return nodes.some((node) => node.parent_id === id)
}

export function canDeleteNode(nodes: Node[], node: Node): boolean {
  return node.kind !== 'folder' || !hasChildren(nodes, node.id)
}

export function nodeKindLabel(node: Node, zh: boolean): string {
  if (node.kind === 'folder') return zh ? '目录' : 'Folder'
  if (node.kind === 'app') return zh ? '轻应用' : 'App'
  return zh ? '文档' : 'Document'
}

export function roleLabel(role: string, zh: boolean): string {
  const table: Record<string, { zh: string; en: string }> = {
    owner: { zh: '所有者', en: 'owner' },
    editor: { zh: '可编辑', en: 'editor' },
    viewer: { zh: '只读', en: 'viewer' },
  }
  const entry = table[role]
  if (!entry) return role
  return zh ? entry.zh : entry.en
}

