// The bridge to the portal shell.
//
// The framed app never reads the portal's storage: the token arrives as a
// message from the parent, and routing changes are reported back so the address
// bar can follow. Both directions check the origin, so another frame on the
// page cannot hand this app a token.

export type Theme = 'light' | 'dark'

type InitMessage = {
  type: 'nanoartifacts.init'
  token: string | null
  locale?: string
  theme?: Theme
}

// A panel runs inside the shell's page, not in a frame: there is no parent to
// postMessage a token to. The host component sets these once per render, before
// any effect can call the API, so the request path stays the same in both modes.
type RuntimeHost = { token: string | null; locale: string }
let runtime: RuntimeHost | null = null

let token: string | null = null
let locale = navigator.language || 'zh-CN'
let theme: Theme = 'light'
let embedded = false

const listeners = new Set<() => void>()

function emit() {
  for (const fn of listeners) fn()
}

export function onHostChange(fn: () => void): () => void {
  listeners.add(fn)
  return () => listeners.delete(fn)
}

/** setRuntimeHost installs the panel identity (null returns to frame mode). */
export function setRuntimeHost(host: RuntimeHost | null): void {
  runtime = host
}

export function isPanelHost(): boolean {
  return runtime !== null
}

export function hostToken(): string | null {
  return runtime ? runtime.token : token
}

export function hostLocale(): string {
  return runtime ? runtime.locale : locale
}

export function hostTheme(): Theme {
  return theme
}

export function isEmbedded(): boolean {
  return embedded
}

// listen installs the message handler. It also adopts the theme from the
// document when running standalone, so the app is readable outside the portal.
export function listen(): void {
  theme = document.documentElement.classList.contains('dark') ? 'dark' : 'light'
  window.addEventListener('message', (event: MessageEvent) => {
    if (event.origin !== window.location.origin) return
    const data = event.data as InitMessage | null
    if (!data || data.type !== 'nanoartifacts.init') return
    embedded = true
    token = data.token ?? null
    locale = data.locale || locale
    theme = data.theme || theme
    applyTheme(theme)
    emit()
  })
}

export function applyTheme(next: Theme): void {
  document.documentElement.classList.toggle('dark', next === 'dark')
}

// reportHash keeps the parent's address bar in step with this app's routing.
export function reportHash(hash: string): void {
  if (!embedded) return
  window.parent.postMessage({ type: 'nanoartifacts.path', hash }, window.location.origin)
}

// readHash is the current route as "#/..." — the portal carries this string in
// its own URL, which is what makes a deep link survive a refresh.
export function readHash(): string {
  return window.location.hash || '#/'
}

export function writeHash(hash: string, replace = false): void {
  if (readHash() === hash) return
  const url = `${window.location.pathname}${window.location.search}${hash}`
  if (replace) window.history.replaceState({}, '', url)
  else window.history.pushState({}, '', url)
  reportHash(hash)
  emit()
}
