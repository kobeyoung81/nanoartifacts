import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { api, ApiError, type Detail, type Node, type Space } from './api'
import {
  hostLocale,
  hostToken,
  isEmbedded,
  isPanelHost,
  listen,
  onHostChange,
  readHash,
  reportHash,
  setRuntimeHost,
  writeHash,
} from './host'
import {
  dropMode,
  parseRoute,
  portalWorkURL,
  previewBootstrap,
  readStoredBool,
  readStoredNumber,
  writeStored,
  type DropMode,
} from './lib'
import { loadRoute, saveRoute } from './routeMemory'
import { NewSpaceDialog, NodeDialog, PromptDialog, SpaceSettings, TransferDialog } from './library/dialogs'
import { canDeleteNode, type Toast } from './library/model'
import { DocumentView } from './library/DocumentView'
import { DetailView } from './library/DetailView'
import { SidebarView } from './library/SidebarView'

// Storage is optional: private mode and blocked partitions both throw on access,
// and a missing preference is not an error.
function browserStorage(): Storage | null {
  try {
    return typeof window === 'undefined' ? null : window.localStorage
  } catch {
    return null
  }
}

function clamp(value: number, min: number, max: number): number {
  return Math.min(max, Math.max(min, value))
}

// ready  = we have a credential (token or cookie)
// probing = asking the cookie who we are
// expired = no credential; a portal visit is required
type Auth = 'probing' | 'ready' | 'expired'

// The library is one surface: a space on the left, its tree below, the node in
// the middle and the node's versions on the right. There is no landing page
// between the portal and the work — the portal's "Library" link lands here and
// the space switcher at the top of the tree is how you move between spaces.
/**
 * What a workbench panel hands the app.
 *
 * The standalone page has a parent frame (or a bare URL) and reads its identity
 * from there; a panel is rendered by the portal's shell, so the same three
 * things — who the user is, which language, which route — arrive as props, and
 * navigation is the shell's shared store instead of the address bar.
 */
export type AppHost = {
  token: string
  locale: string
  mode: 'library' | 'document'
  /**
   * Which surface the host asked for. A panel renders one view inside the
   * workbench; a page renders the whole three-column library. The mode alone
   * cannot say it: both the library panel and the page are `library`.
   */
  surface?: 'panel' | 'page'
  route: string
  navigate: (hash: string) => void
}

