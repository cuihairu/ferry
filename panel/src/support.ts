import { get } from './api'

// 客服入口（servify 真嵌验收）：面板登录后按需加载外部组件（widget.js 直嵌，
// 嵌入指南 §2 方案 A）。未配置时 /api/panel/support 回 enabled:false——
// 不加载任何脚本、无外呼，面板保持零依赖。访客 token 由服务端按用户会话签发，
// service key 只在 ferry 服务端出现。

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

// initSupport 拉嵌入配置并挂载组件；任何失败只 warn 不打扰面板主流程。
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
