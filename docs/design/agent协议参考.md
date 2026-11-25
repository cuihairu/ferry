# Agent 协议参考（agentproto 消息全表）

本文是 `packages/agentproto` 全部消息类型（F-0721 审计后的契约面，F-0722 文档化）的
独立参考：方向、载荷结构、语义、确认与幂等规则。逐字段定义以 Go 源码注释为准
（`messages.go`）；本文与其同步维护，消息表与 `TestMessageTypesStable` 互为锚点。

## 1. 传输与信封

- **传输**：WebSocket，端点 `/agent/ws`（面板侧入站单帧上限 8 MiB，超限断开）。
- **编码**：JSON。每帧一个 `Envelope`：`V`（协议版本，必须等于 `ProtocolVersion=1`）、
  `ID`（请求方生成，应答方原样回显，用于配对与幂等判重）、`Type`（消息类型）、`Payload`
  （按类型解码的载荷）。
- **版本守卫**：`Envelope.Valid()` 拒绝未知 `V`；两端 readLoop 对坏帧（版本不符/解码失败）
  一律**丢弃不致命**，连接继续。
- **未知消息类型**：面板侧分发不命中且无等待中的应答配对时记日志丢弃（`readAgentLoop`
  default 分支）；agent 侧同样丢弃，向前兼容面板先升级的场景。
- **请求/应答超时**：面板经 `agenthub.Hub.Request` 等待应答，超时返回 `ErrTimeout`
  （如升级 ack 窗口 15s）；节点不在线返回 `ErrOffline`。

## 2. 消息全表

| Type | 方向 | 载荷结构 | 语义 | 应答 / 幂等 |
| --- | --- | --- | --- | --- |
| `agent.hello` | agent→panel | `Hello` | 连接后首帧：token 认证 + 注册元数据 `Meta` + 静态能力集 `Caps`（首注册记录，面板存量优先） | `panel.hello_ack`；认证失败直接断开 |
| `panel.hello_ack` | panel→agent | `HelloAck` | 握手应答：心跳周期校准 + 面板权威元数据（回同步 agent） | — |
| `agent.heartbeat` | agent→panel | `Heartbeat` | 周期心跳：存活 + 负载快照 + 进程快照 + 动态能力态 `Caps` | `panel.heartbeat_ack`（带 `NextIntervalSec`）；快照覆盖写，天然幂等，不参加判重 |
| `panel.heartbeat_ack` | panel→agent | `HeartbeatAck` | 心跳应答，可携带下一心跳周期 | — |
| `agent.alarm` | agent→panel | `Alarm` | 异常告警（进程崩溃、探测失败等） | `panel.alarm_ack`；FERRY-004 判重后落 Alert |
| `panel.alarm_ack` | panel→agent | （空） | 告警接收确认 | — |
| `agent.traffic` | agent→panel | `TrafficReport` | 周期流量增量上报（节点/用户两级，计费口径） | `panel.traffic_ack`（带 `Recorded`）；FERRY-004 判重先于记账（双算=多扣费） |
| `panel.traffic_ack` | panel→agent | `TrafficAck` | 流量上报确认 | — |
| `agent.proc_report` | agent→panel | `ProcReport` | 进程状态事件上报（单向无 ack）；running 事件触发崩溃告警自愈与装机探测判定 | 无 ack；FERRY-004 判重只保副作用 |
| `panel.proc_ctl` | panel→agent | `ProcCtl` | 进程控制命令（start/stop/restart） | `agent.proc_ctl_ack` |
| `agent.proc_ctl_ack` | agent→panel | `ProcCtlAck` | 控制结果（OK/Error） | — |
| `panel.proc_logs` | panel→agent | `ProcLogsReq` | 进程运行日志抓取（内存环形缓冲，重启清空） | `agent.proc_logs_ack` |
| `agent.proc_logs_ack` | agent→panel | `ProcLogsAck` | 日志内容或错误 | — |
| `panel.config_push` | panel→agent | `ConfigPush` | 完整配置下发；应用失败由 agent 自动回滚旧配置 | `agent.config_ack` |
| `agent.config_ack` | agent→panel | `ConfigAck` | 应用结果；`Reverted=true` 表示已回滚 | — |
| `agent.probe_report` | agent→panel | `ProbeReportBatch` | 边缘探测判定批量上报 | `panel.probe_ack`（带 `Recorded`）；FERRY-004 判重后逐条落行 |
| `panel.probe_ack` | panel→agent | `ProbeAck` | 探测上报确认 | — |
| `agent.calibrate` | agent→panel | `Calibrate` | 注册后的轻量测速结果（E-8） | `panel.calibrate_ack`；判定是否采纳为有效容量 |
| `panel.calibrate_ack` | panel→agent | `CalibrateAck` | 校准应答 | — |
| `panel.upgrade` | panel→agent | `Upgrade` | 自升级命令（A-23）：目标版本 + 下载 URL + 可选 sha256 | `agent.upgrade_ack`（同步窗口 15s，拒绝带原因） |
| `agent.upgrade_ack` | agent→panel | `UpgradeAck` | 立即接受/拒绝；真正结果见 `agent.upgrade_status` 与重连后 hello.Version | — |
| `agent.upgrade_status` | agent→panel | `UpgradeStatus` | 升级进度单向报告（F-0712）：`downloading→applying→applied`，失败 `failed` 带原因；信封 ID = 命令 ID + 态 | 无 ack；FERRY-004 判重落 `node_upgrades`，未知状态丢弃 |

