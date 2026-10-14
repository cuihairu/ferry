# ferry todo（需求清单）

口径：完成项=一行证据（落地日期+commit+测试）；未开工项=需求一句话，触发条件在句尾。闸门与观察项维持原状不重开，触发条件成立再派工。
调研来源：3x-ui、Marzban、Hiddify、Remnawave（backend 取材 2026-10-08 快照）；横向对比见 [docs/面板横向对比.md](docs/面板横向对比.md)。

## 完成项（按族一行证据）

- [x] P0 核心闭环（P0-1..19）：用户/节点/订阅 v2ray+clash/流量记账/到期停用/工程骨架与门禁——首批 2026-10-07 落地，make test 全绿
- [x] agent 协议契约与骨架（A-1..24）：agentproto/心跳/进程管理/配置下发/流量采集/批量操作/自升级/一键部署——2026-10-07 前后落地，make test 全绿
- [x] 入口与负载均衡（E-1..E-33）：注册元数据/探测/摘挂状态机/换线/区域传输判定/ws-tls 与 quic/ssh 插件/分配与成本/智能 DNS/成本参考库/价格关注——2026-10-07..09 落地，make test 全绿
- [x] 支付（PAY-1..11）：Provider 契约/卡密三账/兑换防爆破/epusdt Provider/对账视图/stub 等资质——2026-10-07 落地，make test 全绿
- [x] 计划扩充（OS-1..4/BR-1..5）：OpenTofu 供给/入池流水线/判封恢复/域名前置/IP 池/证书 ACME/动作留痕——2026-10-07..08 落地，make test 全绿
- [x] 分销与客服（DS-1..3/SV-1..2）：代理账目/代理面板/servify 客服真嵌——DS 2026-10-08、servify 2026-10-10（18d045b/46d81e7），make test 全绿
- [x] 开源优先换件（OSS-1..3）：gopsutil 已换；libdns 与 lego 核验后保留自有实现（选型留档开源选型设计.md）——2026-10-08
- [x] 用户触达（TOUCH-1..7）：联系绑定/RSS 公告/月账单与例行邮件/投递失败闭环/TG bot/断联容灾——2026-10-08 落地，make test 全绿
- [x] 流量节省（SAVE-1..8）：GeoIP 分流/静态域名直连/缓存层指标位/广告拦截/配额联动降档/节省报表/panel 汇总——2026-10-07..08 落地（SAVE-5 复用率不可执行如实注记），make test 全绿
- [x] 告警通道（HERALD-1..5）：outbox/Herald 集成/九类告警/触达事件/回执端点验签——2026-10-08 落地，make test 全绿
- [x] 面板可用性（BK-1..3）：周期备份/手动留痕/恢复 runbook——2026-10-08 落地（S3 外发 2026-10-09 接入），make test 全绿
- [x] P1 安全面（P1-12/13）：2FA/恢复码/登录审计与告警——2026-10-08 落地，make test 全绿
- [x] 操作审计（AU-1..3）：审计表/middleware 捕获/查询页/保留轮转——2026-10-10（cc33912/38870ee/24e6c0c），make test 全绿
- [x] 环境面与工程面：env 全集登记/compose 透传/gofmt 全覆盖/provision.Shared 单例/鉴权实机走查（apiAuth 双凭据+dist 令牌收紧）——2026-10-09..10，make test 全绿
- [x] CI 与构建面：test.yml 门禁（6 模块+3 前端）/codecov 徽章/nightly 每日构建——2026-10-10（3e67780/348a414），CI 双绿
- [x] 面板运营（OD/NT/PROMO）：订单中心/通知中心与触发/优惠码与活动（取优判定收口）——2026-10-07..10（PROMO fbf46c7/33e45cf/d30e8e6），make test 全绿
- [x] Herald 通道看板（HC-1..3）：投递回执看板/通道自检/样例预览——2026-10-10（96fd57e/9277c5b），make test 全绿
- [x] 协议扩充（hy2+SB）：hysteria2 二进制适配全链/订阅三格式五协议出站——2026-10-10（d869485）/2026-10-11（2fa9440/75955ca），make test 全绿
- [x] P2-1..3：standby 主从同步/多级管理员配额/按用户带宽限速——2026-10-10（0862bc1/79c0f70/cc7009d），make test 全绿
- [x] P2-4 设备面：HWID/设备数/IP 数限制——2026-10-11（df4b3c7），make test 55 包+3 前端绿
- [x] P2-5 公告三项：起止时间/触达统计/外通道重发位——2026-10-11（dc28ae0），make test 55 包+3 前端绿
- [x] P2-7 协议收窄：五协议白名单口径确认（hy2 单增不构成解禁）——2026-10-11，无需新代码
- [x] 公告受众分段：全员/临期分组扇出——2026-10-11（3a803a6），make test 全绿
- [x] 用户批量导入/导出（迁移保址）：export 含令牌/import 沿用——2026-10-11（a543781/7305257），make test 全绿
- [x] 公告级联撤回与设备可见性：删锚点即撤扇出/panel 设备用量卡——2026-10-11（781fd17/fdc1fee），make test 全绿
- [x] 测试缝隙补齐（巡检 13 轮）：5 个零覆盖查询端点+loginlog 边界+证书任务/恢复流水线查询——2026-10-11..13（be4d10c/6475f4e），make test 全绿
- [x] 审计与刷新：文档一致性审计出表与五刷行号重钉——2026-10-08..13（53d3f90/774d423/3808cc7 等），docs vitepress 构建绿
- [x] nightly 日程复核：schedule 延迟自愈实证可用，dispatch 兜底留档——2026-10-11（92c9547）
- [x] 零覆盖包补齐：announce 受众归一/dedup key+agenthub 连接管理 11 例+model ValidProtocol+config env 解析 6 例——2026-10-14（3effa1e/c9a608e），server 36 包全绿
- [x] 零覆盖包补齐（二批）：xray NoopHandler 接口断言与零值行为+announce 扇出/定时/到期受众/重发限频/受众筛选 12 例——2026-10-14（38dc55c），xray 0%→100%、announce 9.6%→83.6%，server 37 包全绿
- [x] 低覆盖包补齐：save Loop/UserSavings 45.7%→85.7%、review Run 48.5%→81.8%、storage 两辅助 52.0%→66.0%、cert Loop/IssueNow/realRun 55.5%→74.4%、pool Suspend/Loop 61.6%→83.2%、cost Loop/WatchLoop 64.0%→83.0%、dns Delete/Kind/truncate 64.5%→79.6%——2026-10-14（6804df5），server 全量绿，7 包 -race -count=2 全净
- [x] 低覆盖包补齐（agent 侧一批）：link Run 重连/退避与 tlsConfig 证书口径 54.2%→95.2%、xraystats Collector 三口径与 pbSkip 64.0%→86.5%、spool Watch 67.6%→80.9%、configd rollback 66.9%→67.6%——2026-10-14（03e1e2d），agent 全量绿，4 包 -race -count=2 全净
- [x] 低覆盖包补齐（agent 侧二批）：tunnel 心跳往返/失活判定与三插件名+空 Close 75.3%→79.0%、relay RunFile 出口 80.3%→85.6%——2026-10-14（071165a），顺带修 TestRegistry 全局注册表重跑撞名，agent 全量绿，-race -count=3 全净
- [x] 零覆盖包补齐（四批）：link 断连重连/非法帧丢弃/握手失败退避、spool 重放与去重、xraystats 解析边界——2026-10-14（092488a），link 54.2%→95.2%、spool 62.0%→80.9%、xraystats 51.9%→86.5%，agent -race -count=3 全净
- [x] 零覆盖包补齐（三批）：agent app 分发七路/升级拒收受理/进程与配置未知项/证书节流/高负载连击 13 例+procs 测试数据竞争修复（collector 裸读+事件异步投递）——2026-10-14（574db7b），app 4.6%→48.3%，agent 全模块 -race 全绿

