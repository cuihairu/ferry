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

## ferry-agent —（代理机侧，设计定稿）

口径：Go 静态二进制（CGO_ENABLED=0）常驻每台代理机；只出站连面板（WebSocket 长连接，节点不开入站端口）；认证=节点令牌（必选）+ mTLS（可开关）；协议契约在 `packages/`，面板 Go 端与 agent 共用。

### 协议契约（先做）

- [ ] [P0] A-1 `packages/agentproto` Go module：Envelope(JSON) + 消息类型常量 + 协议版本号，面板与 agent 以 go.mod replace 共用
- [ ] [P0] A-2 握手与认证消息 `agent.hello`（节点令牌）/`panel.hello_ack`；agent 配置支持 CA/客户端证书路径（mTLS 开关）
- [ ] [P0] A-3 心跳消息：uptime/负载/内存/证书到期/进程状态汇总 + 应答带回周期
- [ ] [P0] A-4 配置下发消息 `config.push`（proc/kind/version/sha256/payload）/`config.ack`（结果/错误/是否已回滚）
- [ ] [P0] A-5 进程管理消息：状态上报 + `proc_ctl`（start/stop/reload）/应答
- [ ] [P0] A-6 流量上报消息：按进程 rx/tx 累计 + 在线连接数，周期主动上报
- [ ] [P0] A-7 告警消息 `alarm`：进程崩溃拉起失败、证书临近到期、负载过高

### 骨架 + 心跳

- [ ] [P0] A-8 `apps/agent` 入口：静态编译产出单文件二进制，`make build-agent` 验收
- [ ] [P0] A-9 agent 配置文件：面板地址、节点令牌、agent ID、证书路径、心跳与重连参数
- [ ] [P0] A-10 连接层：只出站 WebSocket、指数退避自动重连、按消息类型分发
- [ ] [P0] A-11 面板侧 `/agent/ws` 接入：读 hello 校验令牌、注册在线连接、心跳更新在线状态
- [ ] [P0] A-12 面板 API：节点列表带在线状态与 `last_seen`（nodes 表增 token/last_seen/status 字段）

### 进程管理

- [ ] [P0] A-13 进程规格：agent 配置文件定义 name/kind(exec/args/config 路径)/reload 策略
- [ ] [P0] A-14 启停与状态机（running/stopped/crashed），崩溃自动拉起（退避），状态随心跳/事件上报
- [ ] [P1] A-15 面板批量操作：对选中的多节点统一下发 proc 操作与配置

### 配置下发

- [ ] [P0] A-16 agent 收到 `config.push`：sha256 校验 → 落临时文件 → kind 对应校验命令 → 原子替换 → reload
- [ ] [P0] A-17 校验或 reload 失败自动回滚旧配置并恢复，`config.ack` 带错误详情
- [ ] [P0] A-18 面板侧：节点配置存储（node_configs 表）+ 推送接口 + 等待 ack（超时判失败）

### 流量采集

- [ ] [P0] A-19 agent 周期采集流量字节数（xray 走 gRPC stats；其余 kind 标记未实现）并上报
- [ ] [P0] A-20 面板接收 `traffic.report` 写入 traffic_logs（对齐 P0-9 记账写接口）
- [ ] [P0] A-21 按进程的在线连接数采集与上报

### 管理面（面板侧）

- [ ] [P1] A-22 异常告警落库与节点页展示（进程挂/证书到期即时可见）
- [ ] [P1] A-23 agent 自升级：面板下发 upgrade 指令，agent 换二进制重启，失败回滚
- [ ] [P1] A-24 一键部署：deploy/ 安装脚本（下载二进制、装 systemd、签发节点令牌、mTLS 证书生成）

## P2 — 远期或明确不做

- [ ] P2-1（不做）多节点主从同步：ferry 定位单机小内存 VPS，3x-ui Node 心跳/Hiddify Child 同步的复杂度与定位冲突（来源：3x-ui `node_traffic_sync_job.go`；Hiddify `models/child.py`）
- [ ] P2-2（不做）多级管理员/子管理员配额：自用面板单管理员即可，表结构预留空间（来源：Hiddify Role 四级；Marzban is_sudo 两级）
- [ ] P2-3（不做）按用户带宽限速：Marzban/3x-ui 均未见实现，Hiddify 仅全局参数，收益低（来源：三家调研）
- [ ] P2-4（不做）HWID/设备数/IP 数限制：依赖客户端配合上报，复杂度高（来源：3x-ui ClientHwid、check_client_ip_job）
- [ ] P2-5（观察项）公告 announcement：四家均未见成熟实现（来源：各家 grep 无命中）
- [ ] P2-6（观察项）系统级操作审计日志：各家未见独立审计表（来源：各家 grep 无命中）
- [ ] P2-7（收窄）协议支持范围：只做 vless/vmess/trojan/shadowsocks 四种，Hiddify 19 种协议的全家桶不符合小面板定位（来源：Hiddify `models/proxy.py` ProxyProto 枚举）
