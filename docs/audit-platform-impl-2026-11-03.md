# Ferry 平台实施 P0 现状审计报告（2026-11-03）

> 覆盖 todo.md 平台段 P0 现状审计任务 **F-9001~F-9013** 及各章审计类任务。
> 0.1 规则遵循：不把文档/TODO 占位当已实现；不重复实现；所有「已实现」结论均附
> `file:line` + 测试证据（测试名 + 实跑命令）。工作树零源码改动（仅本文档与
> todo.md 回写；`payments/go.mod` 为他人 WIP，未触碰）。
> **0.2 先决（F-0020~F-0039）已全部执行**，逐项证据见 §7。

## 1. 测试证据汇总（实跑，2026-11-03）

| 命令 | 结果 |
|---|---|
| `make test`（server `go test ./...` + dash/panel build） | 37 包 ok；`internal/handler` 在默认 `-timeout=600s` 下 **超时 panic**（机器负载，非断言失败） |
| `cd server && go test -timeout=40m ./internal/handler/` | **ok 456.208s**（EXIT=0，410 个 Test 函数全过） |
| `cd agent && go test ./...` | 20 包 ok（EXIT=0） |
| `packages/agentproto`、`packages/costref`、`packages/payment`、`payments/*` | 全 ok（EXIT=0） |
| `make test-pg`（PG15 方言，storage 包） | **ok 4.966s**（EXIT=0，F-1909 证据） |
| `dash && pnpm build` / `panel && pnpm build` | EXIT=0（20.12s / 9.08s） |
| `bash deploy/e2e-smoke.sh` | **53 条 `ok -` 断言，EXIT=0**（含 compose 配置合法、真 WS 链路、令牌轮换/吊销、备份恢复腿） |

全仓 Go 测试函数总数 **928**（server+agent+packages+payments）；handler 包 410。
观察项：`make test` 的 handler 包在满载下 600s 默认超时会假红，建议 Makefile 对该包显式 `-timeout`（未改，登记）。

## 2. 仓库/模块目录索引（F-9001）

```
ferry/
├── server/                      Go 模块 github.com/cuihairu/ferry/server（控制面，单二进制 ferry）
│   ├── cmd/ferry/               入口（ctx 驱动、优雅退出；main.go:227 cost.WatchLoop）
│   └── internal/                37 包（47k+ 行）：
│       handler/   192 条路由（router.go），410 测试；无 Service/Repository 层，
│                   Handler 直连 gorm（见 §3.16 结论）
│       storage/   GORM 模型 46 个 type（models.go 754 行）+ AutoMigrate + SQLite/PG 双方言
│       alloc/     调度（Pick/sweep/4 策略/权重）   pool/ 池态机（suspend/resume/standby）
│       sub/       订阅生成（clash/links/singbox/rotate/availability）
│       provision/ OpenTofu 供给（HCL 渲染、tofu CLI、cloud-init、补机循环）
│       cost/ + costref(包)   成本聚合/阈值告警/价格观察
│       monitor/ + aggregate/ 维度状态灯、区域/ISP 故障聚合
│       agenthub/  agent WS 请求-应答（超时） relaypush/ 配置重推
│       recovery/  恢复流水线  provision 供给  backup/ 加密快照+轮转
│       audit/ + secret/ + keyinit/   审计保留轮转、AES-GCM 密钥库、生产 keyinit 门禁
│       herald/    事件 outbox+退避投递   notify/ Webhook 等通道
│       sub/quota/ratelimit/review/toucher/...  运营侧
├── agent/                       Go 模块 …/agent（数据面，单二进制，可 -role 独立跑）
│   └── internal/  app/link/configd/config/probe/roles{relay,proberole,speedtest}/
│                  tunnel{ws-tls,ssh,quic}/quictunnel/upgrade/proc/…
├── dash/                        Vue3 管理端，17 视图（router.ts:36 角色守卫），pnpm build 绿
├── panel/                       Vue3 用户端，5 视图（Home/Orders/Redeem/Notifications/Login）
├── packages/                    agentproto（协议信封）、costref（成本参考）、payment（Provider 接口）
├── payments/                    epusdt（真接入）+ alipay/wechat（NewDisabled 占位）
├── deploy/                      Caddyfile、docker-compose、start.sh、agent-install.sh、
│                                restore.sh、e2e-smoke.sh、compose/systemd/start-args/
│                                install-rollback 冒烟、mtls-ca.sh、MIGRATION-RUNBOOK
└── docs/                        gap-analysis、审计-文档一致性、设计/指南/对比文档
```

Go 模块依赖：`server` replace → agentproto/costref/payment/payments；`agent` replace → agentproto；
`payments` replace → payment。前端经 Caddy 反代 HTTP 调 server（dash 另反代 `/admin/*`、`/distributor/*`）。

## 3. 审计表（16 节；F-9002~F-9010）

结论口径：**已实现** = 源码 + 测试双证据；**部分** = 等价物存在但与任务口径有差；
**未实现** = 全仓零命中；**待核实** = 本轮未核完（标注后续批次）。

### 3.1 核心域模型 Node / Capability / Path / PathGroup / Policy（F-9002）