## 未开工项（需求一句话）

- [ ] agent 组件独立进程与 unix socket IPC（C-03）：当前组件为进程内角色调用；拍板维持原状不重开
  - 状态=维持不做｜原因：进程内角色调用已满足需求，独立 IPC 增加复杂度无收益｜触发条件：无（拍板关闭）
- [ ] 节点令牌轮换窗口与吊销 CRL（C-06）：未实现；拍板维持原状不重开
  - 状态=维持不做｜原因：节点令牌当前无轮换需求，CRL 吊销未触发｜触发条件：无（拍板关闭）
- [ ] mTLS CA 编排：受 mTLS 默认关闭抑制，维持挂起；开启 CA 消费方后再评估
  - 状态=阻塞｜原因：mTLS 默认关闭，无 CA 消费方｜触发条件：mTLS 开启且有节点证书签发需求
- [ ] 微信/支付宝直连支付：等商户资质到位再开工
  - 状态=阻塞｜原因：无商户资质，无法对接直连支付｜触发条件：用户提供商户资质（appid/merchant_id/证书）
- [ ] 通道维度偏好：单项增补不构成解禁，维持不做
  - 状态=维持不做｜原因：Herald 单通道已满足，多通道偏好无实际需求｜触发条件：无（拍板关闭）
- [ ] TOUCH-4 触达周期 dash 化：已核实 env 配置口径更优，维持不做
  - 状态=维持不做｜原因：env 配置已满足可调需求，dash 化无增量价值｜触发条件：无（拍板关闭）
