import { defineConfig } from 'vitepress'

export default defineConfig({
  lang: 'zh-CN',
  title: 'ferry',
  description: '轻量代理集群管理面板：用户、节点、订阅、计费与触达一体化',
  base: '/ferry/',
  head: [['link', { rel: 'icon', type: 'image/svg+xml', href: '/ferry/favicon.svg' }]],
  themeConfig: {
    logo: '/logo.svg',
    siteTitle: 'ferry',
    nav: [
      { text: '设计文档', link: '/design/调度核心设计' },
      { text: 'GitHub', link: 'https://github.com/cuihairu/ferry' }
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
          { text: '开源选型设计', link: '/design/开源选型设计' }
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
  }
})
