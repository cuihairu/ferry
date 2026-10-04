# ferry 功能拆解（todo）

- 调研来源：3x-ui（MHSanaei/3x-ui）、Marzban（Gozargah/Marzban）、Hiddify（hiddify/hiddifypanel）；Remnawave 调研中，横向对比附录后补。
- 口径：每条=原子动作，可独立验收；`来源`=参考哪个面板的哪个做法（文件级）。P0=最小可用面板闭环（用户/节点/订阅/流量记账）。

## P0 — 最小可用闭环

### 后端

- [ ] P0-1 用户 CRUD API：创建/列表/查询/修改/删除，字段=用户名、流量配额、到期时间、启用开关（来源：Marzban `app/models/user.py` 的 data_limit/expire/status）
- [ ] P0-2 用户创建时自动生成订阅 token，并提供重置接口（来源：Marzban `/sub/{token}`；Hiddify `auth.py` 的 api_key 即订阅标识）
- [ ] P0-3 节点 CRUD API：地址/端口/协议/配置模板 JSON/启用开关（来源：3x-ui `internal/database/model/model.go` 的 Inbound；Marzban `/api/inbounds`）
- [ ] P0-4 订阅 v2ray 格式：可用节点编码为 vmess/vless/ss/trojan 分享链接后 base64 打包（来源：Marzban `app/subscription/v2ray.py`；3x-ui `internal/sub/links.go`）
- [ ] P0-5 订阅 clash 格式：生成 clash/mihomo YAML（来源：3x-ui `internal/sub/clash_yaml.go`；Marzban clash-meta 输出）
- [ ] P0-6 订阅端点 `GET /sub/{token}`（`?target=v2ray|clash`），并按 UA 自适应默认格式（来源：Marzban `subscription.py` 的 client_type 路由；Hiddify `user/user.py` get_proper_config）
- [ ] P0-7 订阅响应带 `subscription-userinfo`（已用/配额/到期）与 `profile-update-interval` 头（来源：Marzban `app/routers/subscription.py:180-196`）
- [ ] P0-8 订阅只返回当前可用节点：用户启用且未到期且未超配额（来源：Hiddify `models/user.py` is_active 计算属性）
- [ ] P0-9 流量记账写接口：记录 user/node/上下行字节数，可单条可批量（来源：Marzban `app/jobs/record_usages.py`；Hiddify `models/usage.py` DailyUsage）
- [ ] P0-10 流量读接口：按用户汇总 + 按日明细列表（来源：Hiddify `models/usage.py` 的 today/total 聚合）
- [ ] P0-11 到期/超限自动停用：定时任务扫表置 disabled（来源：Marzban `app/jobs/review_users.py`；Hiddify user_should_reset）
- [ ] P0-12 xray 对接收敛为 `internal/xray.Handler` 接口，P0 用空实现保持边界稳定（来源：Marzban `xray_api/stats.py` 的 stats 抽象）

### 前端

- [ ] P0-13 pnpm workspace + Vue3/Vite/TS/Pinia/Element Plus 应用骨架与路由（用户/节点/设置占位）
- [ ] P0-14 Linear 风格主题：CSS 变量覆盖 Element Plus，暗色为主，侧边导航+顶栏+内容区布局（本机 linear.app.md token 表，自定口径）
- [ ] P0-15 用户管理页：表格 + 新建/编辑对话框（配额/到期/开关）+ 订阅链接展示与复制
- [ ] P0-16 节点管理页：表格 + 新建/编辑对话框（协议/地址/端口/配置模板）

### 工程

- [ ] P0-17 根 Makefile：dev / build / test 一键入口，覆盖前后端
- [ ] P0-18 deploy/：docker-compose.yml 与示例配置
- [ ] P0-19 测试门禁：订阅编码、可用性判定、CRUD 的表驱动/集成测试接入 `make test`

## P1 — 进阶（分期做）

- [ ] P1-1 管理员登录（会话或 JWT）+ 登录失败限速（来源：Marzban `/api/admin/token` 签发 JWT；3x-ui `login_limiter.go`）
- [ ] P1-2 API Token：程序化调用面板 API（来源：3x-ui ApiToken 模型与 `/apiTokens` 路由）
- [ ] P1-3 对接 xray gRPC stats 采集真实流量，替代手工上报（来源：Marzban `xray_api/stats.py` QueryStats）
- [ ] P1-4 流量重置周期：day/week/month（来源：Marzban data_limit_reset_strategy；Hiddify User.mode）
- [ ] P1-5 用户模板：新建用户套用默认配额/时长（来源：Marzban `app/routers/user_template.py`）
- [ ] P1-6 备份导出：SQLite 备份下载端点（来源：3x-ui `dump_sqlite.go` 与 export 路由；Hiddify 6 小时自动备份）
- [ ] P1-7 面板 Web 证书上传/自签管理；ACME 留给部署层（来源：3x-ui getWebCertFiles；Hiddify acme.sh 在 manager 层）
- [ ] P1-8 系统状态：内存/CPU/在线情况展示（来源：3x-ui `check_memory_usage.go`、cpuHistory 路由）
- [ ] P1-9 单节点分享链接/二维码展示（来源：3x-ui `/links/:email` 路由）
- [ ] P1-10 通知渠道：事件外发（如 Telegram/Webhook）（来源：Marzban Telegram Bot；3x-ui discord_notify_job）
- [ ] P1-11 运行日志查看（来源：Marzban 节点 WebSocket 日志；3x-ui clear_logs_job）

## P2 — 远期或明确不做

- [ ] P2-1（不做）多节点主从同步：ferry 定位单机小内存 VPS，3x-ui Node 心跳/Hiddify Child 同步的复杂度与定位冲突（来源：3x-ui `node_traffic_sync_job.go`；Hiddify `models/child.py`）
- [ ] P2-2（不做）多级管理员/子管理员配额：自用面板单管理员即可，表结构预留空间（来源：Hiddify Role 四级；Marzban is_sudo 两级）
- [ ] P2-3（不做）按用户带宽限速：Marzban/3x-ui 均未见实现，Hiddify 仅全局参数，收益低（来源：三家调研）
- [ ] P2-4（不做）HWID/设备数/IP 数限制：依赖客户端配合上报，复杂度高（来源：3x-ui ClientHwid、check_client_ip_job）
- [ ] P2-5（观察项）公告 announcement：四家均未见成熟实现（来源：各家 grep 无命中）
- [ ] P2-6（观察项）系统级操作审计日志：各家未见独立审计表（来源：各家 grep 无命中）
- [ ] P2-7（收窄）协议支持范围：只做 vless/vmess/trojan/shadowsocks 四种，Hiddify 19 种协议的全家桶不符合小面板定位（来源：Hiddify `models/proxy.py` ProxyProto 枚举）
