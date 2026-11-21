import DefaultTheme from 'vitepress/theme'
import type { Theme } from 'vitepress'
import { onDocClick } from './lang-switch'
import './custom.css'

/**
 * 浏览器语言自动跳转的客户端接线（P2 拍板 2026-11-22：仅自动跳转）。
 *
 * 跳转本身由 config.mts 的 `<head>` 内联脚本在首帧前完成（防闪）。
 * 这里只挂点击拦截：点过真实语言切换链接即写显式偏好，之后自动跳转整体让位，
 * 避免「手动切回中文却被弹回英文」。
 *
 * 不挂 router.onAfterRouteChange：VitePress 1.6.4 运行期从不赋值该可选钩子
 * （router 内只是 `??` 兜底取值），注册即抛错。自动跳转只随「整页加载」发生
 * （内联脚本在首帧前跑）；站内点击导航是 SPA pushState，不重跑内联脚本——
 * 这是刻意的：尊重用户显式点击，不把点击者从所选页面弹走，也避免前进/后退往返弹跳。
 */
export default {
  extends: DefaultTheme,
  enhanceApp({ router, siteData }) {
    if (typeof window === 'undefined') return
    const base = siteData.value.base
    document.addEventListener(
      'click',
      (e) => onDocClick(e, base, router.route.path),
      true
    )
  },
} satisfies Theme