export function App({ host }: { host?: AppHost }) {
  // The API path below reads its token from host.ts; install the panel identity
  // before any effect can fetch. Both panels write the same values.
  setRuntimeHost(host ? { token: host.token, locale: host.locale } : null)

  const [localHash, setLocalHash] = useState(readHash())
  const hash = host ? host.route : localHash
  const [localLocale, setLocale] = useState(hostLocale())
  const locale = host ? host.locale : localLocale
  const [embedded, setEmbedded] = useState(isEmbedded())
  const [auth, setAuth] = useState<Auth>(hostToken() ? 'ready' : 'probing')
  // The standalone page learns who it is from the session cookie; the panels
  // get the id from the shell instead (routeStore).
  const [userId, setUserId] = useState<string | null>(null)
  const [spaces, setSpaces] = useState<Space[] | null>(null)

  useEffect(() => {
    listen()
    const off = onHostChange(() => {
      setLocale(hostLocale())
      setEmbedded(isEmbedded())
      const next = hostToken()
      if (next) {
        setAuth('ready')
        void api.session().catch(() => {})
      }
    })
    const onHash = () => {
      const next = readHash()
      setLocalHash(next)
      reportHash(next)
    }
    // writeHash navigates with pushState, which fires popstate rather than
    // hashchange; the back button has to reach the same handler.
    window.addEventListener('hashchange', onHash)
    window.addEventListener('popstate', onHash)
    const onMessage = (event: MessageEvent) => {
      if (event.origin !== window.location.origin) return
      const data = event.data as { type?: string; hash?: string } | null
      if (data?.type === 'nanoartifacts.navigate' && typeof data.hash === 'string') {
        if (isPanelHost()) {
          return
        }
        writeHash(data.hash, true)
        setLocalHash(data.hash)
      }
    }
    window.addEventListener('message', onMessage)
    return () => {
      off()
      window.removeEventListener('hashchange', onHash)
      window.removeEventListener('popstate', onHash)
      window.removeEventListener('message', onMessage)
    }
  }, [])

  const navigate = useCallback(
    (next: string) => {
      if (host) {
        host.navigate(next)
        return
      }
      writeHash(next)
      setLocalHash(next)
    },
    [host],
  )

  const refreshSpaces = useCallback(() => {
    api
      .spaces()
      .then((data) => setSpaces(data.spaces ?? []))
      .catch(() => setSpaces([]))
  }, [])

  useEffect(() => {
    if (hostToken()) {
      void api.session().catch(() => {})
      return
    }
    let alive = true
    api
      .me()
      .then((me) => {
        if (!alive) return
        setUserId(me.user_id)
        setAuth('ready')
      })
      .catch(() => alive && setAuth('expired'))
    return () => {
      alive = false
    }
  }, [])

  useEffect(() => {
    if (auth !== 'ready') return
    refreshSpaces()
  }, [auth, refreshSpaces])

  const route = useMemo(() => parseRoute(hash), [hash])

  // "#/" means "resume where this account left off", once per page load.
  //
  // It sits in the same effect as the personal-space redirect on purpose: two
  // effects would both run in one commit, and the redirect would overwrite the
  // memory before it was used. After the one restore, "#/" is the list again —
  // that is what the portal's "Library" link sends to the frame.
  const restoredRef = useRef(false)
  useEffect(() => {
    if (auth !== 'ready') return
    // The restore is spent on the first settled render, whether or not the URL
    // carried a hash: a "#/" that arrives later (the portal's "Library" link, or
    // the back button after a deleted space) is an action, not a resume.
    if (!restoredRef.current) {
      restoredRef.current = true
      if (!host && route.name === 'spaces') {
        const remembered = userId ? loadRoute(browserStorage(), userId) : null
        if (remembered) {
          // replaceState: the back button should skip the empty hash we came from.
          writeHash(remembered, true)
          setLocalHash(remembered)
          return
        }
      }
    }
    if (route.name !== 'spaces') return
    // The personal space always exists (the service creates it on first list),
    // so this settles immediately rather than showing a chooser.
    if (spaces === null) return
    const home = spaces.find((s) => s.kind === 'personal') || spaces[0]
    if (home) {
      navigate(`#/s/${home.id}`)
    }
  }, [auth, route, spaces, navigate, host, userId])

  // The standalone page keeps the same memory the panels write.
  useEffect(() => {
    if (host || !userId) return
    saveRoute(browserStorage(), userId, hash)
  }, [host, userId, hash])

  if (auth === 'probing') {
    return (
      <div className="center-card">
        <h1>NanoArtifacts</h1>
        <p className="muted">{locale.startsWith('zh') ? '正在确认会话…' : 'Checking your session…'}</p>
      </div>
    )
  }

  if (auth === 'expired') {
    return <ExpiredScreen hash={hash} zh={locale.startsWith('zh')} />
  }

  let body: ReactNode
  if (route.name === 'space') {
    body = (
      <Workbench
        spaceId={route.spaceId}
        nodeId={route.nodeId}
        locale={locale}
        navigate={navigate}
        spaces={spaces ?? []}
        onSpacesChanged={refreshSpaces}
        panelView={host && host.surface !== 'page' ? host.mode : undefined}
      />
    )
  } else if (route.name === 'work') {
    body = <WorkRedirect workId={route.workId} locale={locale} onNavigate={navigate} />
  } else {
    body = spaces && spaces.length === 0 ? <NoSpaces locale={locale} onCreated={refreshSpaces} navigate={navigate} /> : <Booting locale={locale} />
  }

  return (
    <div className={host ? `panel-root panel-${host.mode}` : 'panel-root'}>
      {embedded || host ? null : (
        <div className="standalone-bar">
          <a href="/artifacts">{locale.startsWith('zh') ? '← 返回门户' : '← Portal'}</a>
        </div>
      )}
      {body}
    </div>
  )
}

function Booting({ locale }: { locale: string }) {
  return (
    <div className="center-card">
      <h1>NanoArtifacts</h1>
      <p className="muted">{locale.startsWith('zh') ? '正在准备你的空间…' : 'Preparing your space…'}</p>
    </div>
  )
}

function NoSpaces({
  locale,
  onCreated,
  navigate,
}: {
  locale: string
  onCreated: () => void
  navigate: (hash: string) => void
}) {
  const zh = locale.toLowerCase().startsWith('zh')
  const [creating, setCreating] = useState(false)
  return (
    <div className="center-card">
      <h1>{zh ? '还没有空间' : 'No spaces yet'}</h1>
      <p className="muted">{zh ? '空间是作品的组织单位。先创建一个。' : 'Spaces hold your work. Create one to start.'}</p>
      <button className="nz-btn primary" onClick={() => setCreating(true)}>
        {zh ? '新建空间' : 'New space'}
      </button>
      {creating ? (
        <NewSpaceDialog
          zh={zh}
          onClose={() => setCreating(false)}
          onCreated={(space) => {
            setCreating(false)
            onCreated()
            navigate(`#/s/${space.id}`)
          }}
        />
      ) : null}
    </div>
  )
}