| 项 | 结论 | 源码证据 | 测试证据 |
|---|---|---|---|
| Node 模型（地址/协议/配置/令牌/角色/方向/区域/ISP/成本/带宽） | **已实现** | `storage/models.go:64-114`（Role entry/landing/both :80、Direction out/in/both :81、PoolState :104、Status online/offline/unknown :77、AgentVersion :100、OldToken/TokenRotatedAt :72-74） | `node_test.go` TestNodeCRUD/TestNodeMetaInput；`node.go:347` validateNodeInput |
| 管理状态与健康状态分离（F-0205） | **已实现** | Enabled `models.go:71` / Status `:77` / PoolState `:104`（摘池不动 enabled，`pool.go:2`） | TestNodeCRUD、`pool_test.go` TestSweepSuspendAndResume |
| Agent/协议版本（F-0209） | **已实现** | `models.go:100` AgentVersion + agentproto Envelope.V | `upgrade_test.go` TestAgentWSHelloVersion |
| 期望/实际配置版本（F-0210） | **已实现** | `models.go:211` NodeConfig{Version,Sha256,Status} | `config_err_test.go` TestNodeConfigGuards；`relaypush_test.go` TestPushAckApplied |
| 容量/带宽/成本快照（F-0211） | **已实现** | `models.go:90-96`（BwUp/Down、MonthlyCostCents、TrafficPriceCents、SpeedMeasuredMbps） | `cost_test.go` TestBuild |
| 节点索引（F-0213） | **已实现** | `models.go:72` token uniqueIndex、`:104` pool_state index、`storage.go:141` WAL/外键 | storage 包测试 + 全 handler 测试建表跑通 |
| 节点元数据变更审计（F-0212） | **已实现** | `router.go:99` api 组挂 auditMiddleware（覆盖 PUT /api/nodes/:id） | `audit_test.go` TestAuditLogsCaptureWriteOps 等 6 例 |
| Capability 模型/声明（F-0222~F-0231） | **未实现** | 全仓 `type Capability` 0 命中；能力由 role/labels/LineType 隐式承载 | — |
| Path / PathGroup 模型（F-0244/0256~0268） | **未实现**（等价物部分） | 无 `type Path*`；Entry→Landing 编排由 `models.go:165` LandingAssignment + `alloc.go:497` sweep 承载 | `landing_test.go` TestLandingAssignment |
| Policy 模型（F-0282~0296） | **未实现**（调度策略部分） | 无 `type Policy`；`alloc.go:107-117` ScoreWeights/PolicySetting（settings 承载） | `alloc_test.go` TestPolicyPersistence |
| 用户状态机/套餐 Plan 模型 | **未实现**（部分） | 无 `type Plan/Subscription`；套餐=usertemplate settings + User 字段（ResetCycle `models.go:11`） | `user_test.go` TestUserResetCycle |

### 3.2 Agent（F-9003 前半）

| 项 | 结论 | 源码证据 | 测试证据 |
|---|---|---|---|
| 注册（一次性凭据原子消费） | **已实现** | `router.go:307` open.POST /enroll；`handler/enroll.go` | `enroll_test.go` 8 例（TestEnrollExchange、双花/过期/吊销） |
| 认证（节点令牌）+ 伪造拒绝 | **已实现** | `agentws.go` hello 校验 token | TestAgentWSBadToken |
| 心跳 | **已实现** | `agentws.go:381` MsgHeartbeat → :392 Ack | TestAgentWSHeartbeat |
| 断线重连 | **已实现** | `agent/internal/link/link.go` Run 重连循环 | `link_test.go` TestRunReconnectsUntilCancel 等 6 例 |
| 重连后状态对账（心跳元数据回填） | **已实现** | `agentws.go` syncNodeMeta | TestSyncNodeMetaFills、TestAgentWSHelloMeta |
| 版本上报 | **已实现** | Hello.Version + `models.go:100` | TestAgentWSHelloVersion |
| 资源使用上报（CPU/内存/连接） | **已实现** | agentproto Heartbeat 字段（messages.go:99-111）+ `/api/load` | TestAgentWSHeartbeat、`status_test.go` TestSysStatus |
| 配置下发/ACK/失败回滚 | **已实现** | `relaypush/` + `agent/internal/configd/` | relaypush_test 9 例（TestPushAckApplied/TestPushTimeout）；configd_test 10 例（TestApplyReloadFailRollsBack） |
| 消息超时/重试 | **已实现** | `agenthub/hub.go:114` Request+ErrTimeout；relaypush 重推 | TestPushTimeout、TestPushSendError |
| 幂等键（去重） | **已实现** | `agentws_dedupe.go`（node+type+envelope ID） | `agentws_dedupe_test.go` 7 例（含 TestAgentWSDedupSurvivesReconnect） |
| 未知消息处理 | **已实现** | link readLoop 丢弃坏消息；服务端留痕 | TestReadLoopDropsBadMessages |
| 升级失败恢复（看门狗回滚） | **已实现** | `upgrade/` 看门狗 | upgrade_test TestWatchdogRollback；`upgrade_test.go` TestNodeUpgrade |
| 凭据吊销（F-0710） | **已实现** | `node.go:208` revokeNodeToken + `models.go:386` NodeTokenRevocation + `router.go:111` | TestNodeTokenRevocation、e2e 吊销腿 |
| 凭据轮换（F-0711，10min 宽限） | **已实现** | `node.go:183` rotateNodeToken + `agentws.go:48` nodeTokenGracePeriod + `router.go:110` | TestNodeTokenRotation、TestAuthNodeTokenRotationWindow、e2e 轮换腿 |
| 批量进程/配置操作（F-0749） | **已实现** | `router.go:184-185` batch | `procop_test.go` TestBatchNodeProc/TestBatchNodeConfig |
| 节点摘池/禁用/重启用（F-0745/0747/0748） | **已实现** | `pool.go` suspend/resume；`node.go` enabled | TestSuspend/TestResume/TestNodeCRUD/TestPoolAPI |
| 配置版本查看（F-0752） | **已实现** | `router.go` GET /api/nodes/:id/configs | config_err_test limit/404 例、agentws_test:442 |
| 节点元数据自动采集（F-0742） | **已实现** | `agentws.go` syncNodeMeta（meta_init 置位后面板为准） | TestAgentWSHelloMeta |
| 节点入池检查（F-0744/F-1657，OS-4 流水线） | **已实现** | provision→probe→online 流水线 + `provision_note` | `pipeline_test.go` TestProvisionPipeline |
| 能力上报（F-0706）/ 注册审批（F-0741）/ 详情三态全展示（F-0750） | **待核实/未实现** | 审批流不存在（凭据即授权）；能力上报无独立消息 | 后续批次 |