- [ ] TOUCH-7 边缘探测结论复用（判定源）：需 probe 面扩 entry_domain target_kind，未开工
  - 状态=待触发｜原因：probe 面未扩 entry_domain target_kind，复用无数据源｜触发条件：probe 面扩字段且数据落库
- [ ] costref 接真实在线来源：需真实商家 API 选型与 license 核验，未触发
  - 状态=待触发｜原因：无真实商家 API，选型与 license 核验未启动｜触发条件：用户提供商家 API 文档+license
- [ ] 调度历史权重自动调参（P2 族）：人工调参先行，P2 分期
  - 状态=待触发｜原因：人工调参已满足，自动调参为 P2 分期｜触发条件：P2 分期开启且人工调参瓶颈
- [ ] 节点级流量预算与告警百分比（横向对比观察）：多机放量后再议
  - 状态=待触发｜原因：当前单站规模未达多机放量｜触发条件：多机部署且流量预算管理需求出现
- [ ] MONTH_ROLLING 滚动重置档（横向对比观察）：小站自然月账单够用，维持
  - 状态=维持不做｜原因：小站自然月账单已满足，滚动重置无实际需求｜触发条件：无（拍板关闭）
- [ ] OSS 支付 SDK / Xray proto 替换 / GeoIP 随分流批三候选：均未解禁，挂点未开
  - 状态=阻塞｜原因：三项均未解禁（license/资质/分流批未开）｜触发条件：各自解禁条件满足（OSS 三候选评审通过）
- [ ] hy2 二进制真机联调：外部提供项（二进制/证书/防火墙），仓内单测面已全清
  - 状态=阻塞｜原因：二进制/证书/防火墙均为外部提供项，仓内无法联调｜触发条件：用户提供测试二进制+证书+防火墙环境
- [ ] TestProvisionRunAPI 偶发观察：-race 连跑全净未复现；再出现时抓 -race -verbose
  - 状态=待触发｜原因：-race 连跑全净未复现，无法定位｜触发条件：偶发复现时抓 -race -verbose 日志
- [ ] 上游等待：vitepress 2 stable 落地后撤 pnpm override 直升；dependabot npm ignore 重开条件（vitepress stable 时同步放开）
  - 状态=阻塞｜原因：vitepress 2 stable 未发布，pnpm override 不可撤｜触发条件：vitepress 2 stable 发布且构建验证通过

## 行级证据与解锁条件（R1013B 续）

