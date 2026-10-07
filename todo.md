# ferry 功能拆解（todo）

- 调研来源：3x-ui（MHSanaei/3x-ui）、Marzban（Gozargah/Marzban）、Hiddify（hiddify/hiddifypanel）；Remnawave 调研中，横向对比附录后补。
- 口径：每条=原子动作，可独立验收；`来源`=参考哪个面板的哪个做法（文件级）。P0=最小可用面板闭环（用户/节点/订阅/流量记账）。

## P0 — 最小可用闭环

### 后端

- [x] P0-1 用户 CRUD API：创建/列表/查询/修改/删除，字段=用户名、流量配额、到期时间、启用开关（来源：Marzban `app/models/user.py` 的 data_limit/expire/status）
- [x] P0-2 用户创建时自动生成订阅 token，并提供重置接口（来源：Marzban `/sub/{token}`；Hiddify `auth.py` 的 api_key 即订阅标识）
- [x] P0-3 节点 CRUD API：地址/端口/协议/配置模板 JSON/启用开关（来源：3x-ui `internal/database/model/model.go` 的 Inbound；Marzban `/api/inbounds`）
- [x] P0-4 订阅 v2ray 格式：可用节点编码为 vmess/vless/ss/trojan 分享链接后 base64 打包（来源：Marzban `app/subscription/v2ray.py`；3x-ui `internal/sub/links.go`）
- [x] P0-5 订阅 clash 格式：生成 clash/mihomo YAML（来源：3x-ui `internal/sub/clash_yaml.go`；Marzban clash-meta 输出）
- [x] P0-6 订阅端点 `GET /sub/{token}`（`?target=v2ray|clash`），并按 UA 自适应默认格式（来源：Marzban `subscription.py` 的 client_type 路由；Hiddify `user/user.py` get_proper_config）
- [x] P0-7 订阅响应带 `subscription-userinfo`（已用/配额/到期）与 `profile-update-interval` 头（来源：Marzban `app/routers/subscription.py:180-196`）
- [x] P0-8 订阅只返回当前可用节点：用户启用且未到期且未超配额（来源：Hiddify `models/user.py` is_active 计算属性）
- [x] P0-9 流量记账写接口：记录 user/node/上下行字节数，可单条可批量（来源：Marzban `app/jobs/record_usages.py`；Hiddify `models/usage.py` DailyUsage）
- [x] P0-10 流量读接口：按用户汇总 + 按日明细列表（来源：Hiddify `models/usage.py` 的 today/total 聚合）
- [x] P0-11 到期/超限自动停用：定时任务扫表置 disabled（来源：Marzban `app/jobs/review_users.py`；Hiddify user_should_reset）
- [x] P0-12 xray 对接收敛为 `internal/xray.Handler` 接口，P0 用空实现保持边界稳定（来源：Marzban `xray_api/stats.py` 的 stats 抽象）

### 前端

- [x] P0-13 pnpm workspace + Vue3/Vite/TS/Pinia/Element Plus 应用骨架与路由（用户/节点/设置占位）
- [x] P0-14 Linear 风格主题：CSS 变量覆盖 Element Plus，暗色为主，侧边导航+顶栏+内容区布局（本机 linear.app.md token 表，自定口径）
- [x] P0-15 用户管理页：表格 + 新建/编辑对话框（配额/到期/开关）+ 订阅链接展示与复制
- [x] P0-16 节点管理页：表格 + 新建/编辑对话框（协议/地址/端口/配置模板）

### 工程

- [x] P0-17 根 Makefile：dev / build / test 一键入口，覆盖前后端
- [x] P0-18 deploy/：docker-compose.yml 与示例配置
- [x] P0-19 测试门禁：订阅编码、可用性判定、CRUD 的表驱动/集成测试接入 `make test`

## P1 — 进阶（分期做）