### 3.3 Relay 与 Tunnel（F-9003 后半 / F-0801）

| 项 | 结论 | 源码证据 | 测试证据 |
|---|---|---|---|
| Relay 独立进程（-role） | **已实现** | `agent/internal/roles/roles.go:31` relay Standalone:true；cmd -role 分发 | `roles_test.go` TestRegistry |
| Relay 配置校验/热更新 | **已实现** | roles/relay + loadOverride（SIGHUP） | TestRelayValidation、TestRelayHotRepoint |
| TCP 转发 | **已实现** | roles/relay serveConn | TestRelayForwards、TestRelayRunFile |
| 每种传输独立测试（F-0821） | **已实现** | tunnel{ws-tls,ssh,quic} + quictunnel | wstls_test/ssh_test/quicsidecar_test/tunnel_test/quictunnel_test |
| TLS 证书校验（F-0825） | **已实现** | `link/link.go` tlsConfig | TestTLSConfig、TestRunTLSSetupFailure |
| SSH Host Key 校验（F-0826） | **已实现** | tunnel/ssh | TestSSHTunnelHostKeyVerify |
| UDP 转发（F-0812）/ 隧道断线重连（F-0813） | **待核实** | 本轮未核（UDP 转发与隧道重连的专用断言未取证） | 后续批次 |

### 3.4 Probe（F-9004 前半 / F-0901）

| 项 | 结论 | 源码证据 | 测试证据 |
|---|---|---|---|
| 任务/结果模型 | **已实现** | `models.go:181` ProbeReport（idx_probe_window `:184-195`）；任务规格=agent 本地 `config.go:74` Probes | `config_test.go` TestProbeSpecValidation |
| RTT / 丢包 / TCP connect 探测 | **已实现** | `probe/probe.go:131` net.DialTimeout("tcp")、lossPct、measure | probe_test：TestProbeHealthy/TestVerdictOf/TestNextInterval |
| 结果去重 | **已实现** | agentws repeatReport + dedupe（覆盖 MsgProbeReport） | TestReportDeduperWindow/TestReportDeduperTTL |
| 过期处理 | **已实现** | aggregate 窗口 + idx_probe_window | `aggregate_test.go` TestJudgeWindowAndLatest |
| 健康状态计算 / 时间窗口 | **已实现** | `aggregate.go:65` Judge；`monitor.go:66` applyVerdict；ProbeFresh 30min | TestJudgeRegionAndISP、`monitor_test.go` TestApplyVerdictTransitions |
| 服务端下发探测任务（F-0921） | **未实现** | 服务端无 probes 下发面；规格仅 agent 本地配置 | — |
| UDP 探测（F-0910）/ 应用层探测（F-0911）/ 滞回（F-0931） | **未实现/待核实** | probe.go 仅 TCP dial；其余未核 | 后续批次 |

### 3.5 Scheduler（F-9004 后半 / F-1021）

| 项 | 结论 | 源码证据 | 测试证据 |
|---|---|---|---|
| 按 Role 选 Entry/Landing（F-1004/1005） | **已实现** | `sub.go:74` role IN 过滤；`alloc.go:448-454` role/pool_state/status 过滤 | `alloc_test.go` TestPick* |
| 按 Direction 过滤（F-1006） | **已实现** | `alloc.go:486-489` direction 匹配 | TestDirectionalDefaults |
| 维护/禁用/健康/容量/成本过滤（F-1011~1014） | **已实现** | `alloc.go:448-454`（enabled/pool_state/status）、Healthy 门、CapacityMbps、costPer | TestPickHealthGate、TestPickLeastConnCapacityWeight、TestPickCostFirst |
| 策略：least_conn/cost/perf/balanced（F-1043/1045） | **已实现** | `alloc.go:217` Pick、`:256` lessFor | TestPickBalanced/TestPickCostFirst/TestPickPerfFirstInOnly |
| manual 模式（F-1041） | **已实现** | LandingAssignment.strategy=manual + `alloc.go:473` hasManualAssignment 优先 | `landing_test.go` TestLandingAssignment、TestSweepManualWins |
| 确定性评分/约束（F-1046） | **已实现** | `alloc.go:214-216` 规则注释 + lessFor 纯函数 | TestPick* 断言具体胜者 |
| sweep 自动分配与切换 | **已实现** | `alloc.go:497` | TestSweepAllocatesAndSwitches、TestSweepLoadSignal |
| 区域故障自动切换 | **部分** | `monitor.go:121-136` 落 region_fault 事件 + `pool.go:125` SickStrikes 摘池；**无**「整体故障→自动改绑 Path」执行器 | monitor_test（事件面）；执行面待后续 |
| priority/quality 模式（F-1042/1044）/ 解释与模拟（F-1034/1035） | **未实现** | 策略集仅 4 种；无 explain/simulate 接口 | — |

### 3.6 订阅生成（F-9005 / F-1101）