| # | 条目 | 卡在哪（行级证据） | 需要什么才能解锁 |
|---|------|---------------------|------------------|
| 1 | C-03 IPC | `agent/internal/spool/spool.go:1` spool 已是角色组件 IPC；进程内调用已满足 | 拍板关闭，无解锁条件 |
| 2 | C-06 令牌轮换 | `server/internal/handler/` 无 rotate/CRL 端点（grep 零命中） | 拍板关闭，无解锁条件 |
| 3 | mTLS CA 编排 | `docs/design/安全设计.md:66` mTLS 默认关闭；`:97-98` CA 编排未实现 | mTLS 开启且有节点证书签发需求 |
| 4 | 微信/支付宝直连 | 仓内无商户资质代码（grep 零命中） | 用户提供商户资质（appid/merchant_id/证书） |
| 5 | 通道维度偏好 | 拍板关闭（`docs/design/告警通道设计.md` 单通道口径） | 拍板关闭，无解锁条件 |
| 6 | TOUCH-4 dash化 | `docs/design/用户触达设计.md:73` TOUCH-7 评估=维持 env 配置 | 拍板关闭，无解锁条件 |
| 7 | TOUCH-7 探测复用 | `docs/design/用户触达设计.md:58` ProbeReport 只有 tunnel/exit/peer 目标；`agent/internal/probe/probe.go:94` 有 TargetKind 但无 entry_domain | probe 面扩 entry_domain 字段且数据落库 |
| 8 | costref 在线来源 | `packages/costref/static.go:23` StaticSource 是公开价格表，真实在线来源未扩 | 用户提供商家 API 文档+license |
| 9 | 调度自动调参 | `docs/design/调度核心设计.md:109` P2 才考虑自动调 | P2 分期开启且人工调参瓶颈 |
| 10 | 流量预算告警 | 横向对比观察项，`docs/面板横向对比.md` | 多机部署且流量预算管理需求出现 |
| 11 | MONTH_ROLLING | 拍板关闭（小站自然月账单够用） | 拍板关闭，无解锁条件 |
| 12 | OSS 三候选 | `docs/design/开源选型设计.md:42-43` libdns/lego 核验后保留自有；`:56-57` Xray proto/GeoIP 候选未解禁 | 各自解禁条件满足（OSS 评审通过） |
| 13 | hy2 真机联调 | `docs/design/agent架构设计.md:40` hysteria2 进程管理；二进制/证书/防火墙均为外部提供 | 用户提供测试二进制+证书+防火墙环境 |
| 14 | TestProvisionRunAPI | -race 连跑全净未复现 | 偶发复现时抓 -race -verbose 日志 |
| 15 | 上游等待 | `docs/package.json:10` vitepress ^1.6.4；`pnpm-workspace.yaml:7-11` override 钉 vite ^5.4.14 | vitepress 2 stable 发布（https://github.com/vuejs/vitepress）且构建验证通过 |

## 巡检结论（R1013B）

2026-10-13 巡检复核：15 项未勾选，全挂起/阻塞/待触发/维持不做，无遗漏可推进项。

依据：CI 全绿（test/docs/nightly ec67869）、Dependabot 全 fixed 零 open、make test 5 模块+3 前端全绿、go vet 5 模块干净、gofmt 零未格式化、go mod tidy 零差异、dash/panel vue-tsc 干净、文档漂移已全量审计（43 条 N-1..N-57 全修）、无 open issues/PRs。

## 巡检结论（patrol1224）

2026-10-14 台账体检：29 条完成族行的 commit 证据逐条核 git log，34 个引用哈希全部真实存在；15 项未勾保持一行一条（状态行/原因/触发条件齐全）。

修正 9 处日期区间与族实际落地日不符：P0（首批 10-06/07→10-07）、E 族（10-08..09→10-07..09）、PAY（10-08→10-07）、OS-BR（10-08..09→10-07..08）、SAVE（10-08→10-07..08）、环境面（10-10→10-09..10，compose 透传 5553504 落 10-09）、面板运营（10-08..10→10-07..10，OD-1 f4b183f 落 10-07）、测试缝隙补齐（10-11→10-11..13，6475f4e 落 10-13）、审计与刷新（10-08..11→10-08..13，2af9baa/ec67869 落 10-13）。BK 行「S3 外发 10-09 接入」经 s3.go 历史核为 3eb290a 属实不改。

依据：TestProvisionRunAPI -race -count=5 全净未复现、CI 三路全绿（test bc0a50a/docs ec67869/nightly）、Dependabot 零 open、无 open issues/PRs。