[x] P1-1 管理员登录（会话或 JWT）+ 登录失败限速（来源：Marzban `/api/admin/token` 签发 JWT；3x-ui `login_limiter.go`）
[x] P1-2 API Token：程序化调用面板 API（来源：3x-ui ApiToken 模型与 `/apiTokens` 路由）
- [x] P1-3 对接 xray gRPC stats 采集真实流量，替代手工上报（来源：Marzban `xray_api/stats.py` QueryStats）
- [x] P1-4 流量重置周期：day/week/month（来源：Marzban data_limit_reset_strategy；Hiddify User.mode）
- [x] P1-5 用户模板：新建用户套用默认配额/时长（来源：Marzban `app/routers/user_template.py`）
- [x] P1-6 备份导出：SQLite 备份下载端点（来源：3x-ui `dump_sqlite.go` 与 export 路由；Hiddify 6 小时自动备份）
- [x] P1-7 面板 Web 证书上传/自签管理；ACME 留给部署层（来源：3x-ui getWebCertFiles；Hiddify acme.sh 在 manager 层）
- [x] P1-8 系统状态：内存/CPU/在线情况展示（来源：3x-ui `check_memory_usage.go`、cpuHistory 路由）
- [x] P1-9 单节点分享链接/二维码展示（来源：3x-ui `/links/:email` 路由）
- [x] P1-10 通知渠道：事件外发（如 Telegram/Webhook）（来源：Marzban Telegram Bot；3x-ui discord_notify_job）
- [x] P1-11 运行日志查看（来源：Marzban 节点 WebSocket 日志；3x-ui clear_logs_job）

## ferry-agent —（代理机侧，设计定稿）

口径：Go 静态二进制（CGO_ENABLED=0）常驻每台代理机；只出站连面板（WebSocket 长连接，节点不开入站端口）；认证=节点令牌（必选）+ mTLS（可开关）；协议契约在 `packages/`，面板 Go 端与 agent 共用。

### 协议契约（先做）

- [x] [P0] A-1 `packages/agentproto` Go module：Envelope(JSON) + 消息类型常量 + 协议版本号，面板与 agent 以 go.mod replace 共用
- [x] [P0] A-2 握手与认证消息 `agent.hello`（节点令牌）/`panel.hello_ack`；agent 配置支持 CA/客户端证书路径（mTLS 开关）
- [x] [P0] A-3 心跳消息：uptime/负载/内存/证书到期/进程状态汇总 + 应答带回周期
- [x] [P0] A-4 配置下发消息 `config.push`（proc/kind/version/sha256/payload）/`config.ack`（结果/错误/是否已回滚）
- [x] [P0] A-5 进程管理消息：状态上报 + `proc_ctl`（start/stop/reload）/应答
- [x] [P0] A-6 流量上报消息：按进程 rx/tx 累计 + 在线连接数，周期主动上报
- [x] [P0] A-7 告警消息 `alarm`：进程崩溃拉起失败、证书临近到期、负载过高

### 骨架 + 心跳

- [x] [P0] A-8 `apps/agent` 入口：静态编译产出单文件二进制，`make build-agent` 验收
- [x] [P0] A-9 agent 配置文件：面板地址、节点令牌、agent ID、证书路径、心跳与重连参数
- [x] [P0] A-10 连接层：只出站 WebSocket、指数退避自动重连、按消息类型分发
- [x] [P0] A-11 面板侧 `/agent/ws` 接入：读 hello 校验令牌、注册在线连接、心跳更新在线状态
- [x] [P0] A-12 面板 API：节点列表带在线状态与 `last_seen`（nodes 表增 token/last_seen/status 字段）

### 进程管理

- [x] [P0] A-13 进程规格：agent 配置文件定义 name/kind(exec/args/config 路径)/reload 策略
- [x] [P0] A-14 启停与状态机（running/stopped/crashed），崩溃自动拉起（退避），状态随心跳/事件上报
- [x] [P1] A-15 面板批量操作：对选中的多节点统一下发 proc 操作与配置

### 配置下发

- [x] [P0] A-16 agent 收到 `config.push`：sha256 校验 → 落临时文件 → kind 对应校验命令 → 原子替换 → reload
- [x] [P0] A-17 校验或 reload 失败自动回滚旧配置并恢复，`config.ack` 带错误详情
- [x] [P0] A-18 面板侧：节点配置存储（node_configs 表）+ 推送接口 + 等待 ack（超时判失败）