| 项 | 结论 | 源码证据 | 测试证据 |
|---|---|---|---|
| V2Ray 分享链接 | **已实现** | `sub/links.go:201` PackV2Ray | links_test：TestShareLink/TestPackV2Ray |
| Clash YAML | **已实现** | `sub/clash.go:72` PackClash | clash_test：TestPackClash（yaml.Unmarshal）等 5 例 |
| sing-box JSON | **已实现** | `sub/singbox.go:165` PackSingBox | singbox_test：TestPackSingBox 等 6 例 |
| 配置语法测试（F-1128） | **已实现** | 测试内 yaml/json 严格 Unmarshal 断言 | 上列全部 |
| 用户状态过滤 / 禁用节点过滤（F-1141/1145） | **已实现** | `sub.go:74`（enabled/role/pool_state/status）+ `sub/availability.go:11` UserActive | TestSubscriptionAvailability、TestSubscription |
| 令牌重置（F-1146） | **已实现** | `user.go:300` resetSubToken（`router.go:218`） | TestResetSubTokenErrors、e2e 重置腿 |
| 访问限频（F-1147） | **已实现** | `sub.go:33` subLimiter/subMissLimiter | TestSubscriptionRateLimit（21 连打锁定） |
| 过期/封禁/超限策略（F-1151/1152） | **已实现** | 404/空订阅 + userinfo 额度头 | TestSubscription、TestSubscriptionAvailability |
| 3 种格式 vs 6 种（gap G1） | **部分** | 仅 v2ray/clash/sing-box；无 hysteria/tuic/wireguard 直出 | — |
| ETag（F-1148）/ 真实客户端解析（F-1129） | **未实现** | 无 ETag 头；无真实客户端 fixture 测试 | — |

### 3.7 用户 / 设备 / 流量（F-9006 前半 / F-1201、F-1241）

| 项 | 结论 | 源码证据 | 测试证据 |
|---|---|---|---|
| User/SubDevice/TrafficLog/NodeTrafficLog 模型 | **已实现** | `models.go:6/53/116/244`（SubToken uniqueIndex :9、idx_subdev_key :55-57） | user_test、subdevice_test |
| 设备数量/IP 限制（F-1208/1225） | **已实现** | `handler/subdevice.go` | TestSubscriptionDeviceLimit/TestSubscriptionIPLimit/TestSubDeviceLimitValidation |
| 设备列表（F-1223） | **已实现** | `router.go` GET /api/panel/devices | TestUserDevicesErrors、TestPanelMeDeviceUsage |
| 流量上报链（F-1241） | **已实现** | `agentws.go:445` MsgTraffic → :269 saveNodeTraffic → :291 saveUserTraffic → :460 TrafficAck | TestAgentWSTrafficReport、TestAgentWSUserTraffic |
| 用户/Node 双维度统计（F-1246/1247） | **已实现** | traffic_logs（user 维度）+ node_traffic_logs（node 维度） | 同上 + status 读取面 |
| 周期配额重置（F-1249） | **已实现** | `quota.go:54` UsedBytes + ResetCycle（models.go:11）+ `review/` | TestSubResetCycleWindow、TestDisableInactiveRespectsResetCycle |
| 重复上报去重（F-1250/1254） | **已实现** | `agentws_dedupe.go` | TestAgentWSDuplicateTrafficFrame、TestAgentWSDedupSurvivesReconnect |
| 单端计量口径（F-1242）/ 断线补报（F-1251） | **待核实** | 本轮未取证（spool 覆盖 probe/relay，traffic 补报未核） | 后续批次 |

### 3.8 订单 / 支付（F-9006 后半 / F-1301、F-1304、F-9706）

| 项 | 结论 | 源码证据 | 测试证据 |
|---|---|---|---|
| 订单模型/状态机 | **已实现** | `models.go:327` PaymentOrder（order_no uniqueIndex `:330`，状态 pending/paid/failed/expired/refunded） | payrecon_test、panel_test TestPanelOrders |
| Provider 接口 | **已实现** | `packages/payment/provider.go:14-20`（Name/CreateOrder/Verify） | `payment` 包测试 |
| epusdt 真接入 + 签名验证 | **已实现** | `payments/epusdt/epusdt.go:139` Verify、`:168` sign | epusdt_test：TestSign/TestVerify/TestCreateOrder |
| 回调幂等（F-1306） | **已实现** | `models.go:413-414` idx_provider_external 唯一 + `paynotify.go:51` 事务 | TestEpusdtNotify |
| 订单幂等（F-1302） | **已实现** | order_no 唯一 + 落账事务 | payrecon_test TestReconcileGuards |
| 未接通渠道标记不可用（F-1309） | **已实现** | `packages/payment/disabled.go:11` NewDisabled（alipay/wechat 占位） | payment 包 TestRegistered/TestDisabled |
| 订单/支付操作审计（F-1310） | **已实现** | auditMiddleware 覆盖 refund/redeem（router.go:99 组） | audit_test |
| 对账（退款/差异） | **已实现**（查询面） | `payrecon.go`（退款 `:175` refundOrder） | TestReconcile、TestOrderRefundAndDetail |
| 支付对账**任务**（F-1308，定时）/ 支付补偿机制（F-1307） | **部分/待核实** | 对账为查询接口非定时任务 | 后续批次 |
| 真实支付宝/微信接通 | **未实现** | NewDisabled 占位（有意，F-1309 已按口径标不可用） | — |

### 3.9 云服务商与 Provision（F-9007 / F-0301、F-1651）

| 项 | 结论 | 源码证据 | 测试证据 |
|---|---|---|---|
| Provider 模型 + 凭据加密（F-0321/0325） | **已实现** | `models.go:462` Provider；`providerapi.go` sealSecret（secret.Store AES-GCM） | providerapi_test TestProviderAPI；secret_test 6 例 |
| 不回显 Secret（F-0327）/ 凭据更新（F-0329） | **已实现** | providerapi HasAccessKey、access_key 非空换密 | TestProviderAPI、TestListEnrollmentTokensMasksSecret |
| 凭据变更审计（F-0333） | **已实现** | api 组 auditMiddleware 覆盖 PUT /api/providers | audit_test |
| OpenTofu 集成（F-0301 核心） | **已实现** | `provision/provision.go`（bin=tofu、HCL 渲染、TF_VAR 传密、共享 Manager 串行 `:55`） | provision_test：TestRenderHCL/TestApplyLifecycle/TestExecuteHCLIsolation/TestSharedManagerSerializes |
| 供给流水线 + cloud-init 建后注册（F-1656） | **已实现** | `launch.go:45` PrepareLaunch、`:84` FinishJob；`cloudinit.go`（与 agent-install.sh 同口径） | TestCloudInit/TestPanelWSURL、`pipeline_test.go` TestProvisionPipeline |
| 补机循环 | **已实现** | replenish sweep | TestReplenishSweep/TestReplenishLoop |
| 资源列表拉取（F-0343）/ 开关机重启（F-0307~0310）/ CloudResource、CloudOperation 模型 | **未实现** | 全仓 `type CloudResource/CloudOperation` 0 命中；provision 无云 API 调用（仅 tofu CLI） | — |
| 账单导入（BillImport，F-0521 等） | **未实现** | `type BillImport` 0 命中 | — |

