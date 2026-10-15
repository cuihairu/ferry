---
layout: home

hero:
  name: ferry
  text: 轻量代理集群管理面板
  tagline: Go 单二进制后端 + Vue 3 双前端，面向小内存 VPS——用户、节点、订阅、计费与触达一体化
  actions:
    - theme: brand
      text: 部署指南
      link: /deploy/部署指南
    - theme: alt
      text: 使用指南
      link: /guide/使用指南
    - theme: alt
      text: 设计文档
      link: /design/调度核心设计
    - theme: alt
      text: GitHub 仓库
      link: https://github.com/cuihairu/ferry
    - theme: alt
      text: English
      link: /index.en
    - theme: alt
      text: 中文
      link: /index

features:
  - title: 节点编排与调度
    details: agent 只出站重连入池，负载/成本/优质线权重逐系数可配，迁移恢复有 runbook 与一键脚本。
  - title: 订阅与流量
    details: 单订阅地址多节点分发，端点限频按 token 与 IP 双层防护，流量账本按 agent 上报周期落账（缺省 60s，可配）。
  - title: 计费与触达
    details: 卡密兑换与 USDT 在线支付双通道，到期提醒与月账单邮件例行触达，事件外发 Herald 不静默。
  - title: 设计即文档
    details: 设计文档随码落地并标注口径，文档与源码一致性定期审计，在线站与仓库同源。
---

[English](index.en.md) | [中文](index.md)

> 说明：上方按钮指向的部署指南、使用指南与设计文档目前仅有中文版；现阶段只有四篇顶层文档（本首页、herald 对接实录、面板横向对比、文档一致性审计）提供了英文版。
