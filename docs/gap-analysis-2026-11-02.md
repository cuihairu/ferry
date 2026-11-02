# 功能差距分析（2026-11-02）

> 对照头部同类产品盘点 ferry 已实现 vs 缺失，给出优先级建议。只作决策参考，不直接构成开工依据——凡涉及已拍板关闭/外部阻塞的方向，本文只登记触发条件，不代开闸门。

## 0. 对照对象选型（含换选理由）

任务原定对照 Portainer / Dokploy / Coolify / Komodo。**核实后不对上，已按最近似原则换选**：

- Portainer / Dokploy / Coolify / Komodo 均属**容器部署 / 自托管 PaaS 面板**：管理对象是 Docker/K8s 上的应用与栈——Git 仓库拉取构建镜像、compose 编排、镜像回滚、per-PR 预览部署（Portainer：GitOps + RBAC + registry 管理 + fleet 治理，portainer.io/features；Dokploy：Swarm 集群 + registry 回滚 + 健康检查自动回退，docs.dokploy.com/docs/core/applications/rollbacks；Coolify：Git 多平台源 + 模板目录 + 应用级回滚，coolify.io/compare/coolify-vs-dokploy；Komodo：Core/Periphery 双组件管理服务器构建部署容器栈，komo.do/docs/intro）。
- ferry 是**代理集群管理面板**：管理对象是 Xray/sing-box/hysteria2 代理节点、用户订阅与流量记账（README.zh.md:17）。两者的「部署/编排/回滚」指向完全不同的对象——把容器面板的功能矩阵套在 ferry 上，几乎每一行都是「无此需求」而非「差距」，对照结果不可执行。
- **换选为 3x-ui / Marzban / Hiddify / Remnawave**（代理面板头部成熟产品），理由：① 产品类目相同、维度可直接对齐；② 本仓已有同集合的取材底座——`docs/面板横向对比.md`（Remnawave 为 2026-10-08 仓快照文件级取材，其余三列取自 todo 各条引证），本报告在该底座上补维度、不重复取材。
- 容器面板组仍保留为附录 A：说明各自强项，供「若 ferry 未来想做部署类功能」时判断是否值得借形。

## 1. ferry 已实现盘点（按任务维度，附证据）

> 口径：只列有仓内证据的能力；`router.go` 行号指 `server/internal/handler/router.go` 路由注册，todo 行号指 `todo.md` 完成项族。

| 维度 | ferry 现状 | 证据 |
| --- | --- | --- |
| 部署（节点供给） | OpenTofu 供给模板 apply/plan + 恢复流水线；一次性 enrollment token 注册（原子消费/双花 409/IP 限流）；agent 一键安装脚本（原子替换/失败回滚/journal 诊断）；面板自身 compose 一键启动 + Web 证书上传/自签；ACME 证书任务（dns-01/http、四类 DNS 商） | `/api/provision-templates*`、`/api/enroll`、`/api/cert-tasks*`（router.go:53-73）；todo OS-1..4/BR-1..5 行、FERRY-001/003/005 行；`deploy/agent-install.sh`、`deploy/start.sh` |
| 编排 | 节点三角色（entry/landing/both）+ 池态机（active/suspend/standby）+ 落地编队 landings/pool + 分配策略 alloc + 智能 DNS/geodns + 区域传输判定 + 入口域名 entry_domains + 规则库 rulelib 下发 | `/api/pool*`、`/api/landings`、`/api/alloc`、`/api/dns-*`、`/api/entry-domains*`、`/api/routing/*`（router.go:28-49,101-113）；todo E-1..E-33 行 |
| 监控 | agent 探测回报（tunnel/exit/peer）+ 维度/传输状态灯 + 负载快照（容量口径校准）+ 九类告警（outbox+Herald）+ 系统状态 /admin/status + 崩溃自拉监管（e2e stub 实证） | `/api/dimension-status`、`/api/transport-status`、`/api/probe-reports`、`/api/load`、`/api/alerts*`（router.go:31-37,100）；todo HERALD-1..5 行、FERRY-004 e2e 腿 |
| 日志 | 节点被管进程日志拉取（proclog，5s 超时 504）+ 操作审计表（AU-1..3，保留轮转）+ 登录审计（五连败锁定）+ 管理端日志查询/清理 + 事件 outbox 投递回执看板 | `/api/nodes/:id/logs`、`/api/audit-logs`、`/admin/logs`、`/admin/login-logs`、`/api/events/*`（router.go:99,143,166-167,180-182）；todo AU-1..3 行 |
| 回滚 | agent 侧：配置下发失败回滚（configd）、自升级回滚（upgrade）、安装脚本 `.prev` 字节级还原；面板侧：周期备份 + 手动备份 + restore.sh 解密恢复 + standby 冷备快照 | `/api/nodes/:id/upgrade`（router.go:102）；todo BK-1..3 行、FERRY-005 行（57 断言沙箱回归）、FERRY-004 备份恢复腿 |
| 多机 | agent 常驻每节点（只出站连面板）+ 批量进程/配置操作 + standby 主从同步 + 多节点负载均衡调度 + 节点令牌轮换/吊销 CRL | `/api/nodes/batch/*`、`/open/standby/snapshot`、`/api/nodes/:id/token*`（router.go:103-104,143-145）；todo P2-1 行、C-06 行（f61b1d9） |
| 权限 | 多级管理员（super 管理账号 + 配额 P2-2）+ 2FA/恢复码 + API Token 签发验签 + 分销商独立登录面 + 操作审计 + 登录限速 + 节点注册凭据独立面 | `/api/admins*`（requireSuper）、`/admin/2fa*`、`/api/token`、`/distributor/*`（router.go:114-117,170-191）；todo P1-12/13、P2-2 行 |
| 生态集成 | 支付（epusdt USDT 真接入 + 卡密三账 + 优惠码/活动 + 对账退款）、TG bot 只读对接面、RSS 公告 feed、Herald 告警分发、servify 客服嵌入、GeoIP 分流/成本参考库、DNS 商（cloudflare 等） | `/api/payments/*`、`/bot/summary`、`/feed.xml`、`/panel/support`（router.go:8,151-156,203）；todo PAY-1..11、TOUCH-1..7、SAVE-1..8、DS/SV 行 |