### 3.10 成本核算（F-9008 / F-0501）

| 项 | 结论 | 源码证据 | 测试证据 |
|---|---|---|---|
| 成本字段（时间+流量双计价） | **已实现** | `models.go:90-93` MonthlyCostCents/TrafficPriceCents/BillingType | cost_test TestBuild |
| 成本聚合 | **已实现** | `cost/cost.go:96` Build；分组 byRegion/byISP + Node 列表（`:139-142`） | TestBuild、TestMonthElapsedFraction |
| 按 Provider 成本分组（F-0625） | **未实现** | 分组维度仅 Region/ISP/Node，无 Provider 桶 | — |
| 阈值告警 + 价格观察 | **已实现** | `cost.go` CheckAlerts + PriceWatch/PriceSnapshot（models.go:732/747） | TestCheckAlerts、TestWatchLoopScansUntilCancel、TestThresholdPersistence |
| 成本总览页（F-0621）/ 异常提醒展示（F-0631） | **已实现** | `dash/src/views/CostView.vue`（超阈值高亮 :322） | TestCostAPI、TestCostThresholdGuards + dash build |
| 账单导入/成本分摊 | **未实现** | 无账单表（见 3.9） | — |

### 3.11 Dash 与 Panel（F-9009）

| 项 | 结论 | 源码证据 | 测试证据 |
|---|---|---|---|
| Dash 17 视图（节点/线路/成本/供给/恢复/审计/设置/用户/卡密/活动/对账/DNS/分销…） | **已实现** | `dash/src/views/*`；`router.ts:36` beforeEach 角色守卫（无令牌→login、分销/管理员互斥） | `pnpm build` EXIT=0；后端 API 测试见各章 |
| Panel 5 视图（自助闭环） | **已实现** | `panel/src/views/*`（Home/Orders/Redeem/Notifications） | panel_test TestPanelMe/TestPanelRedeem/TestPanelOrders + build 绿 |
| F-9701 节点与线路管理 / F-9704 配置与任务 / F-9705 用户自助 | **已实现** | NodesView/LandingsView、ProvisionView/RecoveryView/SettingsView、Panel 自助 | node/landing/provision/recovery/panel 各 API 测试 + 双前端 build |
| 云资源管理页、账单管理页（F-9211/9212、F-9702/9703 后半） | **未实现** | 无 CloudResource 模型即无页面（见 3.9） | — |

### 3.12 任务系统（ch14 / F-1601）

| 项 | 结论 | 源码证据 | 测试证据 |
|---|---|---|---|
| ProvisionJob 供给任务 | **已实现** | `models.go:496`（running/ok/failed） | provision_test TestApplyLifecycle |
| Recovery 恢复流水线（触发/动作集/次数/轨迹） | **已实现** | `models.go:510/527`（Level 1-3、RecoveryAction 轨迹） | recovery_test 10 例：TestSweepOpenAndAdvance/TestInstanceActionL3/TestTimeoutExhaustFails/TestTraceRowsReplay |
| TouchJob / Event outbox | **已实现** | `models.go:287/652/670` | toucher/herald 测试 |
| 节点升级/进程重启/配置重应用任务（F-1623~1625） | **已实现** | `router.go:183` upgrade、procop、config 推送 | TestNodeUpgrade、TestNodeProcOp、configd/relaypush 测试 |
| **统一**任务模型/队列/进度/取消（F-1602~1612） | **未实现** | 四套独立任务表，无统一状态机 | — |
| 防循环重启/冷却（F-1644/1646） | **待核实** | 本轮未取证 | 后续批次 |

### 3.13 安全与权限（ch15 / F-1701、F-1702、F-1721）

| 项 | 结论 | 源码证据 | 测试证据 |
|---|---|---|---|
| 双凭据鉴权（JWT 管理员 + API Token） | **已实现** | `apitoken.go:92` apiAuth（`router.go:99`） | apitoken_test 3 例 |
| 角色越权（super/operator） | **已实现** | `router.go:207-210` requireSuper | `adminrole_test.go` TestAdminRoleCRUD（operator 403） |
| 2FA + 恢复码 | **已实现** | `twofa.go:49/60/88/145` + `router.go:341-344`；`models.go:695` RecoveryCode | twofa_test TestTwoFABindLoginRecovery |
| 登录审计 + 爆破告警 | **已实现** | `loginlog.go:30/43/62` + `models.go:705` LoginLog | TestLoginLogsQuery、TestLoginAlerts |
| 敏感操作审计 + 保留轮转 | **已实现** | `router.go:99/267` auditMiddleware；`audit.go:20/30` Prune/Loop | audit_test 6 例 + `audit_test.go` TestPruneRemovesOnlyOutOfWindow |
| 订阅令牌爆破测试（F-1736） | **已实现** | sub 限频枚举锁定 | TestSubscriptionRateLimit |
| secret_store（F-1721） | **已实现** | `secret/` AES-GCM、keyinit 生产门禁 | secret_test 6 例、keyinit_test 8 例 |
| 不回显 Secret（F-1728）/ 凭据吊销验证（F-1724）/ 轮换验证（F-1726） | **已实现** | 见 3.9/3.2 | TestListEnrollmentTokensMasksSecret、TestNodeTokenRevocation、TestNodeTokenRotation |
| 敏感凭据不进日志（F-1727） | **已实现** | `sub.go:34` token 前 6 位脱敏；audit body 脱敏 | TestAuditLogsRedactSensitiveBody |
| 消息伪造/重放（F-1734） | **已实现** | bad token 拒绝 + envelope ID 去重 | TestAgentWSBadToken、dedupe 7 例 |
| 依赖安全扫描（F-1738） | **已实现** | `.github/dependabot.yml`（security alerts 独立于版本更新） | CI 绿（test.yml） |
| 细粒度资源权限（F-1713）/ 高危操作二次确认（F-1715）/ CRL 分发 | **未实现/待核实** | 无 per-resource ACL；轮换吊销有表无 CRL 分发面 | 后续批次 |