### 流量采集

- [x] [P0] A-19 agent 周期采集流量字节数（xray 走 gRPC stats；其余 kind 标记未实现）并上报
- [x] [P0] A-20 面板接收 `traffic.report` 写入 node_traffic_logs（节点级；用户级记账对齐 P0-9，走 traffic_logs 待 P1-3 逐用户映射）
- [x] [P0] A-21 按进程的在线连接数采集与上报

### 管理面（面板侧）

- [x] [P1] A-22 异常告警落库与节点页展示（进程挂/证书到期即时可见）
- [x] [P1] A-23 agent 自升级：面板下发 upgrade 指令，agent 换二进制重启，失败回滚
- [x] [P1] A-24 一键部署：deploy/ 安装脚本（下载二进制、装 systemd、签发节点令牌、mTLS 证书生成）

## 支付（设计：docs/design/支付设计.md）

口径：Provider 抽象在 `packages/payment`，网关插件在 `payments/`；三账=订单/支付流水/发放记录，按 order_no 串联可对账；卡密先行零资质，USDT 二段，微信/支付宝留 Provider 位等资质。

- [x] [P0] PAY-0 支付设计文档落 docs/design/支付设计.md（Provider 接口、三账 schema、阶段划分）
- [x] [P0] PAY-1 `packages/payment` 契约：Provider 接口 + Order/Receipt/Callback 类型 + 三账表迁移
- [x] [P0] PAY-2 卡密批次与卡密表：批次（权益类型/值/有效期/生成人）+ 卡密（唯一索引/状态/失败计数）
- [x] [P0] PAY-3 批次生成接口：crypto/rand 去混淆字符集批量生成 + CSV 导出，仅管理员可调
- [x] [P0] PAY-4 兑换接口：原子核销 → 事务写三账（provider=card）→ 执行加配额/延到期，幂等
- [x] [P0] PAY-5 兑换防爆破：按 IP 滑动窗口限速 + 失败计数锁码 + 统一错误文案
- [x] [P0] PAY-6 dash 卡密管理页：建批次、批次/卡密列表、导出、禁用
- [x] [P0] PAY-7 panel 兑换页：输入卡密兑换并展示结果
- [x] [P1] PAY-8 payments/epusdt Provider：CreateOrder 收银台 + 回调验签 + 到账自动发放
- [x] [P1] PAY-9 dash 对账视图：订单/流水/发放三账按订单分组，标出缺失环节
- [x] [P1] PAY-10 payments/wechat、payments/alipay Provider stub（返回未启用，等商户资质）
- [x] [P1] PAY-11 panel 下单与订单状态查询 API（对 epusdt 段）

## 入口与负载均衡（设计：docs/design/入口与负载均衡设计.md）

口径：国内入口池 relay 隧道接海外落地池；入口/落地都是 agent 节点角色可配；agent 拆「核心+角色组件」；探测下沉入口边缘、面板只收结论；故障按区域/运营商聚合处置；分配做成本感知（仅按流量计费节点参与成本判定）。

### 契约与架构

- [x] [P0] E-1 `agentproto` 注册元数据：role/direction（出海/回国/双向）/line_type（163/CN2 GIA/CU VIP/CMI/IPLC/普通）/region/city/datacenter/isp/labels/transport + 成本字段（billing_type 必填、traffic_price/monthly_cost/currency/note）+ 套餐字段（bw_up/bw_down Mbps 必填、monthly_traffic_quota、rate_limited、burst），isp 与方向线路必填可「未知/普通」不许空，面板可改以面板为准
- [x] [P0] E-2 `probe.report` 探测上报消息与应答：目标类型/rtt/丢包/可达/被封/判定/区域运营商快照
- [x] [P0] E-3 心跳负载字段：连接数/带宽/CPU 随心跳上报（对齐落地分配的负载信号），带宽利用率（实测吞吐/校准容量）随自动均衡补
- [x] [P0] E-4 agent 核心瘦身分层：核心最小集（注册心跳/进程管理/配置接收/流量负载上报，目标 ≤10MB）与角色组件边界
- [x] [P0] E-5 relay 数据面组件（独立进程）：入口角色装配，与核心只经本机 IPC/配置交互，崩不带崩心跳
- [x] [P0] E-6 传输插件抽象：隧道接口（建立/多路复用/心跳/重连）与传输解耦，首个插件 TLS 伪装（默认）
- [x] [P0] E-7 边缘探测器组件（独立进程）：入口/落地可开，崩溃不影响进程管理
- [x] [P0] E-8 agent 自动测速校准：注册后跑轻量测速，实测与套餐偏差大以实测为准，记录实测容量与校准时间