## 2. 差距清单（vs 3x-ui / Marzban / Hiddify / Remnawave）

> 每条标注：来源（对照品）｜ferry 缺口性质｜现状处置（已有裁决的注明，不擅自开闸）。

### 2.1 订阅面

| # | 差距 | 对照品做法 | 性质与处置 |
| --- | --- | --- | --- |
| G1 | 订阅响应格式 3 vs 6：ferry 出 v2ray/clash/singbox 三格式（`server/internal/sub/` 仅 clash.go/singbox.go/links.go）；Remnawave 另有 mihomo/stash/xray-json，且有响应规则（按 UA 路由）与多套模板、独立订阅落地页 | Remnawave `RESPONSE_RULES_RESPONSE_TYPES` 六格式（面板横向对比.md:24-26） | mihomo 与 clash 语法高度同源，实际兼容面损失小。**建议 P2 观察项**：出现用户实际拿 clash-meta 拒载的反馈再补，不预做 |
| G2 | 用户级在线态与最近接入节点：Remnawave `UserTraffic` 单行实时含 `onlineAt/lastConnectedNodeUuid`；ferry `traffic_logs` 逐条记账无在线态（grep 全仓 `online` 仅命中 standby/pool 语境） | 面板横向对比.md:31 | 管理侧「谁在线」不可见。**建议 P1 候选**：agent 心跳已带流量载荷，加 per-user 在线标记成本低、运营价值高（排查盗号/共享） |
| G3 | 用户状态四态：Remnawave 超限（LIMITED）与到期（EXPIRED）分开表达，ferry 到期/超限统一封订阅 | 面板横向对比.md:34 | 展示语义差距，非功能缺口。**P2 观察项**：客服排障时若反复需要区分来源再拆 |

### 2.2 节点与配额

| # | 差距 | 对照品做法 | 性质与处置 |
| --- | --- | --- | --- |
| G4 | 节点级流量预算 + 告警百分比：Remnawave `Nodes.trafficLimitBytes/notifyPercent`；ferry 只有用户级配额 + 节点流量记账 | 面板横向对比.md:20,76 | **已有裁决**：已代决不做 per-node 预算告警（todo 解锁条件表 #10，触发=多机放量+预算需求出现）。维持，不重开 |
| G5 | MONTH_ROLLING 滚动重置档：Remnawave 有，ferry reset_cycle 无此档 | 面板横向对比.md:33 | **已有裁决**：拍板关闭（todo #11，自然月账单够用）。维持 |
| G6 | 消费倍率/机器账单：Remnawave `consumptionMultiplier` + `InfraBilling*` 机器账单与预算；ferry 有 costref 参考价对账但无自动机器账单采集 | 面板横向对比.md:16 | 机器账单靠手动导入参考表（costref）。真实商家 API 属外部阻塞（todo #8）。维持挂起 |

### 2.3 面板形态与权限

| # | 差距 | 对照品做法 | 性质与处置 |
| --- | --- | --- | --- |
| G7 | 无 OAuth/LDAP/OIDC 登录：ferry 仅用户名密码 + JWT + 2FA（grep oauth/ldap/oidc 全仓零命中）；Komodo/Portainer 及部分代理面板支持 OIDC | komo.do/docs/intro（OAuth GitHub/Google/generic OIDC）；portainer.io/features（SSO/LDAP） | 自托管小站场景管理员即本人，2FA 已覆盖安全面。**P2 观察项**：多管理员规模化（P2-2 配额面被真实使用）后再议 OIDC |
| G8 | 无 GitOps/配置即代码：ferry 配置经面板下发节点；容器面板组普遍 Git 仓库驱动 | 见 §0/附录 A | 类目差异而非差距：代理节点配置不适合走 Git PR 流。**不做，登记为类目边界** |
| G9 | 编队三层模型（配置档案/主机/编队批量分发）：Remnawave ConfigProfiles+Hosts+Squads；ferry 节点+池态+入口两层已在单机闭环 | 面板横向对比.md:67-77（仓内已判：过度设计，不抄） | **已有裁决**：反面清单维持。不重开 |
| G10 | mTLS CA 自动编排：安全设计.md 默认关闭 | 面板横向对比.md 无此项，安全设计.md:97-98 | **已有裁决**：维持手签脚本，触发=mTLS 消费方 ≥1（todo #3）。维持 |