### 3.14 可观测与告警（ch16 / F-1801 系列）

| 项 | 结论 | 源码证据 | 测试证据 |
|---|---|---|---|
| 健康端点（DB 可写/agent 在线/备份新鲜度） | **已实现** | `router.go:90/357` health | `health_test.go` TestHealth |
| 系统状态 | **已实现** | `router.go:334` + `status.go:160` | status_test 6 例 |
| 告警（proc_crash/cert_expiry/high_load/config_error） | **已实现** | `models.go:229` Alert；`handler/alerts.go` recordAlert/resolve | alerts_test 4 例 TestAgentWSAlarm*、TestAlertGuards |
| 区域/运营商故障事件 + Herald 分发 | **已实现** | `monitor.go:121` Emit；`herald/herald.go:33-40` kinds；`deliver.go:12-27` 指数退避 1m→1h、MaxAttempts=6 | monitor_test、herald 测试（TestDeliverRetryBackoffAndDeadLetter 等） |
| 备份失败告警（F-1832） | **已实现** | `backup.go:253/260` emitBackupFailed/emitUploadFailed | TestBackupFailedEmit、TestRunS3UploadFailureAlerts |
| 告警去重与恢复（F-1833/1834） | **已实现** | node+kind+proc 去重 + resolveAlert（Alert.ResolvedAt） | TestAlertGuards、TestAgentWSAlarmProcRecover |
| Prometheus /metrics、pprof、请求延迟指标（F-1801~1814） | **未实现** | 全仓 `prometheus|/metrics|pprof` 0 命中 | — |
| Agent 离线告警（F-1821）/ 在线数+心跳延迟采集（F-1803） | **部分/待核实** | 在线数有（health/status）；离线告警与延迟指标未核 | 后续批次 |

### 3.15 数据库与备份（ch17 / F-1901 系列）

| 项 | 结论 | 源码证据 | 测试证据 |
|---|---|---|---|
| Model/索引/关联审计（F-1901） | **已实现**（本轮完成） | 46 个 type（`models.go` 全表，见 §2）；36+ 索引标签 | storage 包 + 全 handler 建表测试 |
| 事务边界（F-1903） | **已实现**（本轮完成检查） | 11 处 `Transaction(`；资金路径全包：paynotify.go:51、card.go:95、userio.go:212、panel.go:346 | paynotify/card/userio/panel 测试 |
| N+1 扫描（F-1902） | **已发现 12 处候选**（本轮完成检查，未修） | 实锤：`panel.go:173` 逐单查 grants、`poolapi.go:38` 逐节点查 probe、`agentws.go:248` 逐条查 node、`procstatus.go:33`；payrecon.go:65/75 已用 IN 批量 | 登记为 P2 优化项（本报告 §6） |
| SQLite 并发写（F-1908） | **已实现** | `storage.go:141` WAL + busy_timeout(5000) + 外键 | storage 测试 |
| PG 方言兼容（F-1909） | **已实现** | `make test-pg` | **ok 4.966s（实跑）** |
| 结构变更 Migration（F-0040） | **部分** | 仅 GORM AutoMigrate（无版本化迁移框架）；`deploy/MIGRATION-RUNBOOK.md` 有操作手册 | AutoMigrate 随全部测试跑通 |
| 周期备份/恢复（F-1925/1927） | **已实现** | `backup.go:108/153/227` Snapshot/Loop/Prune；`deploy/restore.sh` | backup_test 8 例、e2e 备份恢复腿（53 断言内） |
| 审计日志增长（F-1907） | **已实现** | `audit.go:20/30` 保留天数轮转 | TestPruneRemovesOnlyOutOfWindow |
| 探测历史/流量记录增长（F-1905/1906） | **未实现**（无保留策略） | probe_reports/traffic_logs/node_traffic_logs **无** prune/retention（全仓仅 audit+backup 有） | 登记为 P2 缺口（§6） |
| 账单/成本数据增长（F-1904） | **已实现**（本轮完成检查） | 无账单表；成本实时聚合，PriceSnapshot 按观察周期增长（唯一键去重） | cost_test |

### 3.16 部署与测试（F-9010 / F-1921）