### 数据模型

- [x] [P0] E-9 nodes 表扩展：role/direction/line_type/region/city/datacenter/isp/labels/transport/billing_type/traffic_price_cents/monthly_cost_cents/currency/cost_note/套餐带宽与配额/测速校准列（ensureColumns 增量迁移）
- [x] [P0] E-10 分配记录表 landing_assignments：入口/落地/策略/权重/原因/起止
- [x] [P0] E-11 探测历史表 probe_reports：探测者/目标/rtt/丢包/判定/区域运营商快照 + 窗口索引
- [x] [P1] E-12 区域/运营商状态表 dimension_status：状态灯数据（region/isp 两维）

### 探测与故障处置

- [x] [P0] E-13 边缘探测任务：隧道探测（入口→落地）/出口基线/入口互探被封检测，周期上报结论
- [x] [P0] E-14 面板聚合判定：窗口内同区域异常达阈值=区域故障整区域切走；同 ISP 聚合=运营商故障一起切；不逐个摘挂
- [x] [P0] E-15 告警按区域/运营商合并发（「XX 区域入口整体不可达」单条），dash 区域级状态灯
- [x] [P1] E-16a 自动摘挂状态机：连续 sick 摘除/恢复复位（internal/pool 周期判定），订阅入口池即时生效，pool API（列表+手动复位）与通知
- [ ] [P1] E-16b 摘挂热更新换线：config.push 变更列表（落地 relay 重指），随 E-21 分配策略接入
- [x] [P1] E-17 区域传输判定（internal/transport 存活占比推荐+现行对比换线建议，/api/transport-status，dash 调配页面板；切换执行=改 transport 标注重推配置）
- [x] [P1] E-18a ws-tls 传输插件（tunnel 注册，TLS+WebSocket 升级伪装浏览器，消息语义适配流 net.Conn，落地侧任意 RFC6455 监听可前置 CDN）
- [ ] [P1] E-18b QUIC（hysteria2 系）传输插件：需拍板——quic-go 依赖体量 vs E-4 尺寸门禁（独立 plugin 二进制 / 引依赖破门禁 / 延后），真 hysteria2 协议不造，raw QUIC 不冒称 hysteria2

### 分配与成本

- [x] [P0] E-19 订阅下发入口列表+测速信息（clash 分组 url-test、v2ray 按区域分组带延迟备注）
- [x] [P0] E-20 手动落地分配：dash 指定入口/区域的落地列表与权重，配置推送生效
- [x] [P1] E-21 加权最小连接自动分配（internal/alloc 四档策略按方向可配：最小连接/成本优先/性能优先/均衡，容量权重 3M 少分+按流量成本判定+回国线档，换线留痕 landing_assignments+通知，/api/alloc 策略 API+dash 调配页策略卡；手动分配优先不受影响）
- [x] [P1] E-22 低峰再平衡与峰时滞后（低峰窗口内按策略全序换线再平衡，峰时主指标须省 30% 才动/perf_first 只许升档，窗口按方向档可配 1-7 默认；按流量成本参与判定与高成本靠后随 E-21 落地；存量连接排空属 relay 数据面随 E-16b）
- [ ] [P1] E-23 成本看板与高成本告警：节点流量花费、区域/运营商汇总、月度预估、超阈值切流提示

### 管理面

