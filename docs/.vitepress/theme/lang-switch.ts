/**
 * Browser-language auto-redirect for ferry docs (P2 decision 2026-11-22: auto-redirect only).
 *
 * Scope: only takes effect within the five existing zh/en twin pairs; root is not migrated to English
 * and deep-page English versions are not added.
 * Pages without a twin (/design/*, /deploy/*, /guide/*, etc.) are never touched, so no 404s.
 *
 * Semantics:
 *  - Without an explicit preference: target language = the browser's preferred language (navigator.language).
 *    If the current page's language differs from the target and a twin exists -> redirect to the twin.
 *  - With an explicit preference (a real language-switch link was clicked, localStorage `ferry-doc-lang`):
 *    auto-redirect is disabled altogether — the user chose a language manually, so we stop choosing for
 *    them (avoids "switching back gets bounced forward").
 */
export type Lang = 'zh' | 'en'

/** Chinese route key -> English twin route key (base-relative, without .html; `/` is the home page) */
export const LANG_TWINS: Record<string, string> = {
  '/': '/index.en',
  '/herald-对接实录': '/herald-integration-log.en',
  '/面板横向对比': '/panel-comparison.en',
  '/审计-文档一致性': '/doc-consistency-audit.en',
  '/design/术语表': '/design/术语表.en',
}

/** localStorage key for the user's explicit language preference (written only after clicking a real language-switch link) */
export const LANG_PREF_KEY = 'ferry-doc-lang'

/** Browser preferred language -> 'zh' | 'en' | null (no guessing for anything other than zh/en) */
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
    /* stay silent when storage is unreachable, e.g. private browsing mode */
  }
}

/**
 * Normalize a route key: decode percent escapes (the browser's location.pathname percent-encodes Chinese,
 * while twin keys are literal Chinese, so without decoding a Chinese page can never match), strip the base
 * prefix, strip a trailing slash, strip .html, strip a trailing index (`/index.html` -> `/`, the same
 * treatment as VitePress' normalizeHref); empty/`/` normalize to `/`.
 */
export function normRoute(raw: string, base: string): string {
  let p = raw.split('#')[0].split('?')[0]
  try {
    p = decodeURIComponent(p)
  } catch {
    /* keep the value as-is when it contains an invalid escape sequence */
  }
  if (base && base !== '/' && p.startsWith(base)) p = p.slice(base.length - 1) // keep the leading '/'
  p = p.replace(/\/+$/, '')
  p = p.replace(/\.html$/, '')
  p = p.replace(/(^|\/)index$/, '')
  return p || '/'
}

/** If route belongs to a twin pair, return its current language and the twin's route key; otherwise null */
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
 * Target route key for the auto-redirect; null = no redirect.
 * Rule: no explicit preference + the current page is in a twin pair + current language != browser language -> jump to the other side.
 */
export function autoJumpTarget(route: string, base: string): string | null {
  if (readPref()) return null // the user already picked a language manually, so stop choosing for them
  const want = browserLanguage()
  if (!want) return null
  const twin = resolveTwin(normRoute(route, base))
  if (!twin || twin.current === want) return null
  return twin.target
}

/**
 * Click interception: an explicit preference is written only when the click target is the
 * "twin of the current page" (a real language-switch link). Clicking a same-language doc from the sidebar
 * (e.g. a zh comparison page while on a zh page) is not a switch and writes no preference.
 *
 * currentRoute is passed in by the caller (VitePress' router.route.path). Do not read location.pathname:
 * VitePress' window-capture click handler runs before this one, and after its synchronous history.pushState
 * location.pathname already points at the destination, so reading it can never match "the current page's twin".
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