// ExpiredScreen is the only door out of a top-level tab without a session.
function ExpiredScreen({ hash, zh }: { hash: string; zh: boolean }) {
  const route = parseRoute(hash)
  const target =
    route.name === 'space'
      ? `/artifacts?space=${encodeURIComponent(route.spaceId)}`
      : route.name === 'work'
        ? `/artifacts?work=${encodeURIComponent(route.workId)}`
        : '/artifacts'
  return (
    <div className="center-card">
      <h1>NanoArtifacts</h1>
      <p>{zh ? '会话已过期。' : 'Your session has expired.'}</p>
      <p className="muted small">
        {zh
          ? '打开门户后返回，作品库会自动获得授权。'
          : 'Open the portal and come back; the library will be authorized there.'}
      </p>
      <div className="inline-actions">
        <a className="nz-btn primary" href={target}>
          {zh ? '打开门户并返回' : 'Open the portal and come back'}
        </a>
        <a className="nz-btn" href="/agent">
          {zh ? '直接去门户' : 'Go to the portal'}
        </a>
      </div>
    </div>
  )
}

function WorkRedirect({
  workId,
  locale,
  onNavigate,
}: {
  workId: string
  locale: string
  onNavigate: (hash: string) => void
}) {
  const zh = locale.toLowerCase().startsWith('zh')
  useEffect(() => {
    let alive = true
    api
      .detail(workId)
      .then((detail) => {
        if (!alive) return
        const space = (detail as unknown as { space_id?: string }).space_id
        const node = (detail as unknown as { node_id?: string }).node_id
        onNavigate(space ? `#/s/${space}${node ? `/n/${node}` : ''}` : '#/')
      })
      .catch(() => alive && onNavigate('#/'))
    return () => {
      alive = false
    }
  }, [workId, onNavigate])
  return (
    <div className="center-card">
      <p className="muted">{zh ? '正在打开作品…' : 'Opening the work…'}</p>
    </div>
  )
}