## 3. 幂等与断线处理规则（F-0727 统一口径）

**幂等（摘要）**：agent→panel 报告类（`traffic`/`probe_report`/`alarm`/`calibrate`/
`proc_report`/`upgrade_status`）按 `(node_id, 消息类型, envelope ID)` 保证
at-most-once 副作用；判重窗口每节点有界 FIFO（512 个 ID / TTL 10 分钟，跨重连存活）。
完整语义、判据表（C1~C7）与"复用原 envelope ID 重传"纪律见《agent架构设计.md》§8
（FERRY-004）。

**断线期间按消息类别的处理规则**（发送方=agent 侧，接收方=面板侧）：

| 消息类别 | 断线时发送方行为 | 保留/重试 | 断线时面板侧行为 |
| --- | --- | --- | --- |
| 核心进程内报告（`heartbeat`/`traffic`/`alarm`/`proc_report`/`calibrate`/`upgrade_status`） | `sendIfConnected` 失败即**静默丢弃**（at-most-once 发送，无队列） | 不保留；下一周期以新帧重报（heartbeat/calibrate 是快照，traffic 是累计增量口径的下一段，不丢账） | 节点标记 offline（`setNodeStatus`）；无入站帧 |
| 角色进程报告（probe 等，`-role` 独立进程） | 写 `<spool_dir>/<role>.out`（原子 O_APPEND 逐行 JSON 信封） | **保留**：核心 `spool.Watch` 每 2s 扫描转发；send 失败即停、消费偏移不前移，**恢复后整段重放**（角色崩溃只丢自己的文件） | 收到重放帧按正常报告处理，FERRY-004 判重兜底 |
| 面板→agent 命令（`proc_ctl`/`proc_logs`/`config_push`/`upgrade`） | — | — | `Hub.Request` 直接返回 `ErrOffline`（HTTP 502），不排队、不上门重试；由操作方重发 |
| 连接断开本身 | `link.Run` 重连循环持续重试直至取消 | — | `Unregister` 清理连接；判重窗口跨重连保留 |

**重连对账**：重连后以 `agent.hello` 重新登记——认证 → `syncNodeMeta` 元数据对账
（`meta_init` 未设时采纳 agent 首报值，否则面板权威值经 `hello_ack` 回灌）→ 面板覆盖
的心跳周期经 `hello_ack.NextIntervalSec`（及 `heartbeat_ack`）校准；静态能力集首注册
已记录，存量面板值优先（F-0228），动态能力态随首个 heartbeat 重新合并（F-0229）。

**面板命令超时**（节点在线但不应答）：`Hub.Request` 超时返回 `ErrTimeout`（升级 ack
窗口 15s，`upgradeAckTimeout`）；挂起的应答配对随连接注册表清理，不会跨连接误配。

## 4. 兼容规则

- 破坏性变更须 bump `ProtocolVersion` 并更新 `TestMessageTypesStable` 类型表。
- 新增消息类型：旧面板按未知类型丢弃（不致命）；旧 agent 面对新类型同理——两端
  可独立升级。
- 载荷新增字段一律 `omitempty` 可选，旧端解码忽略未知字段（JSON 语义）。
