/**
 * ferry docs 浏览器语言自动跳转（P2 拍板 2026-11-22：仅自动跳转）。
 *
 * 范围：只在既有中英孪生 5 对内生效，不迁 root=英文、不补深页英文版。
 * 对无孪生页面（/design/*、/deploy/*、/guide/* 等）一律不动，绝不 404。
 *
 * 语义：
 *  - 无显式偏好时：目标语言 = 浏览器首选语言（navigator.language）。
 *    当前页语言 ≠ 目标语言且存在孪生 → 跳转到孪生。
 *  - 有显式偏好（点过真实语言切换链接，localStorage `ferry-doc-lang`）：
 *    自动跳转整体关闭——用户手动选过语言，就不再替他选（防「切回被弹回」）。
 */
export type Lang = 'zh' | 'en'

/** 中文路由键 → 英文孪生路由键（base 相对、不含 .html；`/` 为首页） */
export const LANG_TWINS: Record<string, string> = {
  '/': '/index.en',
  '/herald-对接实录': '/herald-integration-log.en',
  '/面板横向对比': '/panel-comparison.en',
  '/审计-文档一致性': '/doc-consistency-audit.en',
  '/design/术语表': '/design/术语表.en',
}

/** 用户显式语言偏好的 localStorage 键（只有点过真实语言切换链接才写） */
export const LANG_PREF_KEY = 'ferry-doc-lang'

/** 浏览器首选语言 → 'zh' | 'en' | null（非中英不猜） */
export function browserLanguage(): Lang | null {
  const raw =
    (typeof navigator !== 'undefined' &&
      ((navigator.languages && navigator.languages[0]) || navigator.language)) ||
    ''
  const lower = String(raw).toLowerCase()
  if (lower.startsWith('zh')) return 'zh'
  if (lower.startsWith('en')) return 'en'
  return null
}

export function readPref(): Lang | null {
  try {
    const v = localStorage.getItem(LANG_PREF_KEY)
    return v === 'zh' || v === 'en' ? v : null
  } catch {
    return null
  }
}

export function setPref(lang: Lang): void {
  try {
    localStorage.setItem(LANG_PREF_KEY, lang)
  } catch {
    /* 隐私模式等取不到存储时静默 */
  }
}

/**
 * 归一化路由键：解码百分号转义（浏览器 location.pathname 把中文百分号编码，
 * 而孪生键是字面中文，不解码则中文页永远匹配不上）、去 base 前缀、
 * 去尾斜杠、去 .html、去尾 index（`/index.html`→`/`，与 VitePress normalizeHref 同款）；
 * 空/`/` 统一为 `/`。
 */
export function normRoute(raw: string, base: string): string {
  let p = raw.split('#')[0].split('?')[0]
  try {
    p = decodeURIComponent(p)
  } catch {
    /* 含非法转义序列时按原样处理 */
  }
  if (base && base !== '/' && p.startsWith(base)) p = p.slice(base.length - 1) // 保前导 '/'
  p = p.replace(/\/+$/, '')
  p = p.replace(/\.html$/, '')
  p = p.replace(/(^|\/)index$/, '')
  return p || '/'
}

/** 若 route 落在孪生对上，返回其当前语言与对端路由键；否则 null */
export function resolveTwin(route: string): { current: Lang; target: string } | null {
  if (Object.prototype.hasOwnProperty.call(LANG_TWINS, route)) {
    return { current: 'zh', target: LANG_TWINS[route] }
  }
  for (const [zh, en] of Object.entries(LANG_TWINS)) {
    if (en === route) return { current: 'en', target: zh }
  }
  return null
}

/**
 * 自动跳转目标路由键；null = 不跳。
 * 规则：无显式偏好 + 当前页落在孪生对 + 当前语言 ≠ 浏览器语言 → 跳对端。
 */
export function autoJumpTarget(route: string, base: string): string | null {
  if (readPref()) return null // 用户手动选过语言 → 不再替他选
  const want = browserLanguage()
  if (!want) return null
  const twin = resolveTwin(normRoute(route, base))
  if (!twin || twin.current === want) return null
  return twin.target
}

/**
 * 点击拦截：仅当点击目标是「当前页的孪生」（真实语言切换链接）时写显式偏好。
 * 侧栏点同语言文档（如 zh 页点 zh 对比页）不算切换，不写偏好。
 *
 * currentRoute 由调用方传入（VitePress router.route.path）。不能读 location.pathname：
 * VitePress 的 window-capture 点击处理器先于我跑，同步 history.pushState 后
 * location.pathname 已是目标页，读它就永远匹配不上「当前页孪生」。
 */
export function onDocClick(e: MouseEvent, base: string, currentRoute: string): void {
  const el = e.target as Element | null
  const a = el && el.closest ? (el.closest('a') as HTMLAnchorElement | null) : null
  if (!a) return
  const href = a.getAttribute('href') || ''
  if (!href || href.startsWith('#') || /^(https?:)?\/\//.test(href)) return
  const targetRoute = normRoute(href, base)
  const cur = resolveTwin(normRoute(currentRoute, base))
  if (cur && cur.target === targetRoute) setPref(cur.current === 'zh' ? 'en' : 'zh')
}