function Workbench({
  spaceId,
  nodeId,
  locale,
  navigate,
  spaces,
  onSpacesChanged,
  panelView,
}: {
  spaceId: string
  nodeId?: string
  locale: string
  navigate: (hash: string) => void
  spaces: Space[]
  onSpacesChanged: () => void
  /**
   * Set by a workbench panel: it renders exactly one view instead of the page's
   * three columns. Undefined on the standalone page.
   */
  panelView?: 'library' | 'document'
}) {
  const zh = locale.toLowerCase().startsWith('zh')
  const [space, setSpace] = useState<Space | null>(null)
  const [nodes, setNodes] = useState<Node[]>([])
  const [error, setError] = useState<string | null>(null)
  const [reload, setReload] = useState(0)
  const [collapsed, setCollapsed] = useState<Set<string>>(new Set())
  const [filter, setFilter] = useState('')
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [newSpaceOpen, setNewSpaceOpen] = useState(false)
  const [newFolder, setNewFolder] = useState<string | null>(null)
  const [nodeDialog, setNodeDialog] = useState<{ kind: 'rename' | 'delete'; node: Node } | null>(null)
  const [rowMenu, setRowMenu] = useState<{ node: Node; x: number; y: number; seq?: number } | null>(null)
  const [transfer, setTransfer] = useState<{ mode: 'move' | 'copy'; node: Node } | null>(null)
  const [toast, setToast] = useState<Toast | null>(null)
  // Panel only: the tree and the detail are two full views, one at a time.
  const [screen, setScreen] = useState<'tree' | 'detail'>('tree')
  // Press-and-hold turns into a drag; a plain click still opens the node. The
  // ghost follows the pointer and the row under it is the drop target.
  const [draggingID, setDraggingID] = useState<string | null>(null)
  const [ghost, setGhost] = useState<{ x: number; y: number } | null>(null)
  const [drop, setDrop] = useState<{ id: string; mode: DropMode } | null>(null)
  const pressRef = useRef<{ id: string; x: number; y: number; timer: number } | null>(null)
  const dragRef = useRef<string | null>(null)
  const dropRef = useRef<{ id: string; mode: DropMode } | null>(null)
  const suppressClickRef = useRef(false)
  const nodesRef = useRef<Node[]>([])
  const applyMoveRef = useRef<(moving: Node, target: Node, mode: DropMode) => void>(() => {})

  // Layout preferences survive a reload. Inside the portal frame this is the
  // library's own origin, so localStorage is available; the helpers degrade to
  // the defaults if it is not.
  const [sideOpen, setSideOpen] = useState(() => readStoredBool(browserStorage(), 'nw.na.side', true))
  // The detail column exists on the page only; the panel replaces the tree with
  // the details instead of showing a column, so this preference is the page's.
  const [detailOpen, setDetailOpen] = useState(() =>
    readStoredBool(browserStorage(), 'nw.na.detail', true),
  )
  const [sideWidth, setSideWidth] = useState(() => readStoredNumber(browserStorage(), 'nw.na.sideWidth', 250, 180, 420))
  const [detailWidth, setDetailWidth] = useState(() =>
    readStoredNumber(browserStorage(), 'nw.na.detailWidth', 300, 240, 520),
  )

  const resizeRef = useRef<{
    kind: 'side' | 'detail'
    startX: number
    startWidth: number
    moved: boolean
    pending: number
  } | null>(null)
  const rootRef = useRef<HTMLDivElement>(null)
  const rafRef = useRef<number | null>(null)

  const notify = useCallback((text: string) => {
    setToast({ id: Date.now(), text })
  }, [])

  useEffect(() => {
    if (!toast) return
    const timer = window.setTimeout(() => setToast(null), 2200)
    return () => window.clearTimeout(timer)
  }, [toast])

  // Drag-to-resize for both side columns.
  //
  // Three things made this feel stuck before, and each one is handled here:
  //  - the pointer crosses into the iframe, which is a separate document and
  //    swallows mousemove, so the drag died at the boundary (`.resizing` turns
  //    off pointer events on the frame);
  //  - the columns carry a width transition, which made every frame chase the
  //    cursor (`.resizing` disables it);
  //  - every mousemove re-rendered the whole tree, so it lagged on big trees
  //    (updates are throttled to one per animation frame).
  useEffect(() => {
    const flush = () => {
      rafRef.current = null
      const drag = resizeRef.current
      if (!drag || !drag.moved) return
      if (drag.kind === 'side') setSideWidth(drag.pending)
      else setDetailWidth(drag.pending)
    }
    const onMove = (event: MouseEvent) => {
      const drag = resizeRef.current
      if (!drag) return
      if (Math.abs(event.clientX - drag.startX) > 3) drag.moved = true
      if (!drag.moved) return
      drag.pending =
        drag.kind === 'side'
          ? clamp(drag.startWidth + (event.clientX - drag.startX), 180, 420)
          : clamp(drag.startWidth - (event.clientX - drag.startX), 240, 520)
      if (rafRef.current === null) rafRef.current = window.requestAnimationFrame(flush)
    }
    const onUp = () => {
      const drag = resizeRef.current
      if (!drag) return
      resizeRef.current = null
      if (rafRef.current !== null) {
        window.cancelAnimationFrame(rafRef.current)
        rafRef.current = null
      }
      rootRef.current?.classList.remove('resizing')
      document.body.style.userSelect = ''
      document.body.style.cursor = ''

      if (drag.moved) {
        // Apply the last position: the pointer can stop between two frames.
        if (drag.kind === 'side') setSideWidth(drag.pending)
        else setDetailWidth(drag.pending)
        writeStored(
          browserStorage(),
          drag.kind === 'side' ? 'nw.na.sideWidth' : 'nw.na.detailWidth',
          String(drag.pending),
        )
        return
      }

      // A press on the seam with no movement is a click: collapse the tree.
      if (drag.kind === 'side') {
        setSideOpen((current) => {
          writeStored(browserStorage(), 'nw.na.side', current ? '0' : '1')
          return !current
        })
      } else {
        setDetailOpen((current) => {
          writeStored(browserStorage(), 'nw.na.detail', current ? '0' : '1')
          return !current
        })
      }
    }
    window.addEventListener('mousemove', onMove)
    window.addEventListener('mouseup', onUp)
    return () => {
      window.removeEventListener('mousemove', onMove)
      window.removeEventListener('mouseup', onUp)
    }
  }, [])

  const startResize = (kind: 'side' | 'detail') => (event: React.MouseEvent) => {
    event.preventDefault()
    const startWidth = kind === 'side' ? sideWidth : detailWidth
    resizeRef.current = {
      kind,
      startX: event.clientX,
      startWidth,
      moved: false,
      pending: startWidth,
    }
    rootRef.current?.classList.add('resizing')
    document.body.style.userSelect = 'none'
    document.body.style.cursor = 'col-resize'
  }

  const toggleSide = () => {
    setSideOpen((current) => {
      writeStored(browserStorage(), 'nw.na.side', current ? '0' : '1')
      return !current
    })
  }

  const toggleDetail = () => {
    setDetailOpen((current) => {
      writeStored(browserStorage(), 'nw.na.detail', current ? '0' : '1')
      return !current
    })
  }

  useEffect(() => {
    let alive = true
    setError(null)
    api
      .tree(spaceId)
      .then((data) => {
        if (!alive) return
        setSpace(data.space)
        setNodes(data.nodes ?? [])
      })
      .catch((err) => alive && setError(err instanceof ApiError && err.status === 404 ? '404' : String(err)))
    return () => {
      alive = false
    }
  }, [spaceId, reload])

  useEffect(() => {
    nodesRef.current = nodes
  }, [nodes])

  const selected = nodes.find((n) => n.id === nodeId) || null
  const canWrite = space ? space.role === 'owner' || space.role === 'editor' : false
  const isOwner = space?.role === 'owner'

  // The selected node's artifact, with its versions, is what the middle and
  // right columns read. It lives here rather than in a detail component so that
  // switching versions in the right column can drive the frame in the middle.
  const [detail, setDetail] = useState<Detail | null>(null)
  const [seq, setSeq] = useState<number | null>(null)
  const [src, setSrc] = useState<string | null>(null)
  const [expired, setExpired] = useState(false)

  const artifactID = selected?.artifact_id

  useEffect(() => {
    setDetail(null)
    setSeq(null)
    setSrc(null)
    setExpired(false)
    if (!artifactID) return
    let alive = true
    api
      .detail(artifactID)
      .then((data) => {
        if (!alive) return
        setDetail(data)
        setSeq(data.artifact.current_version)
      })
      .catch((err) => alive && setError(err instanceof ApiError && err.status === 404 ? '404' : String(err)))
    return () => {
      alive = false
    }
  }, [artifactID])

  // Before loading a body, make sure this view has a credential — but only ask
  // for the one it can actually get. Inside the portal frame we hold a JWT and
  // refresh the cookie; a top-level tab has no token, and POST /session there
  // would 401 for nothing (the cookie it already carries is the credential).
  useEffect(() => {
    if (!artifactID || seq === null) return
    let alive = true
    setSrc(null)
    setExpired(false)

    const ready =
      previewBootstrap(hostToken() !== null) === 'mint'
        ? api.session().catch(() => undefined)
        : api.me().then(() => undefined)

    ready
      .then(() => {
        if (alive) setSrc(api.contentURL(artifactID, seq))
      })
      .catch(() => {
        if (alive) setExpired(true)
      })

    return () => {
      alive = false
    }
  }, [artifactID, seq])

  // A row menu closes on any click that is neither the menu nor its trigger.
  useEffect(() => {
    if (!rowMenu) return
    const onDown = (event: MouseEvent) => {
      const target = event.target as HTMLElement | null
      if (target?.closest('.nz-rowmenu-pop') || target?.closest('[data-rowmenu]')) return
      setRowMenu(null)
    }
    document.addEventListener('mousedown', onDown)
    return () => document.removeEventListener('mousedown', onDown)
  }, [rowMenu])

  const refresh = () => setReload((n) => n + 1)

  const contains = (node: Node, id: string): boolean => {
    const children = nodes.filter((n) => n.parent_id === node.id)
    return children.some((child) => child.id === id || contains(child, id))
  }

  const openNode = (id: string) => navigate(`#/s/${spaceId}/n/${id}`)

  const createFolder = async (title: string) => {
    if (!title.trim()) return
    try {
      const { node } = await api.createNode(spaceId, {
        parent_id: newFolder || undefined,
        kind: 'folder',
        title: title.trim(),
      })
      setNewFolder(null)
      refresh()
      openNode(node.id)
      notify(zh ? '已新建目录' : 'Folder created')
    } catch (err) {
      notify(err instanceof ApiError ? err.message : String(err))
    }
  }

  const rollback = async (target: number) => {
    if (!artifactID) return
    if (!window.confirm(zh ? `回滚到 v${target}？后续版本会保留。` : `Roll back to v${target}? Later versions are kept.`)) return
    try {
      const { artifact: updated } = await api.rollback(artifactID, target)
      setDetail((current) => (current ? { ...current, artifact: updated } : current))
      setSeq(updated.current_version)
      notify(zh ? `已回滚到 v${target}` : `Rolled back to v${target}`)
    } catch (err) {
      notify(err instanceof ApiError ? err.message : String(err))
    }
  }

  const removeNode = async (node: Node) => {
    try {
      await api.deleteNode(node.id)
      setNodeDialog(null)
      refresh()
      navigate(`#/s/${spaceId}`)
      notify(zh ? '已删除' : 'Deleted')
    } catch (err) {
      notify(err instanceof ApiError ? err.message : String(err))
    }
  }

  const copyLink = async (node: Node) => {
    const url = `${window.location.origin}${portalWorkURL(node.artifact_id || node.id)}`
    let granted = false
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(url)
        granted = true
      }
    } catch {
      granted = false
    }
    notify(granted ? zh ? '链接已复制' : 'Link copied' : url)
  }

  const cancelPress = useCallback(() => {
    const press = pressRef.current
    if (!press) return
    window.clearTimeout(press.timer)
    pressRef.current = null
  }, [])

  const beginPress = (event: React.PointerEvent, id: string) => {
    if (event.button !== 0) return
    cancelPress()
    const { clientX, clientY } = event
    pressRef.current = {
      id,
      x: clientX,
      y: clientY,
      timer: window.setTimeout(() => {
        pressRef.current = null
        dragRef.current = id
        setDraggingID(id)
        setGhost({ x: clientX, y: clientY })
        document.body.style.userSelect = 'none'
        document.body.style.cursor = 'grabbing'
      }, 350),
    }
  }

  const finishPress = useCallback(() => {
    cancelPress()
    const movingID = dragRef.current
    if (!movingID) return
    const dropState = dropRef.current
    const moving = nodesRef.current.find((n) => n.id === movingID) || null
    dragRef.current = null
    dropRef.current = null
    setDraggingID(null)
    setGhost(null)
    setDrop(null)
    document.body.style.userSelect = ''
    document.body.style.cursor = ''
    // The pointerup that ends a drag is followed by a click on the row; that
    // click must not navigate.
    suppressClickRef.current = true
    window.setTimeout(() => {
      suppressClickRef.current = false
    }, 300)
    if (!dropState || !moving) return
    const target = nodesRef.current.find((n) => n.id === dropState.id)
    if (target) applyMoveRef.current(moving, target, dropState.mode)
  }, [cancelPress])

  useEffect(() => {
    const onMove = (event: PointerEvent) => {
      const press = pressRef.current
      if (press) {
        // A little movement before the timer means the user is scrolling or
        // clicking, not holding.
        if (Math.abs(event.clientX - press.x) > 6 || Math.abs(event.clientY - press.y) > 6) {
          cancelPress()
        }
        return
      }
      const movingID = dragRef.current
      if (!movingID) return
      setGhost({ x: event.clientX, y: event.clientY })
      const element = document.elementFromPoint(event.clientX, event.clientY) as HTMLElement | null
      const row = element?.closest('[data-node-id]') as HTMLElement | null
      const targetID = row?.getAttribute('data-node-id') || ''
      const target = targetID ? nodesRef.current.find((n) => n.id === targetID) : null
      if (!row || !target || targetID === movingID) {
        dropRef.current = null
        setDrop(null)
        return
      }
      const rect = row.getBoundingClientRect()
      const mode = dropMode(event.clientY - rect.top, rect.height, target.kind === 'folder')
      dropRef.current = { id: targetID, mode }
      setDrop({ id: targetID, mode })
    }
    const onUp = () => finishPress()
    window.addEventListener('pointermove', onMove)
    window.addEventListener('pointerup', onUp)
    window.addEventListener('pointercancel', onUp)
    return () => {
      window.removeEventListener('pointermove', onMove)
      window.removeEventListener('pointerup', onUp)
      window.removeEventListener('pointercancel', onUp)
    }
  }, [cancelPress, finishPress])

  const selectNode = (id: string) => {
    if (suppressClickRef.current) return
    openNode(id)
  }

  const openRowMenu = (node: Node, trigger: HTMLElement) => {
    const rect = trigger.getBoundingClientRect()
    const x = Math.max(8, Math.min(window.innerWidth - 170, rect.right - 150))
    const y = rect.bottom + 4
    const known = detail && selected?.id === node.id ? detail.artifact.current_version : undefined
    setRowMenu({ node, x, y, seq: known })
    // Download needs a version number. If the row is not the open one, ask for
    // the artifact's current version rather than guessing.
    if (node.artifact_id && known === undefined) {
      api
        .detail(node.artifact_id)
        .then((data) => {
          setRowMenu((current) =>
            current && current.node.id === node.id ? { ...current, seq: data.artifact.current_version } : current,
          )
        })
        .catch(() => undefined)
    }
  }

  const applyMove = async (moving: Node, target: Node, mode: DropMode) => {
    if (moving.id === target.id) return
    if (mode === 'inside' && (target.kind !== 'folder' || contains(moving, target.id))) {
      notify(zh ? '目录不能拖进它自己' : 'A folder cannot go inside itself')
      return
    }
    const parentID = mode === 'inside' ? target.id : target.parent_id || ''
    const siblings = nodesRef.current
      .filter((n) => (n.parent_id || '') === parentID && n.id !== moving.id)
      .sort((a, b) => a.position - b.position || a.id.localeCompare(b.id))
    let beforeID = ''
    if (mode === 'before') beforeID = target.id
    if (mode === 'after') {
      const index = siblings.findIndex((n) => n.id === target.id)
      beforeID = index >= 0 ? siblings[index + 1]?.id ?? '' : ''
    }
    try {
      await api.moveNode(moving.id, parentID, beforeID)
      refresh()
      notify(zh ? '已移动' : 'Moved')
    } catch (err) {
      notify(err instanceof ApiError ? err.message : String(err))
    }
  }

  useEffect(() => {
    applyMoveRef.current = (moving, target, mode) => {
      void applyMove(moving, target, mode)
    }
  })

  if (error === '404') {
    return (
      <div className="center-card">
        <h1>404</h1>
        <p>{zh ? '空间不存在，或者你不是成员。' : 'This space does not exist, or you are not a member.'}</p>
        <button className="nz-btn" onClick={() => navigate('#/')}>{zh ? '返回作品库' : 'Back'}</button>
      </div>
    )
  }
  if (error) {
    return (
      <div className="center-card">
        <p>{error}</p>
        <button className="nz-btn" onClick={refresh}>{zh ? '重试' : 'Retry'}</button>
      </div>
    )
  }
  if (!space) {
    return <div className="nz skeleton-block" />
  }

  const parentID = selected?.kind === 'folder' ? selected.id : ''
  const children = nodes
    .filter((n) => (n.parent_id || '') === parentID)
    .sort((a, b) => a.position - b.position || a.title.localeCompare(b.title))

  const overlays = (
    <>
      {ghost && draggingID ? (
        <div className="nz-drag-ghost" style={{ left: ghost.x + 14, top: ghost.y + 12 }}>
          {nodes.find((n) => n.id === draggingID)?.title || ''}
        </div>
      ) : null}

      {!panelView && detailOpen ? (
        <button
          className="nz-detail-close"
          onClick={toggleDetail}
          title={zh ? '收起详情' : 'Hide details'}
          aria-label={zh ? '收起详情' : 'Hide details'}
        >
          ×
        </button>
      ) : null}

      {toast ? <div className="nz-toast show">{toast.text}</div> : null}

      {rowMenu ? (
        <div className="nz-rowmenu-pop open" style={{ left: rowMenu.x, top: rowMenu.y }}>
          {rowMenu.node.kind === 'folder' && canWrite ? (
            <button onClick={() => { setNewFolder(rowMenu.node.id); setRowMenu(null) }}>
              {zh ? '新建子目录' : 'New subfolder'}
            </button>
          ) : null}
          {canWrite ? (
            <button onClick={() => { setNodeDialog({ kind: 'rename', node: rowMenu.node }); setRowMenu(null) }}>
              {zh ? '重命名' : 'Rename'}
            </button>
          ) : null}
          <button
            onClick={() => {
              // Panel: details are a full view of the same panel. Page: a column.
              if (panelView === 'library') {
                openNode(rowMenu.node.id)
                setScreen('detail')
              } else {
                toggleDetail()
              }
              setRowMenu(null)
            }}
          >
            {panelView === 'library'
              ? zh ? '详情' : 'Details'
              : detailOpen
                ? zh ? '隐藏详情' : 'Hide details'
                : zh ? '详情' : 'Details'}
          </button>
          {rowMenu.node.artifact_id ? (
            <button onClick={() => { void copyLink(rowMenu.node); setRowMenu(null) }}>
              {zh ? '复制链接' : 'Copy link'}
            </button>
          ) : null}
          {rowMenu.node.artifact_id && rowMenu.seq ? (
            <a
              className="nz-rowmenu-link"
              href={api.contentURL(rowMenu.node.artifact_id, rowMenu.seq, true)}
              onClick={() => setRowMenu(null)}
            >
              {zh ? '下载' : 'Download'}
            </a>
          ) : null}
          {canWrite ? (
            <button onClick={() => { setTransfer({ mode: 'move', node: rowMenu.node }); setRowMenu(null) }}>
              {zh ? '移动到…' : 'Move to…'}
            </button>
          ) : null}
          {canWrite ? (
            <button onClick={() => { setTransfer({ mode: 'copy', node: rowMenu.node }); setRowMenu(null) }}>
              {zh ? '复制到…' : 'Copy to…'}
            </button>
          ) : null}
          {canWrite ? (
            canDeleteNode(nodes, rowMenu.node) ? (
              <button className="danger" onClick={() => { setNodeDialog({ kind: 'delete', node: rowMenu.node }); setRowMenu(null) }}>
                {zh ? '删除' : 'Delete'}
              </button>
            ) : (
              <button className="danger" disabled title={zh ? '目录非空，不能删除' : 'Folder is not empty'}>
                {zh ? '删除（目录非空）' : 'Delete (folder is not empty)'}
              </button>
            )
          ) : null}
        </div>
      ) : null}

      {newSpaceOpen ? (
        <NewSpaceDialog
          zh={zh}
          onClose={() => setNewSpaceOpen(false)}
          onCreated={(next) => {
            setNewSpaceOpen(false)
            onSpacesChanged()
            navigate(`#/s/${next.id}`)
          }}
        />
      ) : null}

      {newFolder !== null ? (
        <PromptDialog
          zh={zh}
          title={zh ? '新建目录' : 'New folder'}
          label={zh ? '名称' : 'Name'}
          placeholder={zh ? '例如：需求文档' : 'e.g. Requirements'}
          confirm={zh ? '创建' : 'Create'}
          onClose={() => setNewFolder(null)}
          onSubmit={(title) => void createFolder(title)}
        />
      ) : null}

      {settingsOpen ? (
        <SpaceSettings
          zh={zh}
          space={space}
          onClose={() => setSettingsOpen(false)}
          onChanged={() => { refresh(); onSpacesChanged() }}
          onDeleted={() => {
            setSettingsOpen(false)
            onSpacesChanged()
            navigate('#/')
          }}
        />
      ) : null}

      {transfer ? (
        <TransferDialog
          zh={zh}
          mode={transfer.mode}
          node={transfer.node}
          spaces={spaces}
          onClose={() => setTransfer(null)}
          onDone={() => {
            setTransfer(null)
            refresh()
            notify(transfer.mode === 'move' ? (zh ? '已移动' : 'Moved') : zh ? '已复制' : 'Copied')
          }}
        />
      ) : null}

      {nodeDialog ? (
        <NodeDialog
          zh={zh}
          state={nodeDialog}
          onClose={() => setNodeDialog(null)}
          onDelete={() => void removeNode(nodeDialog.node)}
          onSaved={() => { setNodeDialog(null); refresh() }}
        />
      ) : null}
    </>
  )

  // A panel renders exactly one view: the document, the tree, or the node's
  // details. There is no three-column frame, so no column resizes and nothing
  // animates between the views.
  if (panelView) {
    return (
      <div className="panel-view">
        {panelView === 'document' ? (
          <DocumentView
            zh={zh}
            selected={selected}
            artifactID={artifactID}
            expired={expired}
            src={src}
            children={children}
            canWrite={canWrite}
            openNode={openNode}
            onCreate={() => setNewFolder(parentID)}
          />
        ) : screen === 'detail' ? (
          <DetailView
            zh={zh}
            locale={locale}
            space={space}
            selected={selected}
            detail={detail}
            seq={seq}
            setSeq={setSeq}
            artifactID={artifactID}
            isOwner={isOwner}
            rollback={rollback}
            onOpenSettings={() => setSettingsOpen(true)}
            onBack={() => setScreen('tree')}
          />
        ) : (
          <SidebarView
            zh={zh}
            spaces={spaces}
            spaceId={spaceId}
            spaceName={space.name}
            nodes={nodes}
            nodeId={nodeId}
            filter={filter}
            setFilter={setFilter}
            refresh={refresh}
            collapsed={collapsed}
            toggleCollapsed={(id) =>
              setCollapsed((current) => {
                const next = new Set(current)
                if (next.has(id)) next.delete(id)
                else next.add(id)
                return next
              })
            }
            draggingID={draggingID}
            drop={drop}
            openNode={openNode}
            selectNode={selectNode}
            beginPress={beginPress}
            finishPress={finishPress}
            openRowMenu={openRowMenu}
            onNewSpace={() => setNewSpaceOpen(true)}
            navigate={navigate}
            onOpenSettings={() => setSettingsOpen(true)}
            onNewFolder={() => setNewFolder('')}
          />
        )}
        {overlays}
      </div>
    )
  }

  return (
    <div
      ref={rootRef}
      className={`nz${sideOpen ? '' : ' side-off'}${detailOpen ? '' : ' detail-off'}`}
      style={{ ['--nz-side-w' as string]: `${sideWidth}px`, ['--nz-detail-w' as string]: `${detailWidth}px` }}
    >
      <SidebarView
        zh={zh}
        spaces={spaces}
        spaceId={spaceId}
        spaceName={space.name}
        nodes={nodes}
        nodeId={nodeId}
        filter={filter}
        setFilter={setFilter}
        refresh={refresh}
        collapsed={collapsed}
        toggleCollapsed={(id) =>
          setCollapsed((current) => {
            const next = new Set(current)
            if (next.has(id)) next.delete(id)
            else next.add(id)
            return next
          })
        }
        draggingID={draggingID}
        drop={drop}
        openNode={openNode}
        selectNode={selectNode}
        beginPress={beginPress}
        finishPress={finishPress}
        openRowMenu={openRowMenu}
        onNewSpace={() => setNewSpaceOpen(true)}
        navigate={navigate}
        onOpenSettings={() => setSettingsOpen(true)}
        onNewFolder={() => setNewFolder('')}
        onStartResize={startResize}
      />

      {sideOpen ? null : (
        <button
          className="nz-side-reveal"
          onClick={toggleSide}
          title={zh ? '展开目录' : 'Show tree'}
          aria-label={zh ? '展开目录' : 'Show tree'}
        >
          ›
        </button>
      )}

      <DocumentView
        zh={zh}
        selected={selected}
        artifactID={artifactID}
        expired={expired}
        src={src}
        children={children}
        canWrite={canWrite}
        openNode={openNode}
        onCreate={() => setNewFolder(parentID)}
      />

      <div className="nz-detail-resizer" onMouseDown={startResize('detail')} aria-hidden="true" />

      <DetailView
        zh={zh}
        locale={locale}
        space={space}
        selected={selected}
        detail={detail}
        seq={seq}
        setSeq={setSeq}
        artifactID={artifactID}
        isOwner={isOwner}
        rollback={rollback}
        onOpenSettings={() => setSettingsOpen(true)}
      />

      {overlays}
    </div>
  )
}