### 2.4 生态与运营

| # | 差距 | 对照品做法 | 性质与处置 |
| --- | --- | --- | --- |
| G11 | 直连微信/支付宝：仅 epusdt USDT 真接入，微信/支付宝为占位 | 同类面板多接易支付/虎皮椒等聚合 | **外部阻塞**：缺商户资质（todo #104）。解锁钥匙在用户 |
| G12 | 3x-ui/Marzban 式 TG bot 管理命令（管理员侧增删用户）：ferry TG bot 仅只读用量对接面（TOUCH-6） | Marzban TG bot 管理（面板横向对比.md:64） | ferry 走 dash 全功能后台，bot 管理属冗余入口。**P2 观察项**：仅当出现「手机上无法应急处理」的真实诉求再做 |
| G13 | 官方发版未启动：Remnawave/Hiddify 等均有正式 Release 与安装源；ferry 正式 release 卡发版红线，`--version latest` 已就绪但 Releases 为空 | 各对照品官方安装文档 | **外部阻塞**：等用户明确发版指令+版本号（todo FERRY-002 行）。这是当前所有安装类差距里**唯一一步就能解锁**的 |

## 3. 优先级建议

结论口径：**ferry 的功能覆盖面在同类面板中已属第一梯队**（触达/告警/审计/支付/多级管理员/供给流水线等维度超过多数对照品，见 §1）；剩余差距集中在**运营可见性**与**发版交付**两类，而非功能堆叠。

| 优先级 | 事项 | 理由 | 性质 |
| --- | --- | --- | --- |
| P0 | G13 正式发版 | 安装脚本/校验链路/e2e 全部就绪，只差用户一句指令；不发版，所有外部用户的安装体验都停留在 nightly 产物 | 用户动作，非开发项 |
| P1 | G2 用户在线态与最近接入节点 | agent 链路已通，增量小；直接提升盗号/共享排查与客服排障效率；对照品已验证该面价值 | 新开发，建议排入 |
| P2（观察） | G1 mihomo/xray-json 订阅格式、G3 LIMITED/EXPIRED 拆态、G7 OIDC、G12 bot 管理命令 | 均为「有了更好、没有不痛」项；仓内小站定位下收益有限，各设触发条件挂观察 | 不预做 |
| 维持不重开 | G4/G5/G9/G10 及 mTLS/调参等 | 均有成文裁决（决策记录.md / 横向对比反面清单），单机小内存定位自洽 | 已拍板 |

## 附录 A：容器部署面板组一览（选型排除记录）

| 产品 | 类目 | 代表能力（对照 dimension 的「若借形」参考点） | 排除理由 |
| --- | --- | --- | --- |
| Portainer | 容器管理（CE 开源 / BE 商业） | RBAC 全角色层级、GitOps 自动更新+变更窗口、registry 管理、fleet 治理策略+漂移修复（portainer.io/features） | 管理对象是容器/K8s，与代理节点零交集；其 fleet 治理思想 ferry 用「批量配置下发+规则库版本史」已覆盖等价诉求 |
| Dokploy | 自托管 PaaS | Docker Swarm 集群、健康检查自动回滚、registry 镜像级回滚、per-PR 预览部署（docs.dokploy.com） | 部署对象是应用镜像；ferry 的「回滚」对象是节点配置/agent 二进制，机制已在 configd/upgrade/install 脚本落地 |
| Coolify | 自托管 PaaS | 多 Git 源集成、模板目录、应用回滚、一键 Let's Encrypt（coolify.io） | 同上；HTTPS 面 ferry 由 ACME 证书任务（dns-01 四 DNS 商）覆盖 |
| Komodo | 容器自动化 | Core/Periphery 主从代理、CPU/内存/磁盘监控告警、Procedures 自动化、全量审计（komo.do/docs/intro） | Periphery 代理形态与 ferry agent 同构（ferry agent 已含心跳/探测/进程监管/自升级且更贴代理场景）；审计面 ferry AU-1..3 已有 |

> 若 ferry 未来引入「宿主机系统级监控（CPU/内存/盘）」，Komodo 的 agent 阈值告警形态可作参照；当前 ferry 监控聚焦代理链路（探测/负载/流量），属定位选择而非缺口。

---

> 合规口径同仓内惯例：ferry 是代理集群管理工具，部署与运营合规责任在部署者（docs/design/合规定位声明.md）。本报告不含运行时代码引用与任何翻墙服务运营内容。
