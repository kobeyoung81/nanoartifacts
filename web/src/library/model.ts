// Small, pure pieces shared by the library views: labels a node or a role, and
// the shape of a transient toast. No React, so both the views and the page shell
// can use them without pulling a component in.

import type { Node } from '../api'

export type Toast = { id: number; text: string }

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

