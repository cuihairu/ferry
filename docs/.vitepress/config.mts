import { defineConfig } from 'vitepress'
import { LANG_TWINS, LANG_PREF_KEY } from './theme/lang-switch'

const BASE = '/ferry/'

/**
 * 预渲染前的同步自动跳转脚本（防「先中文后英文」闪页）。
 * 逻辑与 theme/lang-switch.ts 保持一致：无显式偏好时按浏览器语言跳孪生，
 * 有偏好（点过真实切换链接）则不跳；只动既有孪生对，无孪生页面一律不动。
 * 两处需同步修改（此处为内联字符串，无法 import 模块）。
 */
const autoJumpHead = [
  '(function () {',
  '  var BASE = ' + JSON.stringify(BASE) + ';',
  '  var TWINS = ' + JSON.stringify(LANG_TWINS) + ';',
  '  var KEY = ' + JSON.stringify(LANG_PREF_KEY) + ';',
  '  var pref = null;',
  '  try { pref = localStorage.getItem(KEY); } catch (e) { return; }',
  '  if (pref === "zh" || pref === "en") return;',
  '  var raw = ((navigator.languages && navigator.languages[0]) || navigator.language || "");',
  '  raw = String(raw).toLowerCase();',
  '  var want = raw.indexOf("zh") === 0 ? "zh" : raw.indexOf("en") === 0 ? "en" : "";',
  '  if (!want) return;',
  '  function norm(p) {',
  '    p = p.split("#")[0].split("?")[0];',
  '    try { p = decodeURIComponent(p); } catch (e) {}',
  '    if (BASE && BASE !== "/" && p.indexOf(BASE) === 0) p = p.slice(BASE.length - 1);',
  '    p = p.replace(/\\/+$/, "");',
  '    p = p.replace(/\\.html$/, "");',
  '    p = p.replace(/(^|\\/)index$/, "");',
  '    return p || "/";',
  '  }',
  '  function twin(p) {',
  '    if (Object.prototype.hasOwnProperty.call(TWINS, p)) return { current: "zh", target: TWINS[p] };',
  '    for (var k in TWINS) if (Object.prototype.hasOwnProperty.call(TWINS, k) && TWINS[k] === p) return { current: "en", target: k };',
  '    return null;',
  '  }',
  '  var t = twin(norm(location.pathname));',
  '  if (!t || t.current === want) return;',
  '  var b = BASE.replace(/\\/$/, "");',
  '  var url = t.target === "/" ? BASE : b + t.target + ".html";',
  '  if (location.pathname !== url) location.replace(url);',
  '})();',
].join('\n')

export default defineConfig({
  lang: 'zh-CN',
  title: 'ferry',
  description: '轻量代理集群管理面板：用户、节点、订阅、计费与触达一体化',
  base: BASE,
  head: [
    ['link', { rel: 'icon', type: 'image/svg+xml', href: '/ferry/favicon.svg' }],
    ['script', {}, autoJumpHead],
  ],
  themeConfig: {
    logo: '/logo.svg',
    siteTitle: 'ferry',
    nav: [
      { text: '部署指南', link: '/deploy/部署指南' },
      { text: '使用指南', link: '/guide/使用指南' },
      { text: '设计文档', link: '/design/调度核心设计' },
    ],
    sidebar: [
      {
        text: '上手',
        items: [
          { text: '部署指南', link: '/deploy/部署指南' },
          { text: '使用指南', link: '/guide/使用指南' },
          { text: 'FAQ', link: '/guide/faq' }
        ]
      },
      {
        text: '概览',
        items: [{ text: '面板横向对比', link: '/面板横向对比' }]
      },
      {
        text: '核心设计',
        items: [
          { text: '调度核心设计', link: '/design/调度核心设计' },
          { text: '入口与负载均衡设计', link: '/design/入口与负载均衡设计' },
          { text: 'Agent 架构设计', link: '/design/agent架构设计' },
          { text: '套餐与成本设计', link: '/design/套餐与成本设计' },
          { text: '计划扩充设计', link: '/design/计划扩充设计' },
          { text: '开源选型设计', link: '/design/开源选型设计' },
          { text: '决策记录', link: '/design/决策记录' }
        ]
      },
      {
        text: '运营与安全',
        items: [
          { text: '支付设计', link: '/design/支付设计' },
          { text: '用户面板运营设计', link: '/design/用户面板运营设计' },
          { text: '用户触达设计', link: '/design/用户触达设计' },
          { text: '流量节省设计', link: '/design/流量节省设计' },
          { text: '面板可用性设计', link: '/design/面板可用性设计' },
          { text: '安全设计', link: '/design/安全设计' },
          { text: '告警通道设计', link: '/design/告警通道设计' },
          { text: '合规定位声明', link: '/design/合规定位声明' }
        ]
      },
      {
        text: '实录与审计',
        items: [
          { text: 'Herald 对接实录', link: '/herald-对接实录' },
          { text: '文档一致性审计', link: '/审计-文档一致性' }
        ]
      }
    ],
    outline: [2, 3],
    socialLinks: [{ icon: 'github', link: 'https://github.com/cuihairu/ferry' }]
  },
  // review/ 是审核底稿（含原始工具记录与未闭合标签），只留档不入站
  srcExclude: ['review/**'],
})