- [x] [P0] E-24 dash 节点视图：按区域分组+区域状态灯、入口/落地分组、运营商视角切换
- [ ] [P1] E-25 dash 负载看板与手动调配（手动摘除/恢复/强制切区域）；节点卡展示套餐与带宽利用率，超 80% 预警

- [ ] [P2] E-26 入口轮换：按探测结论轮换订阅入口顺序
- [ ] [P2] E-27 智能 DNS 分地域：按解析来源地域下发就近区域入口（预留）

### 方向与回国线（出海/回国两篇，见设计稿）

- [x] [P0] E-28 回国回程探测：海外入口探测器测国内落地回程延迟/丢包（复用 probe.report，direction=in），晚高峰探测加密
- [ ] [P1] E-29 晚高峰回程报表：按小时分段，19–23 时单独报表，与成本看板同页
- [ ] [P1] E-30 方向分流分配：出海走性价比、回国优先优质线路（线路档进权重、成本容忍高），landing_assignments 记 direction

### 成本参考

- [ ] [P2] E-31 成本参考库插件位：costref.Source 接口，手录成本为准，参考价接开源比价（infracost 类/公开价格表），偏差提示不自动改价
- [ ] [P2] E-32 价格变动与促销关注：关注条件（机房/配置/价位）命中的降价促销进 dash 通知

## 一键开服与自动入池（设计：docs/design/计划扩充设计.md §1）

口径：底座=OpenTofu/Terraform 供给引擎 + cloud-init 初始化，ferry 只做模板→供给执行→入池流水线三层；state 与云密钥按 R24 加密面口径加密存。

- [ ] [P1] OS-1 dash 提供商配置与机型模板：云凭证录入加密存储（R24 口径），模板含机型/区域/带宽/计费/方向线路标签
- [ ] [P1] OS-2 OpenTofu 供给接入：模板渲染 HCL/调 tofu CLI，state 集中管理与漂移检测（选型对比见设计 §1.2）
- [ ] [P1] OS-3 cloud-init 初始化与注册：装 agent、注入预签发 token，首连注册
- [ ] [P1] OS-4 入池流水线：provisioning→元数据补全→模板下发→探测通过→online，新节点 5 分钟可用

## 被封自动恢复（设计：docs/design/计划扩充设计.md §2，与摘挂同批 P1）

- [ ] [P1] BR-1 判封状态机与恢复流水线编排：接入区域探测/摘挂闭环（E-14/E-15），L1→L2→L3 分级推进
- [ ] [P1] BR-2 域名前置与自动 DNS 切换：DNS 商 API 插件位，域名不换 IP 随换
- [ ] [P1] BR-3 IP 池储备与自动补位：预备清单轮换，池空联动一键开服
- [ ] [P1] BR-4 证书 ACME 自动签发与续期：面板编排，复用 acme.sh/certbot 工具位
- [ ] [P1] BR-5 恢复动作留痕与失败升级：recovery_actions 落表，超时全失败告警升级人工（经 Herald）

## 分销/代理体系（设计：docs/design/计划扩充设计.md §3）

- [ ] [P2] DS-1 代理层级与折扣：distributors 表、二级卡批次归属、分润入三账按 order_no 串联
- [ ] [P2] DS-2 代理结算账目：售卡收入/分润/未结算汇总，对账视图加代理维度
- [ ] [P2] DS-3 代理面板视图：自己的客户/卡密/用量/结算；与优惠码不叠加取优

## 用户面板运营（设计：docs/design/用户面板运营设计.md）

口径：站内信为主；订单账目与支付三账对得上；外部投递统一经 Herald；触达与断联容灾独立成篇。

