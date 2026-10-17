---
layout: home

hero:
  name: ferry
  text: Lightweight Proxy Cluster Management Panel
  tagline: Single-binary Go backend + Vue 3 dual frontend, built for small-memory VPS — users, nodes, subscriptions, billing, and outreach in one place
  actions:
    - theme: brand
      text: Deployment Guide
      link: /deploy/部署指南
    - theme: alt
      text: Usage Guide
      link: /guide/使用指南
    - theme: alt
      text: Design Docs
      link: /design/调度核心设计
    - theme: alt
      text: GitHub Repository
      link: https://github.com/cuihairu/ferry
    - theme: alt
      text: English
      link: /index.en
    - theme: alt
      text: 中文
      link: /index

features:
  - title: Node orchestration and scheduling
    details: The agent only dials out to reconnect into the pool; load, cost, and premium-line weights are configurable per coefficient; migration and recovery have a runbook and a one-click script.
  - title: Subscriptions and traffic
    details: One subscription URL fans out to multiple nodes; endpoint rate limiting is protected on both the token and IP layers; traffic is accounted per agent reporting cycle (60s by default, configurable).
  - title: Billing and outreach
    details: Card-code redemption and USDT online payment as two channels; expiry reminders and monthly bill emails as routine outreach; events are pushed out via Herald and never dropped silently.
  - title: Design is the documentation
    details: Design docs land with the code and label their conventions; doc-to-source consistency is audited regularly; the online site and the repository share one source.

---


> Note: the deployment guide, usage guide, and design docs reachable from the buttons above are currently Chinese-only; only the four top-level documents (this home page, the herald integration log, the panel landscape comparison, and the doc consistency audit) have English versions so far.