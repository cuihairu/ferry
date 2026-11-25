import { get } from './api'

// Support entry (servify real-embed acceptance): after panel login the external widget is loaded on
// demand (widget.js embedded directly, embed guide §2 option A). When unconfigured, /api/panel/support
// replies enabled:false — no scripts are loaded and no outbound call is made, keeping the panel at zero
// dependencies. The visitor token is issued by the server per user session, and the service key appears
// only inside the ferry server.

export interface SupportConfig {
  enabled: boolean
  url?: string
  session_id?: string
  access_token?: string
  expires_at?: number
  context_synced?: boolean
  icon?: string
  color?: string
  theme?: string
  brand?: { name: string; welcome: string }
}

declare global {
  interface Window {
    ServifyWidget?: { create: (opts: Record<string, unknown>) => unknown }
  }
}

function loadScript(src: string): Promise<void> {
  if (document.querySelector(`script[src="${src}"]`)) return Promise.resolve()
  return new Promise((resolve, reject) => {
    const s = document.createElement('script')
    s.src = src
    s.async = true
    s.onload = () => resolve()
    s.onerror = () => reject(new Error(`failed to load ${src}`))
    document.head.appendChild(s)
  })
}

// initSupport fetches the embed configuration and mounts the widget; any failure only warns and never disturbs the panel's main flow.
export async function initSupport(): Promise<void> {
  let cfg: SupportConfig
  try {
    cfg = await get<SupportConfig>('/api/panel/support')
  } catch {
    return
  }
  if (!cfg.enabled || !cfg.url || !cfg.session_id || !cfg.access_token) return
  try {
    const base = cfg.url.replace(/\/$/, '')
    await loadScript(`${base}/demo-sdk/servify-sdk.umd.js`)
    await loadScript(`${base}/demo-sdk/widget.js`)
    window.ServifyWidget?.create({
      baseUrl: base,
      sessionId: cfg.session_id,
      accessToken: cfg.access_token,
      icon: cfg.icon ?? 'headset',
      color: cfg.color ?? '#6e79d6',
      theme: cfg.theme ?? 'auto',
      brand: cfg.brand ?? { name: 'ferry 支持', welcome: '' },
    })
  } catch (e) {
    console.warn('[support] widget load failed', e)
  }
}