- [x] [P0] OD-1 panel 订单中心：兑换与购买记录列表（provider=card 归并），随支付批
- [ ] [P1] OD-2 订单详情与状态流转：待付/已完成/已退款（状态枚举补 refunded），套餐/金额/时长/流量明细
- [ ] [P1] NT-1 通知中心站内信：notifications 表+未读/列表，公告/到期/流量预警/系统四类
- [ ] [P1] NT-2 通知触发与偏好：定时扫描（到期/阈值）+事件触发，用户按类型×通道开关与阈值
- [ ] [P2] PROMO-1 优惠码：满减/折扣/指定套餐，dash 配置+panel 下单原子核销
- [ ] [P2] PROMO-2 限时活动与首单/续费折扣：起止与适用范围配置、panel 活动位
- [ ] [P2] PROMO-3 优惠账目打通：订单记原价+实付+优惠快照，与三账对得上
- [ ] [P2] SV-1 servify 客服集成：panel 入口嵌入（组件/iframe/SDK 以对接面为准）+独立部署对接地址
- [ ] [P2] SV-2 工单上下文打通：客服侧只读用户套餐/流量/订单（服务端接口，不泄凭据）

## 流量节省（设计：docs/design/流量节省设计.md）

口径：分流直连、缓存、拦截、压缩复用报表化、配额联动、节省报表；MITM 缓存默认不做（自用注释位）。

- [x] [P0] SAVE-1 GeoIP/geosite 分流进节点配置模板：国内流量直连不进隧道，规则库可经 config.push 更新
- [x] [P0] SAVE-2 静态资源域名清单直连/CDN（与 geosite 同机制）
- [ ] [P1] SAVE-3 入口缓存层插件位：nginx cache/Squid 作被管进程 kind，命中统计随心跳上报
- [ ] [P1] SAVE-4 广告/追踪拦截：节点侧 DNS 屏蔽名单（可配、可更新），拦截计数上报
- [ ] [P1] SAVE-5 压缩与连接复用/TLS 会话恢复报表化：复用率随心跳上报进报表
- [ ] [P1] SAVE-6 配额联动：流量/费用超阈值自动降速或切低成本节点（可配、留痕）
- [ ] [P1] SAVE-7 节省报表：save_stats 按日汇总，dash 每日/月省下 GB 与折算费用，与成本看板同页
- [ ] [P1] SAVE-8 panel 用户侧「已为你省下」汇总（月账单邮件附亮点）

## 告警通道（设计：docs/design/告警通道设计.md，对接 Herald）

口径：ferry 不自建通道，事件统一投 Herald 分发（管理告警+用户触达同一接口）；本地 outbox 保事件不丢。

- [ ] [P1] HERALD-1 事件 outbox：events/event_deliveries 落表，pending→投递→重试，失败标红不静默丢
- [ ] [P1] HERALD-2 Herald 集成接口：POST /events（kind/severity/target/dedup_key）+ 回执落库，配置 FERRY_HERALD_URL/TOKEN
- [ ] [P1] HERALD-3 管理告警接入：区域故障/被封/证书到期三类先行，其余六类随后
- [ ] [P1] HERALD-4 用户触达事件走同一接口（账单/域名/到期/预警，随触达批）

## P2 — 远期或明确不做

- [ ] P2-1（不做）多节点主从同步：ferry 定位单机小内存 VPS，3x-ui Node 心跳/Hiddify Child 同步的复杂度与定位冲突（来源：3x-ui `node_traffic_sync_job.go`；Hiddify `models/child.py`）
- [ ] P2-2（不做）多级管理员/子管理员配额：自用面板单管理员即可，表结构预留空间（来源：Hiddify Role 四级；Marzban is_sudo 两级）
- [ ] P2-3（不做）按用户带宽限速：Marzban/3x-ui 均未见实现，Hiddify 仅全局参数，收益低（来源：三家调研）
- [ ] P2-4（不做）HWID/设备数/IP 数限制：依赖客户端配合上报，复杂度高（来源：3x-ui ClientHwid、check_client_ip_job）
- [ ] P2-5（观察项）公告 announcement：四家均未见成熟实现（来源：各家 grep 无命中）
- [ ] P2-6（观察项）系统级操作审计日志：各家未见独立审计表（来源：各家 grep 无命中）
- [ ] P2-7（收窄）协议支持范围：只做 vless/vmess/trojan/shadowsocks 四种，Hiddify 19 种协议的全家桶不符合小面板定位（来源：Hiddify `models/proxy.py` ProxyProto 枚举）