| 项 | 结论 | 源码证据 | 测试证据 |
|---|---|---|---|
| Compose/部署脚本审计（F-1921） | **已实现**（本轮完成） | `deploy/`：compose、Caddy（/api /sub /agent /feed.xml /admin/* /distributor/*）、start.sh、agent-install.sh | e2e 53 断言 + compose-smoke/systemd-smoke/start-args-test/install-rollback-test 均在 CI |
| 完整部署冒烟（F-1930） | **已实现** | `deploy/e2e-smoke.sh`（真二进制真 WS 全链路） | **实跑 53 ok，EXIT=0** |
| Agent 安装/升级流程（F-1923） | **已实现** | agent-install.sh（原子替换/失败回滚/sha256 校验，`cloudinit.go:63` 同口径）+ upgrade 包看门狗回滚 | install-rollback-test、upgrade 测试 |
| CI 门禁（6 Go 模块 + 2 前端 + deploy-scripts） | **已实现** | `.github/workflows/test.yml`（go/frontend/deploy-scripts 三 job） | 本报告 §1 全绿 |
| 架构分层 Handler→Service→Repository（F-0023 检查结论） | **不符**（检查完成） | 实际：Handler 直连 gorm（`h.db.`），**无** service/repository 层 | 属设计事实，改造建议见 §6 |
| Server 重启后 Agent 重连（F-1931）/ 数据面独立续跑（F-1932） | **待核实** | 有通用重连测试（link_test）但无「重启 server」专用断言 | 后续批次 |
| 性能/故障测试（F-2061~2076） | **未执行** | 本轮不含压测 | 后续批次 |

## 4. 文档与源码不一致清单（F-9012，7 条）

| # | 文档 | 声称 | 实际（当前 HEAD） | 级别 |
|---|---|---|---|---|
| D-1 | `docs/gap-analysis-2026-11-02.md` §1 表（8 组 `router.go:` 行号） | provision/enroll/cert=`:53-73`；pool/landings/alloc=`:28-49,101-113`；probe/alerts=`:31-37,100`；logs/audit=`:99,143,166-167,180-182`；upgrade=`:102`；batch/token=`:103-104,143-145`；admins/2fa=`:114-117,170-191`；payments/bot/feed=`:8,151-156,203` | 全部不符：实际 provision `:130-155`、enroll `:307`、cert `:147-151`、pool `:121`、alloc `:157`、probe `:114`、logs `:201`、audit `:254`、upgrade `:183`、batch `:184-185`、admins `:207-210`、2fa `:341-344`、payments `:240-243`、bot `:301`、feed `:93`。历史核查：这些行号在任何提交都未出现过（provision 最早在 `:88`，从未 `:53-73`）→ **引用错误**而非漂移 | P2 |
| D-2 | `docs/审计-文档一致性.md` C-06 行（:54） | 「维持原判·未落地……全仓 rotate/CRL 零命中」，证据注 `router.go:199` | **已被 f61b1d9 推翻**：`node.go:183` rotateNodeToken、`node.go:208` revokeNodeToken、`models.go:386` NodeTokenRevocation、`router.go:110-111`；sub-token 现 `:218` 非 `:199`。同文档 N-58 行已改「已实现」→ **文档内部自相矛盾** | P1 |
| D-3 | 同上，五刷行号重钉清单（2026-10-11 后未再钉） | sub-token `:199`、refund `:224`、2FA `:319-322`、health `:335-372`、apiAuth `:86`、alloc/policy `:138`、PaymentOrder `:322`、RecoveryCode/LoginLog `:666-683`、main.go WatchLoop `:191`、panel 兑换 `:86-122` | 实际 `:218`、`:243`、`:341-344`、`:357-394`、`:99`、`:157`、`:327`、`:695-716`、`:227`、`:105+`（10-15 之后十余批增行） | P3 |
| D-4 | `docs/gap-analysis-2026-11-02.md` §2.1 G2 | 「ferry traffic_logs 逐条记账无在线态（grep 全仓 online 仅命中 standby/pool 语境）」 | d48a7f8（2026-11-03，晚于文档一天）已落地 `User.LastNodeID/LastSeenAt`（models.go:44-45）+ `agentws.go:347-354` + dash 在线标签（UsersView）→ 文档过时 | P3 |
| D-5 | 同文档 G1 | 「`server/internal/sub/` 仅 clash.go/singbox.go/links.go」 | 另有 availability.go、rotate.go（+各测试）→ 「仅」字失准；3 格式结论本身正确 | P3 |
| D-6 | `deploy/README.md:24` | dash 反代「/api、/sub、/agent、/feed.xml」 | Caddyfile 另有 `/admin/*`、`/distributor/*`（README:39-40 有部分补述，:24 枚举不全） | P3 |
| D-7 | `README.zh.md:80` | `make test` = Go 测试 + 前端构建检查 | 属实；但 handler 包 600s 默认超时在满载下假红（本次实测），文档/Makefile 未提示 `-timeout` | P3 |

## 5. 模块依赖图（F-9011）

```mermaid
graph TB
    subgraph Go 模块
        SERVER["server<br/>cmd/ferry 单二进制"]
        AGENT["agent<br/>单二进制 -role 可拆"]
        AP["packages/agentproto<br/>协议信封"]
        CR["packages/costref<br/>成本参考"]
        PAY["packages/payment<br/>Provider 接口"]
        EP["payments/epusdt+alipay+wechat"]
    end

    subgraph server/internal
        H["handler (192 路由, 410 测试)<br/>直连 gorm, 无 service 层"]
        ST["storage (46 model, AutoMigrate, SQLite/PG)"]
        AL["alloc 调度"] PO["pool 池态机"] SB["sub 订阅"]
        PV["provision (OpenTofu)"] CO["cost + costref"]
        MO["monitor + aggregate"] AG2["agenthub (WS 请求应答)"]
        RP["relaypush 配置重推"] RC["recovery 恢复流水线"]
        BK["backup 快照/轮转"] AU["audit + secret + keyinit"]
        HD["herald outbox + notify"]
    end

    subgraph 前端
        DASH["dash (17 视图, 角色守卫)"]
        PANEL["panel (5 视图)"]
    end

    subgraph 部署
        CG["deploy: Caddy + compose + start/agent-install/restore + e2e-smoke"]
    end

    SERVER --> H
    H --> ST
    H --> AL & PO & SB & PV & CO & MO & AG2 & RP & RC & BK & AU & HD
    H --> AP
    SERVER --> PAY & CR
    EP --> PAY
    DASH -->|"HTTP /api /admin /distributor"| H
    PANEL -->|"HTTP /api/panel /sub"| H
    CG --> SERVER & DASH & PANEL
    AGENT --> AP
    AGENT -.->|"WS /agent/ws"| AG2
    AG2 -.-> AP
```

依赖要点：server→4 个兄弟模块（replace 本地路径）；agent 只依赖 agentproto；前端仅经
Caddy 反代打 HTTP（无其他耦合）；deploy 层编排三者。无环。

## 6. 分阶段改造建议（F-9013，按本审计缺口排序）

**P2 近期（低风险高收益）**
1. **文档行号治理（D-1/D-2/D-3）**：gap-analysis §1 行号整表按 HEAD 重钉或改为符号锚点
   （`router.go` 函数名而非行号）；`审计-文档一致性.md` C-06 行随 N-58 同步改「已实现」并
   重钉 10 处行号。建议 CI 加「文档 `file:line` 抽查」脚本防再漂。
2. **数据保留策略（F-1905/1906）**：为 probe_reports、traffic_logs、node_traffic_logs 增加
   与 audit.Prune 同口径的保留轮转（现状无限增长）。
3. **N+1 收敛（F-1902，12 处）**：先修实锤 4 处——panel.go:173（grants 批量 IN）、
   poolapi.go:38（probe 窗口批量取最新）、agentws.go:248（node 批量）、procstatus.go:33。
4. **handler 包测试超时**：Makefile/test.yml 对 `internal/handler` 显式 `-timeout=40m`，
   消除满载假红。

**P3 中期（对应平台 P1/P2 阶段任务）**
5. **核心模型收敛（F-9101~9108）**：Path/PathGroup/Policy/Capability 四类模型或「现有模型
   映射」文档化——现状等价物分散在 LandingAssignment（models.go:165）、PolicySetting
   （alloc.go:107）、role/labels（models.go:80-83），建议先出映射表再决定建表，避免重复实现。
6. **云资源直连（F-9201~9224）**：新增 CloudResource/CloudOperation 模型 + provider API
   适配层（现仅 tofu CLI 路径）；开关机/资源同步/账单导入为整块新工程，建议单列 P2 批次。
7. **可观测指标（F-1801~1814）**：补 `/metrics`（Prometheus 文本格式）与 pprof 挂载；
   请求延迟/流量计数器接入现有 health 检查口径。

**P4 结构性（需拍板，勿在审计期动手）**
8. **分层（F-0023 结论）**：Handler 直连 gorm 是现状事实；若 P1 起要做 service/repository
   层，建议先在 1-2 个高频域（sub、alloc）试点，不做全量重写（0.1 规则：不重复实现）。
9. **统一任务系统（F-1602~1612）**：四套任务表（ProvisionJob/Recovery/TouchJob/Event）
   先加统一视图/查询 API，再评估合并状态机。
10. **真实客户端解析测试（F-1129/2046/2047）**：引入 sing-box/Clash 二进制做 fixture
    解析回归，替代仅 yaml/json Unmarshal 断言。

## 7. 0.2 先决执行记录（F-0020~F-0039）

F-0020 读 README/TODO/设计/部署文档 ✓；F-0021 目录索引=§2 ✓；F-0022 模型/索引检查=§3.15 ✓；
F-0023 调用链=§3.16（无 service 层）✓；F-0024~F-0034 各域检查=§3.1~3.16 ✓；
F-0035 审计表=§3（16 节）✓；F-0036 证据=§1+各表测试列 ✓；F-0037 差异清单=§4（7 条）✓；
F-0038 依赖图+建议=§5/§6 ✓；F-0039 审计期零重构 ✓（git 无源码改动）。

## 8. 本批次未覆盖（标注：审计中，后续批次）

以下章节本轮**未核完**，不作结论，留待后续批次：
- ch5 能力上报/注册审批/详情三态全展示（F-0706/0741/0750）；
- ch6 UDP 转发、隧道断线重连（F-0812/0813）；
- ch7 UDP/应用层探测、滞回（F-0910/0911/0931）；
- ch8 priority/quality 模式、调度解释与模拟（F-1042/1044/1034/1035）；
- ch10 单端计量口径、断线补报（F-1242/1251）；
- ch11 定时对账任务、补偿机制（F-1307/1308）；
- ch14 防循环/冷却（F-1644/1646）；
- ch15 细粒度资源权限、高危二次确认、CRL 分发（F-1713/1715 等）；
- ch16 离线告警、心跳延迟采集（F-1821/1803 后半）；
- ch17 Server 重启重连、数据面独立续跑（F-1931/1932）；
- ch18 性能/故障注入测试（F-2061~2076）；
- 0.3 工程约束逐条核验（F-0040~F-0047，其中 F-0040 见 §3.15、其余未逐条展开）。

## 9. todo.md 回写统计（实测）

- 勾选 **216 条**，全部附 §21 模板格式单行备注（Task ID / Status / Evidence / Verification / Commit: 无），无重复勾选；
- 构成：0.2 先决 F-0020~F-0039（20）+ P0 F-9001~F-9013（13）+ 各章审计/检查任务（40，含
  F-0201/0221/0241/0281/0301/0501/0701/0721/0801/0901/1021/1101/1121/1123/1125/1201/1221/1241/
  1301/1304/1321/1322/1323/1341/1601/1651/1701/1702/1721/1901~1909/1921/9706）+
  源码+测试双证据的实现项（143，覆盖 ch1~ch18 各章；Evidence 形态：68 条带
  `file:line + TestName`，其余为函数名+测试名的符号级证据，均可实地核验）；
- 未勾选余量 762 条（含 §8 标注的后续批次章节与未达双证据门槛的实现项）；
- 工作树：仅 `todo.md` 修改 + 本报告新增；`payments/go.mod` 他人 WIP 未动；未 commit。
  （观察：`agent/go.mod`、`server/go.mod` 于 17:06/17:16 出现依赖版本上浮改动，
  diff 为 `go get -u` 特征且非本审计所为，疑为并发作业写入，未触碰、未回滚；
  对应 `agent/go.sum`、`server/go.sum` 同秒联动改动，一并登记。）
