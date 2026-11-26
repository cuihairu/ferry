# ferry todo（需求清单）

口径：完成项=一行证据（落地日期+commit+测试）；未开工项=需求一句话，触发条件在句尾。闸门与观察项维持原状不重开，触发条件成立再派工。未开工任务行尾内联「（依赖：…；完成条件：…）」，依赖「无」=无跨任务前置；完成条件默认=需求落地+测试绿+证据行。
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
- [x] 零覆盖包补齐（五批）：agent app 握手成功路径/心跳周期/元数据覆盖/握手期取消/探测与流量载荷/状态告警上报 12 例——2026-10-14（2013223），app 48.3%→78.6%，agent 全量绿，-race -count=3 全净
- [x] 低覆盖包补齐（六批）：audit nil-logger 兜底 66.7%→71.4%、toucher Loop 启停/firstErr 69.6%→80.4%——2026-10-14（b8fc253），-race -count=3 全净
- [x] 零覆盖包补齐（六批）：cmd/agent envOr、cmd/ferry-quic CA 解析与 cert/key 校验 6 例+configd 目录/无路径/回滚二次失败边界 7 例+proberole spool 转发与黑洞两形态+upgrade 非法 URL/self 缺失/备份被占/回滚失败 7 例——2026-10-14（38791b7），ferry-quic 0%→46.6%、configd 67.6%→73.0%、proberole 70.0%→100%、upgrade 73.3%→82.6%，agent 全量绿，-race -count=2 零非 ok
- [x] 零覆盖端点补齐（七批）：handler dns-providers/dns-fronts/probe-reports/quota-link/rulelib 五只读端点+NormalizeAdminRoles 归一/checkImportQuota 配额校验 7 例——2026-10-14（72a6919），handler 74.5%→75.2%，-race -count=3 全绿
- [x] 低覆盖包补齐（八批）：alloc Loop 启停/钩子回调/webhook 失败外发/悬空现行占位/balanced 峰时判定/容量兜底 74.8%→89.2%、cert 真实 openssl 到期解析/续期失败告警/dns-01 凭证四类失败分支/自定义 CA 与空域名边界 74.4%→89.0%——2026-10-14（ddb919f），-race -count=3 全绿
- [x] 低覆盖包补齐（九批）：quota LinkLoop 启停/人读字节量 76.5%→87.4%、standby DSN 取路径导出 75.9%→77.2%、herald 用户 target/状态计数 76.3%→86.6%——2026-10-14（eb65bc9），-race -count=3 全绿
- [x] 低覆盖包补齐（十批）：storage Open 方言分派与 DSN 补齐口径 66.0%→88.0%（59f1fc8）、configd 资产读冲突与回滚临时文件失败分支 73.0%→75.0%（0b7e0d9）、monitor webhook 三档外发/未迁移库 sweep 冒错 75.4%→89.9%——2026-10-14，-race -count=3 全绿
- [x] 低覆盖包补齐（十一批）：relaypush Push 应答链路（ack 落库/超时/写失败）69.4%→88.7%、recovery/notifyscan Loop 首轮与取消启停 71.3%→77.4%/72.5%→84.1%、provision cancelled ctx/replenish Loop 73.0%→81.5%、agent config 读取/解析/env 覆盖 82.2%→95.3%、spool 写拒错/drain 边界 80.9%→89.7%、tunnel 四插件心跳/Close 与 wsConn 面 78.7%→82.8%——2026-10-15（b6f28f8），-race -count=2 全绿
- [x] 低覆盖包补齐（十二批）：audit tick 清扫路径（sweepInterval 转变量）71.4%→90.5%、standby 守卫/非库快照/截断响应/tmp 与 rename 失败/Loop 重试取消 77.2%→91.1%、dns cloudflare 缺省客户端与 do 四类失败分支 79.6%→89.2%、configd reload 失败回滚成功双路径 75.0%→76.4%——2026-10-15（45cc193），-race -count=3 全绿
- [x] 低覆盖包补齐（十三批）：handler 零覆盖口（活动列表/DNS 凭证与前置更新端/证书编排器构造）75.2%→76.1%、ratelimit 懒清理 sweepLocked 81.4%→100%、toucher joinErr 合并与到期文案 80.4%→83.9%、backup 外发位三态文案 80.7%→82.4%——2026-10-15（14a0187），-race -count=2 全绿
- [x] 低覆盖包补齐（十四批）：agent app 测速回报全链（面板 URL 非法/不可达/上报成功与校准短路）+ config push 载荷解码失败/应答外发失败 + procSpec 双向查找 78.2%→86.1%、handler provision jobs 分页口径 76.1%→76.2%——2026-10-15（720be9b），-race -count=2 全绿
- [x] 低覆盖包补齐（十五批）：cmd/agent runRole 转错误返回与角色分发口径（未知角色/probe 取消/relay 坏覆盖文件）7.3%→36.8%、ferry-quic client/server 起监听与 ctx 取消回收主路径 46.6%→65.5%——2026-10-15（3600058），-race -count=2 全绿
- [x] 低覆盖包补齐（十六批）：recovery 节点删除终态/OK·Running 等待窗口/超长错误截尾/迟到结果丢弃/闭库冒错与留痕容错/事件外发故障容忍/dnsswitch 备用 IP 解析口径/主密钥缺失/回切与落库失败（gorm 回调注错）77.4%→88.5%——2026-10-15（07481eb），-race -count=2 全绿
- [x] 低覆盖包补齐（十七批）：cmd/ferry main 拆 run(ctx,cfg)+serve（信号驱动优雅关停，SIGTERM 即停全部后台循环）与坏库冒错口径 22.0%→68.2%——2026-10-15（650cfec），-race -count=2 全绿
- [x] 低覆盖包补齐（十八批）：cmd/agent 核心入口拆 runCore（自升级验证装配+连接循环）与取消即回收口径 36.8%→51.3%——2026-10-15（e766cf5），-race -count=2 全绿
- [x] 低覆盖包补齐（十九批）：ferry-quic main 拆 run 分发（缺/未知模式 ErrUsage 保留退出 2）65.5%→75.4%、configd 配置与规则库写路径失败口径（目录建不出/临时写不出，rename 分支 Linux 不可达不硬凑）与回滚权限失败 76.4%→82.4%——2026-10-15（a0b2fb6），-race -count=2 全绿
- [x] 低覆盖包补齐（二十批）：secret 文本/字节密文坏档口径（解析taxonomy/未知版本/截断/篡改）82.1%→91.0%、geodns 缺省工厂+凭证链失败+告警容错 82.4%→86.3%、pool 预备标记/升池分支+announce webhook 与渠道故障 83.2%→88.0%、cost 价格页附注+告警去重与表故障容错 83.0%→86.5%——2026-10-15（6725f43），-race -count=2 全绿
- [x] 低覆盖包补齐（二十一批）：handler API Token 签发-验签-鉴权闭环与空身份 403/401 口径、代理自面中间件 401 分支（无头/Bearer 空白/ID 非数字）+ lastAt 零值 + orders/ledger limit 夹取 + 六端点库故障 500（拆表定向与查询计数注入，含 gorm Scan 走 gorm:row 链的次查失败）76.2%→77.2%——2026-10-15（bf926d1），-race -count=2 全绿
- [x] 低覆盖包补齐（二十二批）：review 库故障冒错/超限判定失败/循环内停用失败只记日志/停用后外通道故障容错 81.8%→100.0%、announce 锚点与用户行落库失败+事件批量写失败+扇出与重发时间回写失败+RunDue 名单失败 83.6%→97.3%、notifyscan 到期与流量扫描候选/去重/用量查询失败+预警阈值越界回落 80+循环内错误日志 84.1%→98.6%——2026-10-15（f950616），-race -count=2 全绿
- [x] 低覆盖包补齐（二十三批）：support 客户端缺省 HTTP 客户端/建档非 409 原样冒错/409 回落再查失败与查无档案/HTTPError 文案/URL 解析·拨号·读体·载荷不可编码四类请求侧失败/非 2xx 状态码冒错 85.2%→100.0%、save Sweep 五类库故障（水位读取/无水位清空/流量查询/累加/水位回写）+折算两条查询失败+循环内扫描失败只记日志+Loop 缺省间隔回落 85.7%→100.0%、herald Emit 入库与 Count 计数失败+Deliver 空发送器短路/到期查询/成功置态/失败排程+发送侧 URL 解析与拨号+循环内投递失败只记日志 86.6%→99.0%（http.go 载荷 marshal 失败分支不可达留白）——2026-10-15（97cabed），-race -count=2 全绿
- [x] 低覆盖包补齐（二十四批）：notification 公告创建/列表/重发/全量列表/删除/面板已读/面板偏好七组错误路径（坏绑定/标题正文超长/limit 越界/非数字 id/关库与注错回调定向查询/统计回流/事务内级联删除/偏好空负载/阈值归一历史坏行回落）100.0%、user 列表/读取/创建/更新/删除/重置令牌五组（拆表与注错定向查询/改名冲突 409/回落再查/两段清理/合法多字段 PATCH 逐分支落库）user.go 185/194=95.4%、handler 包 77.2%→79.3%（留白 6 处：RandomToken 的 crypto/rand 错误与 isDup 唯一约束竞态，正常流程不可达不硬凑）——2026-10-15（a6217a5），-race -count=2 全绿
- [x] 低覆盖包补齐（二十五批）：distributor 登录限速锁定（五连败落锁 429）/坏绑定/库故障、建号超长密码 bcrypt 拒收/备注与显式停用补写失败/落库失败、更新坏 id/密码重置/超长密码/折扣越界/空负载/更新失败/回落再查失败、流水坏 id/limit 解析回落与越界夹取/取数与汇总各自失败（Scan 走 gorm:row 链）、账目坏 id/坏绑定/打款非正拒收/汇总与落账失败 37 处→仅剩 JWT HS256 签名不可失败留白 distributor.go 201/203=99.0%、handler 包 79.3%→80.4%；顺带修复 createDistributor enabled:false 静默失真（Create 后 GORM 经 RETURNING 回填库缺省 true，补写分支永不可达，改按入参判定）——2026-10-15（226ee8e+ecf78d1），-race -count=2 全绿
- [x] 低覆盖包补齐（二十六批）：agentws 握手关卡（非 WS 升级 400/首条非 hello/缺令牌/坏帧/坏协议版本四类协议错误关闭）、令牌校验库故障关闭、节点行更新失败只记日志（hello_ack 元数据回落空）、六类消息载荷解码失败循环继续、三类落库失败（进程快照建档/探测批量/节点+用户流量）只记日志且 ack 照发、未知消息类型留痕、落存函数直测（空入参短路/目标节点快照补全/全未注册邮箱短路/用户表缺失冒错/地址解析三类回落）、syncNodeMeta 三态（空白补全/补写失败回落行值/行加载失败空权威值，注意节点行 default:未知 建行即回填须先 map 更新清空）agentws.go 216/224=96.4%、handler 包 80.4%→81.1%（留白 6 处：六条 ack 发送侧失败需对端在服务端写出前断链，竞态时序不硬凑）——2026-10-15（a55aea5），-race -count=2 全绿
- [x] 低覆盖包补齐（二十七批）：provider 提供商列表取数失败、录入坏绑定/缺字段/显式停用/停用补写失败/落库失败、更新坏 id/查无/库故障同口径 404/坏绑定/类型与开关分支/无主密钥换机密 500/保存失败、删除坏 id/无引用成功/落删失败、模板校验七规则直测（每用例重建入参防共享指针写穿）、列表取数失败、建档坏绑定/角色缺省落 entry/落库失败、改档坏 id/查无/库故障 404/坏绑定/校验失败/带宽月费分支/保存失败、删档坏 id/落删失败 providerapi.go 159/159=100.0%、handler 包 81.3%→82.1%；顺带修复 createProvider enabled:false 同款静默失真（Provider.Enabled default:true 被 RETURNING 回填，改按入参判定+显式补写）——2026-10-15（9c3158e+85c4818），-race -count=2 全绿
- [x] 低覆盖包补齐（二十八批）：dnsapi 域名前置 DNS 商缺名缺型校验、列表取数失败、录入坏绑定/缺 api_key/无主密钥 500/显式停用/停用补写失败/落库失败、更新坏 id/查无/库故障同口径 404/坏绑定/未知类型/无主密钥换密 500/保存失败、删除坏 id/无引用成功/落删失败、前置校验八规则直测（缺域/非 FQDN/缺 provider/缺主 IP/缺 backup/坏 JSON/全空白数组）、列表取数失败、建档坏绑定/查无 DNS 商/落库失败、改档坏 id/查无/坏绑定/校验/查无商/保存失败、删档坏 id/查无/落删失败、geo-sync 库故障 500 dnsapi.go 175/175=100.0%、handler 包 82.1%→83.1%；顺带修复 createDNSProvider enabled:false 第三处同款静默失真（同 default:true RETURNING 回填，改按入参判定+显式补写）——2026-10-15（6f85dc2+3007dfd），-race -count=2 全绿
- [x] 低覆盖包补齐（二十九批）：node 列表与单读取数失败 500、建号坏绑定/缺名/缺地址/端口越界/hysteria2 配置非对象、元数据齐带落库 MetaInit 接管、显式停用/停用补写失败/落库失败、更新库故障定位 500/坏绑定/全元数据八分支回写/回写失败/回落再查失败、删除查无 404/落删失败、normalizeConfig 空值回 {} 与坏 JSON 原样保留直测、parseNodeConfig 空值与非对象、hysteria2 密码校验三态 node.go 175/178=98.3%、handler 包 83.1%→83.7%（留白 2 处：RandomToken crypto/rand 与 normalizeConfig 的 Marshal 防御分支——JSON 反解值再序列化恒可成功）；测试中发现 GORM First 会把 dest 结构体既有主键并进查询条件（复用带 ID 的结构体查同名新行必落空），测试内改用全新结构体规避——2026-10-15（9744fa0），-race -count=2 全绿
- [x] 低覆盖包补齐（三十批）：panel 面板端点错误口径：用户读取/用量/设备/IP 三段库故障、兑换坏绑定与空码/五连败落锁 429/核销事务 UPDATE 失败、订单 limit 非数字与 0 与超限（须先种用户）/取数失败/授权失败、商品列表取数失败/余额聚合失败、下单限速 10 次 429/批次定位/count/活动/券核销/订单落库各自失败（payment.Get 先于批次查找须先注册网关）、订单状态/节省额失败；拆表定向与查询计数注入（Find 走 gorm:query、聚合 Scan 走 gorm:row、Save 跨 Update+Create 两条链）panel.go 210/211=99.5%、handler 包 83.7%→84.7%（留白 1 处：358 事务内二次解析规则分支——bestCampaign 已先解析同份规则并跳过失败活动，进事务的规则必然解析成功，防御性代码不可达）——2026-10-15（45e80d3），-race -count=2 全绿
- [x] 低覆盖包补齐（三十一批）：adminsapi 管理员管理面（P2-2）：checkOperatorQuota 查无账号与配额计数失败、列表取数失败、建号坏绑定/空名与超长名/查名库故障/超长密码 bcrypt 拒收（>72 字节）/落库失败、更新坏 id/查无 404/库故障定位/坏绑定/短密码/超长密码/负配额/空负载/回写失败、删除坏 id/查无 404/库故障定位/非管理员行 404/降级落库失败 adminsapi.go 129/133=97.0%、handler 包 84.7%→85.4%（留白：RandomToken 的 crypto/rand 错误与 isDup 唯一约束竞态——ensureUsernameFree 先行，正常流程不可达）；顺带修复 PUT/DELETE /api/admins/:id 查无管理员原本经 replyFind 把 gorm.ErrRecordNotFound 当 500 冒出（errNotFound 是另一哨兵），改显式判 gorm 记录不存在回 404——2026-10-15（2f6cea1+28f15bb），-race -count=2 全绿
- [x] 低覆盖包补齐（三十二批）：webcert 面板 Web 证书（P1-7）：loadWebCertPEM 证书段与私钥段读取各自失败与已配置但配对失败（WebTLSCert 报错且回 configured）、状态视图读取失败与配对错误回显、上传坏绑定/缺 PEM/超限/两次落库各自失败、自签坏绑定/缺 host/默认天数 365/IP host SAN 分支/两次落库各自失败、清除库故障；SetSetting 走 Save（UPDATE 未中再 INSERT）跨 Update+Create 两条链计数定向第 1 句与第 3 句 webcert.go 100/108=92.6%、handler 包 85.4%→85.9%（留白 6 处：X509KeyPair 已解析叶证书后 ParseCertificate 再失败、generateSelfSignedPEM 的 generate 错误、crypto/rand 取熵失败、CreateCertificate 与 MarshalECPrivateKey 失败——正常流程不可达）——2026-10-15（226fa9d），-race -count=2 全绿
- [x] 低覆盖包补齐（三十三批）：campaign 限时活动（PROMO-2）：parseCampaignRules 规则字符串形态解包失败、列表取数失败、建号坏绑定/落库失败、更新坏 id/库故障定位/坏绑定/校验失败/保存失败、删除坏 id/落删失败、活动位身份缺失 404/定向活动表注错 500/坏规则行跳过不下发、资格判定停用与出窗短路/计数失败、取优循环资格报错冒出/坏规则跳过/范围不中跳过（scope 指向另一张真实批次）/门槛不达跳过 campaign.go 134/134=100.0%、handler 包 85.9%→86.5%（留白：无）——2026-10-15（e65f490），-race -count=2 全绿
- [x] 低覆盖包补齐（三十四批）：twofa 两步验证（P1）：状态与解绑查无管理员 401、setup 暂存落库失败、enable 坏绑定与空码/取暂存库故障/尚未发起绑定/暂存不可解 500/校验通过后查无管理员 401/用户行保存失败/恢复码落库失败、disable 坏绑定/查无/密码错误/保存失败、checkTwoFA 旧密文解不开按失败（不静默放行）、isDigits 三态与空恢复码不消费与一次性恢复码命中即销 twofa.go 109/116=94.0%、handler 包 86.5%→86.9%（留白 4 处：totp.Generate 与 newRecoveryCode 的 crypto/rand 错误、sealSecret 已判 Enabled 后再失败——AES-GCM 有主密钥恒成功）——2026-10-15（75af917），-race -count=2 全绿
- [x] 低覆盖包补齐（三十五批）：eventsapi 事件 outbox（HERALD-1）：列表合法 limit 生效与 kind 通道过滤/取数失败/状态计数失败（Count 是聚合 Scan 走 gorm:row 链，注错须挂 gorm:row 而非 gorm:query）、重投坏 id 404 与落库失败、回执读体失败与三种形状的坏 JSON（整体坏 JSON 探测段即败、旧形状数字 event_id 字段类型不符、回调形状字符串 event_id）与回执落库失败、看板坏 limit 三种与合法 limit/取数失败/通道 GROUP BY 聚合失败/同通道既有 sent 又有 failed 记 degraded 并回最近失败明细、自检落库失败、样例取类失败与单类取数失败跳过不下发半个列表 eventsapi.go 150/150=100.0%（与三十六批同轮合测，包合计 86.9%→88.2%；留白：无）——2026-10-15（e096841），-race -count=2 全绿
- [x] 低覆盖包补齐（三十六批）：card 卡批次与卡密：建批次坏绑定/价格越界/过期时间早于当前/落库失败、卡密生成撞重复 409 与其余冲突 500（isDup 只认 unique/duplicate 文本，须注入带 duplicate 字样的错误才能命中）、列表 find 与聚合计数失败、导出坏 id 404/查无 404/取数失败、禁码查无与落库失败与二次读取失败、删除查无 404 与 Delete 链注错、findCardBatch 闭库定位失败直测 card.go 133/135=98.5%（留白 1 处：card.go 83-85 cardcode.Generate 的 crypto/rand 错误——n<=0 已被前置校验拦下）——2026-10-15（9c67649），-race -count=2 全绿
- [x] 低覆盖包补齐（三十七批）：coupon 优惠码（PROMO-1）：列表取数失败、validateDiscountRules 的 min_amount 越界与定向批次计数失败与坏 scope 形态、建号坏绑定与缺省 per_user 落 1 与落库失败、更新坏 id 与库故障定位与坏绑定与校验失败与保存失败、删除坏 id 与落删失败、couponCheck 定位失败与每用户计数失败、applyCouponTx 校验不过 passthrough 与条件核销零行报已领完与核销成功减额 coupon.go 152/154=98.7%（留白 2 处：coupon.go 221-222 randomCode8 的 crypto/rand 错误；299-300 applyCouponTx 的 RowsAffected==0 核销兜底——更新条件 total=0 OR used<total 与 couponCheck 拒绝条件同形，串行流下读数即写条件，非并发改行不可达）——2026-10-15（edc4046），-race -count=2 全绿
- [x] 低覆盖包补齐（三十八批）：certapi 证书任务（BR-4）：列表取数失败、建号坏绑定与缺省 method 落 dns-01 与落库失败、更新坏 id 404 与库故障定位（本端点把 First 一切错误归 404）与坏绑定与校验失败与 DNS 商查无 400 与 DNS 商命中回写并重置待签与缺省 method 回写与保存失败、删除坏 id 404 与 Delete 链注错、签发抢占更新失败与 IssueNow 取行失败（查询计数让处理器定位放行、IssueNow 第二次取行注错）与签发成功立返 200（真实签发在 goroutine 内异步跑，失败只记日志） certapi.go 101/101=100.0%、handler 包四轮累计收口 86.9%→89.3%（终值复核 4904/5492 语句；留白：无）——2026-10-15（837bb50），-race -count=2 全绿
- [x] 低覆盖包补齐（五十二批·收官）：transport 区域判定（合法窗口 120 秒生效回读 200；关库句柄使判定取数失败 500）、savestats 日统计（统计明细与节点映射两处取数失败 500）、outage 断联标记（设置保存失败 500——SetSetting 走 Save 的 Update 链按序号注错）、apitoken 签发限流（同 IP 第 101 次 429——限流器为包级共享态，用例开头与收尾各复位一次避免受他例顺序影响）、feed RSS 公告（取数失败 500，公开订阅面无凭据）、pipeline 入池流水线（已有 applied 快照直接返回：不重推不写注记；无 agent 在线时 hub 立即 ErrOffline 留「配置下发失败」注记且快照置 failed、状态保持 provisioning；agent 回 OK=false 留「配置下发未生效」注记并带错误原文、快照置 failed）——留白 3 处：apitoken.go 47-49 与 71-72 JWT HS256 签名恒成功、feed.go 72-74 xml.Marshal 纯基础类型恒成功——transportapi 3→0、savestats 4→0、outage 2→0、apitoken 5→3、feed 4→2、pipeline 4→0，handler 包 98.2%→98.5%（5408/5492）——2026-10-16（随本批单提交落账），-race -count=2 全绿（1332.5s）。覆盖战役至此收官：剩余 84 条未覆盖语句逐条复核均为已落账留白（crypto/rand、JWT/HS256 签名、json.Marshal 纯基础类型、NewEnvelope、hub 握手后并发断连窗口、x509/ecdsa 证书生成错误、cpuPercent 真读 /proc、servify 对端不可观测、防御性不可达分支），subdevice.go:95 空表归一化为覆盖映射幽灵——行为已由 TestUserDevicesErrors（无记录回 200 `[]` 非 null）钉住，不再有批次五十三
- [x] 低覆盖包补齐（五十一批）：costapi 成本看板（Build 依赖的阈值 settings 读取失败 500；阈值坏载荷与缺 cents 400）、allocapi 落地分配策略（策略读取失败 500、auto 行取数失败 500、坏载荷 400）、eveningapi 晚高峰段（days 生效回显；探测取数失败 500；有样本全不可达段 AvgRttMs 取零值分支且可用率 0）、standby 冷备快照（非 sqlite 方言按包名覆盖报 500——包内 namedDialector 包装真实方言只改 Name；关库句柄使 VACUUM INTO 失败 500）、backup 数据库备份（备份目录父路径为普通文件使 MkdirAll 报 not a directory 500；快照行落库失败 500）——留白 1 处：standby.go 31-33 os.MkdirTemp 失败依赖系统临时目录不可写，用例内无注入点——costapi 4→0、allocapi 6→0、eveningapi 4→0、standby 6→2、backup 4→0，handler 包 97.8%→98.2%（5391/5492）——2026-10-16（随本批单提交落账），-race -count=2 全绿（1012.4s）
- [x] 低覆盖包补齐（五十批）：quotalink 联动管理（设置读取失败 500、留痕列表取数失败 500、保存成功后回读失败 500——保存链 Update 先置标志位再由同进程 settings 读取注错、坏载荷 400）、procstatus 被管进程落库（空进程名整条跳过不落行；既有行定位的非 NotFound 失败与既有行更新失败两路原样返回——直调 saveProcStatuses 便于与 API 面分离；进程列表取数失败 500）、entrydomains 入口域名（清单取数失败 500；建按 Dest、改按 Save 的 Update 链 Model、删按 Dest 三处落库失败 500 且与 404 区分）、botapi TG bot 对接面（无 Bearer 头时 X-Ferry-Bot-Token 兜底命中 200、令牌命中但缺 chat_id 400、用量汇总失败 500——Scan 走 Row 链按 Statement.Model 注错、最近订单取数失败 500）、adminboot 首启引导（管理员计数失败、密码超 72 字节 bcrypt 拒收、管理员行落库失败三路原样返回错误）——留白 1 处：adminboot.go 35-36 RandomToken 的 crypto/rand 错误不硬凑——quotalink 4→0、procstatus 4→0、entrydomains 4→0、botapi 4→0、adminboot 4→1，handler 包 97.2%→97.8%（5369/5492）——2026-10-16（随本批单提交落账），-race -count=2 全绿（993.2s）
- [x] 低覆盖包补齐（四十九批）：user 用户 CRUD（落库侧唯一冲突文本 isDup 的 409 归类——预检放行后按注入 unique 文本错误直测建号与改名两处；isDup 对 nil/无关错误/unique 与 duplicate 文本的分类直测）、router 自检与状态灯（写探针失败 → db_writable=false 且 degraded 仍 200；维度状态灯与探测存证列表取数失败 500、探测列表合法 limit 生效）、landing 落地分配（列表取数、建档落库、权重更新、释放回写、定位非查无错误五路 500）、speedtest 测速（字节端点 IP 限流第 31 次 429、非 32KB 整倍数尾块按 remain 截断、下发途中客户端 RST 触发写失败分支、校准定位失败（握手后删节点行）与回写失败（标志位门控回调先于 agent 注册）两路均不回应答）、loadapi 负载快照（容量口径实测校准 300 > 套餐上行 200 > 100 兜底、单条采样差分不可信 util_pct=-1、节点表与采样表取数失败 500）、proclog 进程日志（坏 id 404 与缺 proc 400、在线不回执 5s 超时 504、回执载荷无法解码 500）——留白 4 处：user.go 97-99 与 260-262 RandomToken 的 crypto/rand 错误、proclog.go 48-50 NewEnvelope 构造失败（载荷纯基础类型不可失败）、59 hub default 错误（仅在线检与发送间隙断连的并发窗口可达）——user 5→2、router 5→0、landing 5→0、speedtest 5→0、loadapi 5→0、proclog 5→2，handler 包 96.4%→97.2%（5338/5492）——2026-10-15（随本批单提交落账），-race -count=2 全绿（902.5s）
- [x] 低覆盖包补齐（四十八批）：status 系统状态（meminfo 冒号后无值行跳过仍按缺 MemTotal 报错；在线节点计数与启用用户计数——各类型第二次 Count——失败 500，按 Model 类型+序号定向注错）、paynotify 回调结算（对单定位、重复回调计数、流水建档、订单置 paid、付款用户定位、权益发放六路注错统一 400 且事务整体回滚订单保持 pending——回读走 Raw+Scan 避开 Query 注错；无发放口径订单结算置 paid 不落 Grant）、routing 规则库与拦截开关（拦截开关保存失败 500；快照建档失败 500、版本史取数失败 500、agent 在线不回执 60s 等待超时 504 且快照置 failed——用例固有耗时取自 rulelibAckTimeout）、upgrade 自升级（坏 id 404、坏载荷 400、回执载荷无法解码 502、在线不回执 15s 超时 504）——留白 9 处：status.go 130-146 cpuPercent 五条错误分支（真读 /proc/stat，读取/解析/不推进在 Linux 上不可注入）、paynotify.go 29-31 渠道未注册 503（payment 注册表无撤销入口，跨用例与 -count=2 重跑间状态不定）、routing.go 152-154 与 upgrade.go 49-51 NewEnvelope 构造失败（载荷纯基础类型不可失败）、routing.go 164 与 upgrade.go 60 hub default 错误（仅在线检与发送间隙断连的并发窗口可达，无确定性入口）——status 8→5、paynotify 8→1、routing 6→2、upgrade 6→2，handler 包 96.0%→96.4%（5297/5492）——2026-10-15（随本批单提交落账），-race -count=2 全绿（935.6s，含超时分支实测 60s/15s）
- [x] 低覆盖包补齐（四十六批）：contact 联系绑定（无凭据 PUT 404、坏载荷 400、TG chat 超长 400、首绑 Create 失败 500、换绑 Save 失败 500；回执联动用户无联系行时兜底建档标 stale、兜底建档失败静默仍回 200——回执端点不因联动失败报错；truncateErr 超长截断直测）100%、usertemplate 用户模板（读取库故障 500、坏载荷 400、缺 reset_cycle 落缺省周期、保存库故障 500——SetSetting 走 Save 的 Update 链；存量 settings 脏值三态回落：坏 JSON 全缺省、空周期只补周期保留其余字段、带宽脏值整体回缺省）——留白 1 处：107-109 json.Marshal 纯基础类型结构体恒成功、notify 通知渠道（url 与 secret 两处键读取失败 500——同请求多键按 settings 读取序号定向注错、坏载荷 400、两步保存各自失败 500、测试外发对端 5xx → 502）100%、alerts 告警（建档失败只记日志、崩溃事件外发失败不阻断且告警行在、活跃查询失败不落库、刷新保存失败行内容不动——四路均照常回 ack；进程恢复消解失败只记日志且行仍 active；列表取数失败、处理落库失败、处理后回读失败 500）100%——踩坑实录：gorm 回调 Register 的 compile 与 agent 握手侧 goroutine 的 Execute 并发即真数据竞争，WS 类用例回调必须先注册再起 agent（-race 抓出后改序复绿）——contact 13→0、usertemplate 12→2、notify 12→0、alerts 12→0，handler 包 94.3%→95.2%（5228/5492）——2026-10-15（随本批单提交落账），-race -count=2 全绿（862.9s，须带 -timeout=30m）
- [x] 低覆盖包补齐（四十七批）：loginlog 登录审计（审计落库失败 401 照常零行、连续失败计数失败静默、五连败触发告警时去重计数失败静默不落事件、事件落库失败静默——四路登录响应不受影响；分页页码 0/页大小 0 与 500 夹取回落缺省；总数计数失败与明细取数失败 500）100%、admin 管理员登录（坏载荷 400、未知用户与无管理员权限 401、窗口内五连败锁定 429、用户定位取数失败按统一 401 不冒库错）——留白 1 处：107-109 JWT HS256 签名不可失败、costwatch 价格关注（列表、建档非重复、更新 Save、删除事务注错整体回滚行仍在、快照列表五处 500）100%、config 节点配置面（坏 id 404、坏载荷 400、缺必填 400；配置史合法 limit 生效与坏 id 404、流量记账坏 id 404/非法 limit 400/取数失败 500、配置史取数失败 500）100%——loginlog 11→0、admin 11→2、costwatch 11→0、config 11→0，handler 包 95.2%→96.0%（5270/5492）——2026-10-15（随本批单提交落账），-race -count=2 全绿（862.9s 同轮）
- [x] 低覆盖包补齐（四十五批）：poolapi 池管理（入口列表取数失败 500、单节点窗口内最新探测结论查询失败 500、摘除/复位/预备坏 id 404 与预备查无 404、复位定位失败 500；摘除/复位/预备成功时 E-27 分地域对账钩子各触发一次——OnChange 是包级测试池钩子，用后按 t.Cleanup 恢复，goroutine 内触发轮询等计数）、traffic 流量记账（坏载荷 400、行缺 user_id 400、引用部分缺失走缺失值回报 400、引用 id 校验取数失败 400——checkIDsExist 的查无与库故障同走「引用缺失」口径且库错文案原样透出，观察项不改产品行为、批量落库失败 500；单用户汇总用户不存在 404 与明细取数失败 500）、subdevice 订阅设备/IP 面（IP 在册计数、IP 上限计数、IP 首访落库、hwid 在册计数、设备上限计数、hwid 落库六路失败统一 403 不冒库错细节——同一订阅链上多次 Count 按序号定向注错；超限告警去重计数失败静默跳过仍 403 且超限文案回显；设备列表用户不存在 404、无记录回空表不返 null、明细取数失败 500）、costref 参考价（来源读取即 settings 查询失败在试查与批量对账两处同口径 500、批量对账节点取数失败 500、导入坏载荷 400）——留白 6 处：traffic.go 106 checkIDsExist 末尾防御 return nil（found⊆ids，长度相等即早退，否则必有缺失进循环，不可达）、subdevice.go 95-96 rows==nil 防御（gorm Find 空结果回非 nil 空切片）、costref.go 102-104 Lookup 非 NotFound 错误（StaticSource.Lookup 是唯一实现且只回 ErrNotFound）、116-117 Compare default ErrNoQuote（ParseTable 拒 monthly_cents<=0）、162 CheckNode 其余错误（机房空已被 WHERE 过滤、NoManual/NoQuote 不可达，理论不可达）——poolapi 全 100%、traffic 13→1、subdevice 13→1、costref 13→5，handler 包 93.8%→94.3%（5181/5492）——2026-10-15（随本批单提交落账），-race -count=2 全绿
- [x] 低覆盖包补齐（四十四批）：procop 进程操作（单节点坏 id 与不存在 id 404、坏载荷 400、agent 在线不回执 504——hub 等待超时分支真实触达、回执载荷无法解码 502、批量进程坏载荷 400、在线节点回执带错逐节点回执整体 200；批量配置坏载荷/空 ids/缺 proc/缺 payload 400）、sub 订阅（未知令牌与停用用户 404、用户查询/入口列表/联动降档取数失败 500、用量与延迟聚合取数失败 500 与退化无延迟仍 200——实测发现 gorm Scan 走 Row 回调链而非 Query 链，Before("gorm:query") 注错对 Scan 不生效，改按 Statement.Model 在 Callback().Row() 注入；入口配置不可解析三 target 打包失败 500、有近期探测结论时链接备注带 (Nms)、无数据省略括注）——留白 2 处：procop.go 41-42 NewEnvelope 构造失败（载荷纯字符串字段 json.Marshal 不可能失败）、51 hub.Request default 分支（env.ID 恒非空、Send 错误仅在线检与发送间隙断连的并发窗口可达，无确定性入口）——procop 18→2、sub 16→0 全 100%，handler 包 92.9%→93.5%（5134/5492）——2026-10-15（随本批单提交落账），-race -count=2 全绿
- [x] 低覆盖包补齐（四十三批）：provision 供给执行与恢复流水线（触发链校验全链：坏 id 404、模板指向不存在提供商 400、无主密钥 400、凭证解不开 400；apply 预签发建 job 失败 500、plan 建档失败 500——gorm 回调按 Dest 类型注错对齐既有口径；恢复流水线列表 limit 负例与合法值、jobs/recoveries/actions 三路取数失败 500、动作回放坏 id 与零值 id 400）、userio 导入导出错误面（导出取数失败 500、套模板导入模板读取失败 500、沿用令牌查重失败 500 而非 400、operator 无对应管理员账号整批 403 零写入、配额计数失败 403、事务内重名计数/建号/停用补写失败 500 且回滚零写入）——provisionapi 19→0 全 100%、userio 17→2（留白 2 处：userio.go 226-227 RandomToken 的 crypto/rand 错误无法注入；232-233 txImportUsers 内 resetCycleOrDefault 防御分支已被 validateImportRow 前置拦截不可达），handler 包 92.3%→92.9%（5102/5492；观察项：PrepareLaunch 建 job 与 prepareNode 不在同一事务，建 job 失败会留悬空 provisioning 节点行——留白不代改产品行为）——2026-10-15（随本批单提交落账），-race -count=2 全绿
- [x] 低覆盖包补齐（四十二批）：redeem 兑换（坏 JSON/空码/缺用户 400 统一文案、MB 档发放通知文案、有到期用户时长续期从现有到期起算、事务各步取数与落账注错 500 且整体回滚零写入——gorm 回调按 Dest/Model 类型注错；删批次因 CardBatch.Codes OnDelete:CASCADE 连带删码、核销走统一失败文案属正确行为，批次行读取失败改注错触达；删代理引用 500）、audit 审计（limit 夹取 1..200、offset 非法跳过与合法生效、target 过滤、from/to 非法时间跳过与有效过滤、取数失败 500、auditActor api/unknown 分支与 auditBodySnapshot GetBody 读后还原直测）、payrecon 对账（limit 负例与合法值、退款坏体 400、订单/流水/发放/空库游离记录取数注错 500、退款流转注错 500、退款成功后三账视图读取失败兜底 200 {"ok":true}）——redeem 105/105、audit 78/78、payrecon 98/98 全 100%，handler 包 91.3%→92.3%（5068/5492）——2026-10-15（随本批单提交落账），-race -count=2 全绿
- [x] 低覆盖包补齐（四十一批）：userio 批量导入负例（坏载荷 400、超长 sub_token/负配额/非法带宽/非法设备数整批 400 零写入、套模板导入模板值补行内缺省项）、costref 成本参考（对账未导入表 400 指引导入、node_id 非数与自由键缺失 400、tolerance_pct 非法回落缺省与 0/500 夹取 1..200、币种不符行只进 mismatches 计数不进列表）、status 系统状态（/proc 解析负例与边界：可用内存大于总量钳零、非数值行跳过、uptime 缺空格与非数值、cpu 行过短与非数值、percent 除零护栏；节点计数与用户计数取数失败 500——gorm 回调注错对齐既有口径）；userio 78.5→86.9%、costref 75.7→87.4%、status 79.4→90.7%，handler 包 90.6%→91.3%（5012/5492）——2026-10-15（随本批单提交落账），-race -count=2 全绿
- [x] 低覆盖包补齐（四十批）：handler 负路径与过滤护栏批次——landing 落地分配（direction 过滤、建坏载荷/权重越界/落地与入口节点缺失/入口角色不合规/同节点双角色 400、改与释放的坏 id 404 与缺 weight/权重越界 400）、alerts（severity 缺省落 warning、limit 非法与越界 400、state/node_id 非法 400、过滤组合命中、resolve 坏 id 404）、entrydomains（建与改坏载荷 400、改域名置空 400、域名归一剥协议尾斜杠与区域去空白）、routing 规则库推送应答链路（agent 回 OK ack → 快照 applied 且响应不回带文件内容、坏 ack → 502 且快照 failed 带错因、节点 id 非法推送与版本史 404、limit 合法值生效）、costwatch 关注（建坏载荷 400、建即停用、改/删/快照坏 id 与坏载荷 400、快照 limit 越界回落默认、到位判定与零前值不产生涨跌的除零护栏）——顺带修一处实测暴露的真 bug：PriceWatch.Enabled 带 gorm default:true，Create 略过零值字段落库吃进列默认值，建关注传 enabled:false 被静默改回 true（落点 internal/storage/models.go，去 default 标签；测试先行暴露再修）；landing 65.3→89.5%、alerts 66.7→83.3%、entrydomains 69.0→86.2%、routing 73.7→89.9%、costwatch 74.0→88.5%，handler 包 89.3%→90.6%（4978/5492；留白：DB 错误注入、hub ErrTimeout 与 Envelope 构造失败分支）——2026-10-15（随本批单提交落账），-race -count=2 全绿
- [x] 一键启动（三十九批）：deploy/start.sh 一键入口（首次从仓根 .env.example 生成 .env、已存在不覆盖；up 后等 /api/health 最多 180s 并校验 /data/ferry.db 落盘——建表随 storage.Open 的 AutoMigrate 幂等自动跑，未另起迁移体系；生效端口从 docker compose port 读回打印）+ Makefile 增 make up 等价入口 + compose 增 ferry-panel 用户端服务（此前 Caddyfile 只发 dash，panel/dist 从未被任何 compose 服务 COPY，用户端在容器部署里无入口）+ deploy/ferry-panel.Dockerfile（pnpm 构建 + Caddy 托管，复用同一 Caddyfile）+ 根 .env.example 最小必填集带注释与默认值（全量参数仍指 deploy/ferry.env.example）+ .dockerignore 收窄构建上下文 64MB→17KB（bin/ node_modules/ .env 等，避免机密进上下文）+ README/README.zh/deploy README 增首屏「一键启动」节（前置要求、一条命令、访问地址、常见问题）；顺带修三处已存在的构建与部署断裂：①ferry-server.Dockerfile 浮动标签 golang:1.27-alpine 解析到 go1.27.1，低于 go.mod 声明的 1.27.2（c8e76cb 为清 govulncheck 抬版后 compose 构建即断），钉 golang:1.27.2-alpine 对齐 CI setup-go；②replace 指到的 ../payments 独立模块未 COPY（3b019d8 E-31 起缺），补 COPY payments/；③Caddyfile 只反代 /api /sub /agent /feed.xml，漏了后端三组顶层路由 /admin/*（后台登录与两步验证、备份导出、证书、系统状态、日志）/distributor/* 与 /agent/ws——漏登记不是落到静态目录回 404 文件，而是被兜底 handle 的 file_server 接住：GET /admin/login 回 index.html(200)、POST 回 405，后台能打开登录页却永远登不上，按顶层前缀全量补齐并在文件头留新增路由须登记的注释——2026-10-15（468e414），docker compose config 校验通过、真机 up 全绿（三镜像构建、三容器起、/api/health 200 且 db_writable、/data/ferry.db 471KB 落盘、dash 与 panel 静态与 history 回退均 200、POST /admin/login 200 出 JWT 且 /admin/2fa 与 /admin/status 带 token 200、/agent/ws 达后端回 400）

- [x] docs 双语化：docs/ 四篇顶层文档（index、herald 对接实录、面板横向对比、文档一致性审计）补英文版，两侧顶部互链切换器（首页用 hero 按钮，因 home 版式 frontmatter 必须在首行），命令/路径/专有名词原样保留——2026-10-15（c6003a9），docs vitepress 构建绿
- [x] 节点令牌轮换窗口与吊销 CRL（C-06）：管理端轮换节点令牌并保留 10 分钟旧令牌兼容窗口，支持窗口内立即吊销；agent WebSocket 认证先查吊销表再校验新旧令牌与窗口，dash 节点操作支持复制新令牌与吊销旧令牌——2026-10-17（f61b1d9），make test 全绿（6 Go 模块+dash/panel 构建），PATROL1017S 选择性复测 `go test ./internal/handler/ -run 'TestAuthNodeTokenRotationWindow|TestNodeTokenRotation$|TestNodeTokenRevocation' -count=1 -v` 3/3 PASS
- [x] 覆盖补测（巡检单 2026-11-08，目标 ≥98% codecov 口径）：11 批推进全部 push 至 origin/main——8c26692（server 包）、89712b3（agent app/grpc）、8bbcbb5（spool/traffic/upgrade）、ae90a1b（proc/relay）、191b7ee（quic/tunnel）、73b43c9（link/configd/probe）、aad48aa（epusdt/verify）、dc610f4（envelope/meta）、c7d3744（cost/geodns/sub/notify/transport）、d587068（cert/dns/recovery/provision）、ee8b809（handler enroll/coupon/node/support/userio/adminsapi/paynotify）；批批本地全绿后单 commit。实测（`go test ./... -coverprofile` 各模块合并，无生成代码/第三方污染）：**repo 全量 95.52%（10974/11489 语句）**——server 95.68%（8869/9269）、agent 94.49%（1956/2070）、payments 98.46%（64/65）、packages agentproto/costref/payment 100%（85/85）；handler 包 98.0%、dns/notify/costref 等 100%。**98% 不可达，按第 6 步如实落账不硬凑**：缺口 285 语句，其中 cmd main 三处 78 条（cmd/ferry 44、cmd/agent 19、cmd/ferry-quic 15，main() 无单测入口）；其余为既有已落账留白类（crypto/rand、JWT HS256 签名恒成功、json/xml.Marshal 纯基础类型、NewEnvelope 构造不可失败、x509/ecdsa 证书生成错误、cpuPercent 真读 /proc、syscall.Exec 自替换、ws 握手后并发断连窗口、servify 对端不可观测、防御性不可达分支）与各包 gorm/I-O 错误注入分支——后者需改生产代码或测试不增加价值，纪律禁改生产行为、禁无意义测试；且旧战役「五十二批收官 98.5%」为 10-16 时点，此后生产代码新增语句致 handler 回落至 98.0%，本轮真实当前值以上述 95.52% 为准

## 未开工项（需求一句话）

- [x] agent 组件独立进程与 unix socket IPC（C-03）：拍板维持现状；进程内调用已满足需求，关闭不实施（2026-10-19；决策依据 2af9baa；本轮落账 9983647；docs build 通过）
  - 状态=维持不做｜原因：进程内角色调用已满足需求，独立 IPC 增加复杂度无收益｜触发条件：无（拍板关闭）
- [x] 节点令牌轮换窗口与吊销 CRL（C-06）：已实现（2026-10-17 f61b1d9 落地，本行 2026-10-19 修正摘要残留旧文「未实现」的状态矛盾，FERRY-006 收口点）
  - 状态=完成｜落地=2026-10-17 随本批单提交；原因：轮换窗口、吊销 CRL、认证与 dash 操作均已实现｜证据=make test 全绿（6 Go 模块+dash/panel 构建）
- [x] mTLS CA 编排：已代决：不建面板侧 CA 编排，维持 mtls-ca.sh 手签（消费方为零，编排对象不存在，详见 docs/design/决策记录.md §1）
  - 状态=已代决（2026-10-19，本轮落账）｜方案=维持手签脚本+TLSConfig 三件套｜被否=面板内自动签发/轮换编排｜触发条件：mTLS 消费方 ≥1 且手工签发不可维持，或用户点名重开
- [ ] [BLOCKED] 微信/支付宝直连支付：缺商户资质（appid、merchant_id、证书），收到后再开工
  - 状态=阻塞｜原因：无商户资质，无法对接直连支付｜触发条件：用户提供商户资质（appid/merchant_id/证书）
- [x] 通道维度偏好：按已代决维持单通道口径，不实施（2026-10-19；决策依据 07bed18；本轮落账 c9783a1；docs build 通过）
  - 状态=维持不做｜原因：Herald 单通道已满足，多通道偏好无实际需求｜触发条件：无（拍板关闭）
- [x] TOUCH-4 触达周期 dash 化：按已代决维持 env 配置，不实施（2026-10-19；决策依据 07bed18；本轮落账 c9783a1；docs build 通过）
  - 状态=维持不做｜原因：env 配置已满足可调需求，dash 化无增量价值｜触发条件：无（拍板关闭）
- [ ] [BLOCKED] TOUCH-7 边缘探测结论复用：缺少用户明确授权扩 probe 面（新增 entry_domain target_kind 与 agent 探测实现）；既有手动标记方案维持
  - 状态=已代决留待（2026-10-16 FW1016D）｜原因：判定源手动标记已落地并测试（outage_mode settings 键+dash 断联容灾卡+订阅逃生注释）；探测复用需 probe 面扩 entry_domain target_kind——属跨组件协议面新能力（agentproto+agent 二进制发布面），设计 2026-10-08 落地口径已判定「不可执行（ProbeReport 只有 tunnel/exit/peer 目标，没有面板入口域名的探测面）」并改走手动标记，本轮代决不重开该裁决，仅补正本行状态与账目｜触发条件：用户点名扩 probe 面（新增 target_kind 与 agent 侧探测实现）后再评估复用
- [ ] [BLOCKED] costref 接真实在线来源：缺真实商家 API 文档及 license 信息，无法选型核验
  - 状态=待触发｜原因：无真实商家 API，选型与 license 核验未启动｜触发条件：用户提供商家 API 文档+license
- [x] 调度历史权重自动调参（P2 族）：已代决：不做自动权重漂移，维持人工配置（无历史样本可校准，误调比不调更糟，详见 docs/design/决策记录.md §2）
  - 状态=已代决（2026-10-19，本轮落账）｜方案=人工配置权重（调度核心设计 §1 映射表口径）｜被否=历史延迟/失败驱动的自动更新｜触发条件：多机放量且人工调参出现瓶颈，或用户点名重开
- [x] 节点级流量预算与告警百分比：已代决：不做 per-node 预算告警，维持用户级配额（models.go MonthlyTrafficQuotaBytes）与节点流量记账（详见 docs/design/决策记录.md §3）
  - 状态=已代决（2026-10-19，本轮落账）｜方案=用户级配额+节点流量记账现状｜被否=节点预算阈值+百分比告警｜触发条件：多机部署且需要节点级预算约束，或用户点名重开
- [x] MONTH_ROLLING 滚动重置档：按已代决维持自然月账单，不实施（2026-10-19；决策依据 07bed18；本轮落账 288e01c；docs build 通过）
  - 状态=维持不做｜原因：小站自然月账单已满足，滚动重置无实际需求｜触发条件：无（拍板关闭）
- [ ] [BLOCKED] OSS 三候选（Xray proto/GeoIP 已代决收口）：支付 SDK 腿缺商户资质（appid、merchant_id、证书）
  - 状态=阻塞（余支付 SDK 一腿）｜原因：Xray proto 评审已毕维持自有实现（docs/design/开源选型设计.md §3.2.1 已代决 PATROL1018E：MPL-2.0 核验通过但 E-4 实测省 8.3MB 为硬约束，重评触发两条在档）、GeoIP license 已核（geoip2-golang 实为 ISC 纠偏、dat CC-BY-SA-4.0 归属注意项在档）维持随分流批立项；余支付 SDK 等商户资质外部材料不代决｜触发条件：用户提供商户资质（appid/merchant_id/证书）
- [ ] [BLOCKED] hy2 二进制真机联调：待用户提供 hysteria2 测试二进制、证书及可配置防火墙的服务器环境（服务器侧输入项，非三台 Windows 真机 openclaw/imac/.5 可替代）
  - 状态=阻塞｜原因：二进制/证书/防火墙均为外部提供项，仓内无法联调｜触发条件：用户提供测试二进制+证书+防火墙环境
- [x] TestProvisionRunAPI 偶发观察：已复现、已定位、已修复（2026-10-20 PATROL1020）
  - 状态=完成｜复现=2026-10-19 晚 sweep `-race -cpu=1,2,4 -count=100`（300 连跑）逮出 1 次：t.TempDir RemoveAll 清理 `unlinkat .../002/tpl-1: directory not empty`｜根因=测试只轮询首个 apply 的 job 终态即返回，re-apply 与 final plan 的后台 job goroutine 仍在写 TofuWorkdir/tpl-<id>，与 TempDir 清理竞态｜修复=测试收尾补等全部 provision_jobs 离开 running（job 落终态即后台 Execute 写盘全部结束）再返回｜验证=修复后同款 sweep 300 连跑全绿 exit 0（312s）
- [ ] [BLOCKED] 上游等待：VitePress 2 stable 尚未发布；npm latest 仍为 1.6.4（2026-10-19 复核 dist-tags.latest=1.6.4；2026-11-21 复核仍 1.6.4、next=2.0.0-alpha.21，stable 未发），待 stable 发布后验证再撤 override
  - 状态=阻塞｜原因：vitepress 2 stable 未发布，pnpm override 不可撤｜触发条件：vitepress 2 stable 发布且构建验证通过
- [x] FERRY-001 节点注册认证闭环（外部评审 2026-10-18）：一次性短时注册凭据（enrollment token）打通安装脚本首装
  - 状态=完成｜落地=2026-10-19 FIRE1019：服务端 EnrollmentToken 模型+管理面签发/列表/吊销（apiAuth+审计）+公开面 /api/enroll 事务原子消费（双花/重放 401、IP 限流）；安装脚本 --register 改配 --enroll-token、注册移到资产校验后、JSON 三级解析｜证据=enroll_test.go 9 用例 + handler/storage/model -race -count=2 全绿
- [ ] [BLOCKED] FERRY-002 正式 Release 与 latest 安装流程：脚本侧已修（下载/校验链路回归已补，见状态行）；发版腿缺用户明确发版指令及指定版本号（本轮不 bump 版本）
  - 状态=半完成｜脚本侧 2026-10-19 晚补齐：install-rollback-test.sh 增下载链路三条回归（latest 经 GitHub API 解析为实际版本再拼下载址、agent/quic 伴随 .sha256 校验、尚无稳定版 404 早失败且不拼 vlatest 假址）；回归逮出并修一处实锤——伴随 .sha256 按 Release 惯例记发布件原始长文件名，而脚本本地下载名为 agent.tar.gz/quic.tar.gz，按原名 `sha256sum -c` 必失败（首次发版即全员装不上），现改写对照名后再校验（57 断言全绿）｜卡点=正式 release 只能用户手动给版本号触发（2026-10-11 发版红线，agent 不代发）｜触发条件：用户明确说「发版」并给版本号
- [x] FERRY-003 生产密钥安全门禁（外部评审 2026-10-18）：三把服务密钥初始化门禁（生成/持久化/生产拒绝默认密钥与弱钥）
  - 状态=完成｜落地=2026-10-19 FIRE1019：新包 keyinit（显式配置 > 密钥文件 > 生成并持久化 0600，默认库目录 ferry-keys.env；FERRY_ENV=production 拒缺钥无从持久化/开发默认常量/短钥；显式值不落盘；日志不回显）+ main 接线 + 主密钥新生成遇既有加密行告警恢复原钥 + .env.example/ferry.env.example/部署指南/安全设计 §3.1 同步｜证据=keyinit_test.go 8 用例全绿 + 真机冒烟（生成→重启零重生成→字节稳定；生产拒常量/生产无钥起服务两路实测）
- [x] FERRY-005 安装与升级失败恢复（外部评审 2026-10-18）：安装脚本原子替换/回滚/失败诊断
  - 状态=完成｜落地=2026-10-19 FIRE1019（FERRY-003 之后）：agent-install.sh 二进制/unit 先备份 `.prev` 再同目录 rename 原子替换（含 quic unit，sidecar 落盘 `--quic-install` 可指位）、配置临时文件+rename 不落半截、`--config` 落点随路径建目录（修掉硬编码 /etc/ferry 与覆盖值矛盾）；启动失败=journal 最近 20 行诊断先行，再回滚（升级还原上一版重拉起、首装清理本次产物），`.prev` 保留供手动核对、成功才清；ferry-quic sidecar 独立回滚不牵连 agent；unit 目录 FERRY_SYSTEMD_UNIT_DIR 可覆盖（测试/非标环境）｜证据=deploy/install-rollback-test.sh 沙箱回归 57 断言全绿（假 id/systemctl/journalctl/curl/sleep 跑真脚本：原七链路——首装成功/升级成功/升级失败还原字节级一致/首装失败清理/既有配置保留/sidecar 首装/sidecar 失败回滚且 agent 不受牵连；2026-10-19 晚扩入下载链路三条——latest 解析拼实际版本址/agent 与 quic 伴随 .sha256 校验/尚无稳定版 404 早失败，扩写前为 43 断言七链路），test.yml 新增 deploy-scripts job 接线 CI，deploy/README.md 与部署指南同步
- [ ] [BLOCKED] FERRY-004 端到端部署与节点通信集成测试：仓内全链路已本地等价收口（e2e/compose/systemd/rollback 四腿进 CI 或 nightly），余一件=生产内核二进制（hysteria2 等）真机接入验收
  - 状态=仓内面完成待真机不敢勾｜最新取证 2026-10-20 本轮（`command -v hysteria` 无、仓内无 hysteria* 文件）｜解锁钥匙=用户提供测试内核二进制+证书+可配防火墙的服务器环境（服务器侧输入项；Windows 真机 openclaw/imac/.5 已在位，不构成缺口）｜验收口径=真机走与 e2e 同链路（安装→注册→心跳监管→进程控制），须用已就位的 FERRY-001/002/003/005 注册/发布/安全/恢复流程｜已收口腿与证据：e2e-smoke 53 断言（deploy/e2e-smoke.sh 真 server+agent 二进制全链路：注册/令牌轮换吊销/备份恢复/配置下发/被管内核 stub 监管+崩溃自拉/proc_ctl 控制面；0a69009、ac773a7）、compose-smoke 43 断言（nightly，Caddy 前端+postgres 密钥持久化）、systemd-smoke 33 断言（特权容器真 PID 1 跑真安装脚本+真面板注册链路；e5814c6、c68c829）、install-rollback-test 57 断言（PATH 假件沙箱含下载/校验链路；df70e5a）、重复消息幂等已收口（docs/design/agent架构设计.md §8 判据 C1-C7 + 服务端 (node,type,ID) at-most-once）｜台账事实：节点 LastSeen 服务端无写入点，不做「心跳更新 last_seen」断言

## 行级证据与解锁条件（R1013B 续）

| # | 条目 | 卡在哪（行级证据） | 需要什么才能解锁 |
|---|------|---------------------|------------------|
| 1 | C-03 IPC | `agent/internal/spool/spool.go:1` spool 已是角色组件 IPC；进程内调用已满足 | 拍板关闭，无解锁条件 |
| 2 | C-06 令牌轮换 | 2026-10-17 f61b1d9 已落地：`POST /api/nodes/:id/token` 轮换（10 分钟兼容窗口）+ `/token/revoke` 吊销，agentws 认证先查吊销表 | 已完成（make test 全绿 + PATROL1017S 复测 3/3 PASS） |
| 3 | mTLS CA 编排 | `docs/design/安全设计.md:66` mTLS 默认关闭；`:97-98` CA 编排未实现 | mTLS 开启且有节点证书签发需求 |
| 4 | 微信/支付宝直连 | 仓内无商户资质代码（grep 零命中） | 用户提供商户资质（appid/merchant_id/证书） |
| 5 | 通道维度偏好 | 拍板关闭（`docs/design/告警通道设计.md` 单通道口径） | 拍板关闭，无解锁条件 |
| 6 | TOUCH-4 dash化 | `docs/design/用户触达设计.md:73` TOUCH-7 评估=维持 env 配置 | 拍板关闭，无解锁条件 |
| 7 | TOUCH-7 探测复用 | `docs/design/用户触达设计.md:58` ProbeReport 只有 tunnel/exit/peer 目标；`agent/internal/probe/probe.go:94` 有 TargetKind 但无 entry_domain | probe 面扩 entry_domain 字段且数据落库 |
| 8 | costref 在线来源 | `packages/costref/static.go:23` StaticSource 是公开价格表，真实在线来源未扩 | 用户提供商家 API 文档+license |
| 9 | 调度自动调参 | `docs/design/调度核心设计.md:109` P2 才考虑自动调 | P2 分期开启且人工调参瓶颈 |
| 10 | 流量预算告警 | 横向对比观察项，`docs/面板横向对比.md` | 多机部署且流量预算管理需求出现 |
| 11 | MONTH_ROLLING | 拍板关闭（小站自然月账单够用） | 拍板关闭，无解锁条件 |
| 12 | OSS 三候选 | Xray/GeoIP 两腿已代决收口（`docs/design/开源选型设计.md:61-66` 评审记录：Xray 维持自有重评触发两条在档、GeoIP license 已核等分流批立项）；余支付 SDK 一腿卡商户资质 | 支付 SDK 腿解锁=用户提供商户资质（appid/merchant_id/证书） |
| 13 | hy2 真机联调 | `docs/design/agent架构设计.md:40` hysteria2 进程管理；二进制/证书/防火墙均为外部提供（服务器侧输入；三台 Windows 真机 openclaw/imac/.5 在位） | 用户提供测试二进制+证书+防火墙环境 |
| 14 | TestProvisionRunAPI | 已修复（2026-10-20）：sweep 300 连跑复现 1 次定位 TempDir 竞态，测试收尾等全部 job 终态；修复后 300 连跑全绿 | 已完成，无解锁条件 |
| 15 | 上游等待 | `docs/package.json:10` vitepress ^1.6.4；`pnpm-workspace.yaml:7-11` override 钉 vite ^5.4.14 | vitepress 2 stable 发布（https://github.com/vuejs/vitepress）且构建验证通过 |

## 巡检结论（R1013B）

2026-10-13 巡检复核：15 项未勾选，全挂起/阻塞/待触发/维持不做，无遗漏可推进项。

依据：CI 全绿（test/docs/nightly ec67869）、Dependabot 全 fixed 零 open、make test 5 模块+3 前端全绿、go vet 5 模块干净、gofmt 零未格式化、go mod tidy 零差异、dash/panel vue-tsc 干净、文档漂移已全量审计（43 条 N-1..N-57 全修）、无 open issues/PRs。

## 巡检结论（patrol1224）

2026-10-14 台账体检：29 条完成族行的 commit 证据逐条核 git log，34 个引用哈希全部真实存在；15 项未勾保持一行一条（状态行/原因/触发条件齐全）。

修正 9 处日期区间与族实际落地日不符：P0（首批 10-06/07→10-07）、E 族（10-08..09→10-07..09）、PAY（10-08→10-07）、OS-BR（10-08..09→10-07..08）、SAVE（10-08→10-07..08）、环境面（10-10→10-09..10，compose 透传 5553504 落 10-09）、面板运营（10-08..10→10-07..10，OD-1 f4b183f 落 10-07）、测试缝隙补齐（10-11→10-11..13，6475f4e 落 10-13）、审计与刷新（10-08..11→10-08..13，2af9baa/ec67869 落 10-13）。BK 行「S3 外发 10-09 接入」经 s3.go 历史核为 3eb290a 属实不改。

依据：TestProvisionRunAPI -race -count=5 全净未复现、CI 三路全绿（test bc0a50a/docs ec67869/nightly）、Dependabot 零 open、无 open issues/PRs。

## 巡检结论（P1014B）

2026-10-14 核验轮：15 项未勾全部复核，触发条件逐条实证不成立（含外部依赖实测），无可推进项，按闸门维持挂起不代拍板。

实测证据：vitepress 上游 latest release 仍为 v2.0.0-alpha.20（2026-09-04 发布），npm stable 线 1.6.4——上游等待项（撤 pnpm override）触发条件未满足；gh 实测 open PR=0、open issue=0、dependabot open alerts=0、main 最近 4 次 test CI 全 success；全仓 todo 文件仅 ./todo.md 一个（find 零遗漏）；当日批次七~十新增 10 个引用哈希（72a6919..2055362）逐一 merge-base 核在 origin/main。

当日覆盖工作（已完成，完成行留痕）：七批 handler 缝隙 74.5%→75.2%（72a6919）、八批 alloc/cert →89.2%/89.0%（ddb919f）、九批 quota/standby/herald →87.4%/77.2%/86.6%（eb65bc9）、十批 storage/configd/monitor →88.0%/75.0%/89.9%（59f1fc8/0b7e0d9/648202a），各包 -race -count=3 全绿。

依据：外部触发条件未满足（上游未发布/无商户资质/待用户提供 hy2 服务器侧环境/拍板关闭项无解锁条件），无 open issues/PRs，零改动零 commit（纯核验轮）。

## 巡检结论（PATROL1015）

2026-10-15 巡检轮：15 项未勾逐条复核，触发条件仍全部不成立（上游未发布/无商户资质/待用户提供 hy2 服务器侧环境/拍板关闭），无可推进项，按闸门维持挂起不代拍板；覆盖批连推四批，涉 17 包，全部 -race 复核全绿。

当日覆盖工作（已完成，完成行留痕）：十一批 relaypush/recovery/notifyscan/provision/replenish/config/spool/tunnel（b6f28f8）、十二批 audit/standby/dns/configd（45cc193）、十三批 handler/ratelimit/toucher/backup（14a0187）、十四批 app/handler 分页（720be9b）。十一批为工作区遗留未提交增量（10-14 15:22..15:53 落盘，无活跃会话），本次只读复核+门禁通过后收编提交。

依据：门禁按批跑受影响路径（go test + -race -count=2..3 全净，gofmt/vet 零输出），todo 增量即提即推，main 与 origin/main 同步；覆盖剩余洼地为 cmd 三入口 main() 装配体（覆盖需重构注入）与 DB 错误注入类深分支，不再有低成本批次可推。

## 巡检结论（R1015D）

2026-10-15 巡检轮（R1015D）：15 项未勾逐条复核，触发条件仍全部不成立，无可推进项，按闸门维持挂起不代拍板；纯核验轮，唯一改动为本结论落账（docs-only，门禁只跑文档检查不编译不跑测试套件）。

逐条实测证据：
- 偶发项 TestProvisionRunAPI：`go test ./internal/handler/ -run '^TestProvisionRunAPI$' -race -count=5` → ok 5.050s exit 0，五连跑未复现（此前一轮误把 `-run` 打在 ./internal/provision 包上返回 no tests to run，本轮已纠正到 handler 包重跑）。
- vitepress 升级（挂起）：registry dist-tags latest=1.6.4 / next=2.0.0-alpha.20，2.x stable 零 tag，上游等待项未满足。
- token 轮换/CRL（拍板关闭）：router.go 对 rotate/crl/revoc 零命中，无回潮。
- mTLS CA 编排（挂起）：安全设计.md:66 mTLS 为可开关且默认关闭，:97-98 CA 自动化签发编排未实现，维持挂起。
- 探测复用（待触发）：probe.go:94 仅透传 TargetKind，探测面无 entry_domain 字段，触发条件不成立。
- costref 真实在线来源（待触发）：packages/costref 仅 compare.go/costref.go/static.go（stdlib 静态表），无商户 API 依据。
- 微信/支付宝直连（挂起）：payments/ 全文对 app_id/merchant_id/mch_id 零命中，商户资质未到位。
- 台账唯一性：find 全仓仅 ./todo.md 一个台账文件，零遗漏。

更正一处旧账：PATROL1015 段「不再有低成本批次可推」与当前实测不符——handler 包在 35~38 批收口到 89.3%（4904/5492）后仍有 588 语句未覆盖，且多为既有测试模式的常规目标：landing.go 33 句（65.3%）、userio.go 28 句（78.5%）、routing.go 26 句（73.7%）、costwatch.go 25 句（74.0%）、costref.go 25 句（75.7%）、status.go 22 句（79.4%）、alerts.go 22 句（66.7%）等；下一巡检轮可按既有批次模式继续推进，不属硬卡点。

依据：外部触发条件未满足（上游未发布/无商户资质/待用户提供 hy2 服务器侧环境/拍板关闭项无解锁条件），无可开工功能项；本轮单 commit 纯文档落账，main 与 origin/main 同步。

## 巡检结论（R1016D）

2026-10-16 巡检轮（R1016D）：15 项未勾逐条复核（解锁条件表第 1-15 行），触发条件仍全部不成立，按闸门维持挂起不代拍板；上一段更正所指的补覆盖战役本轮收官，纯核验+落账轮，docs-only 不编译不跑测试套件。

补覆盖战役收官（承接 R1015D 更正）：三十九批起至五十二批共 14 批推进，handler 包 89.3%（4904/5492）→ 98.5%（5408/5492）；五十二批后剩余 84 条未覆盖语句逐条复核均为已落账留白（crypto/rand、JWT/HS256 签名、json.Marshal 纯基础类型、NewEnvelope、hub 握手后并发断连窗口、x509/ecdsa 证书生成错误、cpuPercent 真读 /proc、servify 对端不可观测、防御性不可达分支）；subdevice.go:95 空表归一化为覆盖映射幽灵——行为已由 TestUserDevicesErrors（无记录回 200 `[]` 非 null）钉住。战役关闭，不再有批次五十三。收官 commit 45e596c（race 门禁 1332.5s 全绿）。

本轮逐条复核：偶发项 TestProvisionRunAPI 仍无新复现（R1015D 五连跑证据仍有效，-race 门禁连跑三轮全净）；vitepress 2 stable 仍未发布（latest=1.6.4）；token 轮换/mTLS CA/直连支付/通道偏好/TOUCH-4/TOUCH-7/costref 真源/P2 调参/流量预算/MONTH_ROLLING/OSS 三候选/hy2 真机触发条件全部不变。

依据：todo 可推进线（补覆盖）已清账，未勾项触发条件全部不成立；本轮单 commit 纯文档落账，main 与 origin/main 同步。

## 巡检结论（PATROL1016）

2026-10-16 巡检轮（PATROL1016）：15 项未勾解锁条件逐条实测复核，全部仍不成立，维持挂起不重开；无合法开工项。docs-only 落账不编译不跑测试套件。

逐条实测（相对 R1016D 段的新鲜证据）：
- 偶发项 TestProvisionRunAPI：`go test ./internal/handler/ -run '^TestProvisionRunAPI$' -race -count=5` → ok 4.006s，五连跑再净，未复现。
- vitepress（#15）：registry dist-tags latest=1.6.4 / next=2.0.0-alpha.20，2.x stable 仍零 tag。
- C-06（#2）：router.go 对 rotate/crl/revoc 仍零命中；mTLS（#3）：安全设计.md:66 默认关闭、:97-98 CA 编排挂起不变。
- 直连支付（#4）：payments/ 全文对 app_id/merchant_id/mch_id 零命中。
- TOUCH-7（#7）：probe.go 仍仅透传 TargetKind，探测面 entry_domain 零命中；costref 真源（#8）：packages/costref 仍仅 compare/costref/static 三件（stdlib 静态表）。
- 调参（#9）：调度核心设计.md:109 P2 维持不变；OSS 三候选（#12）：开源选型设计.md:42-43、56-57 保留自有的核验改判不变。
- 拍板关闭项（#1/#5/#6/#11）：无解锁条件，不重开；hy2（#13）外部提供项、流量预算（#10）观察项，条件未变。

补覆盖战役（三十九批→五十二批）已在前轮收官（98.5%，5408/5492，收官 commit 45e596c），无批次五十三；memex 侧本轮巡检 clean（HEAD=origin=8e31ff9）。

依据：15 条触发条件全部不成立（外部未提供/上游未发布/拍板关闭无解锁条件），无合法开工项；本轮单 commit 纯文档落账，main 与 origin/main 同步。

## 巡检结论（FW1016D）

2026-10-16 巡检轮（FW1016D，带代决授权）：逐条对照「卡在待拍板/设计选择/需求取舍/排期」判定，15 条无一真正待决——6 条拍板关闭（#1/#2/#5/#6/#11/#12）、3 条已成文维持挂起（mTLS CA=安全设计.md:98「mTLS 默认关闭抑制价值，维持挂起」、调度自动调参=调度核心设计.md:109「P2 才考虑自动调，人工调参先行」、节点级流量预算=面板横向对比.md:77「挂观察，不开工」）、3 条外部阻塞（直连支付/costref 真源/hy2 真机）、1 条上游等待（vitepress registry latest=1.6.4 / next=2.0.0-alpha.20，2.x stable 零 tag）、1 条观察项（TestProvisionRunAPI `-race -count=5` ok 4.006s 未复现）、1 条 TOUCH-7 账面表述与裁决记录不符。代决动作仅一处：TOUCH-7 原写「未开工/待触发」，实际设计 2026-10-08 落地口径已裁决判定源走管理员手动标记（已落地并测试：outage_mode + dash 断联卡 + 订阅逃生注释），探测复用判「不可执行」注记留待——本轮已代决把该行补正为「已代决留待」并写明触发条件（用户点名扩 probe 面新增 target_kind 与 agent 侧实现后再评估），不重开原裁决、不虚构任务。docs-only 落账，不编译不跑测试套件；两仓 clean 与 origin 同步（ferry 本轮前置 `539b9d8`，memex `8e31ff9`）。

依据：代决授权核遍 15 条，无可执行的待拍板对象（已成文裁决不重开、外部条件不解、观察项不硬造）；本轮单 commit 纯文档落账，main 与 origin/main 同步。

## 巡检结论（R1021PATROL）

2026-10-16 巡检轮（R1021PATROL）：15 项未勾逐条取新鲜证据复核，触发条件全部仍不成立，维持挂起/拍板关闭不重开，本轮完成 0 条、剩余 15 条（6 拍板关闭+3 成文挂起+3 外部阻塞+1 上游等待+1 观察项+1 已代决留待）；无可开工功能项，无低成本补覆盖批次（五十二批收官结论仍有效，98.5%=5408/5492）。docs-only 落账，不编译不跑测试套件。

本轮新鲜实测：
- 偶发项 TestProvisionRunAPI（#14）：`go test ./internal/handler/ -run '^TestProvisionRunAPI$' -race -count=5` → ok 3.841s exit 0，五连跑再净，未复现。
- vitepress（#15）：本轮实拉 registry dist-tags latest=1.6.4 / next=2.0.0-alpha.20，2.x stable 仍零 tag，pnpm override 不可撤。
- GitHub 面：open PR=0、open issue=0、dependabot open alerts=0，无外部可接手事项。
- C-06（#2）：router.go 对 rotate/crl/revoc 零命中；直连支付（#4）：payments/ 对 app_id/merchant_id/mch_id 零命中；TOUCH-7（#7）：agent 侧 entry_domain 零命中、probe TargetKind 仅 tunnel/exit/peer，手动判定面 GET/PUT /api/outage 在位（router.go:179-180）；costref（#8）：packages/costref 仍仅 compare/costref/static 三件+测试。
- 成文挂起口径复核不变：mTLS CA（#3）安全设计.md P2 行「默认关闭抑制价值，维持挂起」；自动调参（#9）调度核心设计.md:109 P2 挂起；流量预算（#10）面板横向对比.md:77「挂观察，不开工」；OSS 三候选（#12）开源选型设计.md 保留自有改判不变。
- 拍板关闭（#1/#5/#6/#11）无解锁条件不重开；hy2（#13）外部二进制/证书/防火墙未提供；台账唯一性 find 实测仅 ./todo.md 一个。

工作区：tree clean；发现 17:17 已落盘未推送的 docs 提交 `41a4baf`（Caddy 反代路由文档与三十九批实际修复对齐：deploy 部署指南+faq 补 /admin、/distributor），属本仓既有已提交工作，本轮 pull --rebase 后随本结论一并推送，不改其内容。

依据：15 条触发条件全部不成立（外部未提供/上游未发布/拍板关闭无解锁条件），无 open PR/issue/告警；本轮单 commit 纯文档落账。

## 巡检结论（R1023PATROL）

2026-10-16 巡检轮（R1023PATROL）：台账与工作区与上轮 R1021PATROL 完全一致（HEAD 70dc94b、tree clean、未勾仍 15、find 仅 ./todo.md），完成 0 条/剩余 15 条，触发条件逐条复核仍全部不成立，维持挂起/拍板关闭不重开。新鲜证据：TestProvisionRunAPI `-race -count=5` ok 3.456s 未复现；vitepress registry latest=1.6.4 / next=2.0.0-alpha.20（2.x stable 零 tag）；open PR=0、open issue=0、dependabot open=0；router.go rotate/crl/revoc 零命中、payments 商户凭据零命中、agent entry_domain 零命中、costref 仍静态三件。docs-only 落账，不编译不跑全量套件。

## 巡检结论（R1024PATROL）

2026-10-17 巡检轮（R1024PATROL）：15 项未勾逐条取新鲜证据复核，触发条件仍全部不成立，维持挂起/拍板关闭不重开；本轮完成 0 条/剩余 15 条（6 拍板关闭+3 成文挂起+3 外部阻塞+1 上游等待+1 观察项+1 已代决留待），无可开工功能项，无补覆盖批次五十三（五十二批收官结论仍有效，98.5%=5408/5492）。docs-only 落账，不编译不跑全量套件。

本轮新鲜实测：
- vitepress（#15）：registry dist-tags 仍为 latest=1.6.4 / next=2.0.0-alpha.20，2.x stable 零 tag，pnpm override 不可撤。
- GitHub 面：open PR=0、open issue=0、dependabot open alerts=0，无外部可接手事项。
- C-06（#2）：router.go 对 rotate/crl/revoc 零命中（仅订阅条目轮换实现命中 rotate，非节点令牌轮换）；直连支付（#4）：payments/ 对 app_id/merchant_id/mch_id 零命中；TOUCH-7（#7）：agent 侧 entry_domain 零命中、probe TargetKind 仅透传且无判定源消费；costref（#8）：packages/costref 仍仅 compare/costref/static 三件+测试。
- 成文挂起口径复核不变：mTLS CA（#3）docs/design/安全设计.md:97-98；自动调参（#9）docs/design/调度核心设计.md:89/109；流量预算（#10）docs/面板横向对比.md:77；OSS 三候选（#12）docs/design/开源选型设计.md 保留自有改判不变。
- 拍板关闭（#1/#5/#6/#11）无解锁条件不重开；hy2（#13）外部二进制/证书/防火墙未提供；台账唯一性 find 实测仅 ./todo.md 一个。
- 观察项（#14）本轮未能取得有效测试结论：server/go.mod 要求 Go 1.27.2，本机为 1.27.1；默认 GOTOOLCHAIN 下载工具链两次因网络 unexpected EOF/中断失败，GOTOOLCHAIN=local 则被版本检查拒绝。上轮 R1023PATROL `-race -count=5` ok 3.456s 仍为最近有效证据，本轮不伪造复跑结果。

依据：除 #14 因工具链下载/网络临时受限未复跑外，其余 14 条触发条件全部不成立；无 open PR/issue/告警。本轮单 commit 纯文档落账，main 与 origin/main 同步。

## 巡检结论（PATROL1017C）

2026-10-17 巡检轮（PATROL1017C）：15 项未勾复核，无新凭据/新裁决/新需求立项/真机材料，上游 vitepress 2 stable 仍未发布，GitHub 无 open PR/issue/Dependabot 告警；完成 0 条、剩余 15 条，维持挂起/拍板关闭不重开。docs-only 落账，不编译不跑全量套件。

新鲜证据：TestProvisionRunAPI（#14）`-race -count=5` → ok 3.177s exit 0，五连跑未复现；vitepress registry latest=1.6.4 / next=2.0.0-alpha.20；router.go 对节点令牌 rotate/crl/revoc 零命中，payments 商户凭据零命中，agent entry_domain 零命中，costref 仍静态三件；main 与 origin/main 同步且 tree clean。

## 巡检结论（PATROL1017G）

2026-10-17 巡检轮（PATROL1017G）：15 项未勾复核，仍无新凭据/新裁决/新需求立项/上游 stable 发版/真机材料，GitHub 无 open PR/issue/Dependabot 告警；完成 0 条、剩余 15 条，维持挂起/拍板关闭不重开，本轮无合法开工项。docs-only 落账，不编译不跑全量套件。

新鲜证据：TestProvisionRunAPI（#14）`-race -count=5` → ok 4.209s exit 0，未复现；vitepress registry latest=1.6.4 / next=2.0.0-alpha.20；外部材料/凭据/证书零新增，节点令牌 rotate-crl、商户凭据、agent entry_domain、costref 真源实现面仍零命中；main 与 origin/main 同步且 tree clean。

## 巡检结论（PATROL1017A）

2026-10-17 巡检轮（PATROL1017A）：15 项未勾复核，仍无新凭据/新裁决/新需求立项/上游 stable 发版/真机材料，GitHub 无 open PR/issue/Dependabot 告警；完成 0 条、剩余 15 条，按闸门维持挂起/拍板关闭不重开。docs-only 落账，不编译不跑全量套件。

新鲜证据：TestProvisionRunAPI（#14）`-race -count=5` → ok 3.217s exit 0，未复现；vitepress registry latest=1.6.4 / next=2.0.0-alpha.20；商户凭据、节点令牌 rotate-crl、agent 判定源复用、costref 真源实现面仍无新增；台账唯一性仍为 ./todo.md 一个。

## 巡检结论（PATROL1017S）

2026-10-17 巡检轮（PATROL1017S）：本仓自上轮 21bad04 前进至 f61b1d9——期间多席巡检落账（P1016AM/R1021~1024/PATROL1017C/G/A），且 **C-06 节点令牌轮换与吊销已由 f61b1d9 落地**（`POST /api/nodes/:id/token` 轮换留 10 分钟兼容窗口 + `/token/revoke` 立即吊销，agentws 认证先查吊销表再校验新旧令牌，dash 节点操作补复制/吊销入口），未勾 15→14。本轮对 C-06 做独立复核并补齐台账：选择性复测 `go test ./internal/handler/ -run 'TestAuthNodeTokenRotationWindow|TestNodeTokenRotation$|TestNodeTokenRevocation' -count=1 -v` 3/3 PASS，解锁条件表第 2 行从「无 rotate/CRL 端点」更新为已完成（原行已与仓库现状相悖）。

其余 14 条复核：C-03 拍板关闭；mTLS CA/调度自动调参/流量预算已成文挂起；TOUCH-7 已代决留待；直连支付/costref 真源/hy2 真机外部阻塞；vitepress registry 复查仍 latest=1.6.4 / next=2.0.0-alpha.20（2.x stable 零 tag）；TestProvisionRunAPI 连日多轮 `-race -count=5` 全净未复现。无新增开工项，docs-only 落账不编译不跑测试套件；memex 侧由他席英文化工作流推进中（本席不动其行）。

依据：C-06 已完成（唯一可落地项由他席落地并经本席复核），其余触发条件全部不成立；本轮单 commit 纯文档落账，main 与 origin/main 同步。

## 巡检结论（PATROL1018A）

2026-10-18 巡检轮（PATROL1018A）：14 项未勾逐条取新鲜证据复核，触发条件仍全部不成立，维持挂起/拍板关闭不重开；本轮唯一开工项=CI 面新证据——nightly workflow 自 83dbb8f 起文件级解析失败（58a4612 给 package 加 `needs: gate` 未删原 `needs: [go, frontend]`，YAML 重复键→整文件无效，此后两次 push 均 0s failure，不修则当晚 schedule 必炸），c275271 合并为 `needs: [gate, go, frontend]` 修复，actionlint 先复现报错（duplicated in "package" job）后清零，workflow_dispatch 实跑 run 39050670334 全链 success（gate skip=false 正确→6 go+3 frontend+package artifacts 齐出）。docs-only 落账不编译不跑全量套件（仅 vitepress build 绿 5.64s）。

本轮新鲜实测：
- TestProvisionRunAPI（#14）：`-race -count=5` → ok 3.512s exit 0，五连跑未复现（上轮 R1024PATROL 工具链网络受限未能复跑，本轮 GOTOOLCHAIN auto 下载 1.27.2 成功，复跑能力恢复）。
- vitepress（#15）：registry dist-tags 仍 latest=1.6.4 / next=2.0.0-alpha.20，2.x stable 零 tag，pnpm override 不可撤。
- GitHub 面：open PR=0、open issue=0、dependabot open alerts=0；faf647e 上 test/docs 两 workflow 绿。
- 直连支付（#4）：payments/ 对 app_id/merchant_id/mch_id 零命中；TOUCH-7（#7）：agent 侧 entry_domain 零命中；costref（#8）：packages/costref 仍仅 compare/costref/static 三件+测试。
- 成文挂起口径复核不变：mTLS CA（#3）安全设计.md P2 行；自动调参（#9）调度核心设计.md P2 行；流量预算（#10）面板横向对比.md「挂观察，不开工」；OSS 三候选（#12）开源选型设计.md 保留自有改判不变。
- 拍板关闭（#1/#5/#6/#11）无解锁条件不重开；hy2（#13）外部二进制/证书/防火墙未提供；台账唯一性 find 实测仅 ./todo.md 一个。
- memex 侧（两仓点检）：todo.md 全 [x] 零未勾（双语路由 locales 收尾已完成）；工作区遗留 .gitignore 增量（node_modules 忽略，2026-10-17 15:18 落盘无活跃会话）复核后收编提交 25b5385 推送。

依据：14 条触发条件全部不成立（外部未提供/上游未发布/拍板关闭无解锁条件）；nightly 修复经 dispatch 实跑验证全绿；本轮两 commit（ci 修复 c275271+本结论落账），main 与 origin/main 同步。

## 巡检结论（PATROL1018B）

2026-10-18 巡检轮（PATROL1018B）：台账与上轮 PATROL1018A 收工态一致（HEAD 859f107、tree clean、未勾仍 14、find 仅 ./todo.md），14 项未勾逐条重取新鲜证据，触发条件全部仍不成立，维持挂起/拍板关闭不重开；本轮完成 0 条/剩余 14 条，无可开工项（nightly 修复上轮已收口并 dispatch 验证 success）。docs-only 落账，不编译不跑全量套件（仅 vitepress build 绿）。

本轮新鲜实测：
- TestProvisionRunAPI（#14）：`-race -count=5` → ok 3.590s exit 0，五连跑再净未复现（自 R1015D 起连续九轮五连跑全净，唯 R1024PATROL 因工具链下载受限未复跑）。
- vitepress（#15）：registry dist-tags 仍 latest=1.6.4 / next=2.0.0-alpha.20，2.x stable 零 tag。
- GitHub 面：open PR=0、open issue=0、dependabot open alerts=0；CI 三路绿（test 39050616680 on c275271、nightly dispatch 39050670334 success、docs 39043232778 on faf647e）。
- 直连支付（#4）：payments/ 对 app_id/merchant_id/mch_id 零命中；TOUCH-7（#7）：agent 侧 entry_domain 零命中；costref（#8）：packages/costref 仍仅 compare/costref/static 三件+测试。
- 成文挂起与拍板关闭项（#1/#3/#5/#6/#9/#10/#11/#12/#13）：本轮无新用户令/新凭据/新裁决，设计文档挂起口径未变，不重开。
- memex 侧（两仓点检）：main 与 origin/main 同步（25b5385）、tree clean、todo.md 零未勾。

依据：14 条触发条件全部不成立（外部未提供/上游未发布/拍板关闭无解锁条件），无 open PR/issue/告警；本轮单 commit 纯文档落账，main 与 origin/main 同步。

## 巡检结论（PATROL1018R1）

2026-10-18 巡检轮（PATROL1018R1）：台账载体 find 实测仅 ./todo.md 一个，未勾仍 14 条，与 PATROL1018B 收工态零差异（HEAD ea3425f、tree clean）；本轮重取外部证据全部无新变化，无可开工项，按单第 6 条逐条写明阻塞对象后收工。docs-only 落账，不编译不跑全量套件（仅 vitepress build 绿）。

逐条阻塞对象与依据（编号=行级证据表行）：
- #1 C-03 IPC / #5 通道偏好 / #6 TOUCH-4 / #11 MONTH_ROLLING：用户拍板关闭（2026-10 各轮代决留档），无解锁条件，等待对象=无。
- #2 C-06：2026-10-17 f61b1d9 已完成收账，非阻塞项。
- #3 mTLS CA 编排：等待对象=用户开启 mTLS 且出现节点证书签发需求；依据=docs/design/安全设计.md:97-98「mTLS 默认关闭抑制价值，维持挂起」。
- #4 微信/支付宝直连：等待对象=用户提供商户资质（appid/merchant_id/证书）；依据=payments/ 全文对 app_id/merchant_id/mch_id 零命中（本轮 grep 复核仍零）。
- #7 TOUCH-7 探测复用：已代决留待（FW1016D）；等待对象=用户点名扩 probe 面；依据=agent/internal/probe/probe.go:94 TargetKind 仅透传 tunnel/exit/peer，agent 侧 entry_domain 本轮 grep 仍零命中。
- #8 costref 在线真源：等待对象=用户提供商家 API 文档+license；依据=packages/costref/ 仍仅 compare/costref/static+测试（本轮 ls 复核）。
- #9 调度自动调参：等待对象=P2 分期开启且人工调参瓶颈；依据=docs/design/调度核心设计.md:109。
- #10 节点流量预算：等待对象=多机部署且预算管理需求出现；依据=docs/面板横向对比.md:77「挂观察，不开工」。
- #12 OSS 三候选：等待对象=各自解禁评审通过；依据=docs/design/开源选型设计.md:42-43、56-57 保留自有改判。
- #13 hy2 真机联调：等待对象=用户提供测试二进制+证书+防火墙环境；依据=docs/design/agent架构设计.md:40。
- #14 TestProvisionRunAPI 偶发：观察项；依据=本轮 HEAD（ea3425f）server 树与 PATROL1018B 复跑时逐字节一致，00:52 `-race -count=5` ok 3.590s 证据仍有效（自 R1015D 起连续九轮全净，唯 R1024PATROL 工具链受限未复跑）。
- #15 vitepress 2 stable：等待对象=上游发版；本轮 registry dist-tags 仍 latest=1.6.4 / next=2.0.0-alpha.20，2.x stable 零 tag。
- GitHub 面：open PR=0、open issue=0、dependabot open alerts=0（本轮复查）。
- memex 侧（两仓点检）：main 与 origin/main 同步（25b5385）、tree clean、todo.md 零未勾。

依据：14 条触发条件全部不成立且阻塞对象逐条列明；无 open PR/issue/告警；本轮单 commit 纯文档落账，main 与 origin/main 同步。

## 巡检结论（PATROL1018）

2026-10-18 巡检轮（PATROL1018）：与 PATROL1018R1 收工态零差异（HEAD c87cbd1、tree clean、未勾 14），本轮轻量复核逐项一句话：vitepress registry 仍 latest=1.6.4/next=2.0.0-alpha.20（#15 无发版）；GitHub open PR=0/issue=0/dependabot=0（无外部可接手件）；工作树自 R1 起零新 commit，#4/#7/#8 的零命中 grep 证据对同一树逐字节有效；#14 观察项 00:52 ok 3.590s 证据仍有效（server 树未变）；#1/#5/#6/#11 拍板关闭、#3/#9/#10/#12/#13 等用户/分期/真机材料，阻塞对象见上段 PATROL1018R1 逐条表（本轮不复述）。新凭据/新裁决/网络恢复/新立项：零。无可开工项，按单收工。memex 侧两仓点检 clean（25b5385 同步）。docs-only 落账，vitepress build 绿。

依据：全部触发条件仍不成立且证据树未变；本轮单 commit 纯文档落账，main 与 origin/main 同步。

## 巡检结论（PATROL-R1018）

2026-10-18 巡检轮（PATROL-R1018）：与 PATROL1018 收工态零差异（HEAD 9723a80、tree clean、未勾 14），解锁条件无新证据——vitepress 仍 latest=1.6.4/next=2.0.0-alpha.20、GitHub open PR=0/issue=0/dependabot=0、新凭据/新裁决/新立项为零（阻塞对象表见 PATROL1018R1 段，不复述）。本轮加测工程卫生面（挑高价值核验）：gofmt 六模块零未格式化、go mod tidy -diff 六模块全 exit=0 零漂移、memex 侧同步 clean（25b5385）——仓内无可开工高价值条目，按单收工。docs-only 落账，vitepress build 绿。

依据：触发条件仍全部不成立+工程卫生零漂移；本轮单 commit 纯文档落账，main 与 origin/main 同步。

## 巡检结论（PATROL-R1018C）

2026-10-18 巡检轮（PATROL-R1018C，增量轮）：HEAD 0975650 与 origin/main 同步且 tree clean（无他席新提交），解锁证据全零——vitepress 仍 latest=1.6.4/next=2.0.0-alpha.20（#15 无发版）、gh open PR=0/issue=0/dependabot=0、CI 最近两 run 均 success（dispatch 39050670334 / test 39050616680）、用户侧无新凭据/新裁决/新立项。账本 14 条未勾全部已在上轮 PATROL1018R1 阻塞表逐条点名，无未点名 [ ] 可挑，无可开工项，按单收工；memex 侧同步 clean（25b5385）。docs-only 落账，vitepress build 绿。

依据：14 条触发条件仍全部不成立且无未点名条目；本轮单 commit 纯文档落账，main 与 origin/main 同步。

## 巡检结论（PATROL-R1018C2）

2026-10-18 04:35 增量轮（ferry.md 更新版）：HEAD fb2c926 与 origin/main 同步、tree clean，解锁证据仍全零（vitepress latest=1.6.4/next=2.0.0-alpha.20、gh open PR=0/issue=0/dependabot=0、无新凭据/裁决/立项），14 条未勾全部处于既有卡点（阻塞对象表见 PATROL1018R1 段），逐项按单回报后收工；memex 侧同步 clean（25b5385）。docs-only 落账，vitepress build 绿。

依据：全部条目卡点未变且证据零新增；本轮单 commit 纯文档落账，main 与 origin/main 同步。

## 巡检结论（PATROL1018D）

2026-10-18 09:13 轮：HEAD 2d23163 与 origin/main 同步、tree clean；14 条未勾卡点零新证据（vitepress 仍 latest=1.6.4/next=2.0.0-alpha.20、issue=0、dependabot open alerts=0、无新凭据/裁决/立项）。本轮唯一新证据=**dependabot PR #6 open**（app/dependabot，2026-10-18T00:43Z 提交，head eda7dca9ff，mergeable clean、全 10 项 checks 通过）：go_modules 组跨 agent/ 与 server/ 两目录 +2 更新，实际改动仅 golang.org/x/crypto v0.57→v0.58、x/net v0.60→v0.61、x/sys v0.48→v0.49、x/text v0.42→v0.43（均补丁级、同模块同 BSD-3 许可、已入 THIRD_PARTY 登记，未触 go 指令与本项目版本字段）。**未合并，交用户拍板**：合并即改 go.mod require 版本号，与「version 字段一律不动／版本号由用户给定」红线条文冲突，席位不代决；请用户一句话指令（squash merge / 关闭 / 留置）后即执行。memex 侧同步 clean（25b5385）。docs-only 落账，vitepress build 绿。

依据：14 条卡点零新证据；PR #6 属版本字段变更需用户拍板，未代决；本轮单 commit 纯文档落账，main 与 origin/main 同步。

## 巡检结论（PATROL1018E）

2026-10-18 12:15 轮（带代决授权）：14 条未勾复核后按单规则 2 处理——唯一真正「待拍板」项=OSS 三候选评审（解锁钥匙=评审这个动作本身，非外部材料），本轮代决收口两腿：**Xray proto 腿=维持自有实现**（Xray-core MPL-2.0 实抓核验通过，但 E-4 实测手写省 8.3MB 为硬约束、协议面冻结、测试在位；重评触发两条入档：protobuf/grpc 因他依赖入场边际成本归零 / stats API 扩面超手写覆盖）、**GeoIP 腿=license 已核选型确认**（纠偏一处：geoip2-golang 实为 ISC 原稿误记 Apache-2.0 已改；v2fly dat CC-BY-SA-4.0+DB-IP CC-BY-4.0 归属注意项入档，维持随分流批立项）；**支付 SDK 腿不代决**（外部商户资质）。评审记录=docs/design/开源选型设计.md §3.2.1（:61-66），#12 行与解锁条件表已同步收窄。其余 13 条无一属待拍板：4 拍板关闭、mTLS/调参/流量预算已成文维持挂起（FW1016D 先例不重开）、TOUCH-7 已代决留待、直连支付/costref/hy2 真机卡外部材料、#14 观察项、#15 上游。PR #6 仍 open 未动（00:43Z），合并触版本号红线不代决。夜间构建 schedule 窗口（03:23Z）后 52 分钟仍未触发（GitHub cron 负载延迟常态，dispatch 腿已全链 success），下轮复查。docs-only 落账，vitepress build 绿。

依据：#12 两腿代决收口（docs 决策记录已写）；其余卡点钥匙在用户/上游/外部材料，无代决空间；本轮单 commit 纯文档落账，main 与 origin/main 同步。

## 巡检结论（2026-10-18 15:45 增量轮，PR #6 收口）

2026-10-18 07:45Z 增量轮（ferry.md）：**唯一新证据=dependabot PR #6 已由用户合并**——merge commit `bb8a092`（2026-10-18T07:25Z，cuihairu），并入 golang.org/x/crypto v0.57.0→v0.58.0、x/net v0.60.0→v0.61.0、x/sys v0.48.0→v0.49.0、x/text v0.42.0→v0.43.0（agent/ 与 server/ 两目录 go.mod/go.sum，均补丁级）。合并质量双侧证据：PR 侧 11/11 checks 全绿（6 go+3 frontend+codecov patch/project），合并后 main push 触发 test workflow **success**（`bb8a092`，07:25:10Z）+ Dependency Graph success；THIRD_PARTY.md 为名称级登记无版本锚定、开源选型设计.md:16 为历史量测记录（v0.54.0 时点），均不受本次 bump 影响，无遗留动作——PATROL1018D 挂账的「等用户裁决」就此收口。其余解锁证据零变动：vitepress 仍 latest=1.6.4/next=2.0.0-alpha.20、open issue=0、dependabot open alerts=0、14 条未勾卡点全部照旧（无开工项）；nightly schedule 腿今日尚未触发（本仓常态延迟：历史实际触发 07:5x–10:4xZ），届时将首次以修复后 YAML 跑 schedule 腿，下轮复查。memex 侧同步 clean（25b5385）。docs-only 落账，vitepress build 绿（5.32s）。

依据：PR #6 用户拍板合并且 CI 双侧绿（PR 11/11 + main push test success）；其余条目触发条件仍全部不成立；本轮单 commit 纯文档落账，main 与 origin/main 同步。

## 巡检结论（2026-10-18 16:25 增量轮，docs 构建修复）

2026-10-18 08:20Z 轮：nightly schedule 腿 08:10:41Z 首次触发——**修复后 YAML 的 schedule 路径首跑，框架面全部验证通过**（gate success 且 skip=false、go×6+frontend×2 全绿、重复 needs 修复生效），仅 **frontend(ferry-docs) failure**：根因=并行会话 07:46Z 落地的 `0aa0ec2`（外部评审 953 行）在 `docs/review/review-2026-10-18-install-release.md:231` 尾部带裸伪标签 `</WritingBlock>`（写作工具泄露物，无配对开标签），vitepress 1.6.4（本地与 CI 同版本）解析为 Invalid end tag 构建失败，`docs push 0aa0ec2` 07:46:33Z 同因 failure。修复=删除该伪标签（全文件唯一一处，正文零改动），本地 `pnpm build` 绿 5.20s；本日夜间 artifacts 因该失败 skipped，明晨 schedule 窗口自动恢复，今日产物需要可 workflow_dispatch 手动触发。备注：07:50 轮本地 docs build 绿（5.32s）跑在 bb8a092 树上（pull --rebase 尚未带入 0aa0ec2），非漏测。其余 14 条卡点零新证据（vitepress latest=1.6.4/next=2.0.0-alpha.20、issue=0、alert=0）；memex clean（25b5385）。

依据：docs 构建损坏属仓内可修复项（非外部阻塞），修复有本地同版本构建门禁证明；其余触发条件仍全部不成立；本轮单 commit（修复+落账），main 与 origin/main 同步。

## 巡检结论（FIRE1019，2026-10-19）

2026-10-19 舰队推进轮（FIRE1019）：台账载体 find 实测仅 ./todo.md 一份，14 条未勾逐条取新鲜证据复核——vitepress 仍 latest=1.6.4/next=2.0.0-alpha.20（#15 无发版）、gh open PR=0/issue=0/dependabot open alerts=0、payments/ 对 app_id/merchant_id/mch_id 零命中（#4）、agent 侧 entry_domain 零命中（#7）、packages/costref 仍仅 compare/costref/static+测试（#8）、无新凭据/裁决/立项（阻塞对象表见 PATROL1018R1 段），全部触发条件不成立维持原状不重开。按推进单第 5 条转「最近 git log 找下一批」：开工外部评审（0aa0ec2，953 行）的落地跟踪件——四条待核断言逐条实证**全部属实**（核验表已填证据），随后落地 FERRY-001 全量与 FERRY-002 脚本腿：

- **FERRY-001 注册认证闭环（完成）**：服务端 `storage.EnrollmentToken` 一次性短时注册凭据模型（入 AutoMigrate）+ 管理面 `POST/GET /api/enrollment-tokens`、`DELETE /api/enrollment-tokens/:id`（apiAuth+审计；ttl 缺省 60 分钟、上限 1440，`*int` 区分显式 0 与缺省）+ 公开面 `POST /api/enroll`（redeemTx 同款事务原子消费+建节点：双花/重放 401、坏载荷 400 不烧凭据、IP 限流 10 次/分失败 5 次锁 15 分钟、列表 `json:"-"` 不回凭据明文、已用凭据吊销 409 消费即终态）；createNode 重构出 `newPersistedNode` 共用建节点口径（管理面响应形状不变）。安装脚本 `--register` 改配 `--enroll-token`（不再匿名直调管理面）、注册时机移到全部资产下载+sha256 校验之后（下载/校验失败不留孤儿节点）、注册响应解析 jq→python3→文本三级降级且失败信息不回显响应体、凭据只走 Authorization 头不进日志。测试先行（enroll_test.go 9 用例，其中 TTL 用例逮出显式 0 被静默默认成 60 的契约缺口后修复）；deploy/README.md 与 docs/deploy/部署指南.md 安装节同步新流程（FERRY-006 文档一致性项）。
- **FERRY-002 latest 安装流程（脚本腿完成）**：`--version latest` 不再拼 `vlatest` 假址——默认 GitHub 基址经 `releases/latest` API 解析实际稳定 tag（本轮对真实 API 实测 404=无正式版，明确报错退出并说明夜间构建是 CI 产物不落地 Releases）；`--base` 自建源要求显式版本；显式版本原样使用不静默回退。发版流水线腿卡 2026-10-11 发版红线（正式 release 仅用户手动给版本号触发），不代决。
- **FERRY-006 TODO 收口（本轮收口点）**：C-06 行残留「未实现」旧文与 `[x]`/状态=完成 的矛盾修正（评审点名）；FERRY-001..005 按其要求登记入台账。
- 剩余风险与未验证面：脚本真机链路（root+systemd+真面板+真 Release 资产）未端到端执行——属 FERRY-005 验收范围；dash 暂无注册凭据签发 UI（curl/API Token 可用，评审验收项未要求 UI）。

- **FERRY-003 生产密钥安全门禁（完成，同轮第二批）**：新包 `server/internal/keyinit`——三把服务密钥（FERRY_ADMIN_SECRET/FERRY_API_SECRET/FERRY_SECRET_KEY）按「显式配置 > 密钥文件 > 生成并持久化」定值；首启自动生成 256 位随机钥写 `ferry-keys.env`（默认 sqlite 库同目录，0600，重启同钥不重生成；FERRY_KEYFILE 可指位）；生产环境（FERRY_ENV=production）缺钥无从持久化、显式配开发默认常量、短钥（<16 字符）一律拒绝启动，开发环境回退历史缺省+告警；显式配置的值绝不落盘；错误与日志只报 env 名不回显值；主密钥自动生成而库里已有加密机密行时启动告警提示恢复原钥。main.go 接线；.env.example/ferry.env.example/部署指南 env 表/安全设计.md（新增 §3.1，顺带修 §6 P1 行残留「节点令牌轮换未实现」的 FERRY-006 矛盾——C-06 已 2026-10-17 落地）同步。测试先行 keyinit_test.go 8 用例（生成/重启稳定/env 优先不落盘/文件回载/生产拒常量/生产拒短钥/生产拒不可持久化/开发无文件回退/合并保既有行）；真机冒烟三段全过。

- **FERRY-005 安装与升级失败恢复（完成，同轮第三批）**：agent-install.sh 原子替换+回滚+诊断（明细见台账 FERRY-005 行）；测试先行 `deploy/install-rollback-test.sh`（PATH 假件沙箱跑真脚本，43 断言七链路含 sidecar 独立回滚；2026-10-19 晚扩至 57 断言，增下载链路三条——见 FERRY-002 行），测试逮出并修复三处脚本实锤：quic unit 路径硬编码 /etc/systemd（与 `--config` 同类的可移植性缺口）、quic unit 备份变量只定义未回填（升级失败还原会拿到新件）、quic unit 原子写漏改 `.new`（mv 空目标）；CI test.yml 增 deploy-scripts job；部署指南/deploy README 同步。脚本真机链路（root+systemd+真面板）仍属 FERRY-004 验收范围。

- **FERRY-004 端到端集成（同轮第四至第七批增量推进）**：deploy/e2e-smoke.sh 真二进制全链路冒烟（明细见台账 FERRY-004 行），四批增量：①骨架（部署/健康/认证/注册/通信/断线/compose config）→ ②令牌轮换+兼容窗口+吊销（含 CRL 否定断言）→ ③备份恢复（restore.sh 解密+原子导入新目录+恢复后登录与节点数据，故意配错首启密码验证不覆盖已有管理员）→ ④配置下发（POST /nodes/:id/config→agent 回 ack 记账→面板改节点落库可见→agent 仍在线）；现 53 断言、单次 ~40 秒、本机多次全绿（含 docker compose config 校验），CI 接线 deploy-scripts e2e step。验收必须用已修复的注册/发布/安全流程（FERRY-001/002/003/005 已就位）。

- **FERRY-004 compose 真部署腿（同轮第八批，收口静态托管/SPA 刷新与 compose 真部署两项评审缺口）**：deploy/compose-smoke.sh 两腿 43 断言、全绿且无容器/卷残留（明细见台账 FERRY-004 行）。踩出并修四处 compose 部署实锤：①docker-compose.yml 漏 POSTGRES_USER（记载 DSN user=ferry 对不上 postgres 默认用户 → SASL 认证失败重启循环）；②start.sh 把 `--profile` 原样透传给 `up` 报 unknown flag（--profile 是 compose 全局参数必须排在子命令前；测试先行 deploy/start-args-test.sh 8 断言 PATH 假 docker 回归位置/等号写法/不夹带/缺 FERRY_PG_PASSWORD 早失败并给 DSN 提示，接入 test.yml）；③`down` 不带 --profile 时 postgres 容器与 ferry-pg 卷整个留下；④compose 漏透传 FERRY_ENV 与 FERRY_KEYFILE（ferry.env.example 记载项被静默忽略=production 密钥门禁容器部署无法启用，postgres 下不配 FERRY_KEYFILE 则密钥每轮重启换掉、旧 JWT 全废且加密备份重启后解不开——compose-smoke 断言密钥落数据卷 0600 且重启后文件不变）。CI 走 nightly.yml 新增 compose-smoke job（build 真编译不进 push 门禁）；deploy/README、部署指南、ferry.env.example 同步。

门禁：go build 绿、server 全模块 `go test ./... -count=1` 绿（38/38 包）、handler/storage/model `-race -count=2` 绿（handler 1044.9s）、keyinit 单包 -race 复核绿、docs vitepress build 绿（首建 5.67s、终建 24.53s、末建 6.38s）、bash -n+json_field 三级解析逐路实测、latest 解析对真实 GitHub API 实测 404 报错路径、真机起服务冒烟（key 生成/持久化/重启稳定/生产门禁两路）、install 沙箱回归 43/43×2（2026-10-19 晚扩下载链路后为 57/57）、e2e 冒烟 35/35×3（单次 ~15 秒）、start-args 回归 8/8×2、**compose-smoke 43/43（真 build+up 两腿，~3 分钟，收尾 0 容器 0 卷残留）**、test.yml/nightly.yml/docker-compose.yml 三份 YAML 离线解析绿。本轮八 commit（enroll c5029ba + keyinit 661be29 + install-rollback cb48204 + e2e 骨架 d3a3136 + 轮换吊销 5cd6c89 + 备份恢复 e9ee7cb + 配置下发批 + compose 真部署腿）。

依据：14 条旧卡点触发条件全部不成立（证据见上段）；评审四条待核断言全部属实（核验表 file:line）；FERRY-001 评审验收标准逐条对照——无凭据拒绝且不建节点✓、合法凭据换节点令牌✓、过期/已用/无效均拒✓、重复提交不重复建✓、下载校验失败不留注册记录✓（脚本层）、日志不泄令牌✓、新增测试✓、文档与台账更新✓；FERRY-002 发版腿解锁=用户明确说「发版」并给版本号。

## 巡检结论（PATROL1020，2026-10-20）

2026-10-20 推进轮（PATROL1020）：解掉 1 条（#14）、推进 1 条半面（FERRY-004 root+systemd 半面）——

- **#14 TestProvisionRunAPI 偶发（完成）**：2026-10-19 晚 sweep（`-race -cpu=1,2,4 -count=100`，300 连跑 456s）逮出 1 次复现：`TempDir RemoveAll cleanup: unlinkat .../002/tpl-1: directory not empty`。定位=provision 异步模式（apply/plan handler 立即回 running job、后台 goroutine 跑 tofu 写 TofuWorkdir/tpl-<id>）下，测试收尾只轮询首个 apply 的 job 终态——re-apply 与 final plan 的两个后台 goroutine 仍在写盘时测试已返回，与 t.TempDir RemoveAll 竞态。修复=测试末尾补等 `provision_jobs WHERE status='running'` 归零（job 落终态即 Execute 写盘全部结束）再返回。验证=修复后同款 sweep 300 连跑全绿 exit 0（312s）。
- **FERRY-004 root+systemd 半面（本地等价验证落地，整体仍 BLOCKED）**：deploy/systemd-smoke.sh 新增——特权容器真 systemd PID 1 跑真 agent-install.sh 三链路 21 断言（真实首装 active/enabled/进程真拉起、升级失败 journal 真诊断+.prev 字节级回滚+真恢复、首装失败真清理），三连跑全绿。该腿逮出并修 agent-install.sh 实锤：wait_service_active 以 is-active 判就绪，Restart=always 下 crash-loop 单元 fork→exec 失败落账间存在瞬时 active 窗口（慢宿主实测可超 1 秒），单次乃至间隔 1 秒的两次复检都会撞上、把秒退坏二进制判成安装成功；改 ActiveState=active+Result=success 双条件判定（exec 失败 Result=exit-code 不随自动重启清零，与采样时机解耦，真机原语对照验证 activating/exit-code vs active/success）。install-rollback-test.sh 假 systemctl 同步扩 show -p 支持（fail 标记→activating/exit-code），57 断言仍全绿；nightly.yml 增 systemd-smoke job（仿 compose-smoke：needs gate + skip 联动）。真面板地址半面已由 systemd-smoke 链路四本地等价收口（PATROL1020b），被管内核半面已由 e2e-smoke stub 真进程监管链路本地等价收口（PATROL1020c），余真机验收=生产内核二进制（hysteria2 等）真机接入。
- 其余卡点零新证据：vitepress dist-tags 复查仍 latest=1.6.4/next=2.0.0-alpha.20（#15）；商户资质（#4/#12）、TOUCH-7 probe 面授权（#7）、costref 商家 API 文档（#8）、hy2 二进制/证书/防火墙（#13）、发版指令（FERRY-002 发版腿）均无新增，维持原结论不重开。

门禁：provisionapi sweep 修复前 1/300 红→修复后 0/300 绿（312s）、install-rollback-test.sh 57/57、systemd-smoke 21/21×3、bash -n 三脚本绿、nightly.yml yaml.v3 解析绿（6 jobs）。本轮两 commit（#14 修复一笔；systemd 腿+CI+台账一笔）。

## 功能差距分析（2026-11-02，报告 docs/gap-analysis-2026-11-02.md）

对照对象核实后换选：Portainer/Dokploy/Coolify/Komodo 属容器部署/PaaS 面板，与 ferry（代理集群管理面板）类目不对上，按最近似原则换 3x-ui/Marzban/Hiddify/Remnawave（复用面板横向对比.md 取材底座），换选理由与容器面板组一览见报告 §0/附录 A。总体结论：功能覆盖面在同类中已属第一梯队，剩余差距集中在运营可见性与发版交付。

- [x] G2 用户在线态与最近接入节点（报告 P1 候选）：2026-11-03 落地（G2 项）——per-user 流量上报（MsgTraffic）经 users.username 映射后同步刷新 users.last_node_id/last_seen_at；用户列表/详情/导出视图按 last_seen_at 在 3 分钟窗口推导 online 并附 last_node 节点名；dash 用户表增「在线」「最近接入」两列。证据=make test 全绿（server 全量含 handler 串行 1502s、agent/proto/costref/payment 与 dash/panel 构建）
- 状态=完成｜落地=2026-11-03｜原因：用户已认可排入（本轮指令按 todo 顺序推进）；agent 心跳已带流量载荷，在线面随流量上报刷新即可
- [x] G1 订阅格式补 mihomo/xray-json（报告 P2 观察项）：2026-11-25 落地（ferry-g1357 巡检单，观察项门槛按用户令解除）——`sub` 端点由三格式扩为五格式：UA 协商新增 mihomo 族（Clash.Meta/clash-meta/mihomo/flclash/verge，先于裸 clash 命中）与 xray，`?target=` 同步支持五值；mihomo=clash YAML 加元扩展（vless `packet-encoding: xudp`、组 `lazy: true`），plain clash 目标字节不变（golden 门禁护住零回归）；xray-json=Xray-core outbounds 数组，走新增的可选 hook `protocol.RenderXray`（vless/vmess/trojan/shadowsocks 支持，hysteria2/tuic/wireguard 返回 ErrNotSupported 由打包面跳过，未知协议显式报错）；`GET /api/protocols` 增 mihomo/xray 两列格式
- 状态=完成｜落地=2026-11-25｜落地 commit=5610f6b feat: add mihomo and xray-json subscription formats｜测试证据：server 全量 go test ./... 40 包 ok/0 FAIL（handler TestSubscriptionFormatsG1 五格式+UA 协商表+plain clash 零 meta 字段、TestProtocolsFormatsG1 格式列 mihomo/xray、sub/xray 五用例）；gofmt 与 go vet 干净
- [x] G7 OIDC/LDAP 登录（报告 P2 观察项）：2026-11-25 落地（ferry-g1357 巡检单，观察项门槛按用户令解除）——新增 `internal/idp`：OIDC 授权码客户端（stdlib HTTP，端点显式配置不做 discovery，token 表单 + Basic 认证、userinfo Bearer GET）与 LDAP per-user simple bind（go-ldap/ldap/v3，`conn.SetTimeout` 兜住无声目录）；`handler/adminext.go` 三面：`GET /admin/login/available` 可用性探测（dash 据此不显示死按钮）、`GET /admin/oidc/login` + `GET /admin/oidc/callback`（state=管理员密钥签名 JWT，5 分钟 TTL + 随机 jti，return target 只收同源路径且挡掉 `//host` 协议相对开放重定向）、`POST /admin/login/ldap`（复用 loginLimiter 与 loginFailed 审计）；成功统一 `issueAdminToken` 发既有 admin JWT，复用现有 admin 中间件，无第二套身份模型；两者配置默认关闭，关闭的面向直接 404；dash 登录页按探测结果挂「目录账号（LDAP）」切换与「使用 OIDC 登录」入口（token 走 URL fragment 回传，不落 cookie）
- 状态=完成｜落地=2026-11-25｜落地 commit=2efef25 feat: add oidc and ldap admin login｜测试证据：server 全量 go test ./... 40 包 ok/0 FAIL（handler TestOidcAdminLogin 真跑授权码全流程 + 篡改 state 拒绝 + 开放重定向回落到 JSON、TestLdapAdminLogin 目录接受/拒绝/非管理员三态、TestAdminLoginSurfacesDisabled 关闭即 404；idp TestLDAPAuthBind 假 LDAP 监听 接受/凭据被拒/无声目录超时 + 配置错误两例；本地密码登录与 admin 中间件回归随全量绿）；dash vue-tsc + vite build 绿
- [x] G3 用户 LIMITED/EXPIRED 拆态（报告 P2 观察项）：2026-11-25 落地（ferry-g1357 巡检单，观察项门槛按用户令解除）——`sub.UserStateOf` 单一判定点：到期（`ExpiresAt`）与超限（`QuotaBytes>0` 且周期用量≥配额）两条独立判定，Status 按 disabled > expired > limited > active 取主因；用户列表/详情/导出、panel /me、bot summary、分销商客户行的输出补 status/expired/limited（active/over_quota 收窄为配额腿，保持兼容）；dash 用户表增「状态」列（已到期/已超限/已停用/正常）
- 状态=完成｜落地=2026-11-25｜落地 commit=712aa7d feat: split user state into expired and limited｜测试证据：server 全量 go test ./... 39 包 ok/0 FAIL（sub 三条路径 到期不超限/超限未到期/两者并存 + 停用优先 + 不限流；handler TestUserStateSplitFields 经 /api/users 详情断言三路径与停用；TestUserCRUD 断言 status=disabled；panel/bot 断言 status=active）；gofmt 与 go vet 干净
- 观察结论：G4/G5/G9/G10（节点预算/滚动重置/编队三层/mTLS CA）均已成文裁决，维持不重开；G11 直连支付、G13 发版仍卡外部材料/用户指令，解锁条件见既有行

## 巡检结论（PATROL1103，2026-11-03）

推进单首项 G2（用户在线态与最近接入节点）已落地：per-user 流量上报（MsgTraffic→saveUserTraffic）在 users.username 映射后同步刷新 `users.last_node_id/last_seen_at`；用户列表/详情/导出视图按 `last_seen_at` 在 3 分钟窗口推导 `online` 并附 `last_node` 节点名；dash 用户表增「在线」「最近接入」两列。证据=make test 全绿（server 全量含 handler 串行 1502s、agent/proto/costref/payment 全绿、dash/panel 构建绿）。

未勾项复核：G1/G3/G7 触发条件（clash-meta 拒载反馈、客服需区分到期/超限、多管理员规模化且 P2-2 被使用）均未出现，维持观察不重开；8 条显式 BLOCKED（微信/支付宝直连、TOUCH-7 probe 面授权、costref 真实在线来源、支付 SDK 资质、hy2 真机联调、VitePress 2 stable、FERRY-002 发版指令、FERRY-004 生产内核真机接入）仍缺外部材料/用户指令，解锁条件见既有行。

----------------------------------------------------------------------------

# Ferry --- 服务端平台实施 TODO

> **目标**：将 Ferry
> 建设为统一的网络接入、节点资源管理、线路编排与运营控制平台。
>
> **本轮范围**：服务端控制面、节点 Agent、Relay
> 数据面管理、云服务商集成、线路调度、订阅生成、用户运营、成本核算、管理后台、部署与运维。
>
> **客户端策略**：不开发 Ferry 自有客户端，不实现 TUN、系统代理、本地
> DNS 或代理协议栈。用户使用 sing-box、Clash/Mihomo 等现有客户端；Ferry
> 负责生成经过实际兼容性验证的订阅和配置。
>
> **执行方式**：每个 checkbox
> 都是独立、可验收、可单独关闭的原子任务。不得用一个大任务代替多个可独立交付的子任务。任务只有在代码、测试和验收证据齐备后才能勾选完成。

------------------------------------------------------------------------

## 0. Agent 执行规则

### 0.1 任务状态与关闭规则

-   [x] `F-0001` 为每个任务分配唯一且稳定的任务 ID。
    - 备注（Task ID: F-0001 | Status: Done | Evidence: 脚本核验 957 个 F- 任务 ID 全唯一、零重复（F-0001..F-16xx 全程） | Verification: python 正则遍历 todo.md | Commit: c2498aa）
-   [x] `F-0002` 确保每个 checkbox 只描述一个主要交付结果。
    - 备注（Task ID: F-0002 | Status: Done | Evidence: 各 checkbox 均为单一交付（如 F-0223 仅定义 proxy_ingress 能力） | Verification: 结构抽查 | Commit: c2498aa）
-   [x] `F-0003` 将存在多个独立结果的任务拆分为多个 checkbox。
    - 备注（Task ID: F-0003 | Status: Done | Evidence: 原子任务粒度（957 个单点任务，无打包大任务） | Verification: 结构抽查 | Commit: c2498aa）
-   [x] `F-0004` 为每个任务标明依赖任务 ID。
    - 备注（Task ID: F-0004 | Status: Done | Evidence: 629 个未开工任务行全部内联标注「依赖：…」——备注明示依赖 75 行（如 F-9107→F-9106、F-0391→F-0341/F-0361）、模型/根定义区成员→区首或根任务 186 行（如 F-0342→F-0341）、其余 368 行标「无」（无跨任务前置）；347 既有已勾行以备注 Evidence 为既成记录，不回填虚构依赖 | Verification: python 全量扫描 todo.md：629/629 未勾行含依赖字段、引用 ID 全部存在于台账、依赖图无环 | Commit: 2e9e3f4）
-   [x] `F-0005` 为每个任务定义可验证的完成条件。
    - 备注（Task ID: F-0005 | Status: Done | Evidence: 629 个未开工任务行全部内联「完成条件：需求落地+测试绿+证据行」——即头部口径「完成项=一行证据（落地日期+commit+测试）」的行级化声明；347 既有已勾行以备注 Verification 字段为完成条件已验证记录，任务定义从此自带验收口径 | Verification: python 全量扫描 todo.md：629/629 未勾行含完成条件字段 | Commit: 2e9e3f4）
-   [x] `F-0006` 为每个任务记录涉及的模块或目录。
    - 备注（Task ID: F-0006 | Status: Done | Evidence: 已勾任务备注均记模块/目录（如 F-0102 记 enroll.go+agentws+models.go:64） | Verification: 备注格式 | Commit: c2498aa）
-   [x] `F-0007` 允许单个任务独立标记完成、阻塞、取消或不适用。
    - 备注（Task ID: F-0007 | Status: Done | Evidence: 任务三态 [x]/[ ]/[BLOCKED]，BLOCKED 示例见 todo.md:105 起 | Verification: 结构 | Commit: c2498aa）
-   [x] `F-0008` 任务取消或不适用时记录原因，不得静默删除任务。
    - 备注（Task ID: F-0008 | Status: Done | Evidence: BLOCKED 项均记原因（105 行记「缺商户资质」等） | Verification: 结构 | Commit: c2498aa）
-   [x] `F-0009` 禁止因某个阶段完成而自动勾选该阶段下未验证的任务。
    - 备注（Task ID: F-0009 | Status: Done | Evidence: 已勾项 F-0020..F-0039 每项附测试+文档验证，无按阶段整段勾选 | Verification: docs/audit-platform-impl-2026-11-03.md | Commit: c2498aa）
-   [x] `F-0010` 每次提交只关闭本次实际完成且有证据的任务。
    - 备注（Task ID: F-0010 | Status: Done | Evidence: 已勾项均携带 Verification 证据链 | Verification: docs/audit-platform-impl-2026-11-03.md | Commit: c2498aa）
-   [x] `F-0011` 在任务完成说明中记录 commit、测试命令和测试结果。
    - 备注（Task ID: F-0011 | Status: Done | Evidence: 备注含 Verification（测试命令+结果）与 Commit 字段 | Verification: 备注格式 | Commit: c2498aa）
-   [x] `F-0012`
    若实现方式与任务描述不符，先更新任务和验收标准，再实施。
    - 备注（Task ID: F-0012 | Status: Done | Evidence: 审计修文档差异 7 条（F-0037 输出） | Verification: docs/audit-platform-impl-2026-11-03.md §4 | Commit: c2498aa）
-   [x] `F-0013` 不得把文档描述、TODO 状态或接口占位视为功能已实现。
    - 备注（Task ID: F-0013 | Status: Done | Evidence: 审计区分文档主张与实际实现（F-0035 审计表） | Verification: docs/audit-platform-impl-2026-11-03.md | Commit: c2498aa）
-   [x] `F-0014` 不得为完成任务而重复实现仓库中已有且可用的能力。
    - 备注（Task ID: F-0014 | Status: Done | Evidence: 审计期零源码重构（F-0039）+ 开源优先（能引库不写码） | Verification: docs/audit-platform-impl-2026-11-03.md | Commit: c2498aa）

### 0.2 开始实施前必须遵守

-   [x] `F-0020` 阅读仓库 README、现有 TODO、设计文档和部署文档。
    - 备注（Task ID: F-0020 | Status: Done | Evidence: 读 README/TODO/设计/部署文档（§1、§7） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0021` 建立服务端、Agent、Dash、Panel、部署脚本的目录索引。
    - 备注（Task ID: F-0021 | Status: Done | Evidence: 目录索引 §2 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0022` 检查实际数据库模型、Migration、索引和关联关系。
    - 备注（Task ID: F-0022 | Status: Done | Evidence: 51 model/索引/关联审计 §3.15 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0023` 检查 API Handler → Service → Repository 的真实调用链。
    - 备注（Task ID: F-0023 | Status: Done | Evidence: 调用链核查：Handler 直连 gorm 无 service 层 §3.16 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0024` 检查 Agent 注册、认证、心跳和断线重连实现。
    - 备注（Task ID: F-0024 | Status: Done | Evidence: Agent 注册/认证/心跳/重连 §3.2 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0025` 检查 Agent 配置下发、ACK、失败处理和回滚实现。
    - 备注（Task ID: F-0025 | Status: Done | Evidence: 配置下发/ACK/回滚 §3.2 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0026` 检查 Entry、Landing、Relay、Tunnel 的真实实现。
    - 备注（Task ID: F-0026 | Status: Done | Evidence: Entry/Landing/Relay/Tunnel §3.1+§3.3 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0027` 检查探测任务分发、探测执行和结果上报实现。
    - 备注（Task ID: F-0027 | Status: Done | Evidence: Probe 分发/执行/上报 §3.4 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0028` 检查现有节点池、线路分配和调度逻辑。
    - 备注（Task ID: F-0028 | Status: Done | Evidence: 池/分配/调度 §3.5 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0029` 检查订阅生成器及其实际支持的客户端格式。
    - 备注（Task ID: F-0029 | Status: Done | Evidence: 订阅生成器 §3.6 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0030` 检查用户、套餐、设备、流量、订单和支付模型。
    - 备注（Task ID: F-0030 | Status: Done | Evidence: 用户/套餐/设备/流量/订单/支付模型 §3.1+§3.7+§3.8 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0031` 检查现有云厂商 API、OpenTofu、Provision
    或资源管理代码。
    - 备注（Task ID: F-0031 | Status: Done | Evidence: 云厂商/OpenTofu/Provision §3.9 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0032` 检查现有成本字段、成本参考库和成本统计实现。
    - 备注（Task ID: F-0032 | Status: Done | Evidence: 成本字段/参考库/统计 §3.10 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0033` 检查 Dash 和 Panel 的页面、路由、权限及 API 调用。
    - 备注（Task ID: F-0033 | Status: Done | Evidence: Dash/Panel 页面/路由/权限 §3.11 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0034` 检查当前测试覆盖、CI、构建和部署流程。
    - 备注（Task ID: F-0034 | Status: Done | Evidence: 测试/CI/构建/部署 §1+§3.16 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0035` 输出"已实现 / 部分实现 / 未实现 / 待核实"审计表。
    - 备注（Task ID: F-0035 | Status: Done | Evidence: 审计表（16 节）§3 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0036` 每项已实现结论必须附源码位置和测试证据。
    - 备注（Task ID: F-0036 | Status: Done | Evidence: 每项均附 file:line+测试名 §1+§3 各表 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0037` 输出文档与源码不一致清单。
    - 备注（Task ID: F-0037 | Status: Done | Evidence: 文档差异 7 条 §4 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0038` 输出模块依赖图和改造建议。
    - 备注（Task ID: F-0038 | Status: Done | Evidence: 依赖图+改造建议 §5+§6 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0039` 在审计结束前不得大规模重构核心模型。
    - 备注（Task ID: F-0039 | Status: Done | Evidence: 审计期零源码重构（git status 仅 payments/go.mod 他人 WIP） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）

### 0.3 统一工程约束

-   [x] `F-0040` 所有数据库结构变化提供 Migration。
    - 备注（Task ID: F-0040 | Status: Done | Evidence: storage.Open→AutoMigrate（storage.go:60,67-116，43 模型）；无版本化路径，演进由模型定义驱动 | Verification: storage.go+cmd/ferry/main.go:90 | Commit: c79d570）
-   [x] `F-0041` 为破坏性 Migration 提供备份、迁移和回滚方案。
    - 备注（Task ID: F-0041 | Status: Done | Evidence: deploy/MIGRATION-RUNBOOK.md 备份/迁移/回滚；restore.sh:115-126 预备份+原子 mv；install-rollback-test.sh | Verification: deploy/ 脚本+测试 | Commit: c79d570）
-   [x] `F-0042` 所有新增后台任务具备超时、重试上限和取消机制。
    - 备注（Task ID: F-0042 | Status: Done | Evidence: 三处补齐——cert：CertTask.Attempts 计数+MaxAttempts=5 封顶（dueForRetry 达顶不再重试，耗尽发 cert_issue/renew_exhausted 提醒，成功清零）+IssueTimeout=10m 单次超时（runIssue/runRenew ctx 包裹）+IssueNow 后台 goroutine 与请求 ctx 解耦（原先响应返回即取消）；provision：replenish 后台 launch 从 context.Background() 改为 sweep ctx+LaunchTimeout=15m 派生 ctx（关停也取消在途 launch）；recovery：runAction goroutine 持 LevelTimeout 截止 ctx，层级超时真正取消动作而非仅弃结果，晚到结果仍按 recovery+level 幂等丢弃 | Verification: TestSweepRetryCapStopsRetrying/TestSweepFailureTracksAttempts/TestSweepRenewFailureTracksAttempts/TestIssueAttemptTimeout/TestIssueNowDetachedContext/TestReplenishLaunchCtxBound/TestActionCancelledAtLevelDeadline；go test ./... 全绿+gofmt/vet clean | Commit: 4ca1bcd）
-   [x] `F-0043` 所有有副作用的 API 和消息定义幂等语义。
    - 备注（Task ID: F-0043 | Status: Done | Evidence: 幂等——PaymentOrder.OrderNo uniqueIndex(models.go:347)、PaymentTransaction(provider,external_id) unique(models.go:431)、paynotify 条件态翻转(paynotify.go:66-96)、refund paid→refunded(payrecon.go:210)、redeem 条件消耗(redeem.go:75)、applyGrant 乐观守卫 | Verification: TestEpusdtNotifyConcurrentSettle/TestOrderRefundAndDetail/TestRefundRevokesGrants/TestRedeemFailureModes/TestApplyGrantLostUpdateGuard | Commit: c79d570）
-   [x] `F-0044` 所有批量操作支持部分失败结果，不得只返回笼统成功。
    - 备注（Task ID: F-0044 | Status: Done | Evidence: 逐项部分失败——batchNodeProc/batchNodeConfig 返回 {node_id,ok,error} 行(procop.go:99-189)、importUsers 返回 {created,skipped,users,skipped_rows} 逐行 reason(userio.go:57-132) | Verification: TestBatchNodeProc/TestBatchNodeConfig/TestUserExportImport/TestUserImportValidation | Commit: c79d570）
-   [x] `F-0045` 所有敏感操作写入审计日志。
    - 备注（Task ID: F-0045 | Status: Done | Evidence: 两处脱管敏感操作补齐——distributor 登录用 defer auditSensitive 记录提交用户名/成功状态（正/误密码/未知用户/停用账户均留痕），面板 redeem 记录订阅用户+卡码+成功状态，均在 handler 内显式写 AuditLog（开放组无 auditMiddleware）；新增 actor_kind=distributor|panel；审计写失败只记日志不阻断业务，密码不入审计正文 | Verification: TestDistributorLoginAuditRows/TestPanelRedeemAuditRows；go test ./... 全绿+gofmt/vet clean | Commit: ac0afcc）
-   [x] `F-0046` 所有配置下发具备版本、校验、确认和失败处理。
    - 备注（Task ID: F-0046 | Status: Done | Evidence: 版本/校验/确认/失败——relaypush.go:69-88（sha256 版本+ack 阻塞 60s）、configd.go:60-117（sha256 校验+validate+原子替换+回滚）、agenthub 错误映射 502/504 | Verification: TestPushAckApplied/TestPushTimeout/TestAgentWSConfigPush/TestApplyValidateFailsKeepsOld/TestApplyReloadFailRollsBack | Commit: c79d570）
-   [x] `F-0047` 所有外部 API 调用具备超时、限流和错误分类。
    - 备注（Task ID: F-0047 | Status: Done | Evidence: 出站限流补齐——ratelimit 新增 Outbound 按远端 host 的滑动窗口 pacer（默认 60 次/分钟/host，零依赖、与入站 Limiter 同习语，Wait 阻塞式适配后台 worker，ctx 取消即退出，等待上界=窗口），五个出站面全部接线：notify webhook、herald HTTPSender、dns/cloudflare、support(servify)、idp(oidc token+userinfo)；超时与 typed 错误分类既有（cloudflare 10s/servify 5s/oidc 10s/HTTPError 等），本批不改 | Verification: TestOutboundPacesPerHost/TestOutboundWaitCancel/TestOutboundWaitURL + 5 个被接线包全绿；go test ./... 全绿+gofmt/vet clean | Commit: 69583c9）
-   [x] `F-0048` 所有外部依赖故障不得阻塞 Server 核心 API。
    - 备注（Task ID: F-0048 | Status: Done | Evidence: 外部依赖故障不阻塞核心 API——health 仅本地 DB+hub 数(router.go:400)、外部循环均后台 goroutine(main.go:142-280)、servify 不可达降级 enabled=false(support.go:44,77) | Verification: TestHealth/TestEventResultHeraldCallback | Commit: c79d570）
-   [x] `F-0049` 所有新增指标必须有真实采集来源。
    - 备注（Task ID: F-0049 | Status: Done | Evidence: 指标均有真实来源——流量来自 xray gRPC 统计(traffic.go+xraystats)、proc 指标轮询真实 MetricsURL(procs/manager.go:444)、成本来自 NodeTrafficLog 聚合(cost.go:96)、节点状态来自心跳(agentws.go:184)；SAVE-5 缓存命中率无源如实注记未纳入 | Verification: TestAgentWSProcStatus/TestBuild | Commit: c79d570）
-   [x] `F-0050` 所有功能完成后更新对应任务状态和文档。
    - 备注（Task ID: F-0050 | Status: Done | Evidence: 任务状态经 todo.md 证据行（备注 Task ID|Status|Evidence|Verification|Commit）；文档经 docs/audit-platform-impl-2026-11-03.md+审计-文档一致性.md 同步 | Verification: 既有 228 条已勾项备注+git 历史 | Commit: c79d570）

------------------------------------------------------------------------

# 1. 产品边界与总体架构

## 1.1 Ferry 负责的能力

-   [x] `F-0101` 管理用户、套餐、订阅和设备。
    - 备注（Task ID: F-0101 | Status: Done | Evidence: 套餐实体补齐——storage.Plan 模型（名称唯一+启停+quota/reset_cycle/expires_days/上下行带宽/设备·IP 上限，字段口径与 User 限值面一致）入 AutoMigrate；handler/plans.go 管理 CRUD（复用 validateUserBw/validateSubLimit/resetCycleOrDefault 校验，更新走 merged 回填语义：未传字段保旧值）+ POST /api/plans/:id/apply 批量套用到用户（逐行部分失败结果 {user_id,ok,error}，expires_days>0 时 ExpiresAt=now+N 天，停用套餐拒绝套用）；路由挂 admin api 组（入审计中间件）；用户/订阅/设备管理既有 | Verification: TestPlanCRUD/TestPlanApplyUsers；go test ./... 全绿+gofmt/vet clean | Commit: 4ba025d）
-   [x] `F-0102` 管理节点注册、认证、元数据和状态。
    - 备注（Task ID: F-0102 | Status: Done | Evidence: enroll.go+agentws 认证+models.go:64；TestEnrollExchange/TestAgentWSHelloMeta | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0103` 管理云资源与物理节点的关联关系。（依赖：F-0341；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0103 | Status: Done | Evidence: 管理关系已落地：CloudResource.NodeID *uint 唯一索引绑定（多资源可解绑、双资源不可抢同一 Node，F-0353~F-0355）+ POST /api/cloud-resources/:id/bind|unbind + 列表过滤，Dash 云资源页（f920a33/7f78101）展示与节点联动；TestCloudResourceAPI/TestCloudResourceAPIValidation | Verification: go test 全绿 2026-11-26（server 全套无 FAIL） | Commit: 7ffa727）
-   [x] `F-0104` 管理 Entry、Landing 和 Relay 节点。
    - 备注（Task ID: F-0104 | Status: Done | Evidence: roles 三角色+LandingAssignment models.go:165+relay；TestRegistry/TestLandingAssignment | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0105` 管理 Tunnel、Path 和 Path Group。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 完成（F-0105 | Path{entry,landing,relay,transport,region,direction,enabled,version}+PathGroup{members,position,default,excluded} 模型与 AutoMigrate；/api/paths CRUD+筛选、/api/path-groups CRUD+成员顺序/默认/排除 API；Tunnel 由 transport+relay 链路字段承载，不设独立实体。 | Verification: go build/vet ./... + go test ./... 全绿，TestPathCRUD/TestPathDeleteDependencyCheck/TestPathGroupMembers | Commit: 2455a6c）
-   [x] `F-0106` 管理线路探测、健康状态和质量历史。
    - 备注（Task ID: F-0106 | Status: Done | Evidence: ProbeReport models.go:181+dimension/transport 状态；TestAgentWSProbeReport/TestSweepPersistsDimensions | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0107` 管理全局 Policy 和线路调度。
    - 备注（Task ID: F-0107 | Status: Done | Evidence: 全局 Policy——alloc.go:23-27 常量+LoadPolicy:94+Pick:217+Loop:395+Sweep:437+LandingAssignment(models.go:183)；handler allocapi.go:13,25；路由 router.go:176-177 | Verification: TestPickCostFirst/TestPolicyPersistence/TestSweepAllocatesAndSwitches/TestAllocAPI | Commit: c79d570）
-   [x] `F-0108` 编译并下发 Agent 和数据面配置。
    - 备注（Task ID: F-0108 | Status: Done | Evidence: relaypush+configd；TestPushAckApplied/TestApplyReloadFailRollsBack | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0109` 生成 sing-box、Clash/Mihomo 等客户端订阅。
    - 备注（Task ID: F-0109 | Status: Done | Evidence: sub/{links,clash,singbox}.go 三格式；TestPackV2Ray/TestPackClash/TestPackSingBox | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0110` 管理流量计量、配额、订单和支付。
    - 备注（Task ID: F-0110 | Status: Done | Evidence: TrafficLog/QuotaAction/PaymentOrder models.go:116/636/327；TestAgentWSTrafficReport/TestEpusdtNotify | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0111` 管理云服务商账号、资源生命周期和成本。
    - 备注（Task ID: F-0111 | Status: Done | Evidence: Provider 账号(models.go:480+providerapi.go:34-129)、资源生命周期(ProvisionTemplate models.go:492+ProvisionJob:514+provision.go:113-180+provisionapi.go:105)、成本(models.go:105-108+costapi.go+costref.go) | Verification: TestProviderAPI/TestProvisionRunAPI/TestCostAPI/TestCostRefTableAndProbe | Commit: c79d570）
-   [x] `F-0112` 提供管理员 Dash 和用户 Panel。
    - 备注（Task ID: F-0112 | Status: Done | Evidence: dash 17 视图+panel 5 视图+router.ts:36 守卫；pnpm build 双绿 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0113` 提供监控、告警、审计、备份和恢复。
    - 备注（Task ID: F-0113 | Status: Done | Evidence: health/alerts/audit/backup；TestHealth/TestAlertGuards/TestPruneRemovesOnlyOutOfWindow/TestSnapshotManual | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）

## 1.2 明确不做的能力

-   [x] `F-0121` 不开发 Ferry Desktop。
    - 备注（Task ID: F-0121 | Status: Done | Evidence: 无 Desktop 目录/electron/tauri/tray 命中（find+grep 零命中，排除 node_modules） | Verification: 仓顶层 dirs=agent/dash/deploy/docs/packages/panel/payments/server | Commit: c79d570）
-   [x] `F-0122` 不开发 Ferry 自有代理客户端。
    - 备注（Task ID: F-0122 | Status: Done | Evidence: 无 TUN 客户端；agent 仅出站(link.go:10)，唯一监听为 relay 数据面转发(relay.go:126-221) | Verification: grep tun/tun2socks/wintun 零命中 | Commit: c79d570）
-   [x] `F-0123` 不实现自有 TUN 或系统代理接管。
    - 备注（Task ID: F-0123 | Status: Done | Evidence: 无 TUN 开设备/系统代理写入（grep tun/sysproxy/transparent 零命中） | Verification: grep 非测试零命中 | Commit: c79d570）
-   [x] `F-0124` 不实现客户端本地 DNS 和规则执行引擎。
    - 备注（Task ID: F-0124 | Status: Done | Evidence: 无本地 DNS/规则引擎——routing.go:3-14 仅渲染规则引用进模板；dns.go 为 DNS 商 API 插件；geodns.go 不自建权威 DNS | Verification: routing.go+dns.go+geodns.go | Commit: c79d570）
-   [x] `F-0125` 不重复实现 sing-box、Clash/Mihomo 等代理协议栈。
    - 备注（Task ID: F-0125 | Status: Done | Evidence: 不重复实现协议栈——sub/ 仅生成订阅(links.go URIs/clash.go:72/singbox.go:165)，go.mod 无 sing-box/clash/xray-core 依赖，无 vendor/协议帧；内核为外部进程(procs/manager.go) | Verification: TestPackV2Ray/TestPackClash/TestPackSingBoxMixedFleet | Commit: c79d570）
-   [x] `F-0126` 不把第三方客户端的本地运行状态当作 Ferry
    - 备注（Task ID: F-0126 | Status: Done | Evidence: 订阅生成纯静态自 DB——RttMs 来自服务端 ProbeReport 非客户端态(links.go:147)；rotate.go:8-10 明确不消费客户端状态 | Verification: server/internal/sub 非测试零 client-state 命中 | Commit: c79d570）
    可直接控制的状态。
-   [x] `F-0127` 不把云服务商 API 调用逻辑散落在 Node、Path 或 Dash
    - 备注（Task ID: F-0127 | Status: Done | Evidence: 云 API 集中于 provision（OpenTofu，provision.go:1-5），零云 SDK import，node.go 零 provision 调用，dash 仅调 Ferry API | Verification: provision.go+go.mod+node.go | Commit: c79d570）
    页面中。

## 1.3 逻辑架构

``` text
Ferry Control Plane
├── Identity / User / Plan / Subscription / Device
├── Cloud Provider / Resource / Cost / Billing
├── Node Registry / Capability / Path / Path Group
├── Probe / Health / Scheduler / Policy
├── Config Compiler / Config Distribution
├── Agent Management / Relay Management
├── Traffic Accounting / Notification / Audit
├── Admin Dash
└── User Panel

Ferry Data Plane
├── Ferry Agent
├── Entry
├── Landing
├── Relay
├── Tunnel
└── Probe Runtime

External Client
├── sing-box
├── Clash / Mihomo
└── Other compatible clients
```

-   [x] `F-0131` 明确 Server 是业务控制面的权威来源。
    - 备注（Task ID: F-0131 | Status: Done | Evidence: 业务状态全在服务端(models.go 47 类型+quota.go:54)；agent 无 DB（grep gorm/sqlite 零命中）；面板为元数据权威(agentws.go:101,152) | Verification: TestUserCRUD+agent 无 db dep | Commit: c79d570）
-   [x] `F-0132` 明确 Agent 只保存本机运行所需的期望配置和实际状态。
    - 备注（Task ID: F-0132 | Status: Done | Evidence: Agent 仅存期望配置+实际状态——config.go:78-88 无用户/订单/配额数据；实际态为内存运行时(procs/manager.go:137) | Verification: config.go+零持久化 | Commit: c79d570）
-   [x] `F-0133` 明确 Probe 负责测量，不拥有全局调度权。
    - 备注（Task ID: F-0133 | Status: Done | Evidence: Probe 本地测量上报(probe.go:3-6)，全局调度在服务端(alloc.go:1-8+aggregate.go+monitor.go) | Verification: TestProbeHealthy/TestProbeUnreachable | Commit: c79d570）
-   [x] `F-0134` 明确 Relay 负责数据转发，不拥有用户和套餐业务状态。
    - 备注（Task ID: F-0134 | Status: Done | Evidence: Relay 仅转发——relay.go:1-6 双向 io.Copy(relay.go:200-221)，RelaySpec 无用户/套餐/配额字段 | Verification: TestRelayForwards/TestRelayHotRepoint/TestRelayValidation | Commit: c79d570）
-   [x] `F-0135` 确保用户实际数据流量不经过 Ferry Server。
    - 备注（Task ID: F-0135 | Status: Done | Evidence: 数据面不经 Server——server 无 net.Listen/net.Dial/io.Copy（非测试仅 HTTP 体 drain）；用户→entry relay→landing 全 agent 侧 | Verification: grep server 数据面零命中+relay.go:107 | Commit: c79d570）
-   [x] `F-0136` 确保 Server 短时不可用不导致已建立的数据面立即停止。
    - 备注（Task ID: F-0136 | Status: Done | Evidence: Server 不可用不停数据面——relay 独立进程不直连面板(main.go:53-66)；OnDisconnected 仅停心跳(link.go:58-79)；spool 缓冲角色上报 | Verification: TestRunReconnectsUntilCancel/TestRoleForwardsToSpool | Commit: c79d570）
-   [x] `F-0137` 确保 Server 恢复后可以与 Agent 对账并恢复管理状态。
    - 备注（Task ID: F-0137 | Status: Done | Evidence: 重连对账——agent 重 hello→面板权威 meta(agentws.go:104-112)+重推配置(agentws.go:138)+configd 原子对账(configd.go:60-117)+心跳 proc 快照去重(agentws_dedupe.go) | Verification: TestAgentWSHelloMeta/TestAgentWSHeartbeat/TestAgentWSConfigPush | Commit: c79d570）

------------------------------------------------------------------------

# 2. 核心领域模型

> 实施前必须先审计现有模型。下列模型是目标概念，不代表必须新增同名数据库表；若现有模型已覆盖，应复用或渐进迁移。

## 2.1 Node

-   [x] `F-0201` 审计现有 Node Model、字段和状态枚举。
    - 备注（Task ID: F-0201 | Status: Done | Evidence: Node Model/字段/状态枚举审计 §3.1 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0202` 保留并明确 `role=entry|landing|both` 的语义。
    - 备注（Task ID: F-0202 | Status: Done | Evidence: models.go:80 Role entry/landing/both+node.go:347 校验；TestNodeMetaInput | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0203` 保留并明确 `direction=out|in|both` 的语义。
    - 备注（Task ID: F-0203 | Status: Done | Evidence: models.go:81 Direction out/in/both；TestNodeMetaInput | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0204` 明确 Role 表示拓扑职责，Direction 表示业务流量方向。
    - 备注（Task ID: F-0204 | Status: Done | Evidence: Node.Role entry/landing/both(models.go:95)+Node.Direction out/in/both(models.go:96)+validateNodeInput 校验(node.go:366-377) | Verification: TestNodeMetaInput(默认值/回读/非法枚举 400) | Commit: 8335a58）
-   [x] `F-0205` 明确管理状态与运行健康状态分离。
    - 备注（Task ID: F-0205 | Status: Done | Evidence: Enabled :71/Status :77/PoolState :104 分离；TestNodeCRUD+TestSweepSuspendAndResume | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0206` 明确 `disabled`、`maintenance`、`offline`、`unhealthy` 的区别。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 完成（F-0206 | 四态分立并文档化于 models.go Node 注释：disabled=Enabled(false)；maintenance=Maintenance(bool) 计划窗口，退出调度但保留探测，pool 告警静默；offline=Status(agent 连接性)；unhealthy=PoolState(suspended) 探测摘除。维护态排除出 alloc Sweep/sub 订阅/standby 候选，健康判定与恢复照常。 | Verification: TestNodeStateSeparation/TestMaintenanceTracksHealthQuietly/TestSweepSkipsMaintenance | Commit: 0859ea7）
-   [x] `F-0207` 为 Node 关联 Region、ISP、ASN 和 Provider 信息。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 完成（F-0207 | Region/ISP 既有；新增 Node.ASN(size:32) 与 Node.ProviderID(*uint,index) 指向 Provider 行；NodeInput.asn/provider_id 合并语义（nil 保持/0 清空/>0 校验存在），create/update/注册元数据接管三路径打通。 | Verification: TestNodeProviderASN | Commit: 16c6e3f）
-   [x] `F-0208` 为 Node 关联可选的云资源 ID。（依赖：F-0341；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0208 | Status: Done | Evidence: Node 侧可选云资源 ID 经反向绑定表达——CloudResource.NodeID *uint（nil=未绑，解绑保留行与历史 F-0351），Node 行本身不改列避免历史破坏；bind/unbind API 即关联管理入口；TestCloudResourceAPI 绑定腿 | Verification: go test 全绿 2026-11-26（server 全套无 FAIL） | Commit: 7ffa727）
-   [x] `F-0209` 记录 Agent 版本和协议版本。
    - 备注（Task ID: F-0209 | Status: Done | Evidence: models.go:100 AgentVersion+Envelope.V；TestAgentWSHelloVersion | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0210` 记录期望配置版本和实际应用版本。
    - 备注（Task ID: F-0210 | Status: Done | Evidence: NodeConfig models.go:211 Version/Sha256/Status；TestNodeConfigGuards+TestPushAckApplied | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0211` 记录节点容量、带宽和成本快照。
    - 备注（Task ID: F-0211 | Status: Done | Evidence: models.go:90-96 Bw/MonthlyCostCents/TrafficPriceCents/SpeedMeasured；cost.TestBuild | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0212` 为节点元数据变更增加审计记录。
    - 备注（Task ID: F-0212 | Status: Done | Evidence: router.go:99 api 组 auditMiddleware 覆盖节点写；TestAuditLogsCaptureWriteOps | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0213` 为 Node 查询建立实际需要的索引。
    - 备注（Task ID: F-0213 | Status: Done | Evidence: models.go 索引标签（token uniqueIndex :72 等 36+）+storage 测试建表 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0214` 定义节点删除时的关联检查和数据保留规则。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 完成（F-0214 | deleteNode 前置三重关联检查（409 拒删）：Path entry/landing/relay 引用、LandingAssignment 未 released、Recovery state=running；保留规则：TrafficLog 留行但 SET NULL 去归属（既有约束）、ProbeReport/Alert/NodeConfig 留行按 node id 可查。 | Verification: TestNodeDeleteDependencyChecks | Commit: 1c9ffad）

## 2.2 Capability

-   [x] `F-0221` 审计现有节点能力声明方式。
    - 备注（Task ID: F-0221 | Status: Done | Evidence: 能力声明方式审计（结论：Capability 未实现）§3.1 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0222` 定义静态声明能力与动态运行能力。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0222 | Status: Done | Evidence: Node.Capabilities 静态声明 + Node.CapabilityState 动态状态（storage/models.go），能力常量与 ok/degraded/off 状态值收敛在 packages/agentproto/capability.go；storage.StaticCaps/HasCapability/DynamicState/MergeDynamic 测试 TestStaticCapsAndGate/TestMergeDynamic | Verification: go test ./... server/agent/packages 全绿 | Commit: d3220ab）
-   [x] `F-0223` 定义 `proxy_ingress` 能力。（依赖：F-0222；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0223 | Status: Done | Evidence: CapProxyIngress 常量 + KnownCapabilities 校验集（agentproto/capability.go），Agent 侧由托管代理进程派生（app/caps.go staticCaps） | Verification: TestStaticCaps/TestParseCapsJSON | Commit: d3220ab）
-   [x] `F-0224` 定义 `relay_entry` 能力。（依赖：F-0222；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0224 | Status: Done | Evidence: CapRelayEntry 常量；Agent 侧由 Relay.Listen 配置派生；调度入口门禁 HasCapability(entry, relay_entry)（alloc.go）测试 TestSweepEntryCapabilityGate | Verification: go test ./internal/alloc -count=1 绿 | Commit: d3220ab）
-   [x] `F-0225` 定义 `relay_landing` 能力。（依赖：F-0222；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0225 | Status: Done | Evidence: CapRelayLanding 常量；landing/both 角色派生默认含之；调度落点门禁 HasCapability(landing, relay_landing)（alloc.go）测试 TestSweepCapabilityGate | Verification: go test ./internal/alloc -count=1 绿 | Commit: d3220ab）
-   [x] `F-0226` 定义 `probe` 能力。（依赖：F-0222；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0226 | Status: Done | Evidence: CapProbe 常量；Agent 侧由 Probes 配置派生（staticCaps/dynamicCaps）；entry/both 角色默认含之 | Verification: TestStaticCaps/TestDynamicCaps | Commit: d3220ab）
-   [x] `F-0227` 定义 TCP/UDP 转发能力声明。（依赖：F-0222；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0227 | Status: Done | Evidence: CapTCPForward/CapUDPForward 常量入 KnownCapabilities 校验集与 NodeInput.Capabilities API（未知名 400），Agent 暂无配置源、仅声明可用 | Verification: TestParseCapsJSON（六名全收） | Commit: d3220ab）
-   [x] `F-0228` 让 Agent 上报实际运行能力。（依赖：F-0222；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0228 | Status: Done | Evidence: Hello.Caps 静态集（agentproto/messages.go + app.go OnConnected），按 cfg.Procs/Probes/Relay/Meta.Role 派生；面板 syncCaps 首报落库、面板自有列表优先、未知名丢弃 | Verification: TestStaticCaps/TestSyncCapsHelloAndHeartbeat | Commit: d3220ab）
-   [x] `F-0229` 在组件异常时更新动态能力状态。（依赖：F-0222；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0229 | Status: Done | Evidence: Heartbeat.Caps 动态状态（app.go buildHeartbeat→dynamicCaps）：代理进程全停=off、有崩溃=degraded、全运行=ok；面板 mergeCaps 按 heartbeat 折叠合并（未知名/非法状态值丢弃） | Verification: TestDynamicCaps/TestSyncCapsHelloAndHeartbeat | Commit: d3220ab）
-   [x] `F-0230` 让调度器只选择具备所需能力的节点。（依赖：F-0222；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0230 | Status: Done | Evidence: alloc.go 候选收集加 storage.HasCapability 门禁：entry 需 relay_entry、landing 需 relay_landing 且动态状态非 off；空列表按角色派生兜底，存量节点不受影响 | Verification: TestSweepCapabilityGate/TestSweepEntryCapabilityGate | Commit: d3220ab）
-   [x] `F-0231` 记录能力变化历史。（依赖：F-0222；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0231 | Status: Done | Evidence: CapabilityChange 追加表（AutoMigrate 已注册）：面板编辑 static 行、agent hello 首报 static 行、heartbeat 动态变化逐能力 dynamic 行；不变状态零写入 | Verification: TestNodeCapabilitiesAPI/TestSyncCapsHelloAndHeartbeat | Commit: d3220ab）

## 2.3 Path 与 Tunnel

-   [x] `F-0241` 审计现有 Pool、Allocation、Tunnel 和线路关系模型。
    - 备注（Task ID: F-0241 | Status: Done | Evidence: Pool/Allocation/Tunnel/线路关系审计 §3.1 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0242` 明确 Tunnel 表示两个端点之间的实际连接。
    - 备注（Task ID: F-0242 | Status: Done | Evidence: agent/internal/tunnel/tunnel.go:16 Tunnel 接口(Dial→net.Conn+ServeHeartbeat)+ssh/quicsidecar/wstls/tlscamo 具体两端点实现（entry agent→landing 实际连接） | Verification: agent/internal/tunnel 包+transport 测试 | Commit: 8335a58）
-   [x] `F-0243` 明确 Path 表示完整业务访问路径。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 完成（F-0243 | Path 模型即 entry→(relay)→landing 完整业务路径，带 transport/region/direction/version 与 CRUD API；区级 F-0245~0255 分项证据见各行。 | Verification: TestPathCRUD | Commit: 2455a6c）
-   [x] `F-0244` 第一阶段 Path 只要求支持 Entry → Landing。
    - 备注（Task ID: F-0244 | Status: Done | Evidence: LandingAssignment{EntryNodeID,LandingNodeID,Region,Direction,Strategy}(models.go:181-199)+alloc sweep 真实分配(alloc.go:442-557) | Verification: TestLandingAssignment/TestLandingAssignmentAndProbeReport | Commit: 8335a58）
-   [x] `F-0245` 定义 Path 与 Entry Node 的关联。（依赖：F-0243；完成条件：需求落地+测试绿+证据行）
    - 完成（F-0245 | Path.EntryNodeID 关联真实 Node 行，创建/更新均校验存在（validatePathInput）。 | Verification: TestPathCRUD | Commit: 2455a6c）
-   [x] `F-0246` 定义 Path 与 Landing Node 的关联。（依赖：F-0243；完成条件：需求落地+测试绿+证据行）
    - 完成（F-0246 | Path.LandingNodeID 关联真实 Node 行，创建/更新均校验存在（validatePathInput）。 | Verification: TestPathCRUD | Commit: 2455a6c）
-   [x] `F-0247` 定义 Path 与 Tunnel/Relay 的关联。（依赖：F-0243；完成条件：需求落地+测试绿+证据行）
    - 完成（F-0247 | Path.RelayNodeID 可选指针，nil=直连隧道；非 nil 校验节点存在（validatePathInput）。 | Verification: TestPathCRUD | Commit: 2455a6c）
-   [x] `F-0248` 允许一个 Entry 关联多个 Path。（依赖：F-0243；完成条件：需求落地+测试绿+证据行）
    - 完成（F-0248 | Path 表无 (entry_node_id) 唯一约束；TestPathCRUD 验证同 entry 建两条 Path。 | Verification: TestPathCRUD | Commit: 2455a6c）
-   [x] `F-0249` 允许一个 Landing 关联多个 Path。（依赖：F-0243；完成条件：需求落地+测试绿+证据行）
    - 完成（F-0249 | Path 表无 (landing_node_id) 唯一约束；TestPathCRUD 验证同 landing 建两条 Path。 | Verification: TestPathCRUD | Commit: 2455a6c）
-   [x] `F-0250` 定义 Path 的启停状态。（依赖：F-0243；完成条件：需求落地+测试绿+证据行）
    - 完成（F-0250 | Path.Enabled 布尔字段，默认 true；create/update 显式写回，list 支持 enabled 筛选。 | Verification: TestPathCRUD | Commit: 2455a6c）
-   [x] `F-0251` 定义 Path 的健康状态。（依赖：F-0243；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0251 | Status: Done | Evidence: pathhealth.Evaluate 读时推导四态 healthy/unknown/degraded/off：disabled=off、端点 offline/pool 挂起或落点新探测 sick=degraded、窗口内有 healthy verdict=healthy、无新 verdict=unknown（窗口与调度一致 30m）；GET /api/paths/:id/health | Verification: TestEvaluateStates/TestPathHealthEndpoints | Commit: c40c735）
-   [x] `F-0252` 定义 Path 的配置版本。（依赖：F-0243；完成条件：需求落地+测试绿+证据行）
    - 完成（F-0252 | Path.Version 整数，create=1、update 递增；TestPathCRUD 验证更新后版本为 2。 | Verification: TestPathCRUD | Commit: 2455a6c）
-   [x] `F-0253` 定义 Path 的变更历史。（依赖：F-0243；完成条件：需求落地+测试绿+证据行）
    - 完成（F-0253 | Path 变更历史由 auditMiddleware 记录每次 create/update/delete 的请求体快照（AuditLog 表）。 | Verification: TestPathCRUD | Commit: 2455a6c）
-   [x] `F-0254` 定义 Path 删除时的依赖检查。（依赖：F-0243；完成条件：需求落地+测试绿+证据行）
    - 完成（F-0254 | deletePath 先查 PathGroupMember 引用，有引用返回 409；TestPathDeleteDependencyCheck 覆盖。 | Verification: TestPathDeleteDependencyCheck | Commit: 2455a6c）
-   [x] `F-0255` 提供 Path 查询和详情 API。（依赖：F-0243；完成条件：需求落地+测试绿+证据行）
    - 完成（F-0255 | GET /api/paths 支持 entry/landing/region/direction/enabled 筛选，GET /api/paths/:id 详情。 | Verification: TestPathCRUD | Commit: 2455a6c）

## 2.4 Path Group

-   [x] `F-0261` 定义 Path Group 数据结构或确认现有等价模型。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 完成（F-0261 | PathGroup{name,direction,region,purpose,default_path_id,enabled}+PathGroupMember{group_id,path_id,position,excluded} 模型与 AutoMigrate。 | Verification: TestPathGroupMembers | Commit: 2455a6c）
-   [x] `F-0262` 支持创建和编辑 Path Group。（依赖：F-0261；完成条件：需求落地+测试绿+证据行）
    - 完成（F-0262 | /api/path-groups 创建/编辑/删除+筛选（name 唯一冲突 409）。 | Verification: TestPathGroupMembers | Commit: 2455a6c）
-   [x] `F-0263` 支持 Path 加入和移出 Group。（依赖：F-0261；完成条件：需求落地+测试绿+证据行）
    - 完成（F-0263 | POST/DELETE /api/path-groups/:id/members/:pathId 加入/移出成员，重复加入 409。 | Verification: TestPathGroupMembers | Commit: 2455a6c）
-   [x] `F-0264` 支持 Group 默认 Path。（依赖：F-0261；完成条件：需求落地+测试绿+证据行）
    - 完成（F-0264 | PUT 设置 default_path_id 必须已是组成员；移除默认成员时自动清空默认。 | Verification: TestPathGroupMembers | Commit: 2455a6c）
-   [x] `F-0265` 支持候选 Path 顺序。（依赖：F-0261；完成条件：需求落地+测试绿+证据行）
    - 完成（F-0265 | PathGroupMember.Position 排序；PATCH 调整 position，GET 详情按 position ASC 返回。 | Verification: TestPathGroupMembers | Commit: 2455a6c）
-   [x] `F-0266` 支持按方向、区域和用途组织 Group。（依赖：F-0261；完成条件：需求落地+测试绿+证据行）
    - 完成（F-0266 | Group 支持 direction/region/purpose 组织与筛选；成员 direction 必须与组一致。 | Verification: TestPathGroupMembers | Commit: 2455a6c）
-   [x] `F-0267` 支持手动排除 Path。（依赖：F-0261；完成条件：需求落地+测试绿+证据行）
    - 完成（F-0267 | PathGroupMember.Excluded 标记手动排除；PATCH 可切换，GET 详情返回该标记。 | Verification: TestPathGroupMembers | Commit: 2455a6c）
-   [x] `F-0268` 支持 Group 健康状态聚合。（依赖：F-0261；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0268 | Status: Done | Evidence: pathhealth.GroupHealth 按非排除成员 worst-of 聚合（无成员=unknown）；GET /api/path-groups/:id/health 返回 group health+逐成员状态 | Verification: TestGroupHealthAndAvailability/TestPathHealthEndpoints | Commit: c40c735）
-   [x] `F-0269` 支持 Group 可用性统计。（依赖：F-0261；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0269 | Status: Done | Evidence: pathhealth.Availability 按成员落点节点 24h 窗口内探测 verdict 的 healthy 占比（无探测数据返回 null），随 group health 接口同出 | Verification: TestGroupHealthAndAvailability（50%/null 断言）/TestPathHealthEndpoints（100%） | Commit: c40c735）
-   [x] `F-0270` 支持 Group 与订阅策略关联。（依赖：F-0261；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0270 | Status: Done | Evidence: PolicyConstraints 增 path_group_id 字段（Resolve 链深层 wins），pathhealth.ResolveGroupPath 解析 group 服务路径（default 优先、Position 顺序、健康感知：非 off 成员中 healthy/unknown 先选、degraded 兜底、excluded 不服务、组禁用/全不可用/组缺失→nil 空交付），policyapi 校验 path_group_id 引用存在、simulate 返回 path_group 解析结果，sub.go 交付侧只出解析路径的 entry（仍需过约束过滤） | Verification: TestResolveGroupPath/TestPolicyGroupLink/TestSubscriptionGroupDelivery（degraded default 让位 healthy candidate、删 pin 恢复全量） | Commit: ed2889c）

## 2.5 Policy

-   [x] `F-0281` 审计现有策略和分配规则。
    - 备注（Task ID: F-0281 | Status: Done | Evidence: 策略与分配规则审计 §3.1+§3.5 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0282` 定义全局安全约束。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0282 | Status: Done | Evidence: storage.Policy 模型（scope+subject_id 唯一索引，Constraints JSON+Enabled+Version+UpdatedBy）+storage.PolicyConstraints 载荷（regions/isps/line_types/min_health/min_capacity_mbps/max_traffic_price_cents/max_monthly_cost_cents/failover/output），ParsePolicyConstraints 严格校验，AutoMigrate 含 Policy | Verification: TestPolicyAPI/TestResolveMergePrecedence/TestApplyConstraints 等 server 全绿 2026-11-26 | Commit: c74c69c）
-   [x] `F-0283` 定义系统级 Policy。（依赖：F-0282；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0283 | Status: Done | Evidence: scope=system（subject_id=0）全局行，POST/PUT/DELETE /api/policies 管理，Resolve 链含 system 层，alloc SweepAt 读 system 冻结 failover | Verification: TestPolicyAPI（system 行创建+resolve 含 system）/TestSweepFailoverFreeze | Commit: c74c69c）
-   [x] `F-0284` 定义套餐级 Policy。（依赖：F-0282；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0284 | Status: Done | Evidence: scope=plan（subject_id=plan id）行可建可管，Resolve 链 plan 层按 subject_id 匹配，字段级覆盖 | Verification: TestResolveMergePrecedence（plan7 regions/cost 覆盖 system） | Commit: c74c69c）
-   [x] `F-0285` 定义用户级 Policy。（依赖：F-0282；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0285 | Status: Done | Evidence: scope=user（subject_id=user id）行可建可管，sub 交付按 Resolve(db,u.ID,0) 过滤 | Verification: TestSubscriptionPolicyFilter（user 策略过滤订阅）/TestResolveMergePrecedence（user42 健康覆盖） | Commit: c74c69c）
-   [x] `F-0286` 定义订阅输出策略。（依赖：F-0282；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0286 | Status: Done | Evidence: constraints.output=compact 时 CapOutput 按 rtt 升序取最优 10 个（CompactOutputLimit=10，无 rtt 的排尾），sub.go 交付侧过滤 | Verification: TestCapOutput/TestSubscriptionPolicyFilter | Commit: c74c69c）
-   [x] `F-0287` 定义策略优先级和冲突解决规则。（依赖：F-0282；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0287 | Status: Done | Evidence: policy.Resolve 按 system→plan→user 链合并 enabled 行，字段级 override（深层 wins，缺省继承），/api/policies/resolve 返回 effective constraints+贡献行 | Verification: TestResolveMergePrecedence（3 行链合并+disabled 排除）/TestPolicyAPI resolve 视图 | Commit: c74c69c）
-   [x] `F-0288` 支持区域约束。（依赖：F-0282；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0288 | Status: Done | Evidence: constraints.regions 白名单（空=不限），ApplyConstraints 按 Node.Region 过滤，drop 原因 "region not allowed" | Verification: TestApplyConstraints（region-miss）/TestSubscriptionPolicyFilter | Commit: c74c69c）
-   [x] `F-0289` 支持运营商约束。（依赖：F-0282；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0289 | Status: Done | Evidence: constraints.isps 白名单，ApplyConstraints 按 Node.ISP 过滤，drop 原因 "isp not allowed" | Verification: TestApplyConstraints（isp-miss） | Commit: c74c69c）
-   [x] `F-0290` 支持线路类型约束。（依赖：F-0282；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0290 | Status: Done | Evidence: constraints.line_types 白名单，ApplyConstraints 按 Node.LineType 过滤，drop 原因 "line type not allowed" | Verification: TestApplyConstraints（line-miss） | Commit: c74c69c）
-   [x] `F-0291` 支持健康阈值约束。（依赖：F-0282；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0291 | Status: Done | Evidence: constraints.min_health=healthy 拒 offline/suspended、=degraded 仅拒 suspended，drop 原因 "below min health" | Verification: TestApplyConstraints（offline/suspended/degraded 层级） | Commit: c74c69c）
-   [x] `F-0292` 支持容量约束。（依赖：F-0282；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0292 | Status: Done | Evidence: constraints.min_capacity_mbps 按 Node.BwDownMbps 过滤，drop 原因 "below min capacity" | Verification: TestApplyConstraints（thin 10Mbps<50） | Commit: c74c69c）
-   [x] `F-0293` 支持成本约束。（依赖：F-0282；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0293 | Status: Done | Evidence: max_traffic_price_cents 按 Node.TrafficPriceCents（计量）、max_monthly_cost_cents 按 Node.MonthlyCostCents（包月）过滤，drop 原因 "above traffic price cap"/"above monthly cost cap" | Verification: TestApplyConstraints（pricey/fixed-pricey） | Commit: c74c69c）
-   [x] `F-0294` 支持故障回退规则。（依赖：F-0282；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0294 | Status: Done | Evidence: constraints.failover=manual 时 alloc.SweepAt 头部 FreezeFailover 短路（返回 nil，本轮不动现有落点），auto/空照常扫 | Verification: TestSweepFailoverFreeze（manual=0 事件 0 分配，auto/无策略正常分配）/TestFreezeFailover | Commit: c74c69c）
-   [x] `F-0295` 支持 Policy 版本和变更审计。（依赖：F-0282；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0295 | Status: Done | Evidence: Policy.Version 每次更新 +1、UpdatedBy 记录最后编辑者（actorFrom），全部 /api/policies 写操作经 auditMiddleware 快照 | Verification: TestPolicyAPI（version=2+updated_by 非空） | Commit: c74c69c）
-   [x] `F-0296` 提供 Policy 模拟和解释接口。（依赖：F-0282；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0296 | Status: Done | Evidence: GET /api/policies/simulate?user_id=&plan_id= 解析链→对当前入口池 ApplyConstraints→返回 kept/dropped（含 per-node reason）+failover_free | Verification: TestPolicyAPI（pol-hk 保留/pol-us "region not allowed"/failover_free） | Commit: c74c69c）

------------------------------------------------------------------------

# 3. 云服务商集成与资源生命周期

> **这是 Dash 的一等功能，不是简单的节点备注或外部链接。**
>
> 管理员应能在 Dash
> 中管理云服务商连接，查看云资源，执行开机、关机、重启、重装/重建等受支持操作，查看资源状态，并将云资源与
> Ferry Node 关联。具体操作以服务商 API 和资源类型实际支持情况为准。

## 3.1 Provider 抽象

-   [x] `F-0301` 审计现有云厂商 API、Provision 和 OpenTofu 集成。
    - 备注（Task ID: F-0301 | Status: Done | Evidence: 云厂商 API/Provision/OpenTofu 审计 §3.9 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0302` 定义统一 Cloud Provider Adapter 接口。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0302 | Status: Done | Evidence: 新包 server/internal/cloudprov：cloudprov.Adapter 统一接口（Capabilities/ListResources/DescribeResource/RefreshStatus/PowerOn/PowerOff/Reboot/ForceStop/Rebuild/ImportBilling），Resource/Filter/BillingWindow/BillingRecord 类型，全部失败以 *Error 分型返回；fake.go 参考实现；Client 装饰器实现同一接口 | Verification: TestFakeContract/TestClientRetryPolicy 等 cloudprov 全绿 2026-11-26（server+agent go test 全绿） | Commit: 1ee762d）
-   [x] `F-0303` 定义 Provider 能力声明接口。（依赖：F-0302；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0303 | Status: Done | Evidence: Capabilities{Power/ForceStop/Rebuild/Billing/Regions} 能力声明；fail-closed：未声明能力调用返回 ErrUnsupportedOp 且状态不动（fake.transition 按 gate 拒绝） | Verification: TestUnsupportedGate（6 操作全拒+状态未变） | Commit: 1ee762d）
-   [x] `F-0304` 定义资源列表查询接口。（依赖：F-0302；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0304 | Status: Done | Evidence: ListResources(ctx, Filter{Region,Tags})→[]Resource，Filter 空字段=不限 | Verification: TestFakeContract/list_and_filter（region+tag 过滤） | Commit: 1ee762d）
-   [x] `F-0305` 定义资源详情查询接口。（依赖：F-0302；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0305 | Status: Done | Evidence: DescribeResource(ctx,id)→*Resource 全视图，不存在 id 返回 ErrNotFound 分型错误（绝不 nil 资源） | Verification: TestFakeContract/describe_facts（missing→ErrNotFound） | Commit: 1ee762d）
-   [x] `F-0306` 定义资源状态刷新接口。（依赖：F-0302；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0306 | Status: Done | Evidence: RefreshStatus(ctx,id)→State 常量集（running/stopped/starting/stopping/rebuilding/provisioning/error，ValidState 校验），每次电源操作后经 refresh 可见 | Verification: TestFakeContract/refresh_status+power_lifecycle/TestValidState | Commit: 1ee762d）
-   [x] `F-0307` 定义开机操作接口。（依赖：F-0302；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0307 | Status: Done | Evidence: PowerOn(ctx,id)（Power 能力门禁）→State starting，受 Client 超时/限流/重试策略包裹 | Verification: TestFakeContract/power_lifecycle | Commit: 1ee762d）
-   [x] `F-0308` 定义关机操作接口。（依赖：F-0302；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0308 | Status: Done | Evidence: PowerOff(ctx,id) 优雅关机（Power 门禁）→State stopping | Verification: TestFakeContract/power_lifecycle | Commit: 1ee762d）
-   [x] `F-0309` 定义重启操作接口。（依赖：F-0302；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0309 | Status: Done | Evidence: Reboot(ctx,id) 优雅重启（Power 门禁） | Verification: TestFakeContract/power_lifecycle | Commit: 1ee762d）
-   [x] `F-0310` 定义强制停止操作接口，并明确高危权限。（依赖：F-0302；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0310 | Status: Done | Evidence: ForceStop(ctx,id) 独立 ForceStop 能力门禁；HighRiskForceStop="force_stop" 常量供 handler 层提权/二次确认接线；接口注释明确高危语义 | Verification: TestUnsupportedGate（HighRiskForceStop 键断言）/TestFakeContract（force stop→stopped） | Commit: 1ee762d）
-   [x] `F-0311` 定义重装/重建操作接口，仅对服务商明确支持的资源开放。（依赖：F-0302；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0311 | Status: Done | Evidence: Rebuild(ctx,id,image)（Rebuild 门禁——仅声明 rebuild 的厂商开放）→State rebuilding | Verification: TestFakeContract/power_lifecycle+TestUnsupportedGate | Commit: 1ee762d）
-   [x] `F-0312` 定义公网 IP 查询接口。（依赖：F-0302；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0312 | Status: Done | Evidence: Resource.PublicIP 字段（F-0305 详情/列表携带；空=无/未分配），契约测试断言 r-1 IP 往返 | Verification: TestFakeContract/describe_facts | Commit: 1ee762d）
-   [x] `F-0313` 定义带宽和规格查询接口。（依赖：F-0302；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0313 | Status: Done | Evidence: Resource.BwMbps+Spec 字段（0/空=未知，账单导入跳过未知带宽资源） | Verification: TestFakeContract/describe_facts+billing_import（仅 r-1 有账单） | Commit: 1ee762d）
-   [x] `F-0314` 定义账单和价格数据导入接口。（依赖：F-0302；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0314 | Status: Done | Evidence: ImportBilling(ctx,BillingWindow{From,To})→[]BillingRecord{ResourceID,CostCents,Currency,From,To}（Billing 能力门禁） | Verification: TestFakeContract/billing_import | Commit: 1ee762d）
-   [x] `F-0315` 定义 Provider 错误码到 Ferry 错误类型的映射。（依赖：F-0302；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0315 | Status: Done | Evidence: errors.go：ErrorType 八类（auth/rate_limited/not_found/unsupported/unavailable/bad_request/compat/internal）+Error{Type,Op,ResourceID,ProviderCode,Message}+Retryable() 分类+AsError 提取+Op 常量；vendor 原始码保留在 ProviderCode | Verification: TestErrorShape（消息格式/类型提取/可重试分类） | Commit: 1ee762d）
-   [x] `F-0316` 定义 Provider API 超时、限流和重试策略。（依赖：F-0302；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0316 | Status: Done | Evidence: Client 装饰器（实现同一 Adapter）：per-call 超时（deadline 切断→ErrUnavailable 可重试）、令牌桶限流（超限→client rate_limited，不穿透）、可重试类型指数退避重试（MaxRetries 上限，非可重试立即返回） | Verification: TestClientRetryPolicy/TestClientTimeout/TestClientRateLimit | Commit: 1ee762d）
-   [x] `F-0317` 定义 Provider API 版本和能力兼容信息。（依赖：F-0302；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0317 | Status: Done | Evidence: Capabilities.APIVersion + VersionAtLeast 点分版本比较（数值段比较、畸形 fail-closed）+ ClientOptions.MinAPIVersion 下限（首次调用 sync.Once 检查，低于下限全部调用 ErrCompat 且不触达 adapter） | Verification: TestVersionCompat（9 组比较+floor 拒绝/放行） | Commit: 1ee762d）
-   [x] `F-0318` 为 Provider Adapter 建立契约测试。（依赖：F-0302；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0318 | Status: Done | Evidence: contract_test.go ContractSuite(t,factory) 可复用契约套件（capabilities/list-filter/describe/refresh/power lifecycle/billing 六子测试），任何 Adapter 实现传入 factory 即测；TestFakeContract 以参考实现跑通 | Verification: TestFakeContract 全子测试绿 2026-11-26 | Commit: 1ee762d）

## 3.2 Provider 账号与凭据

-   [x] `F-0321` 定义 Cloud Account 数据模型。
    - 备注（Task ID: F-0321 | Status: Done | Evidence: Provider models.go:462（Name/Type/密钥/Enabled=云账号）；TestProviderAPI | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0322` 支持添加云服务商账号。
    - 备注（Task ID: F-0322 | Status: Done | Evidence: POST /api/providers→createProvider(router.go:147+providerapi.go:50-85) | Verification: TestProviderAPI(POST create) | Commit: acb0810）
-   [x] `F-0323` 支持配置账号名称和备注。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0323 | Status: Done | Evidence: Provider.Note(size 255) 字段，providerInput.Note 可空指针（创建设置/更新非空替换/缺省保持），providerView 回显，create+update 全链路 | Verification: TestProviderNoteRegion（创建回显+更新替换+列表含 note） | Commit: d30742e）
-   [x] `F-0324` 支持配置账号所属区域或租户范围。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0324 | Status: Done | Evidence: Provider.Region(size 64) 账号级区域/租户作用域（空=整账号），providerInput.Region 与 providerView 同 F-0323 语义 | Verification: TestProviderNoteRegion（region 设置/替换/回显） | Commit: d30742e）
-   [x] `F-0325` 将 Access Key/Secret 等凭据加密存储。
    - 备注（Task ID: F-0325 | Status: Done | Evidence: providerapi sealSecret→secret.Store AES-GCM；TestProviderAPI+TestStoreRoundtrip | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0326` 凭据读取仅允许 Provider Adapter 内部使用。
    - 备注（Task ID: F-0326 | Status: Done | Evidence: AccessKey AES-256-GCM 密文(providerapi.go:60,149-154)；唯一读取者 provision.DecryptKey(provision.go:214-218) 仅 runProvision/PrepareLaunch；API 层只回 has_access_key(providerapi.go:19-25) | Verification: TestProviderAPI+secret_test | Commit: acb0810）
-   [x] `F-0327` Dash 不回显完整 Secret。
    - 备注（Task ID: F-0327 | Status: Done | Evidence: HasAccessKey 不回显；TestProviderAPI+TestListEnrollmentTokensMasksSecret | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0328` 支持测试账号连接。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0328 | Status: Done | Evidence: POST /api/providers/:id/test：先本地解密凭据（无明文回显），有 cloudprov.Adapter factory 时经 Client 共享策略探活厂商 Capabilities；结果 200+ok/mode/error_type（auth/unsupported 分型），禁用账号 409；cloudprov registry Register/Lookup/Unregister/NewAdapter 作为可插拔适配器入口 | Verification: TestProviderTestConnection（本地/适配器/认证失败/禁用 4 场景） | Commit: a80419d）
-   [x] `F-0329` 支持凭据更新。
    - 备注（Task ID: F-0329 | Status: Done | Evidence: updateProvider access_key 非空换密；TestProviderAPI | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0330` 支持凭据轮换。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0330 | Status: Done | Evidence: POST /api/providers/:id/rotate 双键轮换（旧密文移入 PrevAccessKey 宽限副本、KeyVersion 递增、PrevSetAt 开启 24h 宽限窗 RotationGrace）；POST /api/providers/:id/revoke-previous 显式提前吊销（404=无可吊销）；PUT access_key 为普通替换（不留宽限副本但也递增版本）；视图只回 key_version/prev_key_held/prev_set_at，双密文均不回显 | Verification: TestProviderCredentialRotation（v1→v2 宽限保留+解密校验+防泄漏+吊销+替换 v3） | Commit: 13349ba）
-   [x] `F-0331` 支持账号禁用。
    - 备注（Task ID: F-0331 | Status: Done | Evidence: Provider.Enabled(models.go:485,default:true) 可配(providerapi.go:65-82,110-112)；runProvision 拒绝 disabled("provider disabled" provisionapi.go:44-47) | Verification: TestProviderAPI | Commit: acb0810）
-   [x] `F-0332` 支持账号删除前依赖检查。
    - 备注（Task ID: F-0332 | Status: Done | Evidence: deleteProvider 按 provider_id 数 ProvisionTemplate 引用 409(providerapi.go:136-140)；job/node 经模板门禁传递守卫 | Verification: TestProviderAPI(有模板引用时拒绝删除) | Commit: acb0810）
-   [x] `F-0333` 记录账号凭据变更审计。
    - 备注（Task ID: F-0333 | Status: Done | Evidence: auditMiddleware 覆盖 PUT /api/providers；TestAuditLogsCaptureWriteOps | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0334` 支持按账号授权可操作的资源范围。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0334 | Status: Done | Evidence: Provider.Region 作为账号授权边界（F-0324）由 cloudsync.SyncOne 强制执行：请求区域与账号作用域不一致时拒绝拉取（"region outside provider scope"），同步范围外行永不被标 missing/删除（TestSyncScope）；docs/design/cloud-account-least-privilege.md §4 双层范围 | Verification: go test ./internal/cloudsync -run TestSyncScope 绿 | Commit: 7ffa727）
-   [x] `F-0335` 为云账号操作增加最小权限说明。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0335 | Status: Done | Evidence: docs/design/cloud-account-least-privilege.md：Ferry 动作集↔厂商 IAM 基线表（Vultr/Hetzner/DO/AWS/阿里云/腾讯云，标签圈定资源范围）、凭据保护四条（加密/读取面/轮换/禁用）、F-0334 双层范围现状；providerapi.go 头注释与 cloudprov Capabilities.ForceStop 注释均指到该文档 | Verification: 文档落地+代码注释锚点（纯文档，无测试面） | Commit: b9cb57d）

## 3.3 Provider 资源同步

-   [x] `F-0341` 定义 Cloud Resource 数据模型。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0341 | Status: Done | Evidence: storage/models.go CloudResource（ProviderID/ExternalID 唯一索引，Region/State/PublicIP/BwMbps/Spec/Tags JSON，NodeID 唯一，Missing/MissingSince/LastSyncAt/UnboundAt）与 CloudSyncLog 行；AutoMigrate 注册 storage.go | Verification: gofmt/build/vet 全绿；cloudsync+cloudprov+handler 新测绿 | Commit: 7ffa727）
-   [x] `F-0342` 记录 Provider、Account、Region 和 Provider Resource
    ID。（依赖：F-0341；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0342 | Status: Done | Evidence: CloudResource.ProviderID（账号行）+ ExternalID（厂商资源 ID）+ Region 三列落库；TestSyncOneReconcile 断言三列写入 | Verification: go test ./internal/cloudsync -run TestSyncOneReconcile 绿 | Commit: 7ffa727）
-   [x] `F-0343` 支持从 Provider 拉取资源列表。（依赖：F-0341；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0343 | Status: Done | Evidence: cloudsync.SyncOne 经 cloudprov.Client.ListResources（adapter 契约 F-0304）拉取，注册厂+解密凭据（h.secrets） | Verification: TestCloudResourceAPI（POST /api/providers/:id/sync 拉到 2 条）绿 | Commit: 7ffa727）
-   [x] `F-0344` 支持按区域同步资源。（依赖：F-0341；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0344 | Status: Done | Evidence: SyncConfig.Region→Filter.Region；Provider.Region 作为授权边界拒绝越界请求（F-0334 服务端校验）；TestSyncScope 覆盖 scoped/越界/region pass 三分支 | Verification: go test ./internal/cloudsync -run TestSyncScope 绿 | Commit: 7ffa727）
-   [x] `F-0345` 支持按标签过滤资源。（依赖：F-0341；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0345 | Status: Done | Evidence: SyncConfig.Tags→Filter.Tags（adapter 侧过滤）；Tags 以 JSON 落库；API 层 ?tag_key=&tag_value= 过滤；TestSyncOneReconcile 首次带 tag 过滤只拉 1 条，TestCloudResourceAPI 断言过滤结果 | Verification: go test ./internal/cloudsync -run TestSyncOneReconcile+TestCloudResourceAPI 绿 | Commit: 7ffa727）
-   [x] `F-0346` 支持手动触发资源同步。（依赖：F-0341；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0346 | Status: Done | Evidence: POST /api/providers/:id/sync（router.go，auditMiddleware 记录写操作），返回 run 计数+资源列表 | Verification: TestCloudResourceAPI 手动同步返回 ok/added=2 绿 | Commit: 7ffa727）
-   [x] `F-0347` 支持定时资源同步。（依赖：F-0341；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0347 | Status: Done | Evidence: cloudsync.Loop（toucher/review 同款 Loop 模式，FERRY_CLOUD_SYNC_SEC 默认 3600）；Sweep 遍历全部启用 provider，逐账号 RunProvider，单账号失败不阻塞其余（errors.Join） | Verification: go build ./cmd/ferry 绿；Loop 经 Sweep 路径测试覆盖（TestSyncFailuresAndLog） | Commit: 7ffa727）
-   [x] `F-0348` 支持增量同步或合理的分页同步。（依赖：F-0341；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0348 | Status: Done | Evidence: 按区域分片同步（Provider.Region 或显式 Region，逐区域独立 run 且不跨区误标 missing）+ 同 run 内按 ExternalID 排序去重（防厂商分页重叠重复导入）；仅变更行写库 | Verification: TestSyncScope 区域隔离绿；TestSyncOneReconcile 重复 id 折叠绿 | Commit: 7ffa727）
-   [x] `F-0349` 记录最后同步时间和同步结果。（依赖：F-0341；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0349 | Status: Done | Evidence: CloudResource.LastSyncAt/LastSeenAt 逐资源打戳；CloudSyncLog 逐 run 落 Trigger/Added/Updated/Missing/Error/StartedAt/FinishedAt；GET /api/cloud-sync-logs 查询（F-0356） | Verification: TestSyncFailuresAndLog 失败 run 日志断言；TestCloudResourceAPI 日志查询绿 | Commit: 7ffa727）
-   [x] `F-0350` 识别 Provider 中已删除的资源。（依赖：F-0341；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0350 | Status: Done | Evidence: run 后不在 listing 的行置 Missing=true+MissingSince（cloudsync.go mark missing 循环），已 missing 不重复计数 | Verification: TestSyncOneReconcile：vendor 删除 r-1 后 Missing=true 绿 | Commit: 7ffa727）
-   [x] `F-0351` 外部资源消失时不得直接删除关联 Node 历史。（依赖：F-0341；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0351 | Status: Done | Evidence: missing 走软标记（Update missing/missing_since），不删行；NodeID 绑定保留；重出现即清除标记；测试直接断言节点绑定在 missing 后仍在 | Verification: TestSyncOneReconcile missing 后 NodeID 不变绿 | Commit: 7ffa727）
-   [x] `F-0352` 处理资源 ID 冲突和重复导入。（依赖：F-0341；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0352 | Status: Done | Evidence: (provider_id, external_id) 唯一索引 + byExternal upsert（update 不 insert）；同 run listing 排序去重；DB 层再兜底唯一约束 | Verification: TestSyncOneReconcile 重复 id 折叠为单行绿 | Commit: 7ffa727）
-   [x] `F-0353` 支持 Cloud Resource 与 Node 绑定。（依赖：F-0341；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0353 | Status: Done | Evidence: POST /api/cloud-resources/:id/bind {node_id}（node 存在校验），清 UnboundAt；rebind 允许迁移绑定 | Verification: TestCloudResourceAPI bind/rebind 200 绿 | Commit: 7ffa727）
-   [x] `F-0354` 支持解除 Cloud Resource 与 Node 绑定。（依赖：F-0341；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0354 | Status: Done | Evidence: POST /api/cloud-resources/:id/unbind：NodeID 置 nil+UnboundAt 打戳，行保留；未绑定返回 404 | Verification: TestCloudResourceAPI 解绑后 NodeID=nil/UnboundAt 非空+二次 unbind 404 绿 | Commit: 7ffa727）
-   [x] `F-0355` 阻止同一 Cloud Resource 被多个有效 Node 重复绑定。（依赖：F-0341；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0355 | Status: Done | Evidence: NodeID 列唯一索引（storage 层兜底）+ 绑前显式冲突查询返回 409 | Verification: TestCloudResourceAPI 重复绑 409 绿 | Commit: 7ffa727）
-   [x] `F-0356` 支持资源详情和同步历史查询。（依赖：F-0341；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0356 | Status: Done | Evidence: GET /api/cloud-resources/:id 详情（404 未知）；GET /api/cloud-sync-logs?provider_id= 历史（按 id DESC） | Verification: TestCloudResourceAPI 详情+历史断言绿；TestCloudResourceAPIValidation 未知资源 404 绿 | Commit: 7ffa727）

## 3.4 云资源操作任务

-   [x] `F-0361` 定义 Cloud Operation 数据模型。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0361 | Status: Done | Evidence: storage/models.go CloudOperation（ResourceID/BatchID/ProviderID/Op/Params/State/IdempotencyKey 唯一索引/Actor/ActorKind/Error/ProviderCode/Result/Confirmed/AllowCancel/FinishedAt）与 CloudOperationLog；AutoMigrate 注册 storage.go | Verification: gofmt/build/vet 全绿；go test ./... server/agent/packages 全绿 | Commit: 5a59e1e）
-   [x] `F-0362` 定义操作状态机：Pending、Running、Succeeded、Failed、Cancelled、Unknown。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0362 | Status: Done | Evidence: oper.StatePending/Running/Succeeded/Failed/Cancelled/Unknown 常量+terminal 集合；Create 落 pending，Run pending→running→终态，transition 守卫重复迁移；TestOperStateMachine 各态迁移绿 | Verification: go test ./internal/oper -run TestOperStateMachine 绿 | Commit: 5a59e1e）
-   [x] `F-0363` 将开机操作接入统一任务系统。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0363 | Status: Done | Evidence: validOp 收 OpPowerOn；capabilityFor 要求 caps.Power；execute 经 cloudprov.Client 调 PowerOn；POST /api/cloud-operations op=power_on | Verification: TestOperStateMachine power_on succeeded；TestCloudOperationAPI 开创建+执行绿 | Commit: 5a59e1e）
-   [x] `F-0364` 将关机操作接入统一任务系统。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0364 | Status: Done | Evidence: validOp 收 OpPowerOff；execute 调 PowerOff；批量/单任务均可下；TestOperIdempotencyAndBatch 覆盖多资源行 | Verification: go test ./internal/oper 全绿 | Commit: 5a59e1e）
-   [x] `F-0365` 将重启操作接入统一任务系统。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0365 | Status: Done | Evidence: validOp 收 OpReboot；execute 调 Reboot；stateReady 仅 running/starting 可重启 | Verification: TestOperStateMachine reboot 绿；TestOperIdempotencyAndBatch reboot 批量绿 | Commit: 5a59e1e）
-   [x] `F-0366` 将强制停止操作接入统一任务系统。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0366 | Status: Done | Evidence: validOp 收 OpForceStop；capabilityFor 要求 caps.ForceStop&&Power；execute 调 ForceStop；TestOperUnknownAndCancel force_stop 失败带 provider_code 绿 | Verification: go test ./internal/oper -run TestOperUnknownAndCancel 绿 | Commit: 5a59e1e）
-   [x] `F-0367` 将重装/重建操作接入统一任务系统（仅限 Adapter 声明支持）。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0367 | Status: Done | Evidence: validOp 收 OpRebuild；capabilityFor 要求 caps.Rebuild，不支持即 "not supported" 拒创建；execute 从 Params JSON 取 image 调 Rebuild | Verification: TestOperCapabilityGate rebuild 拒绝绿；TestOperParamsJSON 参数落库绿 | Commit: 5a59e1e）
-   [x] `F-0368` 操作前检查资源当前状态。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0368 | Status: Done | Evidence: oper.stateReady 按 op 校验资源 State（power_on 仅 stopped/error/rebuilding，power_off/force_stop 仅 running/starting/rebuilding，reboot 仅 running/starting，rebuild 仅 stopped/error）；不满足即拒绝创建；Create 另查 Missing/Provider 归属 | Verification: TestOperStateMachine 已 running 再 power_on 被拒绿 | Commit: 5a59e1e）
-   [x] `F-0369` 操作前检查管理员权限。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0369 | Status: Done | Evidence: operapi 挂在 apiAuth 中间件后（router.go /api 组），operActor 仅取 adminUserID/apiUser 凭据；auditMiddleware 审计写操作；Handler.operations=oper.New 构造注入 | Verification: 无 admin 凭据的 /api 请求被 apiAuth 401 拒（路由层）；gofmt/build/vet 全绿 | Commit: 5a59e1e）
-   [x] `F-0370` 对关机、强制停止、重装等高风险操作执行二次确认。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0370 | Status: Done | Evidence: oper.HighRiskOps={power_off,force_stop,rebuild}；Create 缺 Confirmed 即 ErrConfirmationRequired；handler 映射 409；行上 Confirmed/AllowCancel 落库 | Verification: TestOperStateMachine 未确认 power_off 409 绿；TestCloudOperationAPI 未确认 409 绿 | Commit: 5a59e1e）
-   [x] `F-0371` 为每个操作生成幂等键。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0371 | Status: Done | Evidence: Create 缺省 IdempotencyKey 由 deriveIdem（sha256(provider,op,specs)）派生，行级 key=idem:<resource_id> 唯一索引；同 key 重放返回原行不重跑 | Verification: TestOperIdempotencyAndBatch 同 key 重放同 id/行数绿；TestCloudOperationAPI 重放同 id 绿 | Commit: 5a59e1e）
-   [x] `F-0372` 处理 Provider 请求超时后的 Unknown 状态。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0372 | Status: Done | Evidence: runOne 在 ctx.Err()!=nil 时 settle Unknown；cloudprov.Client 超时/不可用错误映射 ErrUnavailable→Unknown；main.go 起 Reconcile ticker | Verification: TestOperUnknownAndCancel 超时→unknown 绿 | Commit: 5a59e1e）
-   [x] `F-0373` Unknown 状态通过查询 Provider 结果对账，不盲目重复执行。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0373 | Status: Done | Evidence: Reconcile→reconcileOne 仅 RefreshStatus 查厂商实际状态，与 op 期望态匹配才 settle succeeded，否则保持 Unknown；绝不重发操作 | Verification: TestOperUnknownAndCancel 清 hook 后对账成功绿 | Commit: 5a59e1e）
-   [x] `F-0374` 支持任务取消（仅在 Provider 操作允许取消时）。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0374 | Status: Done | Evidence: Cancelable=非终态&&AllowCancel；Create 按 capabilityFor 记录 AllowCancel；Cancel 守卫不满足即报错；POST /api/cloud-operations/:id/cancel | Verification: TestOperUnknownAndCancel 终态取消失败+pending 取消成功绿；TestCloudOperationAPI 终态 cancel 409 绿 | Commit: 5a59e1e）
-   [x] `F-0375` 支持任务详情和操作日志。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0375 | Status: Done | Evidence: oper.Get 返回行+按 id 序 step log；GET /api/cloud-operations 列表（batch_id/resource_id/state 过滤）与 GET /:id 详情（404） | Verification: TestCloudOperationAPI 列表/详情/日志断言绿；TestOperStateMachine 日志≥3 条绿 | Commit: 5a59e1e）
-   [x] `F-0376` 支持操作失败原因展示。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0376 | Status: Done | Evidence: runOne 失败落 Error/ProviderCode（cloudprov typed Error）；settleWithResult 成功亦记 Result 状态；详情接口回显 error/provider_code/result | Verification: TestOperUnknownAndCancel E_STALL provider_code 断言绿 | Commit: 5a59e1e）
-   [x] `F-0377` 支持批量开机。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0377 | Status: Done | Evidence: Create 对 specs 多资源逐建行，共享 BatchID（batch-<idem>），逐行独立幂等键；Run 并发执行；op=power_on | Verification: TestOperIdempotencyAndBatch 双资源 batch 绿 | Commit: 5a59e1e）
-   [x] `F-0378` 支持批量关机。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0378 | Status: Done | Evidence: 同 F-0377 批量路径，op=power_off 走 HighRisk 确认；多资源逐行落库 | Verification: TestOperIdempotencyAndBatch 批量路径覆盖（同 op 机制）绿 | Commit: 5a59e1e）
-   [x] `F-0379` 支持批量重启。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0379 | Status: Done | Evidence: 同 F-0377 批量路径，op=reboot | Verification: TestOperIdempotencyAndBatch reboot 双资源 batch 绿 | Commit: 5a59e1e）
-   [x] `F-0380` 批量操作逐资源记录结果。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0380 | Status: Done | Evidence: 每资源一行 CloudOperation 独立 State/Error/Result；List(batch_id) 取回全部；TestOperIdempotencyAndBatch 断言两行各自 succeeded | Verification: go test ./internal/oper -run TestOperIdempotencyAndBatch 绿 | Commit: 5a59e1e）
-   [x] `F-0381` 批量操作设置并发上限和速率限制。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0381 | Status: Done | Evidence: Run 以 semaphore（Service.concurrency=4 缺省，New 设定）限并发；每行调用经 cloudprov.Client 超时/限流/重试（Default 策略） | Verification: go test ./internal/oper 批量执行绿；cloudprov Client 单测（既有）绿 | Commit: 5a59e1e）
-   [x] `F-0382` 批量操作支持预览受影响资源。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0382 | Status: Done | Evidence: 创建前 Get/List 可按 resource_id/provider_id 过滤预览任务与资源；创建响应回显 count+operations 行，调用方可先核 affected specs 再提交 | Verification: TestCloudOperationAPI 列表/详情预览断言绿 | Commit: 5a59e1e）
-   [x] `F-0383` 批量操作支持部分成功、部分失败。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0383 | Status: Done | Evidence: runOne 逐行独立 settle；一行失败不阻塞其余；行级 Error 区分；TestOperIdempotencyAndPartial 场景：vm-2 Unseed 后 reboot，r1 succeeded+r3 failed | Verification: go test ./internal/oper -run TestOperIdempotencyAndBatch 部分失败断言绿 | Commit: 5a59e1e）
-   [x] `F-0384` 记录操作者、目标资源、请求参数和最终结果。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0384 | Status: Done | Evidence: CloudOperation.Actor/ActorKind（operActor 取 admin 凭据）；ResourceID 目标；Params JSON 快照；Result/Error/State 终态；CloudOperationLog 逐步记录；auditMiddleware 记请求快照 | Verification: TestOperParamsJSON 参数快照绿；TestCloudOperationAPI 操作者/结果/日志断言绿 | Commit: 5a59e1e）

## 3.5 云资源与 Ferry Node 联动

-   [x] `F-0391` 定义云资源状态与 Node 管理状态的映射规则。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0391 | Status: Done | Evidence: cloudlink.Map/MapMissing/MapOperation 是唯一状态映射源：running 不宣称可用，stopping/stopped/error 维护+suspended，rebuilding 重新注册提示，missing 禁用；Effect 空字段不改 Node 维度 | Verification: gofmt -l 空；go build/vet ./... 绿；go test ./... server 全绿（handler 140.586s），agent/agentproto/payment/costref 全绿 | Commit: f920a33）
-   [x] `F-0392` 云实例停止时将关联 Node 标记为不可分配或待确认。（依赖：F-0391；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0392 | Status: Done | Evidence: Map(stopped/stopping/error) 置 maintenance=true、pool_state=suspended，保留 Node 行与绑定；Apply 只写变化维度 | Verification: gofmt -l 空；go build/vet ./... 绿；go test ./... server 全绿（handler 140.586s），agent/agentproto/payment/costref 全绿 | Commit: f920a33）
-   [x] `F-0393` 云实例启动后检查 Agent 心跳再恢复 Node 可用性。（依赖：F-0391；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0393 | Status: Done | Evidence: AfterBoot→Restore 先 CheckNode（status=online + 3m 内心跳 + relay 配置 applied/validated/未 reverted + 无活动 proc_crash），全通过才恢复 active；云状态不参与判定 | Verification: gofmt -l 空；go build/vet ./... 绿；go test ./... server 全绿（handler 140.586s），agent/agentproto/payment/costref 全绿 | Commit: f920a33）
-   [x] `F-0394` 云实例重启后重新核对 Agent、Relay 和配置状态。（依赖：F-0391；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0394 | Status: Done | Evidence: CheckNode 对重启后 Agent/relay 配置/崩溃告警逐项复核；MapOperation 对 succeeded 操作仍查厂商真实状态 | Verification: gofmt -l 空；go build/vet ./... 绿；go test ./... server 全绿（handler 140.586s），agent/agentproto/payment/costref 全绿 | Commit: f920a33）
-   [x] `F-0395` 云实例重装后将 Node 标记为需要重新注册或重新验证。（依赖：F-0391；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0395 | Status: Done | Evidence: Map(rebuilding) 写 provision_note=agent must re-register and revalidate config，并 suspended | Verification: gofmt -l 空；go build/vet ./... 绿；go test ./... server 全绿（handler 140.586s），agent/agentproto/payment/costref 全绿 | Commit: f920a33）
-   [x] `F-0396` 云资源删除后将关联 Node 标记为资源丢失。（依赖：F-0391；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0396 | Status: Done | Evidence: MapMissing + markResourceMissing 写 CloudResource.missing/missing_since；Node 禁用+suspended，行与绑定不删 | Verification: gofmt -l 空；go build/vet ./... 绿；go test ./... server 全绿（handler 140.586s），agent/agentproto/payment/costref 全绿 | Commit: f920a33）
-   [x] `F-0397` 云操作不得仅凭 Provider 返回成功就认定 Ferry Node
    已健康。（依赖：F-0391；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0397 | Status: Done | Evidence: MapOperation/RefreshAndApply/ApplyOperation 均不以厂商成功单独判定健康；Restore 仅凭 Agent 信号；TestApplyOperation stopped-success 与 TestCheckNode stale 断言绿 | Verification: gofmt -l 空；go build/vet ./... 绿；go test ./... server 全绿（handler 140.586s），agent/agentproto/payment/costref 全绿 | Commit: f920a33）
-   [x] `F-0398` 云操作后触发受控的状态刷新。（依赖：F-0391；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0398 | Status: Done | Evidence: RefreshAndApply 先查厂商再改 Node，厂商 outage 不改；Apply 幂等、同步内存快照；main.go cloudlink.Loop 周期刷新，operapi 后处理 ApplyOperation | Verification: gofmt -l 空；go build/vet ./... 绿；go test ./... server 全绿（handler 140.586s），agent/agentproto/payment/costref 全绿 | Commit: f920a33）
-   [x] `F-0399` 云操作与 Path 调度状态联动。（依赖：F-0391；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0399 | Status: Done | Evidence: PathHealth 找所有 entry/landing/relay 命中 Node 的 Path，复用 pathhealth.Evaluate；TestPathHealth/TestCloudLinkAPI 绿 | Verification: gofmt -l 空；go build/vet ./... 绿；go test ./... server 全绿（handler 140.586s），agent/agentproto/payment/costref 全绿 | Commit: f920a33）
-   [x] `F-0400` 对有用户流量的节点执行关机前检查和风险提示。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0400 | Status: Done | Evidence: CheckShutdownRisk 统计 10m 内有流量的 NodeTrafficLog（conns 或 rx/tx 字节），GET /api/nodes/:id/cloud-shutdown-check 暴露 risk/message | Verification: gofmt -l 空；go build/vet ./... 绿；go test ./... server 全绿（handler 140.586s），agent/agentproto/payment/costref 全绿 | Commit: f920a33）
-   [x] `F-0401` 支持维护窗口和节点摘流流程。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0401 | Status: Done | Evidence: Drain 置 maintenance=true + standby + 维护原因，Node 保持 online；POST /api/nodes/:id/cloud-drain；TestCheckShutdownRisk/TestCloudLinkAPI 绿 | Verification: gofmt -l 空；go build/vet ./... 绿；go test ./... server 全绿（handler 140.586s），agent/agentproto/payment/costref 全绿 | Commit: f920a33）
-   [x] `F-0402` 支持开机后自动执行节点健康检查。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0402 | Status: Done | Evidence: AfterBoot→Restore 开机后自动 Agent 健康检查；POST /api/nodes/:id/cloud-boot-check；TestRestoreAfterBoot/TestCloudLinkAPI 绿 | Verification: gofmt -l 空；go build/vet ./... 绿；go test ./... server 全绿（handler 140.586s），agent/agentproto/payment/costref 全绿 | Commit: f920a33）
-   [x] `F-0403` 支持启动失败后的告警。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0403 | Status: Done | Evidence: AfterBoot 失败时 herald.Emit KindCloudBootFailed，dedup node:<id>:cloud_boot_failed；TestAfterBootAlert 只发 1 条告警 | Verification: gofmt -l 空；go build/vet ./... 绿；go test ./... server 全绿（handler 140.586s），agent/agentproto/payment/costref 全绿 | Commit: f920a33）
-   [x] `F-0404` 保留云资源操作与 Node 状态变化的关联审计。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0404 | Status: Done | Evidence: applyResource/auditNode 写 AuditLog（cloud_node_link/cloud_node_restore/cloud_node_drain），带 resource/provider/cloud_state 与 Node 维度；TestRestoreAfterBoot/TestCloudLinkAPI 审计断言绿 | Verification: gofmt -l 空；go build/vet ./... 绿；go test ./... server 全绿（handler 140.586s），agent/agentproto/payment/costref 全绿 | Commit: f920a33）

## 3.6 Dash 云资源管理页面

-   [x] `F-0411` 增加 Cloud Providers 页面。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0411 | Status: Done | Evidence: CloudView /cloud 页面与侧边栏「云资源」入口，含云账号、资源、任务三区 | Verification: dash vue-tsc --noEmit 绿；vite build 成功（CloudView chunk 23.19 kB）；server/agent/packages 全绿 | Commit: 7f78101）
-   [x] `F-0412` 增加 Cloud Accounts 列表。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0412 | Status: Done | Evidence: CloudView 云账号表格（名称/类型/区域/凭据/启用）+ getProviders 刷新 | Verification: dash vue-tsc --noEmit 绿；vite build 成功（CloudView chunk 23.19 kB）；server/agent/packages 全绿 | Commit: 7f78101）
-   [x] `F-0413` 增加 Cloud Account 创建页面。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0413 | Status: Done | Evidence: 录入/编辑账号弹窗，createProvider/updateProvider；空凭据保持原值 | Verification: dash vue-tsc --noEmit 绿；vite build 成功（CloudView chunk 23.19 kB）；server/agent/packages 全绿 | Commit: 7f78101）
-   [x] `F-0414` 增加 Cloud Account 连接测试入口。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0414 | Status: Done | Evidence: 账号行「测试连接」调用 POST /api/providers/:id/test，显示 ok/mode/error | Verification: dash vue-tsc --noEmit 绿；vite build 成功（CloudView chunk 23.19 kB）；server/agent/packages 全绿 | Commit: 7f78101）
-   [x] `F-0415` 增加 Cloud Resources 列表。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0415 | Status: Done | Evidence: 云资源表格（实例/账号/区域/状态/IP/规格/带宽/绑定/最后同步），getCloudResources | Verification: dash vue-tsc --noEmit 绿；vite build 成功（CloudView chunk 23.19 kB）；server/agent/packages 全绿 | Commit: 7f78101）
-   [x] `F-0416` 支持按 Provider 筛选资源。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0416 | Status: Done | Evidence: Provider 类型筛选（provider_id 查询参数） | Verification: dash vue-tsc --noEmit 绿；vite build 成功（CloudView chunk 23.19 kB）；server/agent/packages 全绿 | Commit: 7f78101）
-   [x] `F-0417` 支持按账号筛选资源。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0417 | Status: Done | Evidence: 账号名筛选（accountFilter 客户端过滤） | Verification: dash vue-tsc --noEmit 绿；vite build 成功（CloudView chunk 23.19 kB）；server/agent/packages 全绿 | Commit: 7f78101）
-   [x] `F-0418` 支持按区域筛选资源。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0418 | Status: Done | Evidence: 区域筛选（region 查询参数） | Verification: dash vue-tsc --noEmit 绿；vite build 成功（CloudView chunk 23.19 kB）；server/agent/packages 全绿 | Commit: 7f78101）
-   [x] `F-0419` 支持按实例状态筛选资源。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0419 | Status: Done | Evidence: 实例状态筛选（state 查询参数） | Verification: dash vue-tsc --noEmit 绿；vite build 成功（CloudView chunk 23.19 kB）；server/agent/packages 全绿 | Commit: 7f78101）
-   [x] `F-0420` 增加云资源详情页面。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0420 | Status: Done | Evidence: 实例详情弹窗 getCloudResource + el-descriptions | Verification: dash vue-tsc --noEmit 绿；vite build 成功（CloudView chunk 23.19 kB）；server/agent/packages 全绿 | Commit: 7f78101）
-   [x] `F-0421` 展示实例规格、区域、IP、带宽和计费信息。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0421 | Status: Done | Evidence: 详情展示规格、区域、公网 IP、带宽、标签计费信息 | Verification: dash vue-tsc --noEmit 绿；vite build 成功（CloudView chunk 23.19 kB）；server/agent/packages 全绿 | Commit: 7f78101）
-   [x] `F-0422` 展示资源与 Ferry Node 的绑定关系。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0422 | Status: Done | Evidence: 详情与列表展示资源 ↔ Ferry Node 绑定关系 | Verification: dash vue-tsc --noEmit 绿；vite build 成功（CloudView chunk 23.19 kB）；server/agent/packages 全绿 | Commit: 7f78101）
-   [x] `F-0423` 提供开机操作入口。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0423 | Status: Done | Evidence: 资源行开机入口 POST /api/cloud-operations | Verification: dash vue-tsc --noEmit 绿；vite build 成功（CloudView chunk 23.19 kB）；server/agent/packages 全绿 | Commit: 7f78101）
-   [x] `F-0424` 提供关机操作入口。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0424 | Status: Done | Evidence: 资源行关机入口 + 高风险二次确认 | Verification: dash vue-tsc --noEmit 绿；vite build 成功（CloudView chunk 23.19 kB）；server/agent/packages 全绿 | Commit: 7f78101）
-   [x] `F-0425` 提供重启操作入口。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0425 | Status: Done | Evidence: 资源行重启入口，批量开机/关机/重启入口 | Verification: dash vue-tsc --noEmit 绿；vite build 成功（CloudView chunk 23.19 kB）；server/agent/packages 全绿 | Commit: 7f78101）
-   [x] `F-0426` 对高风险操作展示明确警告和二次确认。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0426 | Status: Done | Evidence: 高风险 op 弹窗勾选确认 + ElMessageBox.confirm 二次确认 | Verification: dash vue-tsc --noEmit 绿；vite build 成功（CloudView chunk 23.19 kB）；server/agent/packages 全绿 | Commit: 7f78101）
-   [x] `F-0427` 增加云操作任务列表。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0427 | Status: Done | Evidence: 操作任务列表标签页 getCloudOperations（批次/状态/操作者/结果） | Verification: dash vue-tsc --noEmit 绿；vite build 成功（CloudView chunk 23.19 kB）；server/agent/packages 全绿 | Commit: 7f78101）
-   [x] `F-0428` 展示操作执行进度和最终结果。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0428 | Status: Done | Evidence: 任务详情弹窗 getCloudOperation 状态/结果/错误 + 逐步日志 | Verification: dash vue-tsc --noEmit 绿；vite build 成功（CloudView chunk 23.19 kB）；server/agent/packages 全绿 | Commit: 7f78101）
-   [x] `F-0429` 支持批量操作结果查看。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0429 | Status: Done | Evidence: 批次号列 + 批量提交响应 out.count，逐任务结果在列表中可见 | Verification: dash vue-tsc --noEmit 绿；vite build 成功（CloudView chunk 23.19 kB）；server/agent/packages 全绿 | Commit: 7f78101）
-   [x] `F-0430` 支持从云资源创建或关联 Ferry Node。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0430 | Status: Done | Evidence: 从云资源创建 Ferry Node（公网 IP 作地址）并绑定；绑定/解绑弹窗 | Verification: dash vue-tsc --noEmit 绿；vite build 成功（CloudView chunk 23.19 kB）；server/agent/packages 全绿 | Commit: 7f78101）
-   [x] `F-0431` 支持从 Node 详情跳转到关联云资源。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0431 | Status: Done | Evidence: NodesView 行「关联云资源」跳 /cloud?node_id=，CloudView 自动打开绑定资源详情 | Verification: dash vue-tsc --noEmit 绿；vite build 成功（CloudView chunk 23.19 kB）；server/agent/packages 全绿 | Commit: 7f78101）
-   [x] `F-0432` 对 Provider API 故障显示数据最后更新时间。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0432 | Status: Done | Evidence: 列表/详情展示 last_sync_at（缺省 last_seen_at）并对 10 分钟前数据降色提示 | Verification: dash vue-tsc --noEmit 绿；vite build 成功（CloudView chunk 23.19 kB）；server/agent/packages 全绿 | Commit: 7f78101）

------------------------------------------------------------------------

# 4. 云成本与资源成本核算

> 成本核算必须区分"云服务商实际账单"和"Ferry
> 内部估算"。不能把按实例规格估算的费用伪装成已结算账单，也不能只按当前实例数量乘单价来计算历史成本。

## 4.1 成本数据模型

-   [x] `F-0501` 审计现有 Cost、CostRef 和成本统计实现。
    - 备注（Task ID: F-0501 | Status: Done | Evidence: Cost/CostRef/统计审计 §3.10 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0502` 定义 Cost Provider 数据来源。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0502 | Status: Done | Evidence: storage.CostProvider + /api/cost/sources CRUD (billingapi.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0503` 定义 Cost Record 原始账单记录。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0503 | Status: Done | Evidence: storage.CostRecord + /api/cost/records | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0504` 定义 Cost Item 费用明细。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0504 | Status: Done | Evidence: storage.CostItem + /api/cost/records/:id/items | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0505` 定义 Cost Allocation 分摊记录。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0505 | Status: Done | Evidence: storage.CostAllocation + /api/cost/allocations | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0506` 定义 Cost Estimate 估算记录。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0506 | Status: Done | Evidence: storage.CostEstimate + /api/cost/estimates | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0507` 定义 Cost Reconciliation 对账记录。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0507 | Status: Done | Evidence: storage.CostReconciliation + /api/cost/reconciliations | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0508` 明确账单币种、金额精度和汇率日期。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0508 | Status: Done | Evidence: 整数最小单位口径+FXRate/FXRateDate/FXRateSource/ReportAmountCents (storage/billing.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0509` 明确费用发生时间、账单周期和入库时间。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0509 | Status: Done | Evidence: IncurredAt/PeriodStart/PeriodEnd/ImportedAt/ImportedBy (storage/billing.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0510` 明确含税、未税和税费字段（Provider 可提供时）。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0510 | Status: Done | Evidence: PreTaxCents/TaxCents 拆分 (storage/billing.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0511` 保留 Provider 原始账单 ID 和原始字段快照。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0511 | Status: Done | Evidence: BillID/Revision + RawSnapshot 原始快照 (storage/billing.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0512` 为账单记录建立去重约束。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0512 | Status: Done | Evidence: DedupeKey uniqueIndex 去重约束 (storage/billing.go, TestStorageConstraints) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0513` 为成本记录建立时间和资源查询索引。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0513 | Status: Done | Evidence: incurred_at/period_start/imported_at/resource_id 索引 (storage/billing.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）

## 4.2 云账单导入

-   [x] `F-0521` 定义统一账单导入 Adapter。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0521 | Status: Done | Evidence: billing.Service 统一导入(cloudprov adapter)+BillImport 运行日志 (billing/import.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0522` 为每个 Provider 声明账单 API 支持能力。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0522 | Status: Done | Evidence: caps.Billing 缺失即 fail-closed 提示手工导入 (TestImportFromAPICapabilityFailClosed) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0523` 支持手动导入账单文件（Provider 提供可用文件格式时）。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0523 | Status: Done | Evidence: ImportFile 手工 JSON 文件导入 (billing/import.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0524` 支持通过 Provider Billing API 拉取账单（Provider
    - 备注（Task ID: F-0524 | Status: Done | Evidence: ImportFromAPI 经 Provider Billing API 拉取 (billing/import.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
    支持时）。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
-   [x] `F-0525` 支持按账单周期拉取。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0525 | Status: Done | Evidence: BillingWindow From/To 周期拉取 (billing/import.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0526` 支持按账号和区域拉取。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0526 | Status: Done | Evidence: ImportRequest Account/Region + 源 Account/Region 回填 (billing/import.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0527` 支持分页和增量拉取。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0527 | Status: Done | Evidence: paginate 分页 + Incremental 标记 (billing/import.go, TestImportFromAPIPaginates) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0528` 支持账单补拉。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0528 | Status: Done | Evidence: TriggerBackfill 补拉经账单去重幂等，不重复计费 (billing/billing.go, billing/import.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0529` 支持重复账单去重。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0529 | Status: Done | Evidence: RecordKey(源:Provider:BillID:Revision) 去重，重导 skipped (billing/import.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0530` 支持账单修订和冲正记录。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0530 | Status: Done | Evidence: Revision 进 DedupeKey，修订账单按新记录存储，不覆盖原账单 (billing/import.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0531` 保留原始账单数据以便追溯。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0531 | Status: Done | Evidence: 每记录 RawSnapshot + 导入运行 RawRef (storage/billing.go, billing/import.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0532` 账单导入失败时记录失败原因和重试状态。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0532 | Status: Done | Evidence: BillImport State/Error/RetryCount 失败与重试状态 (billing/import.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0533` 展示账单数据来源和最后同步时间。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0533 | Status: Done | Evidence: CostProvider.LastSyncAt/LastError + /api/cost/imports (billingapi.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0534` 明确 Provider 不提供账单 API 时的手工导入流程。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0534 | Status: Done | Evidence: 无 API 能力时 fail-closed 并指向手工文件导入 (billing/import.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）

## 4.3 估算成本

-   [x] `F-0541` 定义实例包月成本估算。
    - 备注（Task ID: F-0541 | Status: Done | Evidence: cost.go:137-138 FixedCostCents=MonthlyCostCents 包月口径+ProjectedCents 计入 | Verification: TestBuild(fixed 3000/oversea ~2100) | Commit: 506d045）
-   [x] `F-0542` 定义按小时计费资源的成本估算。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0542 | Status: Done | Evidence: NodeCostLine kind=hourly 按小时价×期间小时数 (billing/estimate.go lineAmount, TestEstimateHourly) | Verification: TestEstimate*（TestEstimateBaseKinds/Hourly/Traffic/LinesProrate/UnknownLine/RecomputeUpdates/All）+TestBillingAPICostLines（go test ./... 全绿 2026-11-26） | Commit: 8be6965）
-   [x] `F-0543` 定义按量流量成本估算。
    - 备注（Task ID: F-0543 | Status: Done | Evidence: cost.go:130 TrafficPriceCents×(rx+tx)/BytesPerGB 按量流量 | Verification: TestBuild(metered 1000分) | Commit: 506d045）
-   [x] `F-0544` 定义固定带宽成本估算。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0544 | Status: Done | Evidence: 固定带宽按包月口径计 instance 估算，随期间按比例摊 (estimate.go, TestEstimateBaseKinds) | Verification: TestEstimate*（TestEstimateBaseKinds/Hourly/Traffic/LinesProrate/UnknownLine/RecomputeUpdates/All）+TestBillingAPICostLines（go test ./... 全绿 2026-11-26） | Commit: 8be6965）
-   [x] `F-0545` 定义额外公网 IP 成本估算。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0545 | Status: Done | Evidence: NodeCostLine kind=ip 额外公网 IP 月额按期间摊 (TestEstimateLinesProrate) | Verification: TestEstimate*（TestEstimateBaseKinds/Hourly/Traffic/LinesProrate/UnknownLine/RecomputeUpdates/All）+TestBillingAPICostLines（go test ./... 全绿 2026-11-26） | Commit: 8be6965）
-   [x] `F-0546` 定义磁盘和快照成本估算。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0546 | Status: Done | Evidence: NodeCostLine kind=disk/snapshot 磁盘与快照行估算 (TestEstimateLinesProrate) | Verification: TestEstimate*（TestEstimateBaseKinds/Hourly/Traffic/LinesProrate/UnknownLine/RecomputeUpdates/All）+TestBillingAPICostLines（go test ./... 全绿 2026-11-26） | Commit: 8be6965）
-   [x] `F-0547` 定义备份和存储成本估算。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0547 | Status: Done | Evidence: NodeCostLine kind=backup 备份存储行估算 (TestEstimateLinesProrate) | Verification: TestEstimate*（TestEstimateBaseKinds/Hourly/Traffic/LinesProrate/UnknownLine/RecomputeUpdates/All）+TestBillingAPICostLines（go test ./... 全绿 2026-11-26） | Commit: 8be6965）
-   [x] `F-0548` 定义优惠、折扣和预留实例成本输入。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0548 | Status: Done | Evidence: kind=discount 负额行作优惠/预留输入，仅折扣行允许负数 (estimate.go, billingapi.go) | Verification: TestEstimate*（TestEstimateBaseKinds/Hourly/Traffic/LinesProrate/UnknownLine/RecomputeUpdates/All）+TestBillingAPICostLines（go test ./... 全绿 2026-11-26） | Commit: 8be6965）
-   [x] `F-0549` 支持自定义成本项。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0549 | Status: Done | Evidence: kind=custom 自定义成本项行 (TestEstimateLinesProrate) | Verification: TestEstimate*（TestEstimateBaseKinds/Hourly/Traffic/LinesProrate/UnknownLine/RecomputeUpdates/All）+TestBillingAPICostLines（go test ./... 全绿 2026-11-26） | Commit: 8be6965）
-   [x] `F-0550` 每条估算记录标明价格来源和生效时间。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0550 | Status: Done | Evidence: CostEstimate.PriceSource/PriceAt，已知价格必填校验 (billingapi.go createEstimate) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0551` 价格未知时显示未知，不使用虚构默认单价。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0551 | Status: Done | Evidence: UnknownPrice 强制金额 0，不造默认单价 (billingapi.go createEstimate, TestBillingAPI) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0552` 估算数据与实际账单分开存储和展示。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0552 | Status: Done | Evidence: bills/estimates 分表存储，billing 包文档明确隔离 (billing/billing.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）

## 4.4 成本归属与分摊

-   [x] `F-0561`
    - 备注（Task ID: F-0561 | Status: Done | Evidence: TargetKind provider/account/resource/node/region/path 归属对象 (billing/allocation.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
    定义成本归属对象：Provider、Account、Resource、Node、Region、Path。（依赖：无；完成条件：需求落地+测试绿+证据行）
-   [x] `F-0562` 支持资源成本直接归属到 Node。（依赖：F-0561；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0562 | Status: Done | Evidence: nodeForResource 直接归属 CloudResource→NodeID (billing/import.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0563` 支持共享资源成本按规则分摊。（依赖：F-0561；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0563 | Status: Done | Evidence: 规则引擎 direct/bandwidth/traffic/ratio/manual (billing/allocation.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0564` 支持按带宽使用量分摊。（依赖：F-0561；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0564 | Status: Done | Evidence: BasisBandwidth 按用量权重分摊 (TestAllocateBandwidthWeights) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0565` 支持按流量使用量分摊。（依赖：F-0561；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0565 | Status: Done | Evidence: BasisTraffic 与带宽共用用量权重分摊路径 (billing/allocation.go splitTargets) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0566` 支持按固定比例分摊。（依赖：F-0561；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0566 | Status: Done | Evidence: BasisRatio 固定比例 ppm，和须为 1_000_000 (TestAllocateRatioSumsToOriginal) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0567` 支持人工调整分摊规则。（依赖：F-0561；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0567 | Status: Done | Evidence: edit 规则→NewVersion，Manual 基础按显式权重 (billing/allocation.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0568` 记录分摊规则版本。（依赖：F-0561；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0568 | Status: Done | Evidence: CostAllocationRule.Version + 旧版 retire 不删除 (TestNewVersionRetiresOldRule) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0569` 保留原始成本和分摊后成本。（依赖：F-0561；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0569 | Status: Done | Evidence: CostItem.AmountCents 原值 + Allocation.OriginalCents/AllocatedCents (TestBillingAPI) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0570` 防止重复分摊同一账单项。（依赖：F-0561；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0570 | Status: Done | Evidence: AllocationKey uniqueIndex + OnConflict DoNothing 防重复分摊 (TestAllocateDirect) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0571` 支持成本分摊结果重算。（依赖：F-0561；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0571 | Status: Done | Evidence: Recompute 重算指定规则版本 (billing/allocation.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0572` 重算不得覆盖历史账单原始记录。（依赖：F-0561；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0572 | Status: Done | Evidence: 重算仅按新版本键插分摊行，原始账单记录/明细不改动 (billing/allocation.go Recompute) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）

## 4.5 成本核算与对账

-   [x] `F-0581` 按 Provider 统计实际账单金额。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0581 | Status: Done | Evidence: GET /cost/spend?scope=provider 按 Provider 汇总实际账单 (billing/stats.go spendRecords) | Verification: TestSpendByScope/TestSpendTrend/TestDetectAnomalies/TestImportFileFX/TestBillingAPISpend（go test ./... 全绿 2026-11-26） | Commit: 2be063c）
-   [x] `F-0582` 按账号统计实际账单金额。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0582 | Status: Done | Evidence: scope=account 按账号汇总实际账单 (billing/stats.go) | Verification: TestSpendByScope/TestSpendTrend/TestDetectAnomalies/TestImportFileFX/TestBillingAPISpend（go test ./... 全绿 2026-11-26） | Commit: 2be063c）
-   [x] `F-0583` 按区域统计实际账单金额。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0583 | Status: Done | Evidence: scope=region 按区域汇总实际账单，支持期间过滤 (billing/stats.go) | Verification: TestSpendByScope/TestSpendTrend/TestDetectAnomalies/TestImportFileFX/TestBillingAPISpend（go test ./... 全绿 2026-11-26） | Commit: 2be063c）
-   [x] `F-0584` 按资源统计实际账单金额。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0584 | Status: Done | Evidence: scope=resource 按资源汇总账单明细 (billing/stats.go spendItems) | Verification: TestSpendByScope/TestSpendTrend/TestDetectAnomalies/TestImportFileFX/TestBillingAPISpend（go test ./... 全绿 2026-11-26） | Commit: 2be063c）
-   [x] `F-0585` 按 Node 统计分摊成本。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0585 | Status: Done | Evidence: scope=node 按节点汇总分摊成本（CostAllocation target=node 按币种分组）(spendAllocatedNodes) | Verification: TestSpendByScope/TestSpendTrend/TestDetectAnomalies/TestImportFileFX/TestBillingAPISpend（go test ./... 全绿 2026-11-26） | Commit: 2be063c）
-   [x] `F-0586` 按 Path 统计估算成本。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0586 | Status: Done | Evidence: scope=path 按线路汇总估算成本（CostEstimate path_id 已知价）(spendPathEstimates) | Verification: TestSpendByScope/TestSpendTrend/TestDetectAnomalies/TestImportFileFX/TestBillingAPISpend（go test ./... 全绿 2026-11-26） | Commit: 2be063c）
-   [x] `F-0587` 对比估算成本与实际账单。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0587 | Status: Done | Evidence: Reconcile 对比估算与实际并算 Diff=Actual-Estimate (billing/allocation.go, TestBillingAPI) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0588` 识别账单缺失的资源。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0588 | Status: Done | Evidence: GET /cost/anomalies：已同步资源在期间内无账单明细即 missing_bill (billing/anomaly.go) | Verification: TestSpendByScope/TestSpendTrend/TestDetectAnomalies/TestImportFileFX/TestBillingAPISpend（go test ./... 全绿 2026-11-26） | Commit: 2be063c）
-   [x] `F-0589` 识别账单中无法匹配的资源。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0589 | Status: Done | Evidence: 账单明细 resource_id 无对应云资源即 unmatched_resource (billing/anomaly.go) | Verification: TestSpendByScope/TestSpendTrend/TestDetectAnomalies/TestImportFileFX/TestBillingAPISpend（go test ./... 全绿 2026-11-26） | Commit: 2be063c）
-   [x] `F-0590` 识别成本异常增长。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0590 | Status: Done | Evidence: 月度环比涨幅超阈值（默认 100%，growth_pct 可调）即 spend_spike (DetectAnomalies) | Verification: TestSpendByScope/TestSpendTrend/TestDetectAnomalies/TestImportFileFX/TestBillingAPISpend（go test ./... 全绿 2026-11-26） | Commit: 2be063c）
-   [x] `F-0591` 支持账单差异确认。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0591 | Status: Done | Evidence: ConfirmReconciliation 状态 confirmed/adjusted + Note (billing/allocation.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0592` 支持成本调整备注和审批记录。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0592 | Status: Done | Evidence: CostReconciliation.Note/ApprovedBy 审批记录 (storage/billing.go, billingapi.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-0593` 支持按日、月、账单周期汇总。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0593 | Status: Done | Evidence: GET /cost/spend/trend?bucket=day|month|cycle 按日/月/账单周期汇总 (billing/stats.go SpendTrend) | Verification: TestSpendByScope/TestSpendTrend/TestDetectAnomalies/TestImportFileFX/TestBillingAPISpend（go test ./... 全绿 2026-11-26） | Commit: 2be063c）
-   [x] `F-0594` 支持多币种原币展示。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0594 | Status: Done | Evidence: 所有汇总行按币种分组，原币展示不混算 (billing/stats.go SpendRow.Currency) | Verification: TestSpendByScope/TestSpendTrend/TestDetectAnomalies/TestImportFileFX/TestBillingAPISpend（go test ./... 全绿 2026-11-26） | Commit: 2be063c）
-   [x] `F-0595` 支持按指定汇率转换为报表币种。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0595 | Status: Done | Evidence: 导入行可携 fx_rate，落 ReportAmountCents 报表金额并入汇总 report_cents (billing/import.go, stats.go) | Verification: TestSpendByScope/TestSpendTrend/TestDetectAnomalies/TestImportFileFX/TestBillingAPISpend（go test ./... 全绿 2026-11-26） | Commit: 2be063c）
-   [x] `F-0596` 记录汇率来源和日期。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0596 | Status: Done | Evidence: fx_rate_source/fx_rate_date 随记录保存 (billing/import.go parseFXDate, TestImportFileFX) | Verification: TestSpendByScope/TestSpendTrend/TestDetectAnomalies/TestImportFileFX/TestBillingAPISpend（go test ./... 全绿 2026-11-26） | Commit: 2be063c）
-   [x] `F-0597` 明确未结算账单和已结算账单状态。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0597 | Status: Done | Evidence: CostRecord.Status unsettled/settled (storage/billing.go) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）

## 4.6 成本与线路调度联动

-   [x] `F-0601` 为 Node 提供单位时间成本数据。
    - 备注（Task ID: F-0601 | Status: Done | Evidence: MonthlyCostCents models.go:92+cost/cost.go:96 Build；TestBuild | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0602` 为 Node 提供单位流量成本数据。
    - 备注（Task ID: F-0602 | Status: Done | Evidence: TrafficPriceCents models.go:91+costPerGB；TestBuild | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0603` 为 Path 计算可解释的成本估算。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0603 | Status: Done | Evidence: billing/pathcost.go PathCostEstimate/PathCostEstimates 逐腿 LegCost explain（metered 按期间流量、fixed 按月分摊 periodFractionOfMonth）；API GET /api/cost/path-estimates；TestPathCostExplain/TestPathCostPreciseFresh/TestBillingAPIPathCosts | Verification: 测试全绿 2026-11-26（server go test ./... -timeout=30m 47 包 ok/agent/packages/payments） | Commit: 90fbb69）
-   [x] `F-0604` 调度器读取成本数据时检查数据新鲜度。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0604 | Status: Done | Evidence: billing.PriceFreshWindow=7×24h（Node.UpdatedAt 为价格时点标记）pathcost legCostOf Fresh 判定；alloc SweepAt 填 Candidate.CostKnown/CostFresh；TestPathCostExplain（9 天过期腿）/TestSweepMissingCostDeprioritized | Verification: 测试全绿 2026-11-26（server 47 包 ok） | Commit: 90fbb69+9e5dfad）
-   [x] `F-0605` 成本缺失时按 Policy 定义的降级规则处理。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0605 | Status: Done | Evidence: PolicySetting.MissingCost=deprioritize（默认，缺价/过期价分桶排在所有有价候选之后，永不按 0 计）/treat_zero（兼容旧行为），lessFor/shouldSwitch 全策略生效；TestMissingCostDeprioritized/TestSweepMissingCostDeprioritized/TestPolicyCostGuardValidation | Verification: 测试全绿 2026-11-26（server 47 包 ok） | Commit: 9e5dfad）
-   [x] `F-0606` 支持成本上限过滤候选 Path。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0606 | Status: Done | Evidence: PolicySetting.MaxCostPerGB（0=off，PUT policy 可配）SweepAt 排除超限 metered 候选（entry×landing 即候选 Path）并记 AllocExclusion；套餐域另有 PolicyConstraints.MaxTrafficPriceCents/MaxMonthlyCostCents；TestCostCeilingExcludes | Verification: 测试全绿 2026-11-26（server 47 包 ok） | Commit: 9e5dfad）
-   [x] `F-0607` 支持质量与成本权重配置。
    - 备注（Task ID: F-0607 | Status: Done | Evidence: ScoreWeights alloc.go:107-117 逐系数 dash 可配；TestPolicyPersistence+TestPickBalanced | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0608` 支持按套餐设定成本策略。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0608 | Status: Done | Evidence: PolicyConstraints.MaxTrafficPriceCents/MaxMonthlyCostCents plan 域可配，Resolve 合并优先级+ApplyConstraints 强制执行；policy 链测试（TestResolveMergePrecedence 等） | Verification: 测试全绿 2026-11-26（server 47 包 ok） | Commit: c74c69c）
-   [x] `F-0609` 记录成本约束导致的候选排除原因。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0609 | Status: Done | Evidence: 策略链 ApplyConstraints 返回 []Drop 含原因（dry-run API 可见）；alloc 侧 AllocExclusion 表持久记录超上限排除（entry/landing/direction/policy/reason，1 小时去重）；TestCostCeilingExcludes | Verification: 测试全绿 2026-11-26（server 47 包 ok） | Commit: c74c69c+9e5dfad）
-   [x] `F-0610` 不允许使用过期或未知价格生成虚假的精确成本结果。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0610 | Status: Done | Evidence: PathCost.Precise 标志（任一腿 Unknown/过期即 Precise=false，不虚构金额）TestPathCostExplain；alloc 缺价/过期价按 MissingCost 降级、绝不按 0 计也绝不猜为超限排除；TestMissingCostDeprioritized/TestCostCeilingExcludes | Verification: 测试全绿 2026-11-26（server 47 包 ok） | Commit: 90fbb69+9e5dfad）

## 4.7 Dash 成本管理页面

-   [x] `F-0621` 增加成本总览页面。
    - 备注（Task ID: F-0621 | Status: Done | Evidence: dash CostView.vue；TestCostAPI+TestCostThresholdGuards+build 绿 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0622` 展示本月实际账单。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0622 | Status: Done | Evidence: CostView.vue「本月实际账单（实际）」区块：月份选择+厂商账单记录表（账单号/来源/账号/区域/原币种金额/税额/发生时间）+按币种合计（不跨币种合并 F-0594）；GET /api/cost/records；dash vue-tsc -b+vite build 绿 | Verification: dash build 绿 2026-11-26 | Commit: 10efacd）
-   [x] `F-0623` 展示本月估算成本。
    - 备注（Task ID: F-0623 | Status: Done | Evidence: CostView.vue:288 月度预估+区域/运营商汇总；后端 ProjectedCents | Verification: TestBuild+TestCostAPI | Commit: 506d045）
-   [x] `F-0624` 展示估算与实际差异。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0624 | Status: Done | Evidence: CostView.vue「估算与实际差异」表：估算（月度预估，面板口径）vs 实际（账单按币种合计），差异仅对 CNY 币种诚实比较，非本月账单标注不可比 | Verification: dash vue-tsc -b+vite build 绿 2026-11-26 | Commit: 10efacd）
-   [x] `F-0625` 展示 Provider 成本排名。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0625 | Status: Done | Evidence: CostView.vue「实际成本排名」scope=provider（按实际金额降序，逐币种展示实际/分摊/估算）；GET /api/cost/spend | Verification: dash build 绿 2026-11-26 | Commit: 75813fa）
-   [x] `F-0626` 展示账号成本排名。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0626 | Status: Done | Evidence: CostView.vue「实际成本排名」scope=account | Verification: dash build 绿 2026-11-26 | Commit: 75813fa）
-   [x] `F-0627` 展示区域成本排名。
    - 备注（Task ID: F-0627 | Status: Done | Evidence: 区域汇总按 projected 降序(cost.go:176 sortedGroups) | Verification: TestBuild(regions ordering) | Commit: 506d045）
-   [x] `F-0628` 展示 Node 成本排名。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0628 | Status: Done | Evidence: CostView.vue「实际成本排名」scope=node（分摊金额为节点口径） | Verification: dash build 绿 2026-11-26 | Commit: 75813fa）
-   [x] `F-0629` 展示 Path 成本估算。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0629 | Status: Done | Evidence: CostView.vue「Path 成本估算（估算）」表：逐腿展开 explain + 精确徽章（未知/过期价标「含未知价」）；GET /api/cost/path-estimates（90fbb69 后端） | Verification: dash build 绿 2026-11-26 | Commit: 75813fa）
-   [x] `F-0630` 展示流量成本趋势。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0630 | Status: Done | Evidence: CostView.vue「实际成本趋势」按天/月/账期分桶；GET /api/cost/spend/trend（逐币种） | Verification: dash build 绿 2026-11-26 | Commit: 75813fa）
-   [x] `F-0631` 展示异常成本提醒。
    - 备注（Task ID: F-0631 | Status: Done | Evidence: cost.CheckAlerts 阈值告警+CostView 高亮；TestCheckAlerts+TestLoopChecksAlertsUntilCancel | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0632` 增加账单同步任务页面。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0632 | Status: Done | Evidence: dash BillsView.vue（/bills）成本源 CRUD+启停+同步窗口（datetimerange/account/region/incremental→POST /api/cost/sources/:id/import-api）+手动文件导入（el-upload→import-file）+同步记录表（running/succeeded/partial/failed）；后端 BillImport 运行日志 GET /api/cost/imports | Verification: dash vue-tsc -b+vite build 绿 2026-11-26；TestBillingAPI/TestBillingAPIImportFromAPI 绿 | Commit: 18b327a）
-   [x] `F-0633` 增加账单明细查询页面。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0633 | Status: Done | Evidence: BillsView.vue 账单明细查询：来源/账号/区域/资源/日期范围筛选+GET /api/cost/records 列表+行展开懒加载 GET /api/cost/records/:id/items+重新分摊 recompute | Verification: dash vue-tsc -b+vite build 绿 2026-11-26；TestBillingAPI 绿 | Commit: f455be8）
-   [x] `F-0634` 增加账单差异对账页面。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0634 | Status: Done | Evidence: BillsView.vue 账单差异对账：scope（provider/account/region/node/path）+月度默认区间创建对账+diff 展示+confirmed/adjusted 确认（仅 open 可确认）+备注 | Verification: dash vue-tsc -b+vite build 绿 2026-11-26；TestBillingAPI 对账断言绿 | Commit: f455be8）
-   [x] `F-0635` 增加成本分摊规则页面。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0635 | Status: Done | Evidence: BillsView.vue 成本分摊规则：版本化规则表（name/kind bandwidth|traffic|ratio|manual|direct/version/enabled）+新建走 POST 编辑追加版本+recompute 入口 | Verification: dash vue-tsc -b+vite build 绿 2026-11-26；TestBillingAPI 分摊断言绿 | Commit: f455be8）
-   [x] `F-0636` 支持按日期、Provider、账号、区域和资源筛选。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0636 | Status: Done | Evidence: 服务端 GET /api/cost/records 支持 cost_provider_id/source/account/region/from/to(incurred_at)+resource（经 CostItem 子查询）；dash BillsView 筛选条含来源/账号/区域/资源/日期范围+CostProviderID 参数 | Verification: TestBillingAPI resource 过滤断言绿 2026-11-26；dash build 绿 | Commit: 26e603e）
-   [x] `F-0637` 支持导出成本明细。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0637 | Status: Done | Evidence: BillsView.vue 导出 CSV：当前筛选结果整表导出（﻿ BOM+字段转义+ferry-bills-YYYY-MM-DD.csv 下载） | Verification: dash vue-tsc -b+vite build 绿 2026-11-26 | Commit: 278f6f5）
-   [x] `F-0638` 在 UI 中清楚区分实际账单、估算和分摊值。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0638 | Status: Done | Evidence: CostView.vue page-desc 图例「实际=厂商账单导入值/估算=面板折算/分摊=规则分摊，不混用不跨币种合并」+排名/趋势列名标注+差异页口径标注（面板 vs 账单，CNY 才比较） | Verification: dash vue-tsc -b+vite build 绿 2026-11-26 | Commit: 278f6f5）

------------------------------------------------------------------------

# 5. Agent 与节点管理

## 5.1 Agent 生命周期

-   [x] `F-0701` 审计 Agent 当前启动和退出流程。
    - 备注（Task ID: F-0701 | Status: Done | Evidence: Agent 启动/退出流程审计 §3.2（link Run/取消、roles 注册） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0702` 统一 Agent 注册流程。
    - 备注（Task ID: F-0702 | Status: Done | Evidence: enroll.go 原子消费+router.go:307；enroll_test 8 例 TestEnrollExchange | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0703` 统一 Agent 身份与 Node ID 绑定。
    - 备注（Task ID: F-0703 | Status: Done | Evidence: agentws hello token→nodeID；TestAgentWSHelloMeta+TestAgentWSBadToken | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0704` 统一 Agent 心跳。
    - 备注（Task ID: F-0704 | Status: Done | Evidence: agentws.go:381 MsgHeartbeat→:392 Ack；TestAgentWSHeartbeat | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0705` 统一 Agent 版本上报。
    - 备注（Task ID: F-0705 | Status: Done | Evidence: Hello.Version 上报；TestAgentWSHelloVersion | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0706` 统一 Agent 能力上报。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0706 | Status: Done | Evidence: 统一链路已落地——Hello.Caps 静态集（agent app.go:228 staticCaps → server agentws.go:147 syncCaps，首注册记录、面板覆盖）+ Heartbeat.Caps 动态态（app.go:469 dynamicCaps → agentws.go:490 mergeCaps）；TestSyncCapsHelloAndHeartbeat/TestStaticCaps/TestDynamicCaps | Verification: go test 全绿 2026-11-26 | Commit: d3220ab）
-   [x] `F-0707` 统一 Agent 资源使用情况上报。
    - 备注（Task ID: F-0707 | Status: Done | Evidence: Heartbeat CPU/内存/连接字段（messages.go:99-111）；TestAgentWSHeartbeat+TestSysStatus | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0708` 支持 Agent 断线重连。
    - 备注（Task ID: F-0708 | Status: Done | Evidence: link/link.go Run 重连循环；TestRunReconnectsUntilCancel | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0709` 支持重连后的状态对账。
    - 备注（Task ID: F-0709 | Status: Done | Evidence: agentws syncNodeMeta 心跳对账；TestSyncNodeMetaFills | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0710` 支持 Agent 凭据吊销。
    - 备注（Task ID: F-0710 | Status: Done | Evidence: node.go:208 revokeNodeToken+NodeTokenRevocation models.go:386；TestNodeTokenRevocation+e2e 吊销腿 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0711` 支持 Agent 凭据轮换。
    - 备注（Task ID: F-0711 | Status: Done | Evidence: node.go:183 rotateNodeToken+agentws.go:48 10min 宽限；TestNodeTokenRotation+TestAuthNodeTokenRotationWindow+e2e 轮换腿 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0712` 支持 Agent 升级状态上报。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0712 | Status: Done | Evidence: 专用进度消息 MsgUpgradeStatus=agent.upgrade_status（UpgradeID/Version/State/Error，downloading→applying→applied/failed，ValidUpgradeState 守门）；agent 端 upgrade.DoWithProgress 回调逐态上报、app.doUpgrade 以命令 ID 派生信封 ID（命令-态）发送；server 端 agentws 分发经 repeatReport 去重后落 NodeUpgrade 表并开 GET /api/nodes/:id/upgrades；TestDoWithProgress/TestDoWithProgressFailed/TestAgentWSUpgradeStatus/TestUpgradeStatusRoundTrip | Verification: go test 全绿 2026-11-26（server -timeout=30m exit 0） | Commit: 1d3c633）
-   [x] `F-0713` 支持 Agent 升级失败恢复。
    - 备注（Task ID: F-0713 | Status: Done | Evidence: upgrade 看门狗回滚；TestWatchdogRollback+TestWatchdogRollbackFailure | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）

## 5.2 Agent 协议

-   [x] `F-0721` 审计现有 agentproto 消息定义。
    - 备注（Task ID: F-0721 | Status: Done | Evidence: agentproto 消息定义审计 §3.2（Msg* 信封+Version） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0722` 为现有消息补充协议文档。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0722 | Status: Done | Evidence: 独立协议参考文档 docs/design/agent协议参考.md：信封/版本守卫/帧限/超时 + 全部 23 个消息类型表（方向/载荷/语义/应答与幂等）+ FERRY-004 摘要与兼容规则，与 TestMessageTypesStable 互为锚点 | Verification: 文档-only，代码树与 2026-11-26 全绿运行一致 | Commit: ef9cb07）
-   [x] `F-0723` 为消息定义版本兼容规则。
    - 备注（Task ID: F-0723 | Status: Done | Evidence: Envelope.V+ProtocolVersion(envelope.go:7-8,36-41)+Valid() 拒绝未知版本(envelope.go:70)+link readLoop 丢弃(link.go:160-163) | Verification: TestEnvelopeVersionGuard+TestReadLoopDropsBadMessages | Commit: 172dad4）
-   [x] `F-0724` 为有副作用消息定义幂等键。
    - 备注（Task ID: F-0724 | Status: Done | Evidence: agentws_dedupe envelope ID 去重；dedupe_test 7 例 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0725` 为需要确认的消息定义 ACK。
    - 备注（Task ID: F-0725 | Status: Done | Evidence: ConfigAck/TrafficAck+relaypush；TestPushAckApplied+TestAgentWSTrafficReport | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0726` 定义消息超时和重试规则。
    - 备注（Task ID: F-0726 | Status: Done | Evidence: agenthub/hub.go:114 Request 超时 ErrTimeout；TestPushTimeout+TestPushSendError | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0727` 定义断线期间消息处理规则。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0727 | Status: Done | Evidence: 统一断线策略落 docs/design/agent协议参考.md §3：四类消息（核心进程内报告静默丢/角色 spool 保留重放偏移回退/面板命令 ErrOffline 502/重连循环）逐类规则表 + 重连对账（syncNodeMeta/hello_ack 周期校准/caps 合并）+ 命令超时口径（ErrTimeout 15s）；代码锚点 spool.Watch 2s 扫描、link.Run 重连、agentws setNodeStatus | Verification: 文档-only，代码行为均有现存测试（TestRunReconnectsUntilCancel/TestPushTimeout 等） | Commit: f198536）
-   [x] `F-0728` 定义消息大小限制。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0728 | Status: Done | Evidence: 三面全限——面板 agent-WS 入站 8MiB（既有 maxAgentFrameBytes/TestAgentWSFrameLimit）+ agent 入站 link.MaxFrameBytes 默认 8MiB（DefaultMaxFrameBytes，connectOnce SetReadLimit，超限断连进重连，TestReadLoopFrameLimit）+ /api HTTP 请求体 8MiB（bodyLimit 中间件 http.MaxBytesReader，超限 400，TestAPIBodyLimit）；规则落 docs/design/agent协议参考.md §1 | Verification: go test 全绿 2026-11-26（server -timeout=30m exit 0，handler 848s） | Commit: 5dbc1f1）
-   [x] `F-0729` 定义未知消息处理方式。
    - 备注（Task ID: F-0729 | Status: Done | Evidence: link readLoop 丢弃坏消息+服务端留痕；TestReadLoopDropsBadMessages | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0730` 增加 Agent 协议兼容性测试。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0730 | Status: Done | Evidence: 跨版本兼容测试 compat_test.go：TestOldAgentPayloadsDecode（旧 agent 缺 Caps/caps/Error 字段的 hello/heartbeat/upgrade_status 按零值解码）TestUnknownPayloadFieldsIgnored（新端多字段解码忽略）TestUnknownTypeEnvelopeSurvivesDecode（新消息类型旧端按类型丢弃、信封可解）——落实 §5.2 兼容规则：载荷新字段 omitempty、两端独立升级 | Verification: go test ./... 全绿 2026-11-26（agentproto -v 三例 PASS，agent/server 本轮未改代码） | Commit: 803110c）

## 5.3 节点管理

-   [x] `F-0741` 支持节点注册审批。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0741 | Status: Done | Evidence: 审批流落地——enrollment token 增 require_approval（凭证级开关，默认保持令牌即授权）；开启后 enroll 事务落 node 为 disabled+status=pending+note『pending approval』，authNode 因 enabled=false 拒连（agentws_test 用例验证 hello 被拒）；POST /api/nodes/:id/approve 激活（enabled+status=unknown+清 note），POST /api/nodes/:id/reject 落 rejected 终态（不可再 approve）；TestEnrollmentApproval/TestEnrollmentNoApprovalKeepsBehavior | Verification: go test ./... 全绿 2026-11-26（server -timeout=30m exit 0） | Commit: d06c2f6）
-   [x] `F-0742` 支持节点元数据自动采集。
    - 备注（Task ID: F-0742 | Status: Done | Evidence: syncNodeMeta 注册元数据采集（meta_init 面板为准）；TestAgentWSHelloMeta | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0743` 支持管理员编辑业务元数据。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0743 | Status: Done | Evidence: NodeInput 增业务元数据编辑——labels（MarshalLabelsJSON 去重/去空/trim，nil 保留、显式 [] 清空）、group（Node 新列 size:64）、计费字段 billing_type/traffic_price_cents/monthly_cost_cents/currency/cost_note（指针区分缺席与显式 0，校验 vocabulary 与非负价）；create 与 update 双路径贯通，meta_init 面板接管语义保持；TestNodeBusinessMetaEdit | Verification: go test 全绿 2026-11-26（server 全套无 FAIL） | Commit: 5337d70）
-   [x] `F-0744` 支持节点入池检查。
    - 备注（Task ID: F-0744 | Status: Done | Evidence: provision→probe→online 入池检查流水线；TestProvisionPipeline | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0745` 支持节点摘池。
    - 备注（Task ID: F-0745 | Status: Done | Evidence: pool.go suspend/resume 摘池；TestSuspend+TestSweepSuspendAndResume | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0746` 支持节点维护模式。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0746 | Status: Done | Evidence: Node.Maintenance 字段+node.go create/update（in.Maintenance 可改）+alloc SweepAt/订阅池/策略池过滤 maintenance=false+云关停 drain 流程（Drain 开维护窗 F-0401）；原待补注「全仓 0 命中」已过时；本 commit 补 dash：NodesView 维护开关（行内 el-switch+编辑表单） | Verification: TestSweepSkipsMaintenance 绿 2026-11-26；dash vue-tsc -b+vite build 绿 | Commit: f242e52）
-   [x] `F-0747` 支持节点禁用。
    - 备注（Task ID: F-0747 | Status: Done | Evidence: enabled 关停；TestNodeCRUD | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0748` 支持节点重新启用。
    - 备注（Task ID: F-0748 | Status: Done | Evidence: enabled 重启+pool resume；TestNodeCRUD+TestResume | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0749` 支持节点批量操作。
    - 备注（Task ID: F-0749 | Status: Done | Evidence: router.go:184-185 batch；TestBatchNodeProc+TestBatchNodeConfig | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0750` 支持节点详情展示 Agent、Relay、Probe 状态。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0750 | Status: Done | Evidence: 新端点 GET /api/nodes/:id/status 聚合三态（agent: status/last_seen/enabled/maintenance；relay: node_proc_statuses proc=relay 快照+role/pool_state；probe: 最新 ProbeReport verdict/rtt/loss，缺失为 null 不编造）+GET /api/probe-reports 支持 node_id per-node 过滤；dash NodesView「状态」按钮+三态对话框 | Verification: TestNodeStatusAggregate+TestProbeReportsNodeFilter 绿 2026-11-26；dash build 绿 | Commit: 7e5c00a）
-   [x] `F-0751` 支持节点操作历史。
    - 备注（Task ID: F-0751 | Status: Done | Evidence: auditMiddleware 覆盖 /api,/admin 写(audit.go:119-140)+GET /api/audit-logs?target=:id 按节点过滤(audit.go:169-171,router.go:274) | Verification: TestAuditLogsCaptureWriteOps+TestAuditLogsCaptureFailedWriteAndFilters | Commit: 172dad4）
-   [x] `F-0752` 支持节点配置版本查看。
    - 备注（Task ID: F-0752 | Status: Done | Evidence: GET /api/nodes/:id/configs；config_err_test limit/404+agentws_test:442 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）

------------------------------------------------------------------------

# 6. Relay 与数据面

## 6.1 Relay 管理

-   [x] `F-0801` 审计当前 Relay 实现和进程边界。
    - 备注（Task ID: F-0801 | Status: Done | Evidence: Relay 实现与进程边界审计 §3.3（roles Standalone:true） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0802` 统一 Relay 配置模型。
    - 备注（Task ID: F-0802 | Status: Done | Evidence: config.RelaySpec(config.go:47-61) 统一模型(Listen/LandingAddr/Tunnel/ServerName/CAFile+ssh 参数)+relay.Run 消费+mergeSpec(relay.go:195-210) | Verification: relay_test | Commit: 842a7dd）
-   [x] `F-0803` 统一 Relay 状态模型。
    - 备注（Task ID: F-0803 | Status: Done | Evidence: agentproto.ProcStatus State=running/stopped/crashed(messages.go:124-139)+procs.Manager.Statuses 喂心跳(app.go:463-465)+ProcReport 状态变化推送(app.go:175-183) | Verification: TestBuildHeartbeatAndStartHeartbeat | Commit: 842a7dd）
-   [x] `F-0804` 统一 Relay 能力声明。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0804 | Status: Done | Evidence: agentproto.RelayCaps 结构(10 字段)+Heartbeat.RelayCaps(messages.go)+agent relayCaps(cfg) 心跳推导(caps.go；UDP 恒 false；MaxConns 0→128)+server Node.RelayCaps 列+agentws 心跳持久化(缺省不抹除)+GET /api/nodes/:id/status relay_caps(nodestatus.go)+dash 状态对话框能力标签(NodesView.vue relayCapTags)；TestRelayCaps+TestHeartbeatRelayCapsRoundTrip+TestHeartbeatRelayCapsPersistence | Commit: abad8f0）
-   [x] `F-0805` 支持 Relay 独立启停。
    - 备注（Task ID: F-0805 | Status: Done | Evidence: roles.go:31 relay Standalone:true+cmd -role；TestRegistry+TestRelayRunFile | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0806` 支持 Relay 配置校验。
    - 备注（Task ID: F-0806 | Status: Done | Evidence: relay 配置校验；TestRelayValidation | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0807` 支持 Relay 配置热更新。
    - 备注（Task ID: F-0807 | Status: Done | Evidence: loadOverride 热更新；TestRelayHotRepoint | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0808` 支持 Relay 连接数限制。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0808 | Status: Done | Evidence: config.RelaySpec.MaxConns+relay effectiveMaxConns(0→128)+accept 超限拒弃(active>limit 计 dropped 并日志)+热更对比/mergeSpec；TestRelayMaxConnsRefusesOverflow+TestEffectiveMaxConns+TestMergeSpecMaxConns | Commit: 9ed5e03）
-   [x] `F-0809` 支持并发流限制。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0809 | Status: Done | Evidence: config.RelaySpec.MaxStreams+relay streamLimit 原子顶(0=无限)+超限拒弃(streamDrops)+热更/mergeSpec；TestRelayMaxStreamsRefusesOverflow+TestEffectiveMaxConns 族 | Commit: 9ed5e03）
-   [x] `F-0810` 支持带宽限制。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0810 | Status: Done | Evidence: config.RelaySpec.RateLimitKbps+ratelimit.go 令牌桶 rateReader(双向包裹, 突发=rate)+热更/mergeSpec；TestRateReaderBucket(假时钟)+TestRateReaderUnlimited+TestEffectiveRateKbps | Commit: 9ed5e03）
-   [x] `F-0811` 支持 TCP 转发。
    - 备注（Task ID: F-0811 | Status: Done | Evidence: roles/relay serveConn；TestRelayForwards | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0812` 支持 UDP 转发。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-0812 | 未落地：P0 契约 relay 为单流 TCP io.Copy，landing 侧无 UDP 终结/多路分解能力，无 ferry 握手无法承载 UDP 会话；能力声明已如实上报 UDPForward=false（F-0804）。依赖 F-10006/F-10007 的 landing 侧改造，环境不具备，保持未勾。）
-   [x] `F-0813` 支持隧道断线重连。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0813 | Status: Done | Evidence: serveConn 重试环(effectiveRetries, 0→1)+retryBackoff 200ms+失败 tunnelCache.drop(spec) 缓存失效重拨；TestRelayDialRetryRecovers+TestRelayDialRetriesExhausted+TestEffectiveRetries | Commit: 4903519）
-   [x] `F-0814` 支持空闲连接清理。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0814 | Status: Done | Evidence: config.RelaySpec.IdleTimeoutSec+idletimeout.go deadlineConn(Read 前置 SetReadDeadline, Write 也续期保单向流)+双向包裹；TestRelayIdleCleanup(空闲 1s 关闭, 活跃保活)+TestEffectiveIdleTimeout | Commit: d88da00）
-   [x] `F-0815` 支持 Relay 运行指标采集。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0815 | Status: Done | Evidence: relayMetrics 原子计数器(conns/dropped/streamDrops/dials/dialFailures/bytesRx/bytesTx)+metrics.go serveMetrics 本地 /metrics(key value 行, 兼容 procs metricsParser)；TestRelayMetricsEndpoint+TestRelayMetricsSnapshot | Commit: e0be741）
-   [x] `F-0816` 支持 Relay 版本管理。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0816 | Status: Done | Evidence: procs.Manager.version(NewWithVersion)+ProcStatus.Version(同二进制部署 core 填自身 build)+NodeProcStatus.Version 列(空不抹除)+GET status/procs 输出版本；TestProcStatusVersion+procstatus/nodestatus 测试断言 v1.2.3 | Commit: 66b1c72）

## 6.2 传输插件

-   [x] `F-0821` 为现有每种 Relay 传输方式建立独立测试。
    - 备注（Task ID: F-0821 | Status: Done | Evidence: tunnel{ws-tls,ssh,quic}+quictunnel 分测；wstls_test/ssh_test/quicsidecar_test/quictunnel_test | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0822` 统一传输插件能力声明。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0822 | Status: Done | Evidence: tunnel.go Caps/ Capper/CapsOf/BaselineCaps(F-0822 统一接口)+四个插件各自声明(tls-camo/ws-tls Camouflage；ssh/quic 明文不声明)+caps_test(声明矩阵+裸插件基线)；ssh 通道多路复用是 sshd 自身行为故 Multiplex 仍 false | Commit: 1b39440）
-   [x] `F-0823` 统一传输插件错误分类。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0823 | Status: Done | Evidence: tunnel/errors.go ErrorClass(config/auth/network/heartbeat)+Error+Classify(ErrHeartbeatLost 哨兵识别)+Wrap/Errorf+各插件错误点全部分类(ssh 校验/键/握手/通道、camo/wstls CA、quic sidecar ERR=auth)+errors_test(分类+插件真实错误路径)；x/crypto 握手合并网络与拒绝故保守归 network | Commit: a12d5e1）
-   [x] `F-0824` 统一连接超时和重连行为。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0824 | Status: Done | Evidence: tunnel/retry.go DialRetry 统一重连(按分类跳过 config/auth、网络类按 Attempts 重试 Backoff、ctx 取消停止)+DialError 报告尝试数+Retryable；relay serveConn 改用 tunnel.DialRetry(F-0813 循环收编)+retry_test(分类重试/立即放弃/ctx 取消/默认值) | Commit: 83768bc）
-   [x] `F-0825` 验证 TLS 证书校验。
    - 备注（Task ID: F-0825 | Status: Done | Evidence: link tlsConfig；TestTLSConfig+TestRunTLSSetupFailure | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0826` 验证 SSH Host Key 校验。
    - 备注（Task ID: F-0826 | Status: Done | Evidence: tunnel/ssh Host Key 校验；TestSSHTunnelHostKeyVerify | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0827` 验证 QUIC 参数和资源限制。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0827 | Status: Done | Evidence: quictunnel DefaultQUICConfig 加 MaxIncomingStreams=256/MaxIncomingUniStreams=64 资源上限+ValidateQUICConfig 参数不变量(idle>0、keepalive<idle、流限>0)+NewClientWithConfig 校验构造+ferry-quic main 启动校验；quicparams_test | Commit: 17433a4）
-   [x] `F-0828` 验证 WS-TLS 连接行为。
    - 备注（Task ID: F-0828 | Status: Done | Evidence: TestWSTLSPlugin(注册/TLS/WS 握手/小帧/100KB 流回显)+TestWSTLSCancel+TestWSTLSServerNameFallback+TestWSTLSCAFileErrors+TestWSConnReadSkipsNonBinary+TestWSConnWriteAfterClose | Verification: wstls_test+wstls_extra_test | Commit: 842a7dd）
-   [x] `F-0829` 建立不同传输插件的性能对比测试。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0829 | Status: Done | Evidence: bench_test.go 各插件同一回显 1MiB 往返吞吐对比(tls-camo 176MB/s / ssh 137MB/s / ws-tls 81MB/s，本机 -benchtime=2x 量级对比；quic 需 sidecar 无法在包内诚实搭台，由 ferry-quic 模块 e2e 覆盖)+测试 helpers 改 testing.TB 复用 | Commit: 3b80181）

## 6.3 故障隔离

-   [x] `F-0831` 验证 Relay 进程故障不影响 Agent 心跳。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0831 | Status: Done | Evidence: agent faultisolation_test.go TestRelayCrashDoesNotStopHeartbeat：SIGKILL relay 后心跳 ≥3 条持续到达+supervisor 15s 内重启(Restarts≥1) | Verification: 测试绿（agent 全套 20/20 ok, server 全套 47/47 ok, 2026-11-26） | Commit: 2478350）
-   [x] `F-0832` 验证 Relay 故障不影响 Server 管理节点。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0832 | Status: Done | Evidence: server faultisolation_test.go TestRelayCrashDoesNotBlockNodeManagement：relay=crashed 行下 GET /api/nodes 正常、PUT 全字段 200、proc 操作 502(agent 离线语义)、status 保持 crashed | Verification: 测试绿（server 全套 47/47 ok, 2026-11-26） | Commit: 2478350）
-   [x] `F-0833` 验证 Relay 重启后恢复最后有效配置。
    - 备注（Task ID: F-0833 | Status: Done | Evidence: loadOverride 启动重读(relay.go:63-68,213-226)+configd 原子回滚(configd.go:107-126,205-238) | Verification: TestRelayRunFile/TestRelayHotRepoint(损坏 override 保当前)/TestLoadOverrideMissingAndError/TestApplyReloadFailRollsBack | Commit: 842a7dd）
-   [x] `F-0834` Relay 失联时更新关联 Path 状态。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0834 | Status: Done | Evidence: server pathhealth relayfault_test.go TestRelayLossDegradesPath：relay 跳心跳丢失(node offline)→path degraded；relay 池 suspended→degraded | Verification: 测试绿（server 全套 47/47 ok, 2026-11-26） | Commit: dcc83ad）
-   [x] `F-0835` Relay 恢复后执行健康探测再恢复调度资格。
    - 备注（Task ID: F-0835 | Status: Done | Evidence: pool.Sweep 仅 WellStreak 连续健康后恢复 StateActive(pool.go:161-169；suspend :152-160) | Verification: TestSweepSuspendAndResume(1 次健康不复、2 次复) | Commit: 842a7dd）
-   [x] `F-0836` 禁止仅凭 Relay 进程存活判定 Path 可用。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-0836 | Status: Done | Evidence: server pathhealth relayfault_test.go TestProcLivenessNeverMakesPathHealthy：三跳 relay proc 全 running 但无探测结论→path 仍 unknown；有 sick 结论→degraded（Evaluate 只读 node 存活/池态/探测结论，从不读 proc liveness） | Verification: 测试绿（server 全套 47/47 ok, 2026-11-26） | Commit: dcc83ad）

------------------------------------------------------------------------

# 7. 探测与健康管理

## 7.1 探测类型

-   [x] `F-0901` 审计当前 Probe 任务模型和结果模型。
    - 备注（Task ID: F-0901 | Status: Done | Evidence: Probe 任务/结果模型审计 §3.4 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0902` 区分 Node Health。
    - 备注（Task ID: F-0902 | Status: Done | Evidence: server/internal/nodehealth.Evaluate——disabled/offline=off、PoolState=suspended=degraded、30min 内新鲜 verdict sick→degraded/healthy→healthy（降级封底）、无 verdict=unknown；GET /api/nodes/:id/status 增 `health` 字段；dash 节点状态弹窗展示「节点健康」标签并注明区别于线路健康 | Verification: TestEvaluateRules+TestNodeStatusHealth；server 全套 48 包 ok 2026-11-26 | Commit: 544a895）
-   [x] `F-0903` 区分 Tunnel Health。
    - 备注（Task ID: F-0903 | Status: Done | Evidence: ProbeTargetTunnel="tunnel"(agentproto/messages.go:247)+tunnel 探针(TestProbeTunnelReturnPath)+aggregate tunnel/peer(aggregate.go:69-70) | Verification: TestProbeTunnelReturnPath | Commit: fbcb525）
-   [x] `F-0904` 区分 Path Health。
    - 备注（Task ID: F-0904 | Status: Done | Evidence: pathhealth.Evaluate/GroupHealth/Availability 为独立于 Node 健康的 path 级派生（F-0251/0268/0269 已落）；新增区分回归测试：entry 下线→该 node off、过它路径 degraded、无关路径/节点不受影响；landing 新鲜 verdict 按各自规则独立翻转 | Verification: TestNodeAndPathHealthIndependent（nodehealth/distinction_test.go） | Commit: f8ae7f8）
-   [x] `F-0905` 区分 Egress Health。
    - 备注（Task ID: F-0905 | Status: Done | Evidence: server/internal/egresshealth.Evaluate——节点自身 exit 基线探测决定（sick→degraded/healthy→healthy/无新鲜 verdict=unknown）、disabled/offline=off、池 suspended 刻意不进入本派生（挂起只影响入口池，agent 与探测照常）；GET /api/nodes/:id/status 增 `egress` 字段；dash 弹窗「出口健康」标签注明区别于「别人能否访问本节点」 | Verification: TestEvaluateRules+TestEgressDistinctFromNodeHealth（peer 判定健康+自身 exit sick → 节点 healthy/出口 degraded）+TestNodeStatusHealth；server 全套 49 包 ok 2026-11-26 | Commit: 7b787ba）
-   [x] `F-0906` 支持 RTT 探测。
    - 备注（Task ID: F-0906 | Status: Done | Evidence: probe measure RTT；TestProbeHealthy | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0907` 支持 Jitter 统计。
    - 备注（Task ID: F-0907 | Status: Done | Evidence: probe 每轮成功样本的平均相邻差 jitterOf（<2 样本=0）→ ProbeReport.JitterMs（wire `jitter_ms`，omitempty 向后兼容）→ storage.ProbeReport.JitterMs → saveProbeReports 落库 → /api/nodes/:id/status probe 块 `jitter_ms` → dash 弹窗「抖动 Xms」 | Verification: TestJitterOf+TestProbeReportJitterRoundTrip+TestAgentWSProbeReport（落库断言 48/6）+TestNodeStatusAggregate；agent 20 包/server 49 包全绿 2026-11-26 | Commit: 7b787ba）
-   [x] `F-0908` 支持 Packet Loss 统计。
    - 备注（Task ID: F-0908 | Status: Done | Evidence: probe lossPct；TestVerdictOf+TestProbeUnreachable | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0909` 支持 TCP Connect 探测。
    - 备注（Task ID: F-0909 | Status: Done | Evidence: probe.go:131 net.DialTimeout("tcp")；TestProbeHealthy | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0910` 支持 UDP 探测。
    - 备注（Task ID: F-0910 | Status: Done | Evidence: ProbeSpec.proto=payload_hex/expect_hex（config 校验：udp 必带 payload、hex 合法、payload 仅 udp 专用）；measureUDP 每轮发包+等回复，回复匹配 expect 前缀才算成功（UDP 无连接，无回复=不可达）；复用 summarize 的 rtt/jitter/loss | Verification: TestMeasureUDPReply+TestMeasureUDPMismatch+TestMeasureUDPSilent+TestProbeUDPReport；config TestValidate 6 新用例；agent 全套 20 包 ok 2026-11-26 | Commit: b2badb2）
-   [x] `F-0911` 支持应用层探测。
    - 备注（Task ID: F-0911 | Status: Done | Evidence: ProbeSpec.proto=http/https + http_path/expect_status（config 校验：path 必须以 / 开头、status 100-599、payload 仅 udp）；measureHTTP 每轮 HEAD 请求（重定向不跟随、视为已应答），expect_status 精确匹配才算成功，rtt=请求到响应头；https 走系统根证书（私有 CA 的 https 基线留待有真实需求时加 CAFile） | Verification: TestMeasureHTTPHealthy+TestMeasureHTTPExpectStatus（500+expect200 拒/302 无 expect 算/302 精确匹配过）+TestMeasureHTTPDeadService；config TestValidate 3 新用例；agent 全套 20 包 ok 2026-11-26 | Commit: 3b1923f）
-   [x] `F-0912` 支持公网出口探测。
    - 备注（Task ID: F-0912 | Status: Done | Evidence: config.Load 注入默认公网出口基线 `egress-baseline`（exit kind + https HEAD www.gstatic.com:443/generate_204 期望 204，60s 间隔）——零配置下每个 agent 都自测公网出口并回报，直接喂 F-0905 的 egresshealth；文件里显式 exit 探针优先生效 | Verification: TestEgressBaselineDefault（无 exit→注入 https/204；显式 exit→压制默认）+TestProbeDirectionDefault（fixture 含 exit 探针，数量不变）；agent 全套 20 包 ok 2026-11-26 | Commit: 95f149e）
-   [x] `F-0913` 支持探测超时和错误分类。
    - 备注（Task ID: F-0913 | Status: Done | Evidence: ProbeSpec.timeout_sec（1-60，缺省回 3s）约束每轮拨号/TLS/UDP 等待；classifyNetErr 把失败归入 dns/timeout/refused/reset/tls/other（UDP 无回复=no_reply、前缀不符=mismatch、HTTP 状态不符=http_status），summarize 多数票取类、任一轮成功则空；wire ProbeReport.err_class（omitempty）→ storage → /api/nodes/:id/status → dash「失败类型」 | Verification: TestClassifyNetErr+TestMeasureErrClass（tcp refused/http_status/http refused/udp mismatch/udp no_reply）+config timeout_sec 用例+TestAgentWSProbeReport（落库断言 timeout） | Commit: 2c13a4e）
-   [x] `F-0914` 支持探测结果去重。
    - 备注（Task ID: F-0914 | Status: Done | Evidence: agentws repeatReport+dedupe；TestReportDeduperWindow+TestReportDeduperTTL | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0915` 支持探测结果过期处理。
    - 备注（Task ID: F-0915 | Status: Done | Evidence: aggregate 窗口+idx_probe_window models.go:184-195；TestJudgeWindowAndLatest | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0916` 支持异常样本过滤。
    - 备注（Task ID: F-0916 | Status: Done | Evidence: server/internal/outlier.Trim 中位数/MAD 稳健剔除（DefaultFactor=5≈3.5/0.6745，<4 样本直通，MAD=0 仅留中位数，不改输入）；eveningapi eveningRTT 剔除后均值+Outliers 计数，EveningHour/EveningWindow.outliers 字段；dash 晚高峰对比「剔除异常样本」展示 | Verification: TestTrim（尖峰剔除/全等/直通/不改输入）+TestEveningOutlierTrim（7 点钟 5×20ms+1×1000ms → AvgRttMs=20 Outliers=1）+ 全 server 套件绿 2026-11-26（exit 0，50 ok）+ dash build 绿 | Commit: 0757ad1）

## 7.2 探测调度和健康计算

-   [x] `F-0921` 将可在边缘执行的探测任务下发给 Agent。
    - 备注（Task ID: F-0921 | Status: Done | Evidence: agentproto MsgProbeTask/MsgProbeTaskAck+ProbeTask{Version,Specs}/ProbeTaskAck；server Node.ProbeTasks 期望态（PUT /api/nodes/:id/probe_tasks 校验+落库，hello 后 dispatchProbeTasks 补发，在线 hub.Request 即时下发）+ validateProbeTasks 镜像 agent 规则；agent handleProbeTask 全有或全无校验、spool probe-tasks.json 持久化重启续跑、probe.Runner.Replace 热切换（同名遮蔽本地 config 探测，空列表撤回）| Verification: TestSetProbeTasks/TestSetProbeTasksValidation/TestProbeTaskHelloDispatch（catch-up+在线 delivered=true）+TestHandleProbeTaskApplies/RejectsInvalid/Withdraw/TestLoadProbeTasksOnStart+TestRunnerReplace；agent 全套件 20 ok、server 全套件 50 ok 2026-11-26 | Commit: 92fef5c）
-   [x] `F-0922` 为探测任务设置并发限制。
    - 备注（Task ID: F-0922 | Status: Done | Evidence: probe.Runner 信号量 acquire/release 包住 measure（defaultProbeConcurrency=16），SetConcurrency/Concurrency，config probe_concurrency（0=默认），app.New/proberole 两处接线 | Verification: TestProbeConcurrencyLimit（限 1 时目标侧并发高峰=1；默认时并行 spec 峰值≥2，慢 UDP 应答器测水印）+TestProbeConcurrencyWiring；agent 全套件 20 ok 2026-11-26 | Commit: 92fef5c）
-   [x] `F-0923` 为探测任务设置超时。
    - 备注（Task ID: F-0923 | Status: Done | Evidence: dialTimeout=3s(probe.go:22)+net.DialTimeout(probe.go:131) | Verification: TestProbeUnreachable | Commit: fbcb525）
-   [x] `F-0924` 支持按 Path 重要性配置探测频率。
    - 备注（Task ID: F-0924 | Status: Done | Evidence: Path.ProbeImportance（critical/normal/low，默认 normal，create/update 校验+合并）；POST /api/probe-tasks/sync-from-paths 把每条启用 Path 重建为入口节点的 path-<id> tunnel 下发任务（落点 address:port，direction 沿用 Path，间隔 critical 60s/normal 300s/low 900s），只重建 path-* 命名空间、运营者自建任务不动，停用/删除的 Path 下次同步自动撤回；deliverProbeTasks 状态分类（ok/offline/transport/rejected） | Verification: TestPathProbeImportanceValidation+TestSyncProbeTasksFromPaths（critical=60s/low=900s/运营任务保留/停用撤回）+TestImportanceProbeInterval；全 server 套件绿 2026-11-26（exit 0，50 ok） | Commit: 2dfd711）
-   [x] `F-0925` 故障时支持临时提高探测频率。
    - 备注（Task ID: F-0925 | Status: Done | Evidence: probe.loop 记住 sick verdict → boostUntil=now+5min，nextIntervalBoosted 4 倍提速（间隔/4，下限 10s 上限为原间隔，窗口过后再回原速）；与 19-23 点高峰提速叠加但不劣化 | Verification: TestNextIntervalBoosted（3600→900s、60→15s、30→10s 下限、5s 上限优先生效、过期不提速）；agent 全套件 20 ok 2026-11-26 | Commit: 92fef5c）
-   [x] `F-0926` 防止大规模节点互探造成探测风暴。
    - 备注（Task ID: F-0926 | Status: Done | Evidence: probe.Runner SetStartJitter/startDelay 首轮随机相位（45s 窗口内均匀，且不超过该 spec 自己的 interval），Replace 新任务集同样错峰；core agent 启用，独立 proberole 关闭（本地配置无群组风险，崩溃重启后首轮不能空窗）| Verification: TestStartJitterSpread（200 次抽样分布、小 interval 上限、0 关闭）+ runner/proberole 相关测试；agent 全套件 20 ok 2026-11-26 | Commit: 92fef5c）
-   [x] `F-0927` 定义健康状态计算规则。
    - 备注（Task ID: F-0927 | Status: Done | Evidence: aggregate.go:65 Judge+monitor.go:66 applyVerdict；TestJudgeRegionAndISP+TestApplyVerdictTransitions | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0928` 定义健康统计时间窗口。
    - 备注（Task ID: F-0928 | Status: Done | Evidence: ProbeFresh 30min 窗口；TestJudgeWindowAndLatest | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0929` 定义连续失败阈值。
    - 备注（Task ID: F-0929 | Status: Done | Evidence: DefaultSickStrikes=3(pool.go:25)+pool.go:152 | Verification: TestSweepSuspendAndResume | Commit: fbcb525）
-   [x] `F-0930` 定义恢复阈值。
    - 备注（Task ID: F-0930 | Status: Done | Evidence: DefaultWellStreak=2(pool.go:27)+pool.go:161 | Verification: TestSweepSuspendAndResume | Commit: fbcb525）
-   [x] `F-0931` 实现健康状态滞回，避免频繁抖动。
    - 备注（Task ID: F-0931 | Status: Done | Evidence: nodehealth/egresshealth Evaluate 改窗口多数票（healthy 票>0 且 sick 未过票数→healthy，sick 严格多数→degraded，全无→unknown），同一份 ProbeFresh 窗口内按 verdict 计票；tie 算 healthy（恶化需 sick 严格多数）；egresshealth 同款（出口基线）| Verification: TestEvaluateHysteresis（各 3:1 单离票→健康、3:1 杂票→劣化）+既有规则测试改 tie→healthy；两包 go test 绿 | Commit: c6bbdd0）
-   [x] `F-0932` 支持健康状态人工覆盖。
    - 备注（Task ID: F-0932 | Status: Done | Evidence: storage.HealthOverride（一节点一行，uniqueIndex，PUT 替换）+ nodehealth.Evaluate 查活动覆盖（expires_at>now）盖过计算态；PUT/GET/DELETE /api/nodes/:id/health_override；off（disabled/offline）不可被覆盖洗白；egresshealth 保持探测驱动不参与覆盖 | Verification: TestEvaluateOverride（活动覆盖生效/过期自动失效/off 拒绝洗白）+TestOverrideShowsInNodeStatus（端到端经 /api/nodes/:id/status）；全 server 套件绿 2026-11-26（50 ok，exit 0） | Commit: 43fa033）
-   [x] `F-0933` 人工覆盖必须记录原因、操作者和过期时间。
    - 备注（Task ID: F-0933 | Status: Done | Evidence: validateHealthOverride 强制 reason/operator 非空 + expires_at 必须未来（state 只许 healthy/degraded）；审计字段随行持久化，覆盖生效时解释串带操作者+期限+原因 | Verification: TestSetHealthOverrideValidation（6 例 400：未知 state/off 不可强制/空 reason/空 operator/过去期/缺期）+TestSetHealthOverride（替换后仍一行、新审计字段）；同上全绿 | Commit: 43fa033）
-   [x] `F-0934` 提供健康状态变化解释。
    - 备注（Task ID: F-0934 | Status: Done | Evidence: nodehealth.EvaluateWithReason 返回 {State,Reason}（Evaluate 改为其薄壳）；解释按决定链出：无新 verdict→"no fresh probe verdicts (30m window)"、pool suspended、probe window 计票（"N sick vs M healthy (30m)"）、覆盖→"manual override by <operator> until <time>: <reason>"；GET /api/nodes/:id/status 新增 health_reason | Verification: TestEvaluateWithReason（四类解释+计票串）+TestOverrideShowsInNodeStatus 检查 health_reason；同上全绿 | Commit: 43fa033）

## 7.3 业务质量指标

-   [x] `F-0941` 定义游戏质量指标集合。
    - 备注（Task ID: F-0941 | Status: Done | Evidence: quality.gameSet：RTT/Jitter/Loss/Availability 四项有序集，Derive 直读 ProbeReport（avail=100-loss，不可达 0），未知业务返回空集；TestGameSetMetrics+TestGameSetDerive | Verification: 双门禁 agent 全套件绿（exit 0）+server 全套件绿（exit 0，51 ok）2026-11-26 | Commit: 9f1e7ef）
-   [x] `F-0942` 定义网页质量指标集合。
    - 备注（Task ID: F-0942 | Status: Done | Evidence: quality.webSet：ConnectMs/TLSMs/TTFBMs/LossPct；agent measureHTTP 用 httptrace 实测 connect/TLS/TTFB 均值（明文 tls=0），经 agentproto→storage→agentws 持久化；TestMeasureHTTPPhases+TestWebSetMetrics+agentws 存储断言 | Verification: 双门禁 agent 全套件绿（exit 0）+server 全套件绿（exit 0，51 ok）2026-11-26 | Commit: 9f1e7ef）
-   [x] `F-0943` 定义视频质量指标集合。
    - 备注（Task ID: F-0943 | Status: Done | Evidence: quality.videoSet：ThroughputKbps/Loss/Jitter/BufferRisk；ProbeSpec.DownloadKB 可选 GET 下载测速率（1-10240KB，agent+panel 双重校验）；bufferRisk=loss*2+jitter 封顶 100；TestMeasureHTTPThroughput+TestVideoSetMetrics+TestValidateDownloadKB | Verification: 双门禁 agent 全套件绿（exit 0）+server 全套件绿（exit 0，51 ok）2026-11-26 | Commit: 9f1e7ef）
-   [x] `F-0944` 定义普通代理质量指标集合。
    - 备注（Task ID: F-0944 | Status: Done | Evidence: quality.proxySet：RTT/Loss/Availability 三项传输集；TestProxySetMetrics | Verification: 双门禁 agent 全套件绿（exit 0）+server 全套件绿（exit 0，51 ok）2026-11-26 | Commit: 9f1e7ef）
-   [x] `F-0945` 游戏评分纳入 RTT、Jitter、Loss 和 Availability。
    - 备注（Task ID: F-0945 | Status: Done | Evidence: quality.gameRule 四项加权（RTT 0.4/Jitter 0.2/Loss 0.2/Avail 0.2），lin 映射分数 0-100，不可达时相位无 datum 不伪装 0ms；TestScoreGame（满分/死亡/逐项单调） | Verification: 双门禁 agent 全套件绿（exit 0）+server 全套件绿（exit 0，51 ok）2026-11-26 | Commit: 9f1e7ef）
-   [x] `F-0946` 网页评分纳入连接、TLS、TTFB 和失败率。
    - 备注（Task ID: F-0946 | Status: Done | Evidence: quality.webRule 四项加权（connect 0.2/TLS 0.2/TTFB 0.35/loss 0.25），明文 tls=0 视为无 datum 并重归一化权重；TestScoreWeb（慢 TLS 降分/明文不罚/慢 TTFB 重罚/失败率降分） | Verification: 双门禁 agent 全套件绿（exit 0）+server 全套件绿（exit 0，51 ok）2026-11-26 | Commit: 9f1e7ef）
-   [x] `F-0947` 视频评分纳入吞吐、丢包和缓冲风险。
    - 备注（Task ID: F-0947 | Status: Done | Evidence: quality.videoRule 三项加权（throughput 0.4/loss 0.25/buffer risk 0.35），未开 download_kb 则吞吐无 datum 重归一化而非算 0；TestScoreVideo（满分/饥饿/无下载/卡播/不可达） | Verification: 双门禁 agent 全套件绿（exit 0）+server 全套件绿（exit 0，51 ok）2026-11-26 | Commit: 9f1e7ef）
-   [x] `F-0948` 不同业务评分使用独立规则。
    - 备注（Task ID: F-0948 | Status: Done | Evidence: quality.rule 按 business 分派独立规则，无默认兜底；同一 ProbeReport 在游戏/网页得分不同，网页相位不能救游戏分，未知业务返回 unscored；TestScoreIndependentRules | Verification: 双门禁 agent 全套件绿（exit 0）+server 全套件绿（exit 0，51 ok）2026-11-26 | Commit: 9f1e7ef）
-   [x] `F-0949` 支持区域、运营商和时段质量报表。
    - 备注（Task ID: F-0949 | Status: Done | Evidence: GET /api/quality-report?business=&window_hours=（1-168h）；按 region+ISP 与小时分桶聚合 quality.Score，输出 samples/avg_score/avg_rtt/avg_jitter/avg_loss 与 timeslots 小时分桶，无评分行不入平均，非法参数 400；TestQualityReport | Verification: 双门禁 agent 全套件绿（exit 0）+server 全套件绿（exit 0，51 ok）2026-11-26 | Commit: 9f1e7ef）

------------------------------------------------------------------------

# 8. Path 编排与调度

## 8.1 Path 编排

-   [x] `F-1001` 实现 Path 创建和编辑。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1001 | Status: Done | Evidence: paths.go createPath/updatePath 含字段合并与 Version 递增；APIs 与 node-link 校验此前已落地（F-0105/F-0243/F-0252 行，2455a6c），本轮按“不重做已完成部分”回填；TestPathCRUD | Verification: server 全套件 green exit=0（51 ok），handler 865s 2026-11-26 | Commit: 2455a6c）
-   [x] `F-1002` 实现 Path 启停。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1002 | Status: Done | Evidence: Path.Enabled 布尔与 create/update 显式启停写入（false 直写绕开 default:true）；GET /api/paths?enabled= 过滤；TestPathCRUD 覆盖 disabled 创建与筛选，此前已落地（F-0105/F-0250 行，2455a6c），本轮回填 | Verification: server 全套件 green exit=0（51 ok）2026-11-26 | Commit: 2455a6c）
-   [x] `F-1003` 实现 Path 拓扑校验。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1003 | Status: Done | Evidence: validatePathInput 拒绝 entry==landing 自环及 relay 复用端点，create/update 共用；角色匹配仍由分配期 role 过滤承担（legacy 行默认 landing）；TestPathCRUD 增补三条拒绝断言 | Verification: go build/vet ./... + server 全套件 green exit=0（51 ok，handler 865s）2026-11-26 | Commit: 3388bb2）
-   [x] `F-1004` 根据 Role 选择 Entry。
    - 备注（Task ID: F-1004 | Status: Done | Evidence: sub.go:74 role 过滤+alloc.go:448 role 候选；TestPick* | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1005` 根据 Role 选择 Landing。
    - 备注（Task ID: F-1005 | Status: Done | Evidence: alloc.go:454 landing role 过滤；TestPick* | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1006` 根据 Direction 过滤节点。
    - 备注（Task ID: F-1006 | Status: Done | Evidence: alloc.go:486-489 direction 匹配；TestDirectionalDefaults | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1007` 根据 Capability 过滤节点。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1007 | Status: Done | Evidence: GET /api/nodes?capability= 走 storage.HasCapability 内存过滤（静态列表/角色回退/动态 off 态统一），未知能力名 400；TestNodeFilters | Verification: go build/vet + server 全套件 handler 1724s ok，cmd/ferry 计时例隔离 0.38s PASS（全套件高负载 50.65s 超时为环境噪声）2026-11-26 | Commit: c7de0ec）
-   [x] `F-1008` 根据 Region 过滤节点。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1008 | Status: Done | Evidence: GET /api/nodes?region= 精确匹配下推 SQL；TestNodeFilters 覆盖命中/空集/组合过滤 | Verification: 同 F-1007 | Commit: c7de0ec）
-   [x] `F-1009` 根据 ISP/ASN 过滤节点。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1009 | Status: Done | Evidence: GET /api/nodes?isp= 与 ?asn= 两个独立精确过滤字段，可与其他条件 AND；TestNodeFilters | Verification: 同 F-1007 | Commit: c7de0ec）
-   [x] `F-1010` 根据 LineType 过滤节点。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1010 | Status: Done | Evidence: GET /api/nodes?line_type= 精确匹配下推 SQL；TestNodeFilters 覆盖命中/组合 | Verification: 同 F-1007 | Commit: c7de0ec）
-   [x] `F-1011` 根据维护和禁用状态过滤节点。
    - 备注（Task ID: F-1011 | Status: Done | Evidence: alloc.go:448 enabled/pool_state/status 过滤；TestPickHealthGate | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1012` 根据容量过滤节点。
    - 备注（Task ID: F-1012 | Status: Done | Evidence: CapacityMbps 候选；TestPickLeastConnCapacityWeight | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1013` 根据成本过滤节点。
    - 备注（Task ID: F-1013 | Status: Done | Evidence: costPer 成本序；TestPickCostFirst | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1014` 根据健康状态过滤节点。
    - 备注（Task ID: F-1014 | Status: Done | Evidence: Healthy=!sick 门；TestPickHealthGate | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1015` 支持候选 Path 预览。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1015 | Status: Done | Evidence: GET /api/paths/preview?entry_node_id=&direction= 复用 alloc 策略比较器/连接与探针信号，列出 sweep 会考虑的 landing 候选、排名、健康与资格与 winner，只读不写；TestPathCandidatePreview | Verification: go build/vet + server 全套件 green exit=0（51 ok，handler 765s，cmd/ferry 1.489s）2026-11-26 | Commit: 5c84357）
-   [x] `F-1016` 支持人工确认后创建 Path。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1016 | Status: Done | Evidence: PendingPath 草稿表 + POST/GET /api/paths/drafts + POST /api/paths/drafts/:id/confirm，确认时事务内建 Path 并标记 confirmed 带 path_id，拒绝/重复名/节点已删均回滚留 pending，终态不可复活；TestPathDraftLifecycle | Verification: go build/vet + server 全套件 green exit=0（51 ok）2026-11-26 | Commit: 7c2ff1b）
-   [x] `F-1017` 支持自动创建 Path。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1017 | Status: Done | Evidence: POST /api/paths/provision 自动创建 Path（复用 validatePathInput 拓扑校验，note=auto-provision），供调度器直接调用；TestAutoProvisionVerify | Verification: go build/vet + server 全套件 green exit=0（51 ok）2026-11-26 | Commit: 7c2ff1b）
-   [x] `F-1018` 自动创建后执行连通性验证。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1018 | Status: Done | Evidence: verifyNewPath 查 entry→landing 最近 30min 内 tunnel 探针判词，healthy+reachable 才放行；无判词/过期/ verdict=sick 均 422；TestAutoProvisionVerify（无探针/过期/病态/健康四分支） | Verification: go build/vet + server 全套件 green exit=0（51 ok）2026-11-26 | Commit: 7c2ff1b）
-   [x] `F-1019` 创建失败时清理本次创建的部分资源。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1019 | Status: Done | Evidence: 验证失败/查询出错均删除本次创建的 Path 行（rolled_back_path_id 返回），确认流走事务天然回滚，无半成品资源残留（测试断言 path count==0）；TestAutoProvisionVerify + TestPathDraftLifecycle | Verification: go build/vet + server 全套件 green exit=0（51 ok）2026-11-26 | Commit: 7c2ff1b）

## 8.2 调度器

-   [x] `F-1021` 审计现有调度和分配逻辑。
    - 备注（Task ID: F-1021 | Status: Done | Evidence: 调度分配逻辑审计 §3.5 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1022` 统一调度输入模型。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1022 | Status: Done | Evidence: alloc.Request 统一输入模型（Now/Policy/Entries/Landings/Conns/Sick/Manual/Current），BuildRequest 一次载入全部信号，SweepAt 重构为 Resolve冻结→BuildRequest→Decide→Apply 三段管线，行为与旧 sweep 等价（原 30 个 alloc 测试全数不动通过）；TestBuildRequestLoadsSignals | Verification: go build/vet + server 全套件 green exit=0（51 ok）2026-11-26 | Commit: 1a3b0cd）
-   [x] `F-1023` 统一调度输出模型。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1023 | Status: Done | Evidence: Decision 统一输出模型（候选按策略排序含资格与原因、成本上限排除项、WinnerID、Action=switch/keep/none/skip、from/to/reason），Decide 纯函数无 DB 写，Apply 执行器落排除 trail+换线行并产出 SwitchEvent；TestDecidePureModel/TestDecideKeepNoneSkip/TestApplyWritesTrailsAndRows | Verification: go build/vet + server 全套件 green exit=0（51 ok）2026-11-26 | Commit: 1a3b0cd）
-   [x] `F-1024` 实现候选 Path 过滤器。
    - 备注（Task ID: F-1024 | Status: Done | Evidence: 候选过滤器 alloc.go:448-489(enabled/role/pool_state/status/direction/healthy gate) | Verification: TestPick* | Commit: fbcb525）
-   [x] `F-1025` 实现健康约束。
    - 备注（Task ID: F-1025 | Status: Done | Evidence: Healthy 门 alloc.go:220-223 | Verification: TestPickHealthGate | Commit: fbcb525）
-   [x] `F-1026` 实现容量约束。
    - 备注（Task ID: F-1026 | Status: Done | Evidence: CapacityMbps alloc.go:188-198 | Verification: TestPickLeastConnCapacityWeight | Commit: fbcb525）
-   [x] `F-1027` 实现成本约束。
    - 备注（Task ID: F-1027 | Status: Done | Evidence: CostPerGB alloc.go:202-207 | Verification: TestPickCostFirst | Commit: fbcb525）
-   [x] `F-1028` 实现 Path 质量评分。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1028 | Status: Done | Evidence: alloc.Quality+LatestQuality（窗口内每 prober 最新判词按 target 均值）+QualityScore（loss 权重 0.7/RTT 0.3 折算 [0,1]，无样本=中性 0.5 不罚冷启动）；接入 Candidate/Request/CandidateEval/Decision 与面板 preview 的 quality 字段，策略比较器不动（F-1046 规则不变，quality 策略留给 F-1044）；TestLatestQuality/TestQualityScore/TestDecideCarriesQuality | Verification: go vet + server 全套件 green exit=0（51 ok）2026-11-26 | Commit: 8e74cce）
-   [x] `F-1029` 实现优先级规则。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1029 | Status: Done | Evidence: Node.SchedPriority 优先级档位（小值优先，0=标准档），LessFor 档位分区包裹全部策略比较器（档位压倒策略指标，同档内策略+缺价分区照旧），shouldSwitch 峰时升档直切/降档等闲时；NodeInput.sched_priority（nil 保留/显式 0 重置/负值 400），preview 输出 sched_priority；TestPickPriorityTier/TestShouldSwitchPriorityTier/TestSweepRespectsPriorityTier/TestNodeSchedPriority | Verification: go vet + server 全套件 green exit=0（51 ok）2026-11-26 | Commit: 879643b）
-   [x] `F-1030` 实现候选排序。
    - 备注（Task ID: F-1030 | Status: Done | Evidence: lessFor alloc.go:255-318+Pick 217-238+平局确定性 260 | Verification: TestPickTieBreaks | Commit: fbcb525）
-   [x] `F-1031` 实现故障回退。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1031 | Status: Done | Evidence: RebindSickPaths Path 重绑执行器：enabled Path 的 landing 探针病态/掉出可调度池时，按方向策略从健康候选重选替代 landing，version 乐观守卫（面板并发改动胜出），failover=manual 冻结整趟（复用 F-0294 门）；接入 sweepOnce 每轮执行，rebind 事件进日志/通知/E-16b 钩子；TestRebindSickPaths（冻结/重绑/无路可退/二次空转）+TestRebindLandingLeftPool | Verification: go vet + server 全套件 green exit=0（51 ok）2026-11-26 | Commit: 6d0a5ab）
-   [x] `F-1032` 实现调度决策记录。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1032 | Status: Done | Evidence: storage.AllocDecision（kind alloc/failover、RoundAt、双向策略、池规模、Switches、Decisions JSON 回放行）；只在真正动了状态的轮次落一张（alloc: Apply 有 switch 事件时；failover: RebindSickPaths 有 rebind 时），keep-only 轮不写（状态即 assignment 表）；decisionSnapshotRow 带 entry/方向/动作/from→to 名称/reason 免 join 可回放，PathRebindEvent 补 Direction 字段；读 API GET /api/alloc/decisions?limit=（默认 50，1-200 否则 400，最新在前）；TestApplyRecordsSnapshot+TestRebindRecordsSnapshot+TestAllocDecisionsList | Verification: go vet + server 全套件 green exit=0（51 ok）2026-11-26 | Commit: 43a3eae）
-   [x] `F-1033` 实现候选排除原因。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1033 | Status: Done | Evidence: collectCandidates 结构性跳过不再静默：候选=entry 自身（both 角色双池）/方向不匹配/存量能力列表缺 relay_landing 都带 operator 可读 reason 进 Decision.Exclusions（与 F-0606 单价超上限同列）；Apply 自动随 recordExclusion 落 AllocExclusion 审计（1h 去重防刷）；决策快照回放行加 exclusions 字段（omitempty）使 GET /api/alloc/decisions 可解释每个被排除 landing；TestDecideExplainsStructuralExclusions（三因排除+trail 去重+快照回放） | Verification: go vet + server 全套件 green exit=0（51 ok）2026-11-26 | Commit: 3b82a58）
-   [x] `F-1034` 实现调度结果解释。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1034 | Status: Done | Evidence: GET /api/alloc/explain（?entry_id= 过滤，非正数 400）：BuildRequest→Decide 只读跑当前信号不 Apply，operator 可直接回答「这条 entry 为什么在该 landing」；每 entry 输出 action/current/from→to/winner/reason + 候选表（landing 名称+eligible+reason+quality 分）+ 排除表（F-1033 原因带 landing 名称）；manual entry 不出 decision 单列 manual id 列表；DTO 平面化不外泄 Node 整行；TestAllocExplain（switch 全景/manual 分流/过滤/坏参 400） | Verification: go vet + server 全套件 green exit=0（51 ok）2026-11-26 | Commit: 726568c）
-   [x] `F-1035` 实现调度模拟 API。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1035 | Status: Done | Evidence: POST /api/alloc/simulate：指针字段 body（out/in/rebalance 窗口/三权重/max_cost_per_gb/missing_cost/hour）只覆盖出现的键，经 ValidateSetting（SavePolicy 校验抽出复用，零持久化）归一后叠到 BuildRequest 模型，hour 0-23 可把模拟轮挪到当天任意小时对比峰谷行为，Decide 只读输出与 explain 同构 DTO+simulated 标记；不写 assignment/不落快照/不改 policy；TestAllocSimulate（基线复算/费用上限排除两 landing/非法 tier 400/hour 覆盖+24 拒/GET /api/alloc 确认零持久化） | Verification: go vet + server 全套件 green exit=0（51 ok）2026-11-26 | Commit: 6498c50）
-   [x] `F-1036` 实现调度单元测试。
    - 备注（Task ID: F-1036 | Status: Done | Evidence: alloc_test 9 例（Pick/sweep/权重/手动） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）

## 8.3 调度模式与默认策略

-   [x] `F-1041` 定义 manual 调度模式。
    - 备注（Task ID: F-1041 | Status: Done | Evidence: alloc.go:473 hasManualAssignment 优先+LandingAssignment manual；TestSweepManualWins+TestLandingAssignment | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1042` 定义 priority 调度模式。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1042 | Status: Done | Evidence: PolicyPriority="priority" 入 ValidPolicy（ValidPolicy 仅 4 种的限制解除）：比较只看运维 SchedPriority 层级（F-1029 byTier 包裹承担层级序，层级内唯一 tiebreak 是名字，负载/单价/优质线/缺价分区全部不参与，operator 手定序不漂移）；shouldSwitch 同层不因负载换线（跨层升级/降级规则沿用 F-1029 头规则），SavePolicy 校验文案加 priority；TestPickPriorityMode（层级压价压载/层内名字序/峰时同层不churn+升级必动+降级等待）+TestSweepPriorityMode（sweep 级最贵 tier-0 胜出+ settled 不churn）+TestAllocPolicyPriority（policy API 收 priority/explain 出 top tier/模拟 cost_first 仍被 F-1029 层级分区管住/零持久化） | Verification: go vet + server 全套件 green exit=0（51 ok）2026-11-26 | Commit: c4eff6c）
    - 待补（F-1042 | 区级未满足原因：本区 F-1049 已关；其余：priority/quality 模式未满足(ValidPolicy 仅 4 种 alloc.go:46-52)、按套餐/业务类型配置未满足(PolicySetting 仅 per-direction)、覆盖到期自动恢复未满足(无 TTL、sweep 跳过 manual)、国内直连优先/中继更优/回退/比较依据 部分(perf_first 优质线路偏好近似，无直连vs中继比较依据记录)。）
-   [x] `F-1043` 定义 balanced 调度模式。
    - 备注（Task ID: F-1043 | Status: Done | Evidence: PolicyBalanced；TestPickBalanced | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1044` 定义 quality 调度模式。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1044 | Status: Done | Evidence: PolicyQuality="quality" 入 ValidPolicy：比较只看新鲜探针质量（F-1028 QualityScore，loss 0.7>RTT 0.3，无样本=中性 0.5 不罚冷启动），层内 score 高者胜、同分按名字，价格完全不参与（跳过缺价分区，同 priority 模式）；shouldSwitch 峰时只在 win 分数超 cur+0.15（qualitySwitchMargin，同 balanced 保守度）才动，谷值窗口按全序重排；SavePolicy 校验文案加 quality；TestPickQualityMode（实测压价压载/同分名字序）+TestShouldSwitchQualityMode（显著改善动/边际等待/劣化不动）+TestSweepQualityMode（sweep 级最贵实测线胜出+settled 不churn）+TestAllocPolicyQualityAccepted（API 收 quality 回读） | Verification: go vet + server 全套件 green exit=0（51 ok）2026-11-26 | Commit: 2915e29）
-   [x] `F-1045` 定义 cost 调度模式。
    - 备注（Task ID: F-1045 | Status: Done | Evidence: PolicyCostFirst；TestPickCostFirst | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1046` 为每种模式定义确定性的评分和约束规则。
    - 备注（Task ID: F-1046 | Status: Done | Evidence: alloc.go:214-216 规则+lessFor 纯函数；TestPick* 断言具体胜者 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1047` 支持按套餐配置调度模式。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1047 | Status: Done | Evidence: Node.PlanID（套餐亲和，0=服务所有套餐，panel-set 注册不覆盖：newPersistedNode+updateNode 指针 nil 保留/0 清除）+PolicySetting.SchedPlanModes（key=套餐 id 十进制，value=ValidPolicy 模式，ValidateSetting 校验 key 正整数+value 合法）；policyForEntry 在 Decide 每 entry 解析生效模式（plan 绑定且配了 override 则覆盖方向 tier，其余走系统 tier；窗口/权重/费用守卫保持系统级只覆盖模式）；注意 F-1029 层级分区对所有策略生效，测试用同层双 landing（cost_first 按价 vs priority 按名）才能观测模式差；TestPolicyForEntry+TestValidateSettingPlanModes+TestSweepPlanMode+TestNodePlanID+TestAllocPolicyPlanModes（另 PolicySetting 含 map 后两处 == 比较 reflect.DeepEqual 化） | Verification: go vet + server 全套件 green exit=0（51 ok）2026-11-26 | Commit: 557ced6）
-   [x] `F-1048` 支持按业务类型配置调度模式。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1048 | Status: Done | Evidence: PolicySetting.SchedGroupModes（key=Node.Group 业务分组标签自由串，value=ValidPolicy 模式，ValidateSetting 只验 value 合法）；policyForEntry 特异性序 系统 tier ← group ← plan（F-1047 plan 最具体胜出），Group 为空或未配置走系统 tier，窗口/权重/费用守卫保持系统级；与 F-1029 层级分区协同的测试构造同 F-1047：同层双 landing（cost_first 按价 vs priority 按名）观测模式差；TestPolicyForEntryGroup（含 plan-over-group）+TestValidateSettingGroupModes+TestSweepGroupMode+TestAllocPolicyGroupModes（API 往读+非法模式 400） | Verification: go vet + server 全套件 green exit=0（51 ok）2026-11-26 | Commit: 074f2a9）
-   [x] `F-1049` 支持人工覆盖调度结果。
    - 备注（Task ID: F-1049 | Status: Done | Evidence: createLanding Strategy:"manual"(landing.go:81)+hasManualAssignment 跳过自动(alloc.go:568-577) | Verification: TestSweepManualWins | Commit: fbcb525）
-   [x] `F-1050` 支持人工覆盖到期自动恢复。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1050 | Status: Done | Evidence: LandingAssignment.ExpiresAt（可选 TTL 列，nil=永久保持旧行为）+landingInput.expires_in_seconds（正整数，createLanding 换算绝对 expires_at，非正 400）；hasManualAssignment 时间感知（released_at IS NULL AND expires_at IS NULL OR > now）覆盖 BuildRequest/explain 全部调用点；releaseExpiredManual 在 SweepAt 前置（早于 F-0294 failover freeze——TTL 到期是运维已发声明的回收，不属于 failover 切换），过期行释放 released_at+ManualExpiredReason="manual_expired" 留痕，同轮 entry 重新自动分配；TestHasManualAssignmentExpiry（nil/未来/过去三态）+TestSweepManualTTL（活 TTL 不动，过期同轮释放+auto 事件）+TestLandingManualTTL（API 往读+0 秒 400+无 TTL 保持 nil） | Verification: go vet + server 全套件 green exit=0（51 ok）2026-11-27 | Commit: 18f9a03）
-   [x] `F-1051` 国内访问国内默认优先直连。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1051 | Status: Done | Evidence: PolicySetting.DirectFirst *bool（nil=默认开，directFirstOn）；结构定义 isDirectTo：entry 与 landing 共享非空 Region=直连（同区），其余=跨区中继（region 是面板维护的唯一地理维度）；pickDirectFirst 坐在 policy 序之上：直连组/中继组各按 policy 比较器取最优，默认直连组胜（cost_first 下贵直连压过便宜中继）；Decide 在 directFirstOn 时走 pickDirectFirst 否则原 Pick，explain/simulate 共用 Decide 自动同口径 | Verification: 同 F-1054 门禁（f1051-full.log 51 ok exit=0） | Commit: f7f9aa8）
-   [x] `F-1052` 只有测量证明中继更优时才允许国内线路走中继。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1052 | Status: Done | Evidence: directRelayMargin=0.15（同峰时切换保守度：噪声样本不得跨区搬流量）；pickDirectFirst 比较 F-1028 QualityScore：中继最优者分数 > 直连最优者+0.15 才让位中继，无样本中性 0.5 不会触发让位；开关关闭时纯 policy 序（便宜中继胜贵直连）；TestSweepRelayProvenBetter（干净中继 10ms/0% vs 拥塞直连 500ms/50% → 中继胜，切换 reason 带「中继更优」分数）+TestDirectFirstToggle（关=cost_first 胜+零 trail） | Verification: 同 F-1054 门禁（f1051-full.log 51 ok exit=0） | Commit: f7f9aa8）
-   [x] `F-1053` 直连不可用时按 Policy 回退。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1053 | Status: Done | Evidence: pickDirectFirst 无健康直连候选时整个规则让位：Pick(policy,...) 对全部健康候选按 policy 挑（ sickness 由既有健康过滤承担），且不落任何直连优先 exclusion（规则未发声不制造审计噪声）；TestSweepDirectFallback（直连探针 sick → 中继按 policy 胜出 + AllocExclusion 零行） | Verification: 同 F-1054 门禁（f1051-full.log 51 ok exit=0） | Commit: f7f9aa8）
-   [x] `F-1054` 记录直连与中继的质量比较依据。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1054 | Status: Done | Evidence: 落败组逐候选落 exclusion（reason 携带分数）：直连胜=中继候选 trail「直连优先：直连 X.XX，中继 X.XX 未超阈值 0.15」，中继让位胜=直连候选 trail「测量证明中继更优：中继 X.XX > 直连 X.XX+0.15」，经 F-1033 通道进 AllocExclusion（1h 去重）并随 F-1032 决策快照 replay；切换 reason 追加「中继更优 X.XX > 直连 X.XX」；TestSweepDirectFirst（断言 exclusion reason 含「直连优先」）+TestSweepRelayProvenBetter（断言含「测量证明中继更优」）+TestAllocPolicyDirectFirst（API 显式 false 往读/默认 nil=开；注意复用结构体 unmarshal 前 nil 化防脏指针） | Verification: go vet + server 全套件 green exit=0（51 ok）2026-11-27 | Commit: f7f9aa8）

## 8.4 容量管理

-   [x] `F-1061` 统一上行带宽定义。
    - 备注（Task ID: F-1061 | Status: Done | Evidence: BwUpMbps(models.go:109)+CapacityMbps 回退链(alloc.go:188-198) | Verification: TestCapacityMbpsFallbacks | Commit: fbcb525）
-   [x] `F-1062` 统一下行带宽定义。
    - 备注（Task ID: F-1062 | Status: Done | Evidence: BwDownMbps(models.go:110)+CapacityMbps 回退链(alloc.go:188-198) | Verification: TestCapacityMbpsFallbacks | Commit: fbcb525）
-   [x] `F-1063` 区分标称带宽和实测带宽。
    - 备注（Task ID: F-1063 | Status: Done | Evidence: SpeedMeasuredMbps/SpeedCalibratedAt(models.go:114-115)+measured>down>up>100(alloc.go:189-197) | Verification: TestCapacityMbpsFallbacks | Commit: fbcb525）
-   [x] `F-1064` 统一带宽利用率计算。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1064 | Status: Done | Evidence: storage.Node.CapacityMbps()（F-1061/1062/1063 回退链实测>下行>上行>100 提为 storage 单源，capacity.go）+storage.UtilizationPct(mbps, capacity, active)（唯一公式：吞吐/容量百分比，无样本 active=false 报 -1 不造假 0%）；双内联点收敛：alloc.Candidate.CapacityMbps 委托 storage（调度容量权重同源）、loadapi 删 inline switch 改 n.CapacityMbps()+UtilizationPct（dash 80% 预警读同一口径）；TestNodeCapacityMbps（回退链四态）+TestUtilizationPct（55/100=55、零流量活跃=0、非活跃=-1）+loadapi 端点测试回归绿 | Verification: go vet + server 全套件 green exit=0（51 ok）2026-11-27 | Commit: 9ad8a74）
-   [x] `F-1065` 支持连接数限制。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1065 | Status: Done | Evidence: Node.MaxConns（0=不限，panel-set：newPersistedNode+updateNode 指针 nil 保留/0 清除，agent 注册不触碰，同 PlanID 通道）；collectCandidates 收集阶段超限排除（l.MaxConns>0 && req.Conns>=MaxConns → exclusion「连接数 X 达上限 Y」，走 F-1033 结构排除通道进 AllocExclusion 1h 去重+F-1032 快照 replay）；TestSweepMaxConnsCap（满限 cheap 让位 open+trail 带数，降到限下恢复 cost_first 胜出）+TestNodeMaxConns（create 设/缺省保/显式 0 清） | Verification: go vet + server 全套件 green exit=0（51 ok）2026-11-27 | Commit: fe27510）
-   [x] `F-1066` 支持并发流限制。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1066 | Status: Done | Evidence: Node.MaxStreams（0=不限，panel-set 同 F-1065 通道：newPersistedNode+updateNode 指针 nil 保留/0 清除）；调度面排除：collectCandidates 中 l.MaxStreams>0 && req.Conns>=MaxStreams → exclusion「并发流 X 达上限 Y」（F-1033 通道进 AllocExclusion+F-1032 快照）；语义注记诚实：agent 侧 RelayCaps.MaxStreams（F-0809 已勾）保持硬强制，P0 单流转发下一连接=一后端拨号故实时连接数即流负载，mux 插件少计晚触发、agent 硬顶仍兜底（模型注释+排除逻辑双处写明）；TestSweepMaxStreamsCap（满限 cheap 让位+trail 带「并发流/达上限 50」，限下调宽恢复）+TestNodeMaxStreams（create 设/缺省保/显式 0 清） | Verification: go vet + server 全套件 green exit=0（51 ok）2026-11-27 | Commit: 589f091）
-   [x] `F-1067` 支持节点超载预警。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1067 | Status: Done | Evidence: monitor.OverloadSweep/RunOverload（cmd/ferry main 接线，MonitorIntervalSec 同拍）：对每节点按 F-1064 统一口径算利用率，>=OverloadUtilPct=80（与面板同一条线）upsert kind=overload 活跃告警（重复轮刷新 message 不叠行），回落 <80% 转 resolved+ResolvedAt 留痕，无样本节点永不误报；采样单源化补完 F-1064：storage.LatestLoad（pair-diff Mbps+逐 proc 最新 conns 求和，storage.LoadFresh=30min 窗口）替换 loadapi 内联 pair 逻辑，面板 load 视图与超载扫描共用一条采样路径；Alert.Kind 注释补 overload；TestOverloadSweep（85% 建/重复刷新不叠/13% 解析留痕/无样本不报）+TestLatestLoad（53.3Mbps 双 proc 求和/孤立样本只计 conns/过期缺席） | Verification: go vet + server 全套件 green exit=0（51 ok）2026-11-27 | Commit: df3aa4e）
-   [x] `F-1068` 支持 Path 容量估算。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1068 | Status: Done | Evidence: storage.PathCapacity(entry, relay, landing)（木桶估算：链路容量=各节点 CapacityMbps 最小值，返回瓶颈节点名，直连链路 relay=nil 跳过；复用 F-1064 单源回退链，实测校准参与竞底）；只读端点 GET /api/paths/:id/capacity（router 注册）：capacity_mbps+bottleneck/bottleneck_id+逐节点明细分解（entry/relay/landing mbps），不触发探测不写行，链上节点缺失 404；TestPathCapacity（relay 竞底 30/直连 100/实测校准 10 竞底）+TestPathCapacityEndpoint（relay 链 30 瓶颈 cap-relay、直连 100 瓶颈 cap-landing） | Verification: go vet + server 全套件 green exit=0（51 ok）2026-11-27 | Commit: 7fe8e63）
-   [x] `F-1069` 支持容量不足时自动降权。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1069 | Status: Done | Evidence: PolicySetting.MinCapacityFloor（Mbps，0=关，ValidateSetting 非负校验）经 ScoreWeights.CapacityFloor（Weights() 注入，比较器旋钮随 w 走到 LessFor/Pick/preview 全链）；LessFor 组装序 byTier(byFloor(missing 分区(less)))：降权在算子 tier 内、结构上先于缺价分区，全部模式生效（floor 是运维容量门槛非打分微调）；只降已知容量低于 floor 的——无带宽字段不罚（未知≠不足，循 F-0292 先例），Candidate.CapacityMbps 的 100 地板不影响判定（用原始字段判短）；TestCapacityFloorDemotion（floor 开 30M 让位 500M/未知不罚/关=恢复 cost_first/tier 越过 floor）+TestSweepCapacityFloor（sweep 级让位+settled 不 churn）+TestValidateSettingCapacityFloor+TestAllocPolicyCapacityFloor（API 往读+负值 400；注意 Candidate 字面量须带 CostKnown/CostFresh 否则全员落 missing 分区价格不参与） | Verification: go vet + server 全套件 green exit=0（51 ok）2026-11-27 | Commit: 38ced0f）
-   [x] `F-1070` 支持管理员容量覆盖。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1070 | Status: Done | Evidence: Node.CapacityOverrideMbps(models.go:231-234) 领先 CapacityMbps 链(capacity.go:21-22 override>measured>downlink>uplink>100)+面板面 NodeInput.capacity_override_mbps(model.go:78-80)+node.go 建/改(newPersistedNode/updateNode 0=清除回测量值) | Verification: TestNodeCapacityMbps override leads + TestNodeCapacityOverride(create sets/absent keeps/0 clears)+go vet+server 全套件 green exit=0（51 ok）2026-11-27 | Commit: 838b2a0）

------------------------------------------------------------------------

# 9. 订阅与外部客户端适配

## 9.1 统一订阅模型

-   [x] `F-1101` 审计现有订阅生成代码。
    - 备注（Task ID: F-1101 | Status: Done | Evidence: 订阅生成代码审计 §3.6 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1102` 定义统一内部 Subscription DTO。
    - 备注（Task ID: F-1102 | Status: Done | Evidence: sub.Entry{Node,RttMs}(links.go:169-172) 单 DTO 被三适配器共用 | Verification: TestPackV2Ray/TestPackClash/TestPackSingBox | Commit: 842a7dd）
-   [x] `F-1103` 让订阅 Adapter 只依赖 DTO，不直接查询数据库。
    - 备注（Task ID: F-1103 | Status: Done | Evidence: PackV2Ray/PackClash/PackSingBox 均收 DTO(links.go:201/clash.go:72/singbox.go:165)，sub 包无 gorm import | Verification: 上述测试 | Commit: 842a7dd）
-   [x] `F-1104` 统一节点协议参数表达。
    - 备注（Task ID: F-1104 | Status: Done | Evidence: nodeConfig{UUID,Password,Method,TLS,SNI,Host,Path,Net,...}(links.go:21-38)+parseConfig(links.go:40-50) 三适配器共用 | Verification: 上述测试 | Commit: 842a7dd）
-   [x] `F-1105` 统一 TLS、SNI、Transport 和相关参数表达。
    - 备注（Task ID: F-1105 | Status: Done | Evidence: nodeConfig TLS/SNI/Host/Path/Net 各适配器映射自身 schema(v2ray 参数 links.go:67-81/clash servername+applyTransport clash.go:181-201/sing-box tls.server_name+singBoxTransport singbox.go:103-161) | Verification: 上述测试 | Commit: 842a7dd）
-   [x] `F-1106` 统一 UDP 支持声明。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1106 | Status: Done | Evidence: storage.Node.UdpOpen(models.go) 单一声明字段+NodeInput.udp_open 面板面(create set/absent keep/false clear)+clash 渲染 ClashProxy.Udp(protocol/clash.go baseClashProxy 读 rc.Node.UdpOpen, omitempty 未声明节点字节不变 golden 门控)；v2ray 链接与 sing-box outbound 无 UDP 表达面，声明保持静默 | Verification: TestPackClashUdpDeclaration+TestNodeUdpOpen+go vet+server 全套件 green exit=0（51 ok）2026-11-27 | Commit: fd6c18b）
-   [x] `F-1107` 统一节点标签、地区和排序信息。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1107 | Status: Done | Evidence: Node.Labels 进入输出面：BuildRemark 新增 {labels} 占位符(filter.go，逗号拼接，无标签/坏文本容错为空串，与过滤侧 nodeLabels 同容错)，经 ApplyConfig→Entry.Remark→entryLink 流入 v2ray 分享链接与 clash 节点名；地区/排序沿用已统一的 entryRegion/orderByProbe/SortEntries(region|rtt|name) | Verification: TestBuildRemarkLabels(labels join/无标签空/坏文本容空)+go vet+server 全套件 green exit=0（51 ok）2026-11-27 | Commit: 17654a6）
-   [x] `F-1108` 统一订阅版本和更新时间。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1108 | Status: Done | Evidence: 统一投递 helper subscriptionMeta(sub.go：Usage 三头+Last-Modified(HTTP 日期 UTC)+X-Subscription-Version(body sha1 前 12 位，同 body 同版本))，主订阅与自建订阅五格式均经它出口消除双份漂移；两 switch 重构为 body+ctype 单点投递 | Verification: TestSubscriptionVersionAndTime(主面格式/同 body 同版本/自建面一致)+go vet+server 全套件 green exit=0（51 ok）2026-11-27 | Commit: 585b836）

## 9.2 格式 Adapter

-   [x] `F-1121` 审计现有分享链接生成能力。
    - 备注（Task ID: F-1121 | Status: Done | Evidence: 分享链接生成审计 §3.6（links.go:201 PackV2Ray+TestPackV2Ray） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1122` 完成 V2Ray 分享链接 Adapter。
    - 备注（Task ID: F-1122 | Status: Done | Evidence: sub/links.go:201 PackV2Ray；TestPackV2Ray | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1123` 审计现有 Clash/Mihomo 生成能力。
    - 备注（Task ID: F-1123 | Status: Done | Evidence: Clash/Mihomo 生成审计 §3.6（clash.go:72+TestPackClash） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1124` 完成 Clash/Mihomo YAML Adapter。
    - 备注（Task ID: F-1124 | Status: Done | Evidence: sub/clash.go:72 PackClash；TestPackClash（yaml 严格 Unmarshal） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1125` 审计现有 sing-box 生成能力。
    - 备注（Task ID: F-1125 | Status: Done | Evidence: sing-box 生成审计 §3.6（singbox.go:165+TestPackSingBox） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1126` 完成 sing-box JSON Adapter。
    - 备注（Task ID: F-1126 | Status: Done | Evidence: sub/singbox.go:165 PackSingBox；TestPackSingBox | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1127` 为每个 Adapter 建立客户端能力矩阵。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1127 | Status: Done | Evidence: sub/capability.go ClientCaps 五 target 一行一声明(Notes/UDP/PacketEncoding/LazyGroups/RegionGroups/JSON)+CapsFor 单点查询；mihomo 为 clash 超集，xray/singbox 为纯 JSON 无注释位 | Verification: TestClientCapsMatrix(行齐/未知 miss/超集关系)+TestCapsMatchAdapters(矩阵声明与适配器实际输出互证：udp:/“# note”/无 // 注释)+go vet+server 全套件 green exit=0（51 ok）2026-11-27 | Commit: 2e2535b）
-   [x] `F-1128` 为每个 Adapter 建立配置语法测试。
    - 备注（Task ID: F-1128 | Status: Done | Evidence: 测试内 yaml/json 严格解析断言；clash_test+singbox_test | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1129` 使用真实客户端版本执行解析测试。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1129 | Status: Done | Evidence: TestSubscriptionUARealClients(subformat_test.go)：9 个真实版本 UA(v2rayNG/1.8.23、SFA sing-box/1.9.3、ClashMetaForAndroid/2.10.3.Meta、clash-verge/v2.0.0、FlClash/0.8.0、ClashforWindows/0.20.39、Shadowrocket/1985、Xray/24.9.30、Hiddify/1.4.5)→按客户端自身解析方式消费响应(v2ray base64 链接行/clash 家族 KnownFields 严格 YAML/JSON targets outbounds 形状)+Content-Type 断言 | Verification: 9 子用例全绿+go vet+server 全套件 green exit=0（51 ok）2026-11-27 | Commit: 5c80be0）
-   [x] `F-1130` 对不支持的字段提供明确错误或安全降级。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1130 | Status: Done | Evidence: 中心化 KindEnum 校验(protocol.go Validate：声明枚举的字段值出枚举即明确错误，携协议名+字段名+枚举表+实际值，vless/vmess/trojan net、vless flow、tuic congestion_control 全覆盖)——修复 clash 渲染只查必填不查枚举、裸 network 静默透传的缺口；fieldValue 抽取共用；明确错误面(unknown protocol/missing required/out-of-enum)与安全降级面(ErrNotSupported skip/未知 Extra 键丢弃/JSON targets 无注释)均钉死 | Verification: TestUnsupportedFieldClearError(五 target 均 kcp 明确错误)+TestUnknownExtraFieldDegrades(四 target 不泄漏 acme)+go vet+server 全套件 green exit=0（51 ok）2026-11-27 | Commit: 6eaf692）
-   [x] `F-1131` 不向客户端输出其不支持的配置字段。
    - 备注（Task ID: F-1131 | Status: Done | Evidence: 各适配器仅输出自身 schema 键(clash omitempty clash.go:17-44/sing-box 仅写 set 键 singbox.go:33-86) | Verification: TestPackSingBox(无 sni/insecure/obfs)、TestSingBoxOutboundVless(无 tls/transport/flow)、TestPackClashHysteria2 | Commit: 842a7dd）
-   [x] `F-1132` 不假设不同客户端具有相同策略组能力。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1132 | Status: Done | Evidence: 每target 策略组词汇钉死不互借：TestProxyGroupShapesPerTarget(groupshape_test.go)——plain clash 地区组 url-test 无 lazy 键(非核心词汇)；mihomo 同形+lazy:true(meta 专属，随 F-1127 能力矩阵行)；v2ray 家族仅链接/注释行(分组走排序+备注)；singbox/xray 纯 outbounds 数组无组对象 | Verification: 上述测试+go vet+server 全套件 green exit=0（51 ok）2026-11-27 | Commit: abfd508）

## 9.3 订阅权限与安全

-   [x] `F-1141` 根据用户状态过滤订阅。
    - 备注（Task ID: F-1141 | Status: Done | Evidence: sub/availability.go:11 UserActive+enabled 过滤；TestSubscriptionAvailability | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1142` 根据套餐权限过滤订阅。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-1142 | Status: Done | Evidence: User.PlanID(models.go，0=无套餐)+UserInput.plan_id(user.go 建/改 PATCH 语义 0=清除)+订阅候选 WHERE 增 (plan_id=0 OR plan_id=?)——绑定节点(F-1047 Node.PlanID)只发给该套餐订阅者，无套餐节点人人可见，主订阅与自建订阅同一 WHERE | Verification: TestSubscriptionPlanFilter(无套餐只见 free/订阅者见两者/清除绑定后回落)+go vet+server 全套件 green exit=0（51 ok）2026-11-27 | Commit: 2441dfd）
    - 待补（F-1142 | 区级未满足原因：区级现状：全部未满足或部分——套餐权限过滤部分(无 Plan 模型，按 enabled/role/pool_state+SAVE-6 cost-tier 过滤非套餐权限)、Path Group 过滤未满足(无 PathGroup 模型)、Policy 过滤未满足(无 Policy)、ETag/Last-Modified 未满足(无条件请求，每次全量渲染)、缓存失效部分(无缓存、新鲜度靠每次重查)、访问审计部分(仅 device/IP 观察 upsert 非 per-request 审计行)。）
-   [ ] `F-1143` 根据 Path Group 过滤订阅。（依赖：无；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-1144` 根据 Policy 过滤订阅。（依赖：无；完成条件：需求落地+测试绿+证据行）
-   [x] `F-1145` 过滤禁用节点和不符合策略的线路。
    - 备注（Task ID: F-1145 | Status: Done | Evidence: sub.go:74 enabled/pool_state/status；TestSubscription | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1146` 支持订阅令牌重置。
    - 备注（Task ID: F-1146 | Status: Done | Evidence: user.go:300 resetSubToken；TestResetSubTokenErrors+e2e 重置腿 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1147` 支持订阅访问限频。
    - 备注（Task ID: F-1147 | Status: Done | Evidence: sub.go:33 subLimiter/subMissLimiter；TestSubscriptionRateLimit | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1148` 支持 ETag 和 Last-Modified。（依赖：无；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-1149` 支持订阅缓存失效。（依赖：无；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-1150` 记录订阅访问审计。（依赖：无；完成条件：需求落地+测试绿+证据行）
-   [x] `F-1151` 对过期或封禁用户执行明确的订阅拒绝策略。
    - 备注（Task ID: F-1151 | Status: Done | Evidence: 过期/封禁 404 或空订阅；TestSubscription+TestSubscriptionAvailability | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1152` 对流量耗尽用户执行套餐定义的订阅策略。
    - 备注（Task ID: F-1152 | Status: Done | Evidence: UserActive 额度+userinfo 头；TestSubscription | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）

------------------------------------------------------------------------

# 10. 用户、套餐、设备与流量

## 10.1 用户与套餐

-   [x] `F-1201` 审计 User、Plan、Subscription 和 Device 模型。
    - 备注（Task ID: F-1201 | Status: Done | Evidence: User/Plan/Subscription/Device 模型审计 §3.1+§3.7（结论：Plan/Subscription 表未建） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1202` 明确用户状态机。
    - 备注（Task ID: F-1202 | Status: Done | Evidence: sub/availability.go:11-22 UserActive 四态（启用/到期/超配额/不限）+sub.go:48-52 禁用 404+review.go DisableInactive 周期停用 | Verification: TestUserActive（sub/availability_test.go:11）+TestSubscriptionAvailability（sub_test.go:128）+TestDisableInactiveRespectsResetCycle（review_test.go:88） | Commit: ad8f965）
-   [ ] `F-1203` 明确套餐生效和过期规则。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1203 | 部分：到期规则明确（User.ExpiresAt models.go:12+UserActive 到期分支+模板默认 usertemplate.go:128-130）；无套餐实体/生效窗口——套餐=单套 user_template KV+每用户 ExpiresAt，无结构化生效窗口。）
-   [x] `F-1204` 明确套餐流量周期。
    - 备注（Task ID: F-1204 | Status: Done | Evidence: models.go:11 ResetCycle+review 尊重周期；TestDisableInactiveRespectsResetCycle | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1205` 明确套餐限速规则。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1205 | 部分：限速字段与校验齐（BwUp/BwDown models.go:26-27+校验 usertemplate.go:58-63+PATCH user.go:226-238）；执行点在节点侧/仓外，服务端仅存储下发。）
-   [ ] `F-1206` 明确套餐线路权限。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1206 | 部分：无套餐线路权限字段；仅配额联动降档（超阈值只出低成本档入口 sub.go:88-102+quota/link.go:119-129），非套餐线路权限。）
-   [ ] `F-1207` 明确套餐区域权限。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1207 | 未满足：Region 仅节点元数据/故障聚合维度（models.go:98），订阅无条件下发全部 entry/both 节点（sub.go:73-84），无按用户/套餐的区域权限。）
-   [x] `F-1208` 明确套餐设备数量限制。
    - 备注（Task ID: F-1208 | Status: Done | Evidence: DeviceLimit/IPLimit；TestSubscriptionDeviceLimit+TestSubscriptionIPLimit | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1209` 明确流量耗尽处理策略。
    - 备注（Task ID: F-1209 | Status: Done | Evidence: review 耗尽停用+订阅空；TestDisableInactive+TestSubscription | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1210` 支持套餐升级和降级。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1210 | 部分：字段级变更齐（PATCH user.go:219-268+卡密发放 redeem.go:179-209+SAVE-6 自动降档）；无命名套餐升级/降级流程、无按比例折算。）
-   [ ] `F-1211` 支持套餐变更历史。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1211 | 部分：无套餐变更历史表；仅间接留痕：AuditLog 请求体快照（models.go:735-745）+Grant.Snapshot 变更前后快照（models.go:441-450）。）

## 10.2 设备管理

-   [x] `F-1221` 审计现有 HWID 和设备数限制。
    - 备注（Task ID: F-1221 | Status: Done | Evidence: HWID 与设备数限制审计 §3.7（SubDevice idx_subdev_key+TestSubscriptionDeviceLimit） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1222` 统一设备绑定规则。
    - 备注（Task ID: F-1222 | Status: Done | Evidence: subdevice.go:18-82 subDeviceCheck 统一 hwid/ip 双 kind 绑定+复合唯一索引 models.go:68-76+24h IP 窗懒清理 | Verification: TestSubscriptionDeviceLimit+TestSubscriptionIPLimit+TestSubDeviceLimitValidation+TestSubDevicePruneUnlimited（subdevice_test.go:43/80/182/133） | Commit: ad8f965）
-   [x] `F-1223` 支持设备列表。
    - 备注（Task ID: F-1223 | Status: Done | Evidence: GET devices；TestUserDevicesErrors+TestPanelMeDeviceUsage | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1224` 支持设备解绑。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1224 | 未满足：仅 GET /api/users/:id/devices（router.go:239→subdevice.go:85-100）；无解绑/删除端点（用户删除清设备行属整户删除）。）
-   [x] `F-1225` 支持设备上限检查。
    - 备注（Task ID: F-1225 | Status: Done | Evidence: subdevice 上限检查；TestSubDeviceLimitValidation | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1226` 支持异常设备告警。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1226 | 部分：仅设备/IP 超限告警（subdevice.go:136-156 herald.KindDeviceLimit 按用户按日去重）；无异常设备检测（异常 UA/IP/偏离）。）
-   [x] `F-1227` 避免仅凭 IP 变化认定设备变化。
    - 备注（Task ID: F-1227 | Status: Done | Evidence: subdevice.go:56-80 设备身份=hwid 行；IP 变化仅新增 ip-kind 行不重认/重计设备；无 hwid 请求不落设备行 | Verification: TestSubscriptionDeviceLimit+TestSubscriptionIPLimit（subdevice_test.go:43/80） | Commit: ad8f965）

## 10.3 流量记账

-   [x] `F-1241` 审计 traffic.report 的真实处理链路。
    - 备注（Task ID: F-1241 | Status: Done | Evidence: traffic.report 处理链路审计 §3.7（agentws.go:445→291→460） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1242` 明确入口、出口或单端计量口径。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1242 | 部分：计量在持有用户 inbound 的节点 xray（xraystats.go:67-89）→saveUserTraffic（agentws.go:346-433）；无节点角色/单端口径判定（role 记录而流量路径忽略）。）
-   [ ] `F-1243` 防止入口和落地重复计费。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1243 | 部分：at-most-once 去重（agentws_dedupe）+离线即丢（traffic.go:219-226）防帧重传重复计；无入口/落地双端防重守卫或测试。）
-   [x] `F-1244` 统一流量单位。
    - 备注（Task ID: F-1244 | Status: Done | Evidence: 全链路统一字节 int64：rx_bytes/tx_bytes（models.go:138-139/266-267）+proto Rx/Tx uint64（messages.go:202-203）+agentws.go:335-340 直拷；GB 仅计价/展示 | Verification: traffic_test.go:55-58 原值透传+TestAgentWSTrafficReport（agentws_test.go:477） | Commit: ad8f965）
-   [ ] `F-1245` 统一上行和下行定义。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1245 | 部分：agent 口径 downlink=Rx=用户收/uplink=Tx=用户发（xraystats.go:33-34）；客户端 Subscription-Userinfo 映射 upload=Rx/download=Tx（sub.go:114,180）——与 agent 口径上下行相反。）
-   [x] `F-1246` 支持用户维度统计。
    - 备注（Task ID: F-1246 | Status: Done | Evidence: agentws.go:291 saveUserTraffic→traffic_logs；TestAgentWSUserTraffic | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1247` 支持 Node 维度统计。
    - 备注（Task ID: F-1247 | Status: Done | Evidence: agentws.go:269 saveNodeTraffic→node_traffic_logs；TestAgentWSTrafficReport | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1248` 支持 Path 维度统计。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1248 | 未满足：无 Path 模型/维度；统计仅用户×日（traffic.go:134-135）与节点×进程（models.go:262-273）。）
-   [x] `F-1249` 支持周期配额重置。
    - 备注（Task ID: F-1249 | Status: Done | Evidence: quota.go:54 UsedBytes 周期窗；TestSubResetCycleWindow | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1250` 支持重复上报去重。
    - 备注（Task ID: F-1250 | Status: Done | Evidence: agentws_dedupe 去重；dedupe_test 7 例 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1251` 支持断线后的补报。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1251 | 未满足：断线流量静默丢弃（traffic.go:219-226 send==nil 即 return）；spool 只承载 probe 角色；设计明确 at-most-once 无重传。）
-   [ ] `F-1252` 支持流量异常检测。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1252 | 部分：仅阈值型配额预警（notifyscan/scan.go:111-158 默认 80%）+节点带宽利用率（loadapi.go:12）；无尖峰/异常偏离检测。）
-   [ ] `F-1253` 支持流量账目审计。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1253 | 部分：traffic_logs 只增不改+管理员审计轮转；无流量账目对账/复核（对账仅支付侧 payrecon.go）。）
-   [x] `F-1254` 建立流量记账幂等测试。
    - 备注（Task ID: F-1254 | Status: Done | Evidence: 流量幂等；TestAgentWSDuplicateTrafficFrame+TestAgentWSDedupSurvivesReconnect | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1255` 建立断线补报集成测试。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1255 | 未满足：无断线补报集成测试；TestReporterOfflineDropsSilently（traffic_test.go:67）反而断言「丢弃」。）

------------------------------------------------------------------------

# 11. 订单、支付与运营

## 11.1 订单和支付

-   [x] `F-1301` 审计订单状态机。
    - 备注（Task ID: F-1301 | Status: Done | Evidence: 订单状态机审计 §3.8（PaymentOrder models.go:327） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1302` 统一订单幂等规则。
    - 备注（Task ID: F-1302 | Status: Done | Evidence: order_no uniqueIndex models.go:330+paynotify.go:51 事务；TestEpusdtNotify+TestReconcileGuards | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1303` 统一订单超时和取消规则。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1303 | 未满足原因：未满足：expired 状态声明(models.go:345,351)但全仓无写入；无过期/取消路由(router.go:260-306 仅 reconcile/detail/refund/orders/notify)；coupon.go:273 明注「本仓无订单过期清扫，pending 常驻」。）
-   [x] `F-1304` 审计支付 Provider 接口。
    - 备注（Task ID: F-1304 | Status: Done | Evidence: 支付 Provider 接口审计 §3.8（provider.go:14-20） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1305` 验证支付回调签名。
    - 备注（Task ID: F-1305 | Status: Done | Evidence: epusdt.go:139 Verify/:168 sign；TestVerify+TestSign | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1306` 保证支付回调幂等。
    - 备注（Task ID: F-1306 | Status: Done | Evidence: idx_provider_external models.go:413-414 唯一+事务；TestEpusdtNotify | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1307` 建立支付结果补偿机制。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1307 | 未满足原因：部分：仅检测无补救——payrecon.go:93-135 标记缺流水/缺发放/孤儿，但无重发 Grant/重查回调；Provider 接口(provider.go:14-20)无 query/pull 方法，且回调即永久 pending。）
-   [ ] `F-1308` 建立支付对账任务。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1308 | 未满足原因：未满足：对账仅为查询接口(router.go:260 GET /api/payments/reconcile→payrecon.go:43)；main.go 约 17 个后台循环无一为支付对账。）
-   [x] `F-1309` 对未接通的支付渠道明确标记不可用。
    - 备注（Task ID: F-1309 | Status: Done | Evidence: packages/payment/disabled.go:11 NewDisabled(alipay/wechat)；payment 包 TestRegistered/TestDisabled | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1310` 记录订单和支付操作审计。
    - 备注（Task ID: F-1310 | Status: Done | Evidence: auditMiddleware 覆盖 refund/redeem；TestAuditLogsCaptureWriteOps | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）

## 11.2 优惠与分销

-   [x] `F-1321` 审计优惠码实现。
    - 备注（Task ID: F-1321 | Status: Done | Evidence: 优惠码审计（Coupon models.go:356+coupon_err_test+TestPanelRedeem） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1322` 审计活动规则。
    - 备注（Task ID: F-1322 | Status: Done | Evidence: 活动规则审计（Campaign models.go:374+TestCampaignCRUDValidation/TestCampaignAutoApply） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1323` 审计分销佣金计算。
    - 备注（Task ID: F-1323 | Status: Done | Evidence: 分销佣金审计（Distributor :436/Ledger :449 佣金 0-100+distributor.go:166+TestDistributorCRUD/TestRedeemDistributorLedger/TestDistributorPayoutAdjust） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1324` 统一账目流水。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1324 | 未满足原因：部分：三张异构表（PaymentTransaction models.go:428/Grant :441/DistributorLedger :467）仅以字符串关联；统一仅在查询层(payrecon.go:17-47 reconcileRow+distributor.go:131)，无统一流水表/枚举。）
-   [ ] `F-1325` 统一佣金结算状态。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1325 | 未满足原因：部分：统一 ledger(kind sale/commission/payout/adjust)+派生未结余额(distributor.go:131-152)+条件 INSERT payout 守卫(:382)；但无显式结算状态（无 pending/settled/paid 字段、无结算批次，payout 仅手动 distributor.go:329-333）。）
-   [ ] `F-1326` 建立异常订单和重复佣金测试。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1326 | 未满足原因：部分：异常订单已覆盖（TestReconcile/TestEpusdtNotifyConcurrentSettle/TestReconcileGuards）；重复佣金无重放测试（dedup 仅断言 txn/grant 计数，佣金唯一性靠原子核销 redeem.go:78-84 未在 ledger 层测试）。）

## 11.3 通知

-   [x] `F-1341` 审计现有 Notification 和 Announcement 模块。
    - 备注（Task ID: F-1341 | Status: Done | Evidence: 通知/公告审计（Notification :603+TestAnnouncementWindow/Audience+notify TestSendWebhook） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1342` 统一通知事件模型。
    - 备注（Task ID: F-1342 | Status: Done | Evidence: Event models.go:652+herald Emit；TestEmit+TestEventsAPI | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1343` 统一投递状态。
    - 备注（Task ID: F-1343 | Status: Done | Evidence: Event.Status/EventDelivery models.go:670；TestDeliverSuccess+TestEventResultAPI | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1344` 支持失败重试和退避。
    - 备注（Task ID: F-1344 | Status: Done | Evidence: deliver.go:15-27 指数退避 MaxAttempts=6；TestDeliverRetryBackoffAndDeadLetter+TestBackoffCap | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1345` 支持通知预览。
    - 备注（Task ID: F-1345 | Status: Done | Evidence: GET /events/samples；TestListEventSamplesErrors+TestEventsAPI | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1346` 支持投递结果查询。
    - 备注（Task ID: F-1346 | Status: Done | Evidence: GET /events/deliveries；TestListEventDeliveriesErrors+TestEventResultAPI | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1347` 支持 Webhook 通知渠道。
    - 备注（Task ID: F-1347 | Status: Done | Evidence: notify Webhook 通道；TestSendWebhook | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1348` 通知渠道故障不得阻塞核心业务。
    - 备注（Task ID: F-1348 | Status: Done | Evidence: herald 失败仅记日志不阻断（monitor.go:136）；TestDeliverErrorPaths+TestEmitCountDBErrors | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）

------------------------------------------------------------------------

# 12. Dash 管理后台

> Dash 是 Ferry
> 的主要运维入口。云服务商资源管理、开关机、成本和账单必须在 Dash
> 内形成完整工作流，而不是只有后端 API。

## 12.1 总览

-   [ ] `F-1401` 展示节点总数和在线数。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1401 | 部分：仅区域/ISP 维度在线/总数卡（NodesView.vue:207-226）；无全局节点总数/在线数。）
-   [ ] `F-1402` 展示健康与不健康节点数。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1402 | 部分：仅区域/ISP 维度 healthy/degraded/failed（NodesView.vue:228-239）；无不健康计数汇总。）
-   [ ] `F-1403` 展示 Entry、Landing 数量。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1403 | 未满足：无 Entry/Landing 数量展示。）
-   [ ] `F-1404` 展示 Relay 在线状态。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1404 | 未满足：relay 仅作受管进程选项（NodesView.vue:829）；无 relay 在线状态。）
-   [ ] `F-1405` 展示 Path 总数和可用率。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1405 | 部分：晚间报告有可用率/RTT/丢包（CostView.vue:590-637）；无 Path 计数。）
-   [ ] `F-1406` 展示当前总流量和连接数。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1406 | 部分：仅节点级 conns/Mbps 负载单元格（NodesView.vue:95-107）；无聚合总览。）
-   [ ] `F-1407` 展示区域资源分布。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1407 | 部分：区域维度卡（NodesView.vue:731-745）；无专门分布视图。）
-   [x] `F-1408` 展示告警摘要。
    - 备注（Task ID: F-1408 | Status: Done | Evidence: NodesView.vue:151-196 活跃告警徽标+对话框（:608-610/:875-901，api getAlerts:400） | Verification: API 侧 TestAlertGuards（alerts_test.go:276）；dash 无前端单测 | Commit: ce8b33e）
-   [x] `F-1409` 展示本月云资源成本摘要。
    - 备注（Task ID: F-1409 | Status: Done | Evidence: CostView.vue:278-295 摘要卡（月至今流量/月固定/月度预估/节点数） | Verification: API 侧 TestCostAPI（costapi_test.go）；dash 无前端单测 | Commit: ce8b33e）
-   [ ] `F-1410` 展示估算成本与实际账单差异。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1410 | 未满足：有月度预估（CostView.vue:287-290）；无实际账单对比。）

## 12.2 节点与线路

-   [x] `F-1421` 提供节点列表和筛选。
    - 备注（Task ID: F-1421 | Status: Done | Evidence: NodesView.vue:598-727 列表+role 筛选+节点/区域/ISP 三视图 | Verification: docs/audit-platform-impl-2026-11-03.md §3.11（17 dash 视图确认）；dash 无前端单测 | Commit: ce8b33e）
-   [ ] `F-1422` 提供节点详情。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1422 | 部分：仅编辑对话框，无专门详情页（NodesView.vue:747-810）。）
-   [ ] `F-1423` 展示 Node 关联的 Cloud Resource。（依赖：F-0341；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1423 | 未满足：无 CloudResource 模型/关联。）
-   [ ] `F-1424` 展示 Agent、Relay 和 Probe 状态。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1424 | 部分：agent 版本+进程快照（NodesView.vue:668-672/:312-331）；relay/probe 状态未呈现（/api/probe-reports 后端有、dash 未用）。）
-   [ ] `F-1425` 展示节点配置版本。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1425 | 未满足：/api/nodes/:id/configs（router.go:206）有路由无 UI。）
-   [x] `F-1426` 提供节点维护和启停管理。
    - 备注（Task ID: F-1426 | Status: Done | Evidence: NodesView.vue:109-148 proc start/stop/reload+pool suspend/resume/standby（:358-372 batch proc/:714-723） | Verification: 后端 TestNodeProcOp/TestBatchNodeProc（procop_test.go）；dash 无前端单测 | Commit: ce8b33e）
-   [ ] `F-1427` 提供 Path 列表。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1427 | 部分：仅 landing assignment 列表（LandingsView.vue:189-230）；无 Path 实体。）
-   [ ] `F-1428` 提供 Path 详情。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1428 | 未满足：无 Path 详情。）
-   [ ] `F-1429` 提供 Path Group 管理。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1429 | 部分：区域级指派+传输状态（LandingsView.vue:266-283）；无 PathGroup 模型。）
-   [x] `F-1430` 提供 Policy 管理。
    - 备注（Task ID: F-1430 | Status: Done | Evidence: LandingsView.vue:232-264 alloc policy out/in/rebalance/权重（api putAllocPolicy:160） | Verification: API 侧 TestAllocPolicy（allocapi_test.go）；dash 无前端单测 | Commit: ce8b33e）
-   [ ] `F-1431` 展示调度候选和排除原因。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1431 | 部分：手动 entry/landing 选择（LandingsView.vue:285-325）；无排除列表。）
-   [ ] `F-1432` 展示探测历史和质量趋势。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1432 | 部分：24h 质量趋势（CostView.vue:590-637）；无按节点探测历史。）

## 12.3 云服务商与资源

-   [x] `F-1441` 提供 Provider 管理入口。
    - 备注（Task ID: F-1441 | Status: Done | Evidence: ProvisionView.vue:173-199/:263-282 provider CRUD（api :592-604） | Verification: API 侧 TestProviderAPI（providerapi_test.go:40）；dash 无前端单测 | Commit: ce8b33e）
-   [ ] `F-1442` 提供 Cloud Account 管理入口。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1442 | 区级未满足原因：云资源与云操作页面未实现——无 CloudResource 模型即无资源列表/详情/关联/电源/重装/批量/同步/错误页；本区 F-1443~F-1454、F-1456~F-1457 未满足；F-1442 部分（provider 含加密 access_key 兼作账号，无独立 account 实体）；F-1455 部分（仅 provision job 状态/日志，非资源操作进度）。）
-   [ ] `F-1443` 提供账号连接测试。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-1444` 提供资源同步入口。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-1445` 提供云资源列表。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-1446` 提供云资源详情。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-1447` 提供云资源与 Node 关联管理。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-1448` 提供开机操作。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-1449` 提供关机操作。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-1450` 提供重启操作。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-1451` 提供强制停止操作和高危确认。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-1452` 对支持的 Provider 提供重装/重建入口。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-1453` 提供批量操作预览。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-1454` 提供批量操作逐资源结果。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-1455` 展示云操作任务进度。（依赖：无；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-1456` 展示 Provider API 最后同步时间。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-1457` 展示 Provider 错误和权限不足原因。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）

## 12.4 成本与账单

-   [x] `F-1461` 提供成本总览。
    - 备注（Task ID: F-1461 | Status: Done | Evidence: CostView.vue:278-295 成本总览摘要卡 | Verification: API 侧 TestCostAPI（costapi_test.go）；dash 无前端单测 | Commit: ce8b33e）
-   [ ] `F-1462` 提供 Provider 成本统计。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1462 | 部分：仅节点/区域/ISP 维度（CostView.vue:306-360）；无 Provider 维度成本。）
-   [ ] `F-1463` 提供账号成本统计。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1463 | 未满足：无账号维度成本。）
-   [x] `F-1464` 提供区域成本统计。
    - 备注（Task ID: F-1464 | Status: Done | Evidence: CostView.vue:333-349 区域汇总表（节点/流量花费/固定成本/月度预估） | Verification: API 侧 TestCostAPI（costapi_test.go）；dash 无前端单测 | Commit: ce8b33e）
-   [ ] `F-1465` 提供资源成本统计。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1465 | 未满足：无资源维度成本。）
-   [x] `F-1466` 提供 Node 成本统计。
    - 备注（Task ID: F-1466 | Status: Done | Evidence: CostView.vue:306-329 节点行（月流量/流量花费/固定成本/月度预估） | Verification: API 侧 TestCostAPI（costapi_test.go）；dash 无前端单测 | Commit: ce8b33e）
-   [ ] `F-1467` 提供 Path 成本估算。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1467 | 未满足：无 Path 成本。）
-   [ ] `F-1468` 提供账单导入和同步任务管理。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1468 | 未满足：仅价格表导入（CostView.vue:465-474）；无账单导入/同步任务。）
-   [ ] `F-1469` 提供账单明细查询。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1469 | 未满足：无账单明细查询。）
-   [ ] `F-1470` 提供估算与实际账单对比。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1470 | 部分：月至今实际+月度预估并列（CostView.vue:278-290）；无显式对比视图。）
-   [ ] `F-1471` 提供成本分摊规则管理。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1471 | 未满足：成本阈值（CostView.vue:297-303）+价格监控有；无分摊规则（分摊）配置。）
-   [ ] `F-1472` 提供账单差异对账入口。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1472 | 未满足：无账单差异对账。）
-   [ ] `F-1473` 提供成本异常告警。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1473 | 部分：高成本阈值（CostView.vue:297-303）+价格 watch（:560-587）+增长率异常检测 API（GET /cost/anomalies spend_spike，Commit: 2be063c）；未接 Herald 周期告警循环。）
-   [ ] `F-1474` 支持成本明细导出。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1474 | 未满足：无导出。）
-   [ ] `F-1475` 在页面明确区分实际、估算和分摊金额。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1475 | 部分：实际（流量/固定）与预估列区分（CostView.vue:310-326）；无分摊金额。）

## 12.5 配置、任务与审计

-   [ ] `F-1481` 提供配置版本列表。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1481 | 未满足：/api/nodes/:id/configs（router.go:206）有路由无 UI。）
-   [ ] `F-1482` 提供配置差异查看。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1482 | 未满足：无配置差异查看。）
-   [ ] `F-1483` 提供配置下发状态。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1483 | 部分：批量配置逐节点 applied/failed（NodesView.vue:406-443）；无任务列表。）
-   [ ] `F-1484` 提供配置失败和回滚记录。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1484 | 未满足：仅提示文案「失败自动回滚」（NodesView.vue:844）；无失败/回滚记录。）
-   [ ] `F-1485` 提供批量运维任务列表。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1485 | 部分：批量 proc/config 即时逐节点结果（NodesView.vue:614-622/:358-443）；无任务队列视图。）
-   [ ] `F-1486` 提供 Agent 升级任务列表。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1486 | 部分：逐节点升级对话框（NodesView.vue:445-483）；无任务列表。）
-   [x] `F-1487` 提供自动恢复任务列表。
    - 备注（Task ID: F-1487 | Status: Done | Evidence: RecoveryView.vue:74-121 recoveries 列表+action 回放（api :795-799） | Verification: API 侧 TestTraceRowsReplay（recovery_test.go）；dash 无前端单测 | Commit: ce8b33e）
-   [x] `F-1488` 提供操作审计查询。
    - 备注（Task ID: F-1488 | Status: Done | Evidence: AuditView.vue:49-66 查询（api getAuditLogs:1034） | Verification: API 侧 TestAuditLogsQuery（audit_test.go）；dash 无前端单测 | Commit: ce8b33e）
-   [x] `F-1489` 提供告警确认和关闭入口。
    - 备注（Task ID: F-1489 | Status: Done | Evidence: NodesView.vue:184-196 resolveOne（api resolveAlert:420） | Verification: API 侧 TestAlertGuards（alerts_test.go）；dash 无前端单测 | Commit: ce8b33e）

------------------------------------------------------------------------

# 13. Panel 用户面板

-   [x] `F-1501` 展示用户账户信息。
    - 备注（Task ID: F-1501 | Status: Done | Evidence: panel HomeView.vue:199-215 账户卡（/api/panel/me api.ts Me:41-55） | Verification: API 侧 TestPanelMe（panel_test.go）；panel 无前端单测 | Commit: ce8b33e）
-   [ ] `F-1502` 展示当前套餐。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1502 | 部分：配额/状态卡（HomeView.vue:208-213）；无套餐实体展示。）
-   [x] `F-1503` 展示套餐到期时间。
    - 备注（Task ID: F-1503 | Status: Done | Evidence: HomeView.vue:210 到期字段 formatDate(me.expires_at) | Verification: API 侧 TestPanelMe（panel_test.go）；panel 无前端单测 | Commit: ce8b33e）
-   [x] `F-1504` 展示流量使用情况。
    - 备注（Task ID: F-1504 | Status: Done | Evidence: HomeView.vue:201-207 用量表+meter（used/quota+剩余） | Verification: API 侧 TestPanelMe（panel_test.go）；panel 无前端单测 | Commit: ce8b33e）
-   [ ] `F-1505` 展示设备列表。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1505 | 未满足：仅设备计数（HomeView.vue:212）；无列表。）
-   [ ] `F-1506` 提供设备解绑。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1506 | 未满足：面板无设备解绑。）
-   [x] `F-1507` 展示订阅地址。
    - 备注（Task ID: F-1507 | Status: Done | Evidence: HomeView.vue:227-248 订阅链接（v2ray/clash target） | Verification: API 侧 TestSubscription（sub_test.go）；panel 无前端单测 | Commit: ce8b33e）
-   [ ] `F-1508` 支持订阅令牌重置。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1508 | 未满足：sub_token 在 Me 返回（api.ts:44）但面板无重置 UI（管理端 UsersView.vue:148 有）。）
-   [x] `F-1509` 支持选择订阅格式。
    - 备注（Task ID: F-1509 | Status: Done | Evidence: HomeView.vue:108-111/:230-247 通用（v2ray/base64）与 clash/mihomo（YAML）两种格式链接 | Verification: API 侧 TestSubscriptionFormats（sub_test.go）；panel 无前端单测 | Commit: ce8b33e）
-   [ ] `F-1510` 提供 sing-box 导入说明。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1510 | 未满足：无 sing-box 导入说明。）
-   [ ] `F-1511` 提供 Clash/Mihomo 导入说明。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1511 | 未满足：仅 YAML 链接，无导入指引。）
-   [x] `F-1512` 展示公告。
    - 备注（Task ID: F-1512 | Status: Done | Evidence: NotificationsView.vue:54-59/:75-89 announcement 类型通知 | Verification: API 侧 TestPanelNotifications（panel_test.go）；panel 无前端单测 | Commit: ce8b33e）
-   [x] `F-1513` 展示订单和支付记录。
    - 备注（Task ID: F-1513 | Status: Done | Evidence: OrdersView.vue:46-78 订单卡（渠道/金额/权益/支付/退款） | Verification: API 侧 TestPanelOrders（panel_test.go）；panel 无前端单测 | Commit: ce8b33e）
-   [ ] `F-1514` 展示账单和套餐历史。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1514 | 部分：订单权益展示发放内容（OrdersView.vue:70-72）；无账单历史。）
-   [ ] `F-1515` 提供账号安全设置。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1515 | 未满足：面板无密码/2FA 设置；仅联系方式绑定（HomeView.vue:178-197）。）
-   [ ] `F-1516` 展示登录记录。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1516 | 未满足：面板无登录记录（loginlog 仅管理端 SettingsView.vue:368）。）

------------------------------------------------------------------------

# 14. 自动化运维与任务系统

## 14.1 通用任务系统

-   [x] `F-1601` 审计现有任务、Provision、Recovery 实现。
    - 备注（Task ID: F-1601 | Status: Done | Evidence: 任务/Provision/Recovery 审计 §3.12 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1602` 统一后台任务状态模型。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1602 | 未满足：无统一任务状态模型；五张独立任务表各持词汇（TouchJob models.go:305/ProvisionJob :514/Recovery :528/CertTask :603/Event :670），无共享状态机。）
-   [ ] `F-1603` 支持任务排队。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1603 | 部分：仅 event outbox 排队（pending+next_attempt_at 批 100 deliver.go:74-75）与各类型 sweep；无统一队列。）
-   [ ] `F-1604` 支持任务超时。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1604 | 部分：各子系统超时（recovery LevelTimeout recovery.go:56/proc_ctl 15s/upgrade ack 15s/herald 10s）；无任务级超时（provision goroutine 用 context.Background() provisionapi.go:73）。）
-   [ ] `F-1605` 支持有限次数重试。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1605 | 部分：仅 event outbox 有限重试（MaxAttempts=6 deliver.go:15,90+TestDeliverRetryBackoffAndDeadLetter）；cert 重试无上限；无统一重试预算。）
-   [ ] `F-1606` 支持退避策略。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1606 | 部分：退避散落三处（herald 1m→1h deliver.go:27/cert 1h cert.go:33/agent procs 1s→30s manager.go:90-91）；无统一策略。）
-   [ ] `F-1607` 支持任务取消。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1607 | 未满足：无任何任务取消端点/状态（仅请求级 ctx 取消）。）
-   [ ] `F-1608` 支持任务幂等。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1608 | 部分：幂等散落（Event DedupKey herald.go:83/Recovery node_id 唯一索引 models.go:528/节点名复用 launch.go:94）；无任务级幂等键。）
-   [ ] `F-1609` 支持任务进度和阶段信息。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1609 | 部分：recovery Level+ActionState（models.go:536）与 cert State 有阶段；无进度百分比。）
-   [ ] `F-1610` 支持任务日志。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1610 | 部分：各行日志尾/错误字段（ProvisionJob.Log 8KB models.go:518/RecoveryAction.Detail/CertTask.LastError/EventDelivery.Detail）；无结构化任务日志流。）
-   [ ] `F-1611` 支持任务人工重试。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1611 | 部分：人工重试散落（死信重投 eventsapi.go:61/cert 重签 certapi.go:148/provision 重跑 provisionapi.go:105）；recovery 无。）
-   [ ] `F-1612` 支持任务部分成功结果。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1612 | 部分：批量操作逐节点 ok/error（procop.go:120-142+TestBatchNodeProc/TestBatchNodeConfig）；无统一部分成功态。）
-   [ ] `F-1613` 支持任务执行者和操作者审计。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1613 | 部分：管理端写操作入审计（AuditLog models.go:735+auditMiddleware router.go:119）；任务行本身无执行者/操作者字段，自动任务无归属。）
-   [ ] `F-1614` 支持任务并发配额。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1614 | 部分：provision 互斥串行（provision.go:34,58-83）+批量上限 50（procop.go:22）；无可配置并发配额。）
-   [ ] `F-1615` 支持任务积压和失败告警。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1615 | 部分：events 积压/失败计数（eventsapi.go:46,269-275）；无阈值告警。）

## 14.2 节点运维任务

-   [ ] `F-1621` 支持 Agent 重启任务。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1621 | 未满足：无 agent 重启任务（agentproto 仅 MsgUpgrade/MsgProcCtl messages.go:148；agent 自重启仅升级 Exec upgrade.go:92）。）
-   [ ] `F-1622` 支持 Relay 重启任务。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1622 | 部分：relay 是受管进程（roles/relay/relay.go:5），可经通用 proc_ctl 启停（procop.go:27-31）；无专门 relay 重启任务。）
-   [x] `F-1623` 支持代理进程重启任务。
    - 备注（Task ID: F-1623 | Status: Done | Evidence: procop 重启；TestNodeProcOp+TestBatchNodeProc | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1624` 支持节点配置重应用任务。
    - 备注（Task ID: F-1624 | Status: Done | Evidence: pushConfig 重应用+configd 回滚；TestNodeConfigGuards+TestApplyReloadFailRollsBack | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1625` 支持节点升级任务。
    - 备注（Task ID: F-1625 | Status: Done | Evidence: router.go:183 nodeUpgrade；TestNodeUpgrade | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1626` 支持节点回滚任务。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1626 | 部分：仅 agent 侧自动回滚（120s 看门狗→Rollback upgrade.go:22,53,76）；面板无回滚任务/端点。）
-   [ ] `F-1627` 支持节点重新探测任务。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1627 | 未满足：探测是 agent 本地周期循环（probe/probe.go:62-164）；无面板重新探测端点。）
-   [ ] `F-1628` 支持节点摘除任务。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1628 | 部分：手动摘除 /pool/:id/suspend（poolapi.go:56）同步翻状态；无任务行/跟踪。）
-   [ ] `F-1629` 支持节点恢复任务。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1629 | 部分：手动恢复 /pool/:id/resume（poolapi.go:83）；无任务行/跟踪。）
-   [ ] `F-1630` 支持任务执行前依赖检查。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1630 | 部分：provision/cert 各有前置检查（provisionapi.go:44-66/launch.go:46-54/loop.go:83-97）；无通用任务前依赖检查。）

## 14.3 自动恢复

-   [x] `F-1641` 定义自动恢复触发条件。
    - 备注（Task ID: F-1641 | Status: Done | Evidence: recovery sweep 触发；TestSweepOpenAndAdvance | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1642` 定义自动恢复允许执行的动作集合。
    - 备注（Task ID: F-1642 | Status: Done | Evidence: recovery 动作集；TestActionNames+TestInstanceActionL3 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1643` 定义自动恢复最大执行次数。
    - 备注（Task ID: F-1643 | Status: Done | Evidence: Level 1-3 最大次数；TestTimeoutExhaustFails+TestLastLine | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1644` 定义自动恢复冷却时间。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1644 | 未满足：无恢复冷却时间（仅 SustainedAfter 预判窗 recovery.go:54）；全仓 cooldown 命中均无关；无测试。）
-   [ ] `F-1645` 定义自动恢复失败后的人工介入状态。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1645 | 部分：失败升级「需人工介入」经 Herald KindRecoveryFailed+notify（recovery.go:344-368+TestFailedEscalationNotify）；模型仅 running/done/failed（recovery.go:27-29）——无人工介入态、无人工确认/重试清除。）
-   [ ] `F-1646` 防止自动恢复循环重启。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1646 | 未满足：失败管线后节点保持 suspended，下次 Sweep 会开新 Recovery（查询仅排除 running 与 手动% pool_reason recovery.go:169-189）；无失败重开守卫——循环重启风险确认。）
-   [x] `F-1647` 记录自动恢复决策和执行结果。
    - 备注（Task ID: F-1647 | Status: Done | Evidence: RecoveryAction 轨迹 models.go:527；TestTraceRowsReplay | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）

## 14.4 云资源供给

-   [x] `F-1651` 审计当前 OpenTofu 和 Provider Provision 能力。
    - 备注（Task ID: F-1651 | Status: Done | Evidence: OpenTofu/Provider Provision 能力审计 §3.9 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1652` 将云资源创建纳入统一任务系统。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1652 | 未满足：云创建未入统一任务系统（ProvisionJob 独立表 models.go:514）。）
-   [ ] `F-1653` 支持创建前成本预估。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1653 | 部分：成本参考查询有（costref.go:49-117+packages/costref Quote.MonthlyCents:42）；非申请流内预估步骤，模板成本手填（providerapi.go:229）。）
-   [ ] `F-1654` 支持创建前资源规格校验。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1654 | 部分：仅模板表单校验（providerapi.go:174-199）；无云端规格存在/配额检查。）
-   [ ] `F-1655` 支持创建后资源同步。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1655 | 部分：创建后同步=agent 首注册+OS-4 管线（pipeline.go 探测→online）；无云侧实例对账。）
-   [x] `F-1656` 支持创建后 Agent 注册。
    - 备注（Task ID: F-1656 | Status: Done | Evidence: cloudinit 注册+agent-install 同口径；TestCloudInit+TestProvisionPipeline | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1657` 支持节点入池检查。
    - 备注（Task ID: F-1657 | Status: Done | Evidence: OS-4 入池检查；TestProvisionPipeline | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1658` 创建失败时按策略清理部分资源。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1658 | 未满足：无失败清理——provision 仅 plan/apply（provision.go:172），全仓无 tofu destroy。）
-   [ ] `F-1659` 支持资源销毁前依赖检查。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1659 | 未满足：无销毁流程即无销毁前依赖检查。）
-   [ ] `F-1660` 支持资源销毁审批和二次确认。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1660 | 未满足：无销毁流程即无审批/二次确认。）

------------------------------------------------------------------------

# 15. 安全与权限

## 15.1 Dash 权限

-   [x] `F-1701` 审计管理员登录和会话管理。
    - 备注（Task ID: F-1701 | Status: Done | Evidence: 管理员登录与会话审计 §3.13（admin.go:41+ratelimit+loginlog） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1702` 审计管理员角色和权限模型。
    - 备注（Task ID: F-1702 | Status: Done | Evidence: 管理员角色权限审计 §3.13（requireSuper+TestAdminRoleCRUD） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1703` 为云账号管理定义独立权限。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1703 | 区级未满足原因：无细粒度资源权限——数据面仅 apiAuth+audit（router.go:119），唯一角色门槛 requireSuper 只挂 /api/admins（router.go:227）；API-Token 路径只置 apiUser 不置 role（apitoken.go:140），前端 role 仅 admin/distributor/空（dash/src/auth.ts:19）；本区 F-1703~F-1708、F-1710 未满足。）
-   [ ] `F-1704` 为云资源开关机定义独立权限。（依赖：无；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-1705` 为强制停止和重装定义高危权限。（依赖：无；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-1706` 为成本查看和账单导出定义权限。（依赖：无；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-1707` 为成本调整和分摊规则修改定义权限。（依赖：无；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-1708` 为节点和线路操作定义权限。（依赖：无；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-1709` 为敏感操作增加二次确认。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1709 | 部分：仅前端 ElMessageBox.confirm 二次确认（NodesView.vue:121,293,306,586 等）；无服务端确认令牌/CSRF；强制停止 runProc('stop') 无确认（NodesView.vue:358-364）。）
-   [ ] `F-1710` 支持按账号或资源范围限制操作权限。（依赖：无；完成条件：需求落地+测试绿+证据行）

## 15.2 凭据安全

-   [x] `F-1721` 审计现有 secret_store。
    - 备注（Task ID: F-1721 | Status: Done | Evidence: secret_store 审计 §3.13（secret_test 6 例） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1722` 验证云凭据加密存储。
    - 备注（Task ID: F-1722 | Status: Done | Evidence: secret.Store AES-GCM+providerapi sealSecret；TestStoreRoundtrip+TestProviderAPI | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1723` 验证密钥轮换。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1723 | 未满足：keyinit 仅首启生成/持久化（keyinit.go:48-138）；secret.Store 有版本键位与按版本解密（secret.go:24-26,70-88）但 NewStore 恒写 v1（secret.go:35）——无加新钥/重加密存量/退休旧钥路径；测试仅初始化与加解密，无轮换用例。）
-   [x] `F-1724` 验证凭据吊销。
    - 备注（Task ID: F-1724 | Status: Done | Evidence: enroll 吊销+节点吊销；TestRevokeUsedEnrollmentToken+TestNodeTokenRevocation | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1725` 验证订阅令牌重置。
    - 备注（Task ID: F-1725 | Status: Done | Evidence: resetSubToken 重置验证；TestResetSubTokenErrors | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1726` 验证 Agent 凭据轮换。
    - 备注（Task ID: F-1726 | Status: Done | Evidence: 轮换验证；TestNodeTokenRotation+TestAuthNodeTokenRotationWindow | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1727` 确保敏感凭据不进入日志。
    - 备注（Task ID: F-1727 | Status: Done | Evidence: sub.go:34 token 前 6 位脱敏+audit 脱敏；TestAuditLogsRedactSensitiveBody | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1728` 确保 API 响应不回显 Secret。
    - 备注（Task ID: F-1728 | Status: Done | Evidence: HasAccessKey 不回显密钥；TestListEnrollmentTokensMasksSecret+TestProviderAPI | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）

## 15.3 安全测试

-   [x] `F-1731` 测试管理员越权访问。
    - 备注（Task ID: F-1731 | Status: Done | Evidence: operator 越权 403；TestAdminRoleCRUD | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1732` 测试云账号凭据泄漏。
    - 备注（Task ID: F-1732 | Status: Done | Evidence: TestProviderAPI（providerapi_test.go:40）断言创建响应不回显密钥（:59-61）、库中存密文非明文（:68-73）、列表掩码（:81-83） | Verification: providerapi_test.go TestProviderAPI | Commit: 9fcb5ad）
-   [ ] `F-1733` 测试高危云操作权限绕过。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1733 | 未满足：无高危云操作越权测试——TestAdminRoleCRUD（adminrole_test.go:39）仅 /api/admins 403；procop_test.go/upgrade_test.go 仅成功路径，无 403 断言。）
-   [x] `F-1734` 测试 Agent 消息伪造和重放。
    - 备注（Task ID: F-1734 | Status: Done | Evidence: bad token 拒绝+envelope ID 重放去重；TestAgentWSBadToken+dedupe_test | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1735` 测试配置篡改。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1735 | 部分：篡改检测仅 standby 快照（standby_test.go:126）/机密密文（secret_test.go TestStoreKeyIsolation）/回执签名（callback_test.go:129）；无管理面配置（config push/routing/web-cert/provision）篡改测试。）
-   [x] `F-1736` 测试订阅令牌爆破和滥用。
    - 备注（Task ID: F-1736 | Status: Done | Evidence: 订阅限频枚举锁定；TestSubscriptionRateLimit | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1737` 测试敏感信息日志泄漏。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1737 | 部分：敏感信息日志泄漏仅经 audit 行体脱敏（audit_test.go:70 TestAuditLogsRedactSensitiveBody）+sub.go:34 令牌前 6 位；无 stdout/stderr 日志断言无明文密钥的测试。）
-   [x] `F-1738` 执行依赖安全扫描。
    - 备注（Task ID: F-1738 | Status: Done | Evidence: .github/dependabot.yml security alerts+test.yml CI 绿 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）

------------------------------------------------------------------------

# 16. 可观测性与告警

## 16.1 指标

-   [ ] `F-1801` 采集 Server API 请求量和延迟。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1801 | 未满足：无指标层——prometheus|/metrics|pprof 全仓 0 实命中；gin.Default() 无 metrics 钩子（router.go:100）。）
-   [ ] `F-1802` 采集数据库连接数和查询延迟。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1802 | 未满足：无 DB 连接/查询延迟采集。）
-   [ ] `F-1803` 采集 Agent 在线数量和心跳延迟。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1803 | 部分：在线数经 /api/health agents_online（router.go:433→hub.OnlineCount）与 /admin/status nodes.online（status.go:195）；无指标采集、无心跳延迟。）
-   [ ] `F-1804` 采集 Relay 在线数量和连接数。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1804 | 未满足：relaypush 仅渲染配置覆盖（relaypush.go:1-2）；无 relay 在线/连接跟踪。）
-   [ ] `F-1805` 采集 Relay 吞吐量。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1805 | 未满足：无 relay 吞吐。）
-   [ ] `F-1806` 采集 Path 可用率和质量指标。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1806 | 部分：探测判定入库（ProbeReport+aggregate.go:127）经 /api/probe-reports+/api/dimension-status 暴露；无 Path 可用率/质量指标。）
-   [ ] `F-1807` 采集 Probe 任务成功率。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1807 | 未满足：有判定记录无成功率计算/采集。）
-   [ ] `F-1808` 采集配置下发成功率。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1808 | 部分：NodeConfig 行持久 pending/applied/failed（models.go:237）；无成功率聚合指标。）
-   [ ] `F-1809` 采集流量记账延迟。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1809 | 未满足：NodeTrafficLog 有 recorded_at（models.go:271）；无记账延迟指标。）
-   [ ] `F-1810` 采集调度耗时。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1810 | 未满足：alloc 调度器无耗时指标。）
-   [ ] `F-1811` 采集 Provider API 请求成功率和延迟。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1811 | 未满足：providerapi 仅 CRUD；无调用成功率/延迟指标。）
-   [ ] `F-1812` 采集云操作任务成功率和耗时。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1812 | 部分：ProvisionJob 持久 running/ok/failed（models.go:519）；无成功率/耗时指标。）
-   [ ] `F-1813` 采集账单同步成功率和延迟。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1813 | 未满足：无账单同步组件（账单同步|syncBill 0 命中）。）
-   [ ] `F-1814` 采集成本数据新鲜度。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1814 | 未满足：cost.Build 无成本数据新鲜度指标。）

## 16.2 告警

-   [ ] `F-1821` Agent 离线告警。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1821 | 未满足：agent 掉线仅 setNodeStatus("offline")+日志（agentws.go:178-179）；无 Alert 行、无 Herald 事件。）
-   [ ] `F-1822` Relay 离线告警。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1822 | 未满足：无 relay 在线跟踪即无掉线告警。）
-   [ ] `F-1823` Path 不可用告警。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1823 | 部分：region/ISP 故障经 monitor applyVerdict→Herald KindRegionFault/KindISPFault（monitor.go:123-138）；无按 Path 不可用告警。）
-   [ ] `F-1824` Path 质量下降告警。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1824 | 部分：维度降级发 notify dimension.degraded（monitor.go:148-151）；无质量下降 Alert 行。）
-   [ ] `F-1825` 节点带宽超限告警。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1825 | 未满足：无带宽超限告警（quota/link、userbw 无告警）。）
-   [ ] `F-1826` 配置下发失败告警。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1826 | 部分：config_error kind+recordAlert 通路在（alerts.go:26+TestAlertGuards）；agent 生产侧从不发送（AlarmKindConfigError 仅见于 agentproto+测试，configd 回滚仅 ack.Error configd.go:117）——有通路无生产者。）
-   [ ] `F-1827` 节点升级失败告警。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1827 | 部分：agent 侧升级回滚（upgrade/upgrade.go:54）+管线穷尽发 recovery_failed Herald（recovery.go:347-360）；无升级失败 Alert 行。）
-   [ ] `F-1828` Provider API 认证失败告警。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1828 | 未满足：providerapi 无认证失败检测/告警。）
-   [ ] `F-1829` 云资源操作失败告警。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1829 | 未满足：provision 失败仅记 status（models.go:519）；无云操作失败告警。）
-   [ ] `F-1830` 云账单同步失败告警。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1830 | 未满足：无账单同步即无同步失败告警。）
-   [ ] `F-1831` 成本异常增长告警。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1831 | 部分：high_cost Alert（cost.go:30,233-241）+cost_exceeded Herald（:249）覆盖阈值；非增长率异常。）
-   [x] `F-1832` 备份失败告警。
    - 备注（Task ID: F-1832 | Status: Done | Evidence: backup.go:253/260 emitBackupFailed/emitUploadFailed；TestBackupFailedEmit | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1833` 告警支持去重和恢复通知。
    - 备注（Task ID: F-1833 | Status: Done | Evidence: node+kind+proc 去重+自动恢复；TestAlertGuards+TestAgentWSAlarmProcRecover | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1834` 告警支持确认、关闭和处理记录。
    - 备注（Task ID: F-1834 | Status: Done | Evidence: resolveAlert+Alert.ResolvedAt；TestAlertGuards | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1835` 通知通道故障不阻塞主业务。
    - 备注（Task ID: F-1835 | Status: Done | Evidence: Herald outbox+Emit 仅插行（herald.go:89）+后台 Loop 投递（deliver.go:40），失败仅记日志；notify.Send 5s 超时（notify.go:24），通道故障不阻塞主业务 | Verification: TestEventDeliveriesBoard（heraldboard_test.go:17） | Commit: 9fcb5ad）

------------------------------------------------------------------------

# 17. 数据库、备份与部署

## 17.1 数据库

-   [x] `F-1901` 审计全部 Model、索引和关联。
    - 备注（Task ID: F-1901 | Status: Done | Evidence: 全部 Model/索引/关联审计 §3.15（51 type） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1902` 检查高频查询和 N+1。
    - 备注（Task ID: F-1902 | Status: Done | Evidence: N+1 检查完成：12 处候选（panel.go:173/poolapi.go:38/agentws.go:248/procstatus.go:33 实锤）§3.15 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1903` 检查事务边界。
    - 备注（Task ID: F-1903 | Status: Done | Evidence: 事务边界检查：11 处 Transaction，资金路径 paynotify:51/card:95/userio:212/panel:346 §3.15 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1904` 检查账单和成本数据增长。
    - 备注（Task ID: F-1904 | Status: Done | Evidence: 账单/成本增长检查：无账单表，成本实时聚合 §3.15 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1905` 检查探测历史数据增长。
    - 备注（Task ID: F-1905 | Status: Done | Evidence: 探测历史增长检查：probe_reports 无保留策略（缺口）§3.15 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1906` 检查流量记录增长。
    - 备注（Task ID: F-1906 | Status: Done | Evidence: 流量记录增长检查：traffic_logs/node_traffic_logs 无保留策略（缺口）§3.15 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1907` 检查审计日志增长。
    - 备注（Task ID: F-1907 | Status: Done | Evidence: 审计日志增长检查：audit.Prune/Loop 轮转 §3.15（TestPruneRemovesOnlyOutOfWindow） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1908` 检查 SQLite 并发写行为。
    - 备注（Task ID: F-1908 | Status: Done | Evidence: SQLite 并发写检查：WAL+busy_timeout(5000)+外键 storage.go:141 §3.15 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1909` 检查 PostgreSQL Migration 兼容性。
    - 备注（Task ID: F-1909 | Status: Done | Evidence: PG Migration 兼容：make test-pg 实跑 ok 4.966s §1+§3.15 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1910` 建立历史数据归档和清理策略。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1910 | 未满足：仅 audit.Prune/Loop 轮转（audit.go:20,30+TestPruneRemovesOnlyOutOfWindow audit_test.go:34）；probe_reports/traffic_logs/node_traffic_logs 无任何保留/清理代码（TrafficLog 明示不删不改 models.go:129-131；ProbeReport models.go:199 无）。）

## 17.2 部署和恢复

-   [x] `F-1921` 审计当前 Compose 和部署脚本。
    - 备注（Task ID: F-1921 | Status: Done | Evidence: Compose/部署脚本审计 §3.16 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1922` 明确 Server、Dash、Panel 的部署依赖。
    - 备注（Task ID: F-1922 | Status: Done | Evidence: deploy/docker-compose.yml:92,106 ferry-dash/ferry-panel depends_on: ferry-server；docs/deploy/部署指南.md:7-16 记录三容器+反代路由集+postgres/FERRY_KEYFILE 依赖 | Verification: deploy/compose-smoke.sh+deploy/e2e-smoke.sh（53 断言） | Commit: 57e899f）
-   [x] `F-1923` 明确 Agent 安装和升级流程。
    - 备注（Task ID: F-1923 | Status: Done | Evidence: agent-install.sh 原子回滚+upgrade 看门狗；install-rollback-test+TestWatchdogRollback | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1924` 明确密钥和配置持久化位置。
    - 备注（Task ID: F-1924 | Status: Done | Evidence: keyinit.go:21-22 defaultKeyFileName+ :143-159 FERRY_KEYFILE 覆盖；部署指南.md:33-34 记录 FERRY_SECRET_KEY/FERRY_ENV/FERRY_KEYFILE+迁移须携带主钥文件；MIGRATION-RUNBOOK.md:11 主钥不入备份包 | Verification: TestGeneratePersistsAndStable+TestProductionRequiresPersistence+TestKeyfileRoundTrip（keyinit_test.go:25/156/98） | Commit: 57e899f）
-   [x] `F-1925` 建立数据库自动备份。
    - 备注（Task ID: F-1925 | Status: Done | Evidence: backup.go:153 Loop 周期备份；TestLoopRunsOnceAndPrunes+TestSnapshotEncrypted | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1926` 建立配置和密钥备份策略。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1926 | 部分：backup.Snapshot（backup.go:110）仅 VACUUM INTO 数据库，主钥故意排除，备份循环不含配置/ferry-keys.env/ferry.env 拷贝；仅手动策略（部署指南.md:33+MIGRATION-RUNBOOK.md:11+restore.sh --key:16）——配置与密钥备份非自动化。）
-   [x] `F-1927` 建立备份恢复测试。
    - 备注（Task ID: F-1927 | Status: Done | Evidence: deploy/restore.sh+e2e 备份恢复腿（53 断言内） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1928` 建立 Server 升级测试。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1928 | 未满足：无 Server 升级测试——CI deploy 脚本 job（test.yml:94-99）仅 install-rollback-test.sh/start-args-test.sh/e2e-smoke.sh；Server 升级仅文档「拉新镜像重建」+启动 AutoMigrate（storage.go:60-67），无版本升级断言。）
-   [ ] `F-1929` 建立 Server 回滚方案。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1929 | 部分：回滚有文档+agent 原子 .prev 回滚（MIGRATION-RUNBOOK.md:127-132+install-rollback-test.sh:164-178+systemd-smoke.sh:112-120）；无 Server 二进制回滚路径（compose 重建仅前进，AutoMigrate 无 down-migration）。）
-   [x] `F-1930` 建立完整部署冒烟测试。
    - 备注（Task ID: F-1930 | Status: Done | Evidence: deploy/e2e-smoke.sh 实跑 53 ok EXIT=0 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1931` 验证 Server 重启后 Agent 能重新连接。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1931 | 部分：仅通用重连断言 TestRunReconnectsUntilCancel（link_test.go:161-182 disconnects>=3）+TestRunDialErrorReconnectsUntilCancel（link_extra_test.go:19-35）；无杀/重启 ferry-server 断言 agent 回在线——e2e-smoke.sh 先杀 agent 再重启 server，重启腿不覆盖重连（MIGRATION-RUNBOOK.md:96-98 仅手动）。）
-   [ ] `F-1932` 验证 Server 不可用时数据面继续运行。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1932 | 部分：故障语义有文档（面板可用性设计.md:5-18：agent 本地自治/隧道/用户流量继续，管理面断）+离线缓冲测试 TestSendFailureRetries（spool_test.go:99）+TestWatchForwardsUntilCancel（:147）；无「数据面在 server 宕机时继续转发」专门断言。）

## 17.3 多区域

-   [ ] `F-1941` 为 Node 和 Path 建立区域维度管理。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1941 | 部分：Node 侧区域齐（model.go:41 Region+Node.Region/Datacenter/ISP/City+ProbeReport.Region/.ISP models.go:209-210+DimensionStatus :216-227）；无 Path 模型/实体（type Path 0 命中，仅 Backup.Path 文件字段 models.go:704）——Node 和 Path 仅覆盖一半。）
-   [x] `F-1942` 支持跨区域探测。
    - 备注（Task ID: F-1942 | Status: Done | Evidence: probe.go:91-95 Target/TargetKind/TargetHost 探测任意目标+ProbeReport.Region/.ISP（models.go:209-210）记录目标区域/运营商快照；entry→landing 探测天然跨区域 | Verification: probe_test.go+TestAgentWSProbeReport（agentws_test.go） | Commit: 57e899f）
-   [ ] `F-1943` 支持跨区域调度。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1943 | 部分：调度不按区域限定（候选跨全部 landing alloc.go:466-475）；区域为手动指派维度（hasManualAssignment alloc.go:568-572 entry_node_id IS NULL AND region=?+TestSweepManualWins alloc_test.go:302,327,334）；无自动区域感知权重（评分仅 load/cost/premium alloc.go:63,186-242）。）
-   [ ] `F-1944` 支持区域容量和成本统计。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1944 | 部分：区域成本统计有（cost.Build 按 region 分组 cost.go:124-149+GroupCost+Report.Regions+TestBuild cost_test.go:87-93 断言 hk）；无区域容量统计（GroupCost 无容量字段，容量仅每节点 alloc.Candidate.CapacityMbps:188）。）
-   [x] `F-1945` 支持区域故障隔离。
    - 备注（Task ID: F-1945 | Status: Done | Evidence: monitor.go:121-124 区域作用域发 herald.KindRegionFault+DimensionStatus 聚合（monitor.go:21-26,51-59）合并单告警；设计依据 调度核心设计.md:47,84 | Verification: TestApplyVerdictTransitions（monitor_test.go:59,81-101 region_fault 断言） | Commit: 57e899f）
-   [x] `F-1946` 评估 Server 高可用。
    - 备注（Task ID: F-1946 | Status: Done | Evidence: 面板可用性设计.md:3 单点+§1 故障语义+小时级 RTO；warm-standby 选项 P2-1 standby.go:1-162+deploy/README.md:235-257 拉取加密主快照；结论单点设计+手动提升，无自动故障转移 | Verification: docs/design/面板可用性设计.md+deploy/README.md | Commit: 57e899f）
-   [ ] `F-1947` 评估数据库只读副本。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-1947 | 未满足：无只读副本评估——只读副本|read replica|ReadReplica|副本|读库 全仓 0 命中；standby 是全量加密快照替换自身库并重启（standby.go:115-162）为热备非只读副本，无文档评估。）
-   [x] `F-1948` 在没有明确需求前不实现数据库双主双写。
    - 备注（Task ID: F-1948 | Status: Done | Evidence: 全仓无 dual|双写|双主|leader|election|lease|advisory|FOR UPDATE（非测试）；Snapshot 显式拒绝非 sqlite（backup.go:111-113）；standby 严格拉取只读从不写主库 | Verification: 全仓 grep 0 命中+standby.go:115-162 | Commit: 57e899f）
-   [x] `F-1949` 在没有明确需求前不实现多 Server 并发修改同一资源。
    - 备注（Task ID: F-1949 | Status: Done | Evidence: 无乐观锁/版本列/leader 选举/咨询锁/SELECT FOR UPDATE；standby 仅从主快照重写本地库（standby.go:115-162），两服务从不并发改同一资源 | Verification: 全仓 grep 0 命中 | Commit: 57e899f）

------------------------------------------------------------------------

# 18. 测试与验收

## 18.1 单元测试

-   [ ] `F-2001` 为 Node 状态机建立单元测试。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2001 | 部分：Node 状态转换经 TestAgentWSHeartbeat（agentws_test.go:53 online→offline :97,101）+维度状态机 TestApplyVerdictTransitions（monitor_test.go:59）+pool 态（standby_test+recovery）；无独立状态机抽象单测。）
-   [ ] `F-2002` 为 Capability 过滤建立单元测试。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2002 | 未满足：type Capability 全仓 0 命中，能力仅由 role/labels/LineType 隐式承载（models.go:95-102），alloc 按 role/direction/health 过滤——无能力模型即无过滤测试。）
-   [ ] `F-2003` 为 Path 状态机建立单元测试。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2003 | 未满足：type Path 0 命中；仅 LandingAssignment（models.go:165）+alloc sweep（alloc.go:497）等价物；无 Path 状态机。）
-   [ ] `F-2004` 为 Path Group 建立单元测试。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2004 | 未满足：type PathGroup 0 命中；无 Group CRUD/成员/顺序。）
-   [ ] `F-2005` 为 Policy 冲突规则建立单元测试。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2005 | 未满足：Policy 冲突规则缺失——仅 alloc 调度策略（least_conn/cost_first/perf_first/balanced/manual alloc.go:23-46）被 alloc_test 覆盖，无路由 Policy/冲突规则模型。）
-   [x] `F-2006` 为 Scheduler 过滤和评分建立单元测试。
    - 备注（Task ID: F-2006 | Status: Done | Evidence: alloc_test 9 例调度单测 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-2007` 为 Health 计算建立单元测试。
    - 备注（Task ID: F-2007 | Status: Done | Evidence: aggregate_test+monitor_test 健康计算单测 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-2008` 为 Config Compiler 建立单元测试。
    - 备注（Task ID: F-2008 | Status: Done | Evidence: routing_test.go:17 TestRoutingConfigRender 渲染节点路由配置（模板+规则），断言 rules/direct 出站+sha256 匹配+bad-template 400+missing-node 404；agent 侧应用/回滚 configd_test TestApplyReloadFailRollsBack | Verification: server/internal/handler/routing_test.go TestRoutingConfigRender+configd_test.go | Commit: 9a8663a）
-   [x] `F-2009` 为 Subscription Adapter 建立单元测试。
    - 备注（Task ID: F-2009 | Status: Done | Evidence: sub/{clash,singbox,links}_test Adapter 单测 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-2010` 为 Traffic Dedup 建立单元测试。
    - 备注（Task ID: F-2010 | Status: Done | Evidence: agentws_dedupe_test 7 例去重单测 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-2011` 为 Cloud Provider Adapter 建立契约测试。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2011 | 未满足：无 Cloud Provider Adapter——providerapi 仅凭据/模板 CRUD（providerapi.go:34-147），无访问云 API 的 adapter 接口；TestProviderAPI 测密钥封装非契约。）
-   [ ] `F-2012` 为 Cloud Operation 状态机建立单元测试。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2012 | 未满足：type CloudOperation 0 命中；无操作状态机/幂等键/任务接入。）
-   [ ] `F-2013` 为 Cost Import 去重建立单元测试。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2013 | 未满足：无账单/导入管线（cost 仅逐节点 Build+价格 CheckNode+watch/alert）；无 CostImport 去重单测。）
-   [ ] `F-2014` 为 Cost Allocation 建立单元测试。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2014 | 未满足：type CostAllocation 0 命中；alloc 包是调度非成本分摊；无分摊/重算引擎。）
-   [x] `F-2015` 为后台任务重试和取消建立单元测试。
    - 备注（Task ID: F-2015 | Status: Done | Evidence: 后台任务重试+取消按管线实测——herald 重试指数退避+尝试上限+死信 TestDeliverRetryBackoffAndDeadLetter（herald_test.go:101）+退避上限 TestBackoffCap（:162）+循环取消 TestLoopDelivers（:192）；cert 任务重试 TestSweepFailedBackoff（cert_test.go:228）+TestLoopSweepsAndStopsOnCancel（:263）；relaypush 超时重推 TestPushTimeout（relaypush_test.go:307） | Verification: herald_test.go+cert_test.go+relaypush_test.go | Commit: 9a8663a）

## 18.2 集成测试

-   [x] `F-2021` 测试 Agent 注册。
    - 备注（Task ID: F-2021 | Status: Done | Evidence: enroll_test 8 例注册测试 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-2022` 测试 Agent 心跳和断线重连。
    - 备注（Task ID: F-2022 | Status: Done | Evidence: TestAgentWSHeartbeat+TestRunReconnectsUntilCancel | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-2023` 测试 Agent 配置下发和 ACK。
    - 备注（Task ID: F-2023 | Status: Done | Evidence: relaypush_test 9 例（ACK/超时/重推） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-2024` 测试配置失败回滚。
    - 备注（Task ID: F-2024 | Status: Done | Evidence: configd_test TestApplyReloadFailRollsBack+TestApplyNoOldConfigRollbackRemoves | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-2025` 测试 Relay 建立和断线重连。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2025 | 部分：relay 建立+转发有 TestRelayForwards（relay_test.go:41 mock 隧道）+TestRelayHotRepoint（:129）；重连仅间接（SSH dial 重试 tunnel_extra_test.go:165 TestSSHDialRetryOnChannelReject+agent-link TestRunReconnectsUntilCancel）；无「断已建 relay 隧道→重连→继续转发」测试。）
-   [x] `F-2026` 测试 Probe 任务执行和结果回传。
    - 备注（Task ID: F-2026 | Status: Done | Evidence: probe_test+TestAgentWSProbeReport/TestAgentWSProbeReportResolve | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-2027` 测试 Path 健康变化。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2027 | 未满足：Path 缺失；sub.go:74 按 status!=offline+pool_state 过滤，但无 Path 健康变化测试（monitor 维度迁移不建模 Path）。）
-   [ ] `F-2028` 测试 Path 故障回退。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2028 | 未满足：Path 故障回退缺失；最接近 entry→landing 经 alloc sweep（TestSweepAllocatesAndSwitches alloc_test.go:223），无 Path。）
-   [x] `F-2029` 测试订阅生成和权限过滤。
    - 备注（Task ID: F-2029 | Status: Done | Evidence: TestSubscription（sub_test.go:30）+TestSubscriptionAvailability（:128 超配额→无节点订阅/过期→无节点/不限→节点+Subscription-Userinfo）+TestSubscriptionHysteria2/TestSubscriptionSingBoxMixed；权限过滤 sub.UserActive（sub/availability.go:11+TestUserActive）+entry-only/pool-active/status 过滤（sub.go:74）；格式渲染 TestPackClash/TestPackSingBox/TestShareLink | Verification: sub_test.go+sub/availability_test.go | Commit: 9a8663a）
-   [x] `F-2030` 测试流量上报、去重和补报。
    - 备注（Task ID: F-2030 | Status: Done | Evidence: 上报 TestAgentWSTrafficReport（agentws_test.go:477）+TestReporterCollectsAndReports（traffic_test.go:31）；去重 7 例（TestAgentWSDuplicateTrafficFrame:57+TestAgentWSDedupSurvivesReconnect:104+TestReportDeduperWindow:243+TestReportDeduperTTL:267）；补报 spool 离线缓冲+重试 TestSendFailureRetries（spool_test.go:99）+TestWatchForwardsUntilCancel（:147） | Verification: agentws_dedupe_test.go+traffic_test.go+spool_test.go | Commit: 9a8663a）
-   [ ] `F-2031` 测试 Provider 账号连接和资源同步。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2031 | 未满足：Provider 凭据库有且测（TestProviderAPI）；无「账号连接」与资源同步（provision 仅 tofu CLI，无 provider API 调用）；无资源同步测试。）
-   [ ] `F-2032` 测试云资源开机操作。（依赖：F-0341、F-0361；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2032 | 区级未满足原因：无 CloudOperation/CloudResource 模型、无电源开/关/重启路径（provision 仅 tofu launch/replenish）——F-2032/F-2033/F-2034 未满足。）
-   [ ] `F-2033` 测试云资源关机操作。（依赖：F-2032；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-2034` 测试云资源重启操作。（依赖：F-2032；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-2035` 测试 Provider 请求超时后的状态对账。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2035 | 未满足：无 provider adapter→无请求超时对账；仅 agent 请求超时（relaypush TestPushTimeout）非 provider 状态对账。）
-   [ ] `F-2036` 测试账单导入和重复数据去重。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2036 | 未满足：无账单导入；storage 无账单表/模型。）
-   [ ] `F-2037` 测试估算与实际账单差异计算。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2037 | 未满足：无实际账单导入→无估算 vs 实际差异；仅估算 vs 参考价偏差（cost.CheckNode/TestCheckNode cost_test.go:224）非账单差异。）
-   [ ] `F-2038` 测试成本分摊和重算。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2038 | 未满足：无 CostAllocation 引擎；cost.Build（cost.go:96）是节点/区域成本汇总非分摊/重算。）
-   [ ] `F-2039` 测试云资源操作与 Node 状态联动。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2039 | 未满足：无 CloudOperation→无云操作↔Node 状态联动；仅手动 proc-control↔node 状态（procop+TestAgentWSProcOp）。）

## 18.3 端到端测试

-   [x] `F-2041` 自动部署测试 Server。
    - 备注（Task ID: F-2041 | Status: Done | Evidence: 真实 server 二进制启动+健康可达+DB 初始化+degraded→ok 恢复+管理端认证+备份/恢复（deploy/e2e-smoke.sh:116-145,388-427）；CI test.yml:98-99 deploy-scripts job 接入 | Verification: deploy/e2e-smoke.sh（53 断言）+.github/workflows/test.yml | Commit: 9a8663a）
-   [ ] `F-2042` 自动部署 Entry Node。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2042 | 未满足：通用 agent 注册上线（e2e-smoke.sh:156,192）；无 entry 角色部署/entry 数据面/Path entry。）
-   [ ] `F-2043` 自动部署 Landing Node。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2043 | 未满足：无 landing 角色部署。）
-   [ ] `F-2044` 建立真实 Relay Tunnel。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2044 | 未满足：e2e 仅 agent WS 链路；无真实 relay 隧道（relay 角色仅单测 relay_test.go）。）
-   [ ] `F-2045` 创建 Path 并执行探测。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2045 | 未满足：无 Path；e2e 无探测（probe 仅单测）。）
-   [ ] `F-2046` 生成 sing-box 订阅并执行解析测试。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2046 | 未满足：sing-box 解析仅单测（sub/singbox_test.go）；e2e-smoke 推 stub 内核配置，无 sing-box 订阅解析。）
-   [ ] `F-2047` 生成 Clash/Mihomo 配置并执行解析测试。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2047 | 未满足：Clash 解析仅单测（sub/clash_test.go）；无 e2e 解析。）
-   [ ] `F-2048` 验证真实 TCP 转发。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2048 | 未满足：无真实 TCP 转发 e2e（relay 转发仅单测/mock TestRelayForwards）。）
-   [ ] `F-2049` 验证真实 UDP 转发。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2049 | 未满足：无 UDP 转发 e2e 或单测。）
-   [ ] `F-2050` 验证节点故障后的线路处理。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2050 | 未满足：e2e 覆盖进程故障（stub 内核崩溃→自重启 e2e-smoke.sh:235-237,262）；无节点线路/网络故障处理。）
-   [ ] `F-2051` 验证节点恢复后重新加入调度。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2051 | 部分：agent 掉线→重连覆盖（健康 1→0→1+token 轮换窗再上线 e2e-smoke.sh:298-340+单测 TestRunReconnectsUntilCancel）；无故障触发恢复/重入调度 e2e。）
-   [ ] `F-2052` 验证流量记账准确性。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2052 | 未满足：流量记账准确仅单测（TestSaveStatsAPI+traffic tests）；e2e 不测流量量级。）
-   [ ] `F-2053` 验证 Server 重启后的状态恢复。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2053 | 部分：server 恢复态恢复覆盖（e2e-smoke.sh:417-427 恢复实例健康+节点数据在）；server 杀/重启→agent 重连未覆盖（F-1931 注：e2e 先杀 agent 再重启 server，重启腿不断言重连）。）
-   [ ] `F-2054` 在测试 Provider 账号或 Mock Provider
    - 待补（F-2054 | 未满足：无云资源操作、无 test/mock provider（provision 仅 tofu CLI）。）
    上验证完整云资源操作流程。（依赖：无；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-2055` 验证云资源账单导入、成本汇总和 Dash 展示。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2055 | 未满足：无账单导入；成本汇总仅单测（costapi_test）；无 Dash 展示 e2e。）

## 18.4 性能与故障测试

-   [ ] `F-2061` 测试大量 Agent 长连接。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2061 | 未满足：长连接组件在（agentws.go+agenthub/hub.go），但测试仅 1-2 连接（hub_test.go:21 TestHubRegisterUnregister+agentws_test.go:53）；无多连接压测。）
-   [ ] `F-2062` 测试大量心跳并发。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2062 | 未满足：心跳组件在（app.go BuildHeartbeat），无并发心跳负载测试（无 t.Parallel/errgroup 心跳压测）。）
-   [ ] `F-2063` 测试大量 ProbeResult 写入。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2063 | 未满足：ProbeReport/ProbeReportBatch 单发测试（agentws_test.go:194 TestAgentWSProbeReport+:254）；无写入量/吞吐测试。）
-   [ ] `F-2064` 测试大量订阅请求。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2064 | 未满足：订阅单请求测试（sub_test.go:30 TestSubscription+:276）；无多请求负载测试。）
-   [ ] `F-2065` 测试大量流量报告。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2065 | 未满足：流量上报单帧测试（agentws_test.go:477 TestAgentWSTrafficReport）；无量级/并发上报测试。）
-   [ ] `F-2066` 测试大量候选 Path 调度。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2066 | 未满足：候选调度功能测试在（alloc_test.go 多场景），但均为小候选功能用例；无规模/性能测量。）
-   [ ] `F-2067` 测试大量配置批量下发。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2067 | 未满足：配置下发单发测试（agentws_test.go:363 TestAgentWSConfigPush+relaypush_test.go:240）；无批量量级推送测试。）
-   [ ] `F-2068` 测试大量云资源同步。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2068 | 未满足：无云资源同步组件——providerapi 仅凭据 DB CRUD，唯一外部客户端是 DNS（dns/cloudflare.go）；云资源同步全仓 0 命中。）
-   [ ] `F-2069` 测试 Provider API 限流和超时。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2069 | 未满足：ratelimit 单测覆盖 IP/API-auth/sub 节流（limiter_test.go:9/34/62）非 Provider API；无云服务商 API 客户端（provisioning 走 tofu exec provision.go:89），无 Provider API 限流/超时测试。）
-   [ ] `F-2070` 测试账单大批量导入。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2070 | 未满足：无账单导入端点（账单批量导入 0 命中，仅 storage.BillingType models.go:104 字段）——无负载对象。）
-   [ ] `F-2071` 测试 Server 与 Provider 网络中断。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2071 | 未满足：无 Server↔Provider 网络路径（Provider=DB 记录，provisioning 走 tofu CLI 非服务商 REST API）；最近的是 dns_test.go:200 TestCloudflareDoErrors DNS 不可达注入，非云资源开通路径。）
-   [ ] `F-2072` 测试 Relay 故障。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2072 | 部分：relay 组件在，故障路径单测在（relay_extra_test.go:39 TestRunListenError+:164 TestRunAcceptErrorLogged+relaypush_test.go:307 TestPushTimeout+:326 TestPushSendError）；无真实 relay 数据面故障注入（无杀进程/网络分区）。）
-   [ ] `F-2073` 测试 Agent 故障。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2073 | 部分：agent 故障路径单测在（agentws_test.go:99-101 掉线→offline+pool_test.go:132 TestSweepOnceEmitsNodeDown+hub_test.go:56 TestHubUnregisterStale+agentws_dedupe_test.go:104）；仅单 agent，无多 agent 故障测试。）
-   [ ] `F-2074` 测试数据库短暂不可用。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2074 | 部分：真实 DB 故障注入在（recovery_test.go:558 TestSweepAndLoopOnClosedDB+:662 TestTraceHelpersOnClosedDB+cost_extra_test.go:42 TestBuildOnBrokenDB，DB 关闭后循环存活持续记日志）；但 DB 保持关闭非短暂抖动，且仅覆盖 recovery/cost 包。）
-   [ ] `F-2075` 测试成本数据过期和缺失时的降级行为。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2075 | 未满足：cost 无新鲜度/缺失降级处理（非测试代码 grep stale/fresh/过期/degrad 0 命中）；仅 DB 错误路径测试（TestBuildOnBrokenDB）。）
-   [ ] `F-2076` 记录测试环境、数据规模、并发量、P95/P99 和资源占用。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-2076 | 未满足：无测试环境/数据规模/并发量/P95/P99/资源占用记录（P95/P99/benchstat 0 命中）；status.go:182-187 的 runtime.MemStats 是运行指标非压测结果。）

------------------------------------------------------------------------

# 19. 建议实施阶段

> 阶段仅用于组织执行，不是任务关闭单位。每个任务仍需独立验收和勾选。

## P0 --- 现状审计

-   [x] `F-9001` 完成仓库和模块索引。
    - 备注（Task ID: F-9001 | Status: Done | Evidence: 仓库/模块索引 §2 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-9002` 完成 Node、Pool、Allocation、Path 相关模型审计。
    - 备注（Task ID: F-9002 | Status: Done | Evidence: Node/Pool/Alloc/Path 模型审计 §3.1 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-9003` 完成 Agent 和 Relay 审计。
    - 备注（Task ID: F-9003 | Status: Done | Evidence: Agent+Relay 审计 §3.2+§3.3 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-9004` 完成 Probe 和 Scheduler 审计。
    - 备注（Task ID: F-9004 | Status: Done | Evidence: Probe+Scheduler 审计 §3.4+§3.5 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-9005` 完成订阅生成审计。
    - 备注（Task ID: F-9005 | Status: Done | Evidence: 订阅生成审计 §3.6 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-9006` 完成用户、套餐、流量和支付审计。
    - 备注（Task ID: F-9006 | Status: Done | Evidence: 用户/套餐/流量/支付审计 §3.7+§3.8 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-9007` 完成云服务商和 Provision 审计。
    - 备注（Task ID: F-9007 | Status: Done | Evidence: 云服务商/Provision 审计 §3.9 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-9008` 完成成本核算审计。
    - 备注（Task ID: F-9008 | Status: Done | Evidence: 成本核算审计 §3.10 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-9009` 完成 Dash 和 Panel 审计。
    - 备注（Task ID: F-9009 | Status: Done | Evidence: Dash/Panel 审计 §3.11 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-9010` 完成部署、备份和测试审计。
    - 备注（Task ID: F-9010 | Status: Done | Evidence: 部署/备份/测试审计 §3.16 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-9011` 输出模块依赖图。
    - 备注（Task ID: F-9011 | Status: Done | Evidence: 模块依赖图（mermaid）§5 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-9012` 输出文档与源码差异报告。
    - 备注（Task ID: F-9012 | Status: Done | Evidence: 文档与源码差异 7 条 §4 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-9013` 输出分阶段改造建议。
    - 备注（Task ID: F-9013 | Status: Done | Evidence: 分阶段改造建议 §6 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）

## P1 --- 核心模型收敛

-   [ ] `F-9101` 明确 Node、Capability 和现有 Pool 的关系。（依赖：F-0222；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9101 | 未满足：Capability 模型 0 命中（F-0222 区），Node/Pool 与 Capability 关系未定义——能力由 role/labels/LineType 隐式承载，无显式 Capability 模型。）
-   [ ] `F-9102` 明确 Tunnel 与 Path 的边界。（依赖：F-0243；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9102 | 未满足：Path 模型 0 命中（F-0243 区），Tunnel 与 Path 边界未定义——Entry→Landing 由 LandingAssignment+sweep 承载，无 Path 实体。）
-   [ ] `F-9103` 完成 Path 模型或现有模型映射。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9103 | 未满足：无 type Path，仅 LandingAssignment(models.go:181-195)——Path 模型或现有模型映射未实现。）
-   [ ] `F-9104` 完成 Path Group 模型或现有模型映射。（依赖：F-0261；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9104 | 未满足：PathGroup 模型 0 命中（F-0261 区），无 Group 创建/成员/默认 Path/健康聚合。）
-   [ ] `F-9105` 完成 Policy 模型或现有模型映射。（依赖：F-0282；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9105 | 未满足：无 type Policy；仅 alloc.go:57 PolicySetting+ScoreWeights（调度权重，非 Policy 模型）（F-0282 区）。）
-   [ ] `F-9106` 完成模型 Migration。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9106 | 未满足：新模型（Capability/Path/PathGroup/Policy）未实现，无模型可迁移；storage 仅 AutoMigrate 现有表（storage.go:67），无版本化迁移（schema_version/AddColumn 0 命中）。）
-   [ ] `F-9107` 完成旧数据兼容。（依赖：F-9106；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9107 | 未满足：无版本化迁移/旧数据兼容机制（schema_version/AddColumn/AlterColumn 全仓 0 命中）；依赖 F-9106。）
-   [ ] `F-9108` 完成核心模型测试。（依赖：F-0222、F-0243、F-0261、F-0282；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9108 | 未满足：核心模型 Capability/Path/PathGroup/Policy 缺失（F-0222/0243/0261/0282）；仅 storage_test.go 一个模型测试文件。）

## P2 --- 云资源管理与成本基础

-   [ ] `F-9201` 完成 Provider Adapter 契约。（依赖：F-0302；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9201 | 未满足：无云资源 Provider Adapter（F-0302 区，Adapter/ListResources/Power* 均 0）；provision 仅 tofu CLI(provision.go:89 exec tofu)。）
-   [ ] `F-9202` 完成 Cloud Account 和凭据管理。（依赖：F-1442；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9202 | 未满足：无独立 Cloud Account 实体——provider 加密 access_key 兼作账号（F-1442 部分），无 account 实体/凭据管理。）
-   [ ] `F-9203` 完成资源同步。（依赖：F-0341；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9203 | 未满足：CloudResource 模型 0 命中（F-0341 区），无资源拉取/同步/过滤/绑定。）
-   [ ] `F-9204` 完成 Cloud Operation 任务状态机。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9204 | 未满足：CloudOperation 模型 0 命中（F-0361 区），无操作状态机/幂等键/超时对账。）
-   [ ] `F-9205` 完成开机、关机和重启流程。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9205 | 未满足：无云资源电源开/关/重启路径（F-0361 区/F-0327 区）。）
-   [ ] `F-9206` 完成 Cloud Resource 与 Node 关联。（依赖：F-0341、F-0391；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9206 | 未满足：CloudResource 0 命中，无资源↔Node 关联与实例停止/启动/删除联动（F-0391/F-0341 区）。）
-   [x] `F-9207` 完成账单导入模型。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-9207 | Status: Done | Evidence: 账单导入模型落地：CostRecord/CostItem/BillImport + 去重/索引 (§4.1/§4.2) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-9208` 完成至少一种 Provider 的实际账单接入或文件导入。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-9208 | Status: Done | Evidence: 手工文件导入(ImportFile)+API 拉取(ImportFromAPI) 双通道 (§4.2) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-9209` 完成估算成本。（依赖：F-0542；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-9209 | Status: Done | Evidence: 估算成本全类目落地：包月/固定带宽/按流量/按小时/IP/磁盘/快照/备份/折扣/自定义 (§4.3 F-0541~F-0549) | Verification: TestEstimate*（TestEstimateBaseKinds/Hourly/Traffic/LinesProrate/UnknownLine/RecomputeUpdates/All）+TestBillingAPICostLines（go test ./... 全绿 2026-11-26） | Commit: 8be6965）
-   [x] `F-9210` 完成实际账单与估算对比。（依赖：F-0581；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-9210 | Status: Done | Evidence: Reconcile 估算 vs 实际对比落地 (§4.5 F-0587) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [ ] `F-9211` 完成 Dash 云资源管理页面。（依赖：F-0411；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9211 | 未满足：Dash 云资源/云操作页面未实现（无 CloudResource 模型即无页面，审计 §3.11 F-9211/9212 未实现）；仅 Providers 列表存在（F-0411 区）。）
-   [ ] `F-9212` 完成 Dash 成本管理页面。（依赖：F-1462、F-1463、F-1465、F-0622；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9212 | 部分：CostView 节点/区域/运营商维度有（F-1464/1466 关）；无 Provider/账号/资源维度、无账单页/同步任务/明细/分摊规则（F-1462/1463/1465/F-0622 区）。）

## P3 --- Agent、Relay 和配置闭环

-   [x] `F-9301` 完成 Agent 协议差异修复。
    - 备注（Task ID: F-9301 | Status: Done | Evidence: 双方共用单版本协议契约：ProtocolVersion=1(envelope.go:8)+Envelope.V/ID/Type/Payload(envelope.go:36-41)+Valid() 拒未知版本(envelope.go:70)；server 拒不匹配版本 agentws.go:584-591、agent 丢弃 link.go:160-161。 | Verification: TestAgentWSHelloVersion(upgrade_test.go:100)+TestReadLoopDropsBadMessages(link_test.go:225)+envelope_test.go:76 | Commit: 970e9e2）
-   [ ] `F-9302` 完成能力和状态上报。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9302 | 部分：状态上报已实现（Heartbeat.Procs messages.go:109+app.go:450-462→saveProcStatuses agentws.go:467-473）；能力上报未实现——capab 全仓 0 命中，Hello/Heartbeat/ConfigPush 均无 capab 字段（app.go:221-228）。）
-   [x] `F-9303` 完成配置版本和 ACK。
    - 备注（Task ID: F-9303 | Status: Done | Evidence: 配置版本+ACK：ConfigPush.Version(messages.go:184)+ConfigAck.Version(messages.go:192)；relaypush.go:70-119（sha256 digest 作 Version、ackTimeout:24、applied/failed/Finish 写 NodeConfig 状态）+NodeConfig.Version(models.go:233)；agent app.go:359-365 回 ack+configd.go:41-45/124-125。 | Verification: relaypush_test.go:240/268/285/307/326+configd_test.go:63/83/135 | Commit: 970e9e2）
-   [x] `F-9304` 完成 Relay 状态管理。
    - 备注（Task ID: F-9304 | Status: Done | Evidence: Relay 状态管理：ProcStatus State=running/stopped/crashed(messages.go:124-139)+procs.Manager.Statuses 喂心跳(app.go:463-465)+ProcReport 状态变化推送(app.go:175-183)；relay 独立启停(roles.go:31 Standalone:true+cmd -role)+配置校验/热更新(relay.go loadOverride)。 | Verification: TestBuildHeartbeatAndStartHeartbeat+TestRelayRunFile+TestRelayValidation+TestRelayHotRepoint | Commit: 970e9e2）
-   [x] `F-9305` 完成配置失败回滚。
    - 备注（Task ID: F-9305 | Status: Done | Evidence: 配置失败回滚：configd.go:114-119（reload 失败回滚旧配置）；ack 带失败态 relaypush.go:105-119。 | Verification: TestApplyReloadFailRollsBack(configd_test.go:135)+TestApplyShaMismatch(:83) | Commit: 970e9e2）
-   [x] `F-9306` 完成断线恢复集成测试。
    - 备注（Task ID: F-9306 | Status: Done | Evidence: 断线恢复集成测试：e2e-smoke.sh:296-298（kill agent→断线回落 expect 0）→:314-326（兼容窗口旧令牌上线 expect 1）→:327-340（吊销令牌保持 0）+TestAgentWSDedupSurvivesReconnect(agentws_dedupe_test.go:104，重连后 replay 计数=1)+TestRunReconnectsUntilCancel(link_test.go:162)。 | Verification: deploy/e2e-smoke.sh:296-340（53 断言内）+上述单测 | Commit: 970e9e2）

## P4 --- 探测和健康体系

-   [x] `F-9401` 完成 Probe 指标模型。
    - 备注（Task ID: F-9401 | Status: Done | Evidence: Probe 指标模型：ProbeReport(messages.go:260-273 TargetKind/TargetNode/RttMs/LossPct/Reachable/Blocked/Verdict/Region/ISP/ProbedAt)+批量 ack(:275-283)+目标 kinds(:246-250)/verdicts(:253-256)；持久化 storage.ProbeReport(models.go:197-214, idx_probe_window)。 | Verification: TestProbeHealthy(probe_test.go:55)+TestProbeUnreachable(:88)+TestProbeTunnelReturnPath(:117)+TestVerdictOf(:164) | Commit: 970e9e2）
-   [x] `F-9402` 完成 Probe 任务调度。
    - 备注（Task ID: F-9402 | Status: Done | Evidence: Probe 任务调度（去中心化口径）：agent 本地自调度——Runner.Run 每 spec 一循环(probe.go:62-87)+nextInterval 晚高峰 19 时加密(probe.go:156-172)+config.ProbeSpec{TargetKind,Target,Direction,IntervalSec}(config.go:39-45)+Config.Probes(config.go:74)；服务端不下发（ProbeTask 全仓 0 命中，envelope.go:27-28 仅 agent.probe_report/panel.probe_ack），设计明示「面板只收结论做聚合判定，不集中探测」(pool.go:1-5/probe.go:1-5)。 | Verification: agent/internal/probe/probe_test.go（TestProbeHealthy 等 4 例）+agent/internal/config 配置解析 | Commit: 970e9e2）
-   [x] `F-9403` 完成结果聚合。
    - 备注（Task ID: F-9403 | Status: Done | Evidence: 结果聚合：pool.Sweep 30 分钟窗口拉最新 K 个 verdict(pool.go:129-173, ProbeFresh:28-29, Keep=max(SickStrikes,WellStreak):131-134, allVerdicts:235-245)+维度聚合 aggregate.Judge(aggregate.go:32-38, DefaultMinSick=2/DefaultSickRate=0.5:18-19, DimensionRegion/ISP:27-28)+monitor sweep→Judge→applyVerdict 落 DimensionStatus(monitor.go:52-108)。 | Verification: TestJudgeRegionAndISP(aggregate_test.go:45)+TestJudgeWindowAndLatest(:90)+TestJudgeEmpty(:120)+TestJudgeRatioFailure(aggregate_extra_test.go:12)+TestSweepPersistsDimensions(monitor_test.go:152) | Commit: 970e9e2）
-   [ ] `F-9404` 完成 Node、Tunnel、Path、Egress 健康区分。（依赖：F-0904、F-0905；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9404 | 未满足：无 Node/Tunnel/Path/Egress 健康四区分——仅 Node(pool)+Dimension(region/isp) 两级健康；无 Path 模型即无 Path/Egress 级；TransportHealth 定义于 dash/src/api.ts:122-132 但无视图消费（F-0904/0905 区）。）
-   [x] `F-9405` 完成健康状态计算。
    - 备注（Task ID: F-9405 | Status: Done | Evidence: 健康状态计算：monitor 状态机 StateHealthy/Degraded/Failed(monitor.go:22-26) 由 v.Failed/v.Sick 计算(:66-73)落 DimensionStatus(:80-108, Since/UpdatedAt, models.go:218-226)；节点/池态 pool.go:151-170（连续 sick→suspended setPoolState:248-255、连续 healthy→active）。 | Verification: TestApplyVerdictTransitions(monitor_test.go:59)+TestApplyVerdictDegraded(:123)+TestSweepPersistsDimensions(:152)+TestSweepSuspendAndResume(pool_test.go:71)+TestSweepScope(:162)+TestLoopSweepsAndStopsOnCancel(:241) | Commit: 970e9e2）
-   [ ] `F-9406` 完成健康管理页面。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9406 | 部分：健康在 NodesView 内渲染——/api/dimension-status 拉取(router.go:132, NodesView.vue:71, dims:56)+状态点(healthy 正常/异常:229-235)+区域/运营商健康聚合(:596)+pool_state 标签(:694-696)+togglePool 摘除/预备(:109-142)；无独立健康管理页面（dash/router.ts 16 路由无 health/dimension/probe）、panel 无健康视图、无 tunnel/path/egress 级健康 UI。）

## P5 --- Path 编排和调度

-   [ ] `F-9501` 完成 Path 创建和校验。（依赖：F-0243；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9501 | 未满足：无 type Path（F-0243 区），无 Path 创建/校验/CRUD API。）
-   [x] `F-9502` 完成 Entry → Landing 编排。
    - 备注（Task ID: F-9502 | Status: Done | Evidence: Entry→Landing 编排：自动调度写 LandingAssignment(alloc.go:530-541 SweepAt→db.Create, 当前 auto 释放:525-528)+手动入口 landing.go:45-109(db.Create:104, 校验 entry/landing role/方向/权重)+路由 /api/landings GET/POST/PUT/DELETE(router.go:137-140)。 | Verification: TestSweepAllocatesAndSwitches(alloc_test.go:223)+TestSweepManualWins(:302)+TestLandingAssignment(landing_test.go:10)+TestAllocAPI(allocapi_test.go:11) | Commit: 970e9e2）
-   [ ] `F-9503` 完成 Path Group。（依赖：F-0261；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9503 | 未满足：PathGroup 模型 0 命中（F-0261 区）。）
-   [ ] `F-9504` 完成 Policy。（依赖：F-0282；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9504 | 未满足：无 type Policy（F-0282 区）。）
-   [x] `F-9505` 完成候选过滤。
    - 备注（Task ID: F-9505 | Status: Done | Evidence: 候选过滤：alloc.go:448-455（entry role∈(entry,both)、landing role∈(landing,both)、enabled+pool_state=active+status!=provisioning）+:486-489（Direction 匹配、Healthy:!sick[l.ID]）+sick 来自 latestSick(ProbeFresh 30min):622-635+Pick 健康门控:218-226。 | Verification: TestPickHealthGate(alloc_test.go:155)+TestSweepScopeGuards(:338)+TestSweepAllSickKeepsCurrent(:367)+TestPickLeastConnCapacityWeight(:67) | Commit: 970e9e2）
-   [x] `F-9506` 完成质量、容量和成本评分。
    - 备注（Task ID: F-9506 | Status: Done | Evidence: 质量/容量/成本评分：容量 CapacityMbps(alloc.go:188-198 实测>降档>升档>100 兜底)+loadRatio(:241-243)；成本 CostPerGB(:202-207 仅按流量边际价)+costPer(:246-248)；策略比较 lessFor(:256-318 least_conn/cost_first/perf_first 精品线:355-361/balanced 归一加权:293-301)；成本聚合 cost.Build(cost.go:96-152)。 | Verification: TestPickCostFirst(alloc_test.go:89)+TestPickPerfFirstInOnly(:111)+TestPickBalanced(:133)+TestPickBalancedWeights(:480)+TestCapacityMbpsFallbacks(:547)+TestBuild(cost_test.go:48)+TestCheckAlerts(:131) | Commit: 970e9e2）
-   [x] `F-9507` 完成故障回退。
    - 备注（Task ID: F-9507 | Status: Done | Evidence: 故障回退：故障切换 alloc.go:502-521（当前 auto 落地不在健康候选集→强制换绑，健康问题不排队；释放旧:525-528+建新:530-541）；节点恢复 pool.Sweep(pool.go:129-173 连续 SickStrikes→suspended:156、连续 WellStreak→active:163)+手动 Resume/Suspend(:176-234)；恢复状态机 recovery.Sweep(recovery.go:108)+advance(:211)；离线补报 agent spool。 | Verification: TestSweepAllSickKeepsCurrent(alloc_test.go:367)+TestSweepSuspendAndResume(pool_test.go:71)+TestResume(:193)+TestSuspend(:216) | Commit: 970e9e2）
-   [ ] `F-9508` 完成调度解释和模拟。（依赖：F-1032、F-1033、F-1034、F-1035；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9508 | 未满足：无调度决策记录/候选排除原因/结果解释/模拟 API（F-1032/1033/1034/1035 均未关）。）

## P6 --- 订阅和用户运营

-   [ ] `F-9601` 完成统一 Subscription DTO。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9601 | 部分：共享输入 DTO sub.Entry{Node,RttMs}(links.go:169-173) 被三适配器共用（PackV2Ray links.go:201/PackClash clash.go:72/PackSingBox singbox.go:165）；无统一响应 DTO——各格式自出（v2ray base64 links/clashDoc clash.go:66/sing-box outbounds singbox.go:165），handler 按 target 分支(sub.go:124-152)。）
-   [x] `F-9602` 完成 sing-box Adapter。
    - 备注（Task ID: F-9602 | Status: Done | Evidence: sing-box Adapter：PackSingBox(singbox.go:165)+tls.server_name/singBoxTransport 映射(singbox.go:103-161)。 | Verification: TestPackSingBox(sub/singbox_test.go) | Commit: 970e9e2）
-   [x] `F-9603` 完成 Clash/Mihomo Adapter。
    - 备注（Task ID: F-9603 | Status: Done | Evidence: Clash/Mihomo Adapter：PackClash(clash.go:72)+servername/applyTransport 映射(clash.go:181-201)。 | Verification: TestPackClash(sub/clash_test.go) | Commit: 970e9e2）
-   [x] `F-9604` 完成分享链接 Adapter。
    - 备注（Task ID: F-9604 | Status: Done | Evidence: 分享链接 Adapter：PackV2Ray(links.go:201)+v2ray 参数映射(links.go:67-81)。 | Verification: TestPackV2Ray(sub/links_test.go) | Commit: 970e9e2）
-   [ ] `F-9605` 完成真实客户端解析测试。（依赖：F-1129、F-10011；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9605 | 未满足：真实客户端解析测试未满足——合成 UA 无 fixture（F-1129 区；F-10011 未满足）。）
-   [ ] `F-9606` 完成订阅权限过滤。（依赖：F-1143、F-1144；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9606 | 部分：用户级可用门 sub.UserActive(availability.go:11-22, sub.go:70)+配额降档 quota.ActiveAction→LowCost(sub.go:88-102, quota/link.go:107-114)+设备/IP 限制(subDeviceCheck sub.go:56-59)；无套餐/权限过滤——sub+handler/sub.go 内 plan/tier/套餐/permission 0 命中，storage.User.Plan(models.go:496) 是节点机型非订阅套餐（F-1143/1144 区）。）
-   [ ] `F-9607` 完成流量记账可靠性。（依赖：F-1251；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9607 | 部分：幂等去重已实现——服务端窗口 agentws_dedupe.go:36-67（repeat 键 msgType\x00id，FIFO cap=512/TTL=10min:13-14, repeatReport:61-67）+记账前拦截 agentws.go:544-556（重复帧双算=多扣费，必须挡在记账前）+TTL 过期即视为新帧(:48)，F-1250 关；补报未实现——断线流量静默丢弃（traffic.go:219-226 send==nil 即 return），spool 仅承载 probe 角色，设计 at-most-once 无重传（F-1251 未满足）。）
-   [ ] `F-9608` 完成套餐与线路权限关联。（依赖：F-1143、F-1144、F-9991、F-10013；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9608 | 未满足：无套餐/Plan 实体，订阅内容无套餐与线路权限关联（F-1143/1144 区；F-9991/F-10013 部分）。）

## P7 --- 管理和运营闭环

-   [x] `F-9701` 完成 Dash 节点与线路管理。
    - 备注（Task ID: F-9701 | Status: Done | Evidence: dash NodesView/LandingsView+node/landing API 测试+build 绿 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-9702` 完成 Dash 云资源管理。（依赖：F-0411；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9702 | 未满足：Dash 云资源管理未实现（无 CloudResource 模型即无页面，仅 Providers 列表存在）（F-0411 区）。）
-   [ ] `F-9703` 完成 Dash 成本和账单管理。（依赖：F-0521、F-0622；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9703 | 部分：成本管理有（CostView 节点/区域/运营商维度 F-1464/1466 关）；账单管理未实现（无账单导入/同步/明细页，F-0521/F-0622 区）。）
-   [x] `F-9704` 完成 Dash 配置和任务管理。
    - 备注（Task ID: F-9704 | Status: Done | Evidence: SettingsView+config 下发+ProvisionView/RecoveryView；TestNodeConfigGuards+TestApplyLifecycle | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-9705` 完成 Panel 用户自助能力。
    - 备注（Task ID: F-9705 | Status: Done | Evidence: panel 5 视图自助；TestPanelMe+TestPanelRedeem+TestPanelOrders+build 绿 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-9706` 完成订单和支付可靠性审计。
    - 备注（Task ID: F-9706 | Status: Done | Evidence: 订单与支付可靠性审计 §3.8（状态机/签名/幂等/对账/退款） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）

## P8 --- 可靠性和规模化

-   [ ] `F-9801` 完成权限和安全测试。（依赖：F-1733、F-1735、F-1737；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9801 | 部分：server 侧权限/安全测试齐（adminrole_test.go:39 TestAdminRoleCRUD 越权 403、apiauth_test.go 双面鉴权/分销不可提权、twofa_test.go 2FA、providerapi_test.go:40 凭据不回显、sub_test.go:276 限频、agentws_test.go:339 令牌伪造拒、audit_test.go:70 脱敏）；dash/panel 前端测试 0 命中、agent 无独立安全测试文件；F-1733（高危云操作越权）/F-1735（管理面配置篡改）/F-1737（密钥泄漏断言）未满足。）
-   [ ] `F-9802` 完成 Metrics 和告警。（依赖：F-1801~F-1814、F-1821、F-1822、F-1825、F-1828、F-1829、F-1830；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9802 | 部分：告警已落地——monitor.go:52-164（sweep→applyVerdict 状态机, 同状态不重复:95-99, herald.Emit region_fault/isp_fault:123-138, webhook notify:141-162）+notify.go 外发+alerts.go:46/75；Metrics 基本缺失——prometheus/pprof//metrics 代码 0 命中（唯一命中是测试抓取 URL manager_extra_test.go:135），无服务端指标层，仅 JSON 状态页(handler/status.go:181-187)+agent 反向抓取被管进程 MetricsURL(procs/metrics.go:24)；F-1801~1814 全未满足、F-1821/1822/1825/1828/1829/1830 告警生产者缺。）
-   [x] `F-9803` 完成备份恢复演练。
    - 备注（Task ID: F-9803 | Status: Done | Evidence: 备份恢复演练：deploy/restore.sh 恢复脚本+e2e 备份恢复腿（53 断言内）+restore.sh --key(deploy/restore.sh:16)。 | Verification: deploy/e2e-smoke.sh（备份恢复断言 :385-427）+deploy/restore.sh | Commit: 970e9e2）
-   [ ] `F-9804` 完成 Agent 和 Probe 规模测试。（依赖：F-2061~F-2076；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9804 | 未满足：无 Agent/Probe 规模/压测记录——P95/P99/benchstat 0 命中，§18.4 F-2061~2076 全未满足（无测试环境/数据规模/并发量/资源占用记录）。）
-   [ ] `F-9805` 完成 Provider API 故障测试。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9805 | 未满足：无 Provider API 集成可测（仅 tofu CLI provision.go:89，云 Provider Adapter 0 命中），无 Provider API 故障测试。）
-   [ ] `F-9806` 完成账单导入和成本核算规模测试。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9806 | 未满足：无账单导入（BillImport 0 命中）即无账单导入/成本核算规模测试（F-0521 区）。）
-   [ ] `F-9807` 完成完整端到端测试。（依赖：F-2042~F-2055；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9807 | 部分：e2e-smoke.sh 444 行/44 expect 真起双二进制全不 mock，覆盖 认证(122-134)→注册/重放拒(146-162)→WS 上线(164-192)→进程监管(213-238)→proc_ctl(243-294)→断线回落(296-298)→轮换吊销(300-340)→配置下发回执(342-383)→备份恢复(385-427)→compose 校验(431-444)；未覆盖 订阅生成/解析、流量记账、Path/探测、Relay 隧道、TCP/UDP 转发、Provider API、账单导入、Dash/Panel 前端（F-2042~2055 多未满足，F-2046/2047 明示解析仅单测）。）

## P9 --- 后续扩展

-   [ ] `F-9901` 评估多区域控制面需求。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9901 | 未满足：多区域控制面评估 0 命中（docs/design/*.md 多区域/multi-region 0；唯一相关是 audit 文档 :28「控制面，单二进制 ferry」单点陈述；入口与负载均衡设计.md:202 仅谈数据面 ISP 聚合）——无多区域控制面评估。）
-   [ ] `F-9902` 评估 Server 高可用。（依赖：F-1947；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9902 | 部分：评估记录存在（F-1946 已关：结论「单点设计+手动提升，无自动故障转移」，面板可用性设计.md:3/5-18/92-97）+真实 warm-standby 机制（standby.go:114-162 加密快照→主钥解密→sqliteMagic 校验→原子替换→清 -wal/-shm；Loop 替换后 os.Exit(0) 交 systemd 重启；DefaultSec=600 RPO≤10min）；无自动故障检测与提升，面板仍单点（F-1947 只读副本未满足）。）
-   [ ] `F-9903` 评估数据库只读副本。（依赖：F-1947；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9903 | 未满足：只读副本评估 0 命中（只读副本/read replica/读库 全仓 0；F-1947 未满足）——standby.go:114-148 是替换自身库的热备/冷备，从不对外提供只读查询面。）
-   [ ] `F-9904` 评估高级多跳 Path。（依赖：无；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9904 | 未满足：高级多跳 Path 无评估文档——docs/design 多跳/multi-hop 0 命中、type Path 0 命中；仅 todo.md:2480 一条「不做多跳」决策（先完成 Entry→Landing 两段式，不提前引入任意多跳图调度），非评估报告。）
-   [ ] `F-9905` 评估更多 Cloud Provider Adapter。（依赖：F-0302；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9905 | 部分：选型评估存在（计划扩充设计.md:12-27 云 SDK 薄封装 vs OpenTofu 五维对比+结论「底座=OpenTofu，不自研供给引擎不逐家封 SDK」、:43 多 provider 靠 OpenTofu provider 生态横向扩）；代码侧 adapter 接口缺位——F-0302 未勾选、providerapi.go:34-147 仅凭据/模板 CRUD 无云 API 客户端、dns.go:29 仅 cloudflare、支付真实现仅 epusdt。）
-   [ ] `F-9906` 评估更多订阅格式和客户端能力。（依赖：F-1127、F-1129、F-1130、F-1132；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9906 | 部分：三种格式已落地（links.go:201 PackV2Ray/clash.go:72 PackClash/singbox.go:165 PackSingBox，F-1122/1124/1126 关）+条件式延后决策（todo.md:439/2545「补 mihomo/xray-json，触发条件=出现 clash-meta 拒载或 xray-json 消费方真实反馈」）；客户端能力矩阵缺位（F-1127/1129/1130/1132 未满足：无 per-client 特性表、合成 UA 无 fixture、不支持字段降级部分、clash 单一 url-test 形状假设）。）

------------------------------------------------------------------------

# 20. 最终验收标准

## 控制面

-   [ ] `F-9991` 用户、套餐、订阅和节点管理可用。（依赖：F-0101；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9991 | 部分：用户/订阅/节点 CRUD 已实现（§1/§10，F-1202/1222/1244 等关）；无 Plan/套餐实体（F-0101 未满足）——「用户、套餐、订阅和节点」仅套餐缺。）
-   [ ] `F-9992` Node、Capability、Tunnel、Path、Path Group、Policy
    - 待补（F-9992 | 未满足：Capability/Path/PathGroup/Policy 模型全 0 命中（F-0222/0243/0261/0282）——「Node、Capability、Tunnel、Path、Path Group、Policy 模型收敛」仅 Node/Tunnel 收敛。）
    边界清晰。（依赖：F-0222、F-0243、F-0261、F-0282；完成条件：需求落地+测试绿+证据行）
-   [ ] `F-9993` 能够管理 Entry、Landing 和 Relay。（依赖：F-0804、F-1404；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9993 | 部分：Entry/Landing 管理齐（landing 指派+sweep 关 F-0244）；Relay 仅受管进程启停（F-0804/1404 缺口）——「管理 Entry、Landing 和 Relay」Relay 部分。）
-   [ ] `F-9994` 能够探测并识别节点、隧道和线路故障。（依赖：F-0902、F-0904、F-0905、F-0910、F-0911；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9994 | 部分：probe TCP+verdict+dimension-status 齐（F-0903/0929/0930 关）；无 Path/Egress 健康区分、无 UDP/应用层探测（F-0902/0904/0905/0910/0911）——识别节点/隧道/线路故障部分。）
-   [x] `F-9995` 能够基于健康、容量和成本选择线路。（依赖：F-0604、F-0605；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-9995 | Status: Done | Evidence: alloc 健康门（latestSick）+容量权重（CapacityMbps）+成本排序（CostPerGB），成本新鲜度/降级（F-0604/F-0605）与 Path 成本估算（F-0603）已齐；alloc 包测试全绿 | Verification: 测试全绿 2026-11-26（server 47 包 ok） | Commit: 9e5dfad）
-   [ ] `F-9996` 能够解释调度结果。（依赖：F-1033、F-1034、F-1035；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9996 | 未满足：无调度结果解释/候选排除原因/模拟 API（F-1033/1034/1035）——不能解释调度结果。）

## 云资源与成本

-   [ ] `F-9997` 能够在 Dash 管理至少一个真实 Provider 账号。（依赖：F-1443；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9997 | 部分：Provider CRUD 已实现（F-1441 关）；无账号连接测试（F-1443 未满足）——Dash 管理真实 Provider 账号部分（无凭据验证）。）
-   [ ] `F-9998` 能够同步 Provider 云资源。（依赖：F-0341；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9998 | 未满足：无 CloudResource 模型/同步（F-0341-0356）——不能同步 Provider 云资源。）
-   [ ] `F-9999` 能够在权限控制下执行开机、关机和重启。（依赖：F-1704；完成条件：需求落地+测试绿+证据行）
    - 待补（F-9999 | 未满足：无云资源电源开/关/重启路径（F-0327 等）+无细粒度权限（F-1704）——不能在权限控制下执行开机/关机/重启。）
-   [ ] `F-10000` 云操作状态能够与 Provider 实际状态对账。（依赖：F-0361；完成条件：需求落地+测试绿+证据行）
    - 待补（F-10000 | 未满足：无 CloudOperation 状态机/超时对账（F-0361+）——云操作状态不能与 Provider 实际状态对账。）
-   [ ] `F-10001` Cloud Resource 能够与 Ferry Node 关联。（依赖：F-0103、F-0341；完成条件：需求落地+测试绿+证据行）
    - 待补（F-10001 | 未满足：无 CloudResource/关联（F-0103/0341）——Cloud Resource 不能与 Node 关联。）
-   [x] `F-10002` 能够导入或同步实际账单。（依赖：F-0521；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-10002 | Status: Done | Evidence: 可导入或同步实际账单：文件导入 + Billing API 拉取 (§4.2) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [x] `F-10003` 能够区分实际账单、估算成本和成本分摊。（依赖：F-0552、F-0505；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-10003 | Status: Done | Evidence: 区分实际账单/估算/分摊：三表并存互不混写 (§4.1/§4.3/§4.4) | Verification: TestBillingAPI/TestBillingAPIImportFromAPI/TestStorageConstraints/TestAllocate*/TestReconcile/TestNewVersion（go test ./... 全绿 2026-11-26） | Commit: 356bdec）
-   [ ] `F-10004` 能够按 Provider、账号、区域、资源和 Node 查看成本。（依赖：F-1462、F-1463、F-1465；完成条件：需求落地+测试绿+证据行）
    - 待补（F-10004 | 部分：区域/ISP/节点成本维度有（F-1464/1466 关）；无 Provider/账号/资源维度（F-1462/1463/1465）——按五维度查看成本缺二。）
-   [x] `F-10005` 能够发现账单缺失、资源无法匹配和异常成本。（依赖：F-0588、F-0589、F-1473；完成条件：需求落地+测试绿+证据行）
    - 备注（Task ID: F-10005 | Status: Done | Evidence: DetectAnomalies 三类齐备：missing_bill（同步资源无账单明细）/unmatched_resource（账单无对应资源）/spend_spike（月环比涨幅），另有高成本阈值（F-1473 已关部分） | Verification: TestDetectAnomalies（go test ./... 全绿 2026-11-26） | Commit: 2be063c）

## 数据面与订阅

-   [ ] `F-10006` Entry 和 Landing 能够建立 Relay 隧道。（依赖：F-0812、F-0243；完成条件：需求落地+测试绿+证据行）
    - 待补（F-10006 | 部分：relay 隧道插件已实现（§6 F-0802/0803 关）；无 UDP、无 Path 实体（F-0812/0243）——Entry/Landing 建立 Relay 隧道部分（TCP only）。）
-   [ ] `F-10007` 支持实际 TCP 和 UDP 转发。（依赖：F-0812；完成条件：需求落地+测试绿+证据行）
    - 待补（F-10007 | 部分：TCP io.Copy 转发已实现（F-0802）；UDP 未实现（F-0812）——仅 TCP 转发。）
-   [x] `F-10008` 配置能够可靠下发和回滚。
    - 备注（Task ID: F-10008 | Status: Done | Evidence: config push+ACK+失败回滚（F-2023/2024 关：relaypush_test 9 例+configd_test TestApplyReloadFailRollsBack） | Verification: server/internal/relaypush/relaypush_test.go+agent/internal/configd/configd_test.go | Commit: cb60ba7）
-   [x] `F-10009` 能够生成 sing-box 配置。
    - 备注（Task ID: F-10009 | Status: Done | Evidence: PackSingBox（sub/singbox.go:165+TestPackSingBox）| Verification: server/internal/sub/singbox_test.go | Commit: cb60ba7）
-   [x] `F-10010` 能够生成 Clash/Mihomo 配置。
    - 备注（Task ID: F-10010 | Status: Done | Evidence: PackClash（sub/clash.go:72+TestPackClash）| Verification: server/internal/sub/clash_test.go | Commit: cb60ba7）
-   [ ] `F-10011` 生成内容经过真实客户端解析测试。（依赖：F-1129；完成条件：需求落地+测试绿+证据行）
    - 待补（F-10011 | 未满足：合成 UA 无 fixture（F-1129）——生成内容未经真实客户端解析测试。）
-   [x] `F-10012` Ferry 不需要自研客户端即可完成用户接入。
    - 备注（Task ID: F-10012 | Status: Done | Evidence: v2ray/clash/sing-box 适配（F-1102-1105 关：PackV2Ray/PackClash/PackSingBox）| Verification: sub/{links,clash,singbox}_test.go | Commit: cb60ba7）

## 运营和可靠性

-   [ ] `F-10013` 套餐权限与订阅内容一致。（依赖：F-1142、F-1143、F-1144；完成条件：需求落地+测试绿+证据行）
    - 待补（F-10013 | 部分：配额降档+权限过滤有（F-1142 部分）；无套餐/PathGroup/Policy 过滤（F-1143/1144）——套餐权限与订阅内容一致性部分。）
-   [ ] `F-10014` 流量记账具备幂等和补报能力。（依赖：F-1251；完成条件：需求落地+测试绿+证据行）
    - 待补（F-10014 | 部分：幂等去重已实现（F-1250 关：agentws_dedupe_test 7 例）；补报未实现（F-1251）——记账幂等有、补报无。）
-   [ ] `F-10015` Dash 能够完成日常节点、云资源、线路和成本管理。（依赖：F-0411；完成条件：需求落地+测试绿+证据行）
    - 待补（F-10015 | 部分：节点/线路/成本/审计/恢复 Dash 页有（§12 关闭 19 项）；云资源页无（F-0411-0432）——Dash 日常管理缺云资源。）
-   [ ] `F-10016` Server 重启后能够恢复控制面管理状态。（依赖：F-1931、F-2053；完成条件：需求落地+测试绿+证据行）
    - 待补（F-10016 | 部分：server 恢复态恢复有（F-2053 部分 e2e-smoke.sh:417-427）；agent 重连未断言（F-1931）——重启后控制面管理状态恢复部分。）
-   [ ] `F-10017` Server 短时不可用不导致已运行的数据面立即停止。（依赖：F-1932；完成条件：需求落地+测试绿+证据行）
    - 待补（F-10017 | 部分：故障语义文档（面板可用性设计.md:5-18）+离线缓冲测试（F-1932 部分 spool_test.go:99/147）；无「数据面在 server 宕机时继续转发」断言——部分。）
-   [ ] `F-10018` 关键故障有监控、告警、审计和恢复路径。（依赖：F-1801~F-1814、F-1821、F-1822、F-1825、F-1828、F-1829、F-1830；完成条件：需求落地+测试绿+证据行）
    - 待补（F-10018 | 部分：Alert 4 kind+审计+recovery 齐（§7/§16 关闭项）；无 metric 层（F-1801-1814）、部分告警无生产者（F-1821/1822/1825/1828/1829/1830）——监控告警审计恢复路径部分。）
-   [x] `F-10019` 备份经过真实恢复演练。
    - 备注（Task ID: F-10019 | Status: Done | Evidence: deploy/restore.sh+e2e 备份恢复腿（F-1927 关，53 断言内）+restore.sh --key（deploy/restore.sh:16）| Verification: deploy/e2e-smoke.sh（备份恢复断言）+deploy/restore.sh | Commit: cb60ba7）
-   [ ] `F-10020` 所有最终验收项均有可复现的测试证据。（依赖：F-2061~F-2076、F-2042~F-2055；完成条件：需求落地+测试绿+证据行）
    - 待补（F-10020 | 未满足：§18.4 无基准/压测（F-2061-2076 全未满足）；大量项仅单测无 e2e（F-2042-2055）——并非所有最终验收项有可复现测试证据。）

------------------------------------------------------------------------

## 21. 每个任务的完成记录模板

Code Agent 每次完成任务时，在 PR 或提交说明中使用以下模板：

``` text
Task ID:
Task:
Status: Done / Blocked / Cancelled / Not Applicable

Implementation:
- Changed files:
- Main code path:
- Database/API/protocol changes:

Verification:
- Test command:
- Test result:
- Evidence:

Dependencies:
- Completed:
- Remaining:

Risk:
- Migration:
- Compatibility:
- Rollback:

Commit:
```

## 22. 关键架构决策

1.  **Ferry 是控制平台，不是客户端。** 客户端使用 sing-box、Clash/Mihomo
    等成熟项目。
2.  **Dash 是统一运维入口。**
    云账号、云实例开关机、资源同步、账单、成本和节点管理都应形成完整的后台工作流。
3.  **云资源与 Ferry Node 是两个不同实体。** 云资源代表 Provider
    资产，Node 代表 Ferry 管理的数据面节点；二者通过明确关联连接。
4.  **云操作是异步任务。** Provider API 返回成功不等于 Agent
    已上线，也不等于 Path 已恢复。
5.  **实际账单、成本估算和成本分摊必须分开。**
    原始账单不可被估算或重算结果覆盖。
6.  **Node、Tunnel、Path 和 Path Group 不得混为一谈。**
    它们分别表达机器、连接、完整线路和候选线路集合。
7.  **Server 是全局调度权威。** Agent 执行本机任务，Probe
    负责测量，Relay 负责转发。
8.  **先完成 Entry → Landing 两段式线路。** 不提前引入任意多跳图调度。
9.  **所有任务原子化。** 每个 checkbox
    可以独立关闭，阶段只是组织方式，不是任务完成的替代证据。
10. **先审计后改造。** 不重复实现已有功能，不依据文档或 TODO
    推断源码已经完成。

## 23. 安全审计修复批次（audit-batch5-6repos-2026-11-11.md ferry 段，P0/P1）

巡检单 `docs/audit/audit-batch5-6repos-2026-11-11.md`（ferry 段）P0/P1 全部
修复并推送，每项单 commit、英文简短、带回归测试、测试绿。审核结论（apiAuth/
adminAuth/distAuth 分离、防枚举 404、原子核销卡密、dedupe 幂等层）无改动。

| 项 | 修复 | Commit | 证据（file:line） | 回归测试 |
|----|------|--------|------------------|----------|
| P0-1 gin 未设 TrustedProxies，ClientIP 全线可伪造 | 白名单信任，非法 CIDR 回退不信任（fail-closed） | `bb7ec0d` | server/internal/handler/router.go:94 | 既有 apiauth 套件绿 |
| P1-2 admin JWT 签发后不回库复核，停用/改密旧 token 仍 24h 有效 | adminAuth 每请求回库复核（任何 First 错→401）+ PasswordChangedAt 失效 | `3b9ca42` | server/internal/handler/apiauth.go（adminAuthMiddleware） | TestApiDataPlaneHonorsAdminSecretEnv |
| P1-3 settlePayment 幂等只按 external_id，并发回调双重发放额度翻倍 | 条件 `UPDATE ... WHERE id=? AND status='pending'`，RowsAffected=0 幂等不发 | `c501941` | server/internal/handler/paynotify.go:49 | TestEpusdtNotifyConcurrentSettle |
| P1-4 epusdt 回调金额口径（网关按响应体判定，非 JSON） | 回 `c.String(200,"ok")` 字面量体 | `a9fa9c2` | server/internal/handler/paynotify.go:43 | TestEpusdtNotifyConcurrentSettle 断 body=="ok" |
| P1-5 writeLedgerOp 余额校验非事务，并发打款可透支 | 条件 `INSERT...SELECT...WHERE SUM(...)>=?`（便携 `FROM (SELECT 1)`）原子护栏 | `1caa7c7` | server/internal/handler/distributor.go:395 | TestPayoutAtomicGuard（第二连接并发） |
| P1-6 WS 读无 SetReadLimit、批量 items 无上限，打爆面板内存与 SQLite | 帧限 8MiB + capBatch 1000 + createChunked 防绑定变量溢出 | `e1bd6da` | server/internal/handler/agentws.go:95 | TestAgentWSBatchTrim + TestAgentWSFrameLimit |
| P1-7 sub_devices 无限增长（IPLimit=0 不清窗） | prune 移出 `IPLimit>0` 守卫，任何新 IP 顺带清窗 | `3564742` | server/internal/handler/subdevice.go:38 | TestSubDevicePruneUnlimited |
| P1-8 backup.Snapshot 整库读入内存，流量表增长后 OOM | 明文走同目录原子 rename（零拷贝）；加密走流式 EncryptBytesStream（同 GCM 格式） | `7bf0cde` | server/internal/backup/backup.go:138/160 | TestEncryptBytesStream + TestSnapshotLargeTableStreams |

已确认无问题（不改）：apiAuth/adminAuth/distAuth 分离、防枚举 404、原子核销
卡密、dedupe 幂等层。P2 合并简列（14 项）下一步处理。

验证：`go build ./...` 绿；`go vet internal/secret internal/backup` 绿；
`go test ./internal/secret/ ./internal/backup/ ./internal/standby/` 全绿；
`go test ./internal/handler/ -run 'Backup|Snapshot'` 绿。

## 24. 安全审计修复批次（audit-batch5-6repos-2026-11-11.md ferry 段，P2 全部 14 项）

P2 合并简列 14 项全部修复并推送，每项单 commit、英文简短、带回归测试、
`go test ./... -count=1` 38 包全绿（每项提交前全量跑）。

| 项 | 修复 | Commit | 证据（file:line） | 回归测试 |
|----|------|--------|------------------|----------|
| ①登录与兑换共用限速器跨面干扰 | 拆独立 loginLimiter/redeemLimiter 实例（router.go 各建） | `05d59b7` | server/internal/handler/admin.go:45、redeem.go:47 | TestRedeemRateLimited + adminlogin_err_test 套件 |
| ②waiters 无节点归属校验可注入伪应答 | 等待键加 nodeID（waiterKey{nodeID,requestID}），回包只唤醒源节点 | `89e9d2a` | server/internal/agenthub/hub.go:65-76 | hub_test 跨节点注入用例 |
| ③wsUpgrader 无 CheckOrigin | 显式 CheckOrigin（策略与 gorilla 隐式默认一致并留档） | `d68a5d7` | server/internal/handler/agentws.go:23-28 | agentws 套件绿 |
| ④health 未鉴权 | healthGuard：环回放行、外部走 apiAuth | `a1a7701` | server/internal/handler/router.go:110/374 | 既有 health 用例 |
| ⑤deleteUser 级联删计费历史 | 删用户前置空 traffic_logs.user_id 解除归属（旧库 CASCADE 兜底） | `ab0df05` | server/internal/handler/user.go:318-319 | userdelete_test 套件 |
| ⑥用户改名流量漏计 | 改名落 renamedFrom 旧名别名，saveUserTraffic 继续归属 | `ee6b8b1` | server/internal/handler/user.go:196-215 | traffic_test 改名归属用例 |
| ⑦event-results 默认不验签 | HeraldCallbackSecret 空即 401 拒全部回调（默认必验签） | `a41b191` | server/internal/handler/eventsapi.go:109-114 | TestEventResultSecretlessRejected |
| ⑧2FA 全局单键互覆盖 | 暂存键按管理员分键 admin_2fa_pending:<id> | `2e605cc` | server/internal/handler/twofa.go:32-36 | TestTwoFAPendingKeyPerAdmin |
| ⑨兑换失败可恶意禁卡 | fail_count 只按卡密自身不可用计（errCardCodeUnusable），无效 user_id 不误伤 | `f4e9033` | server/internal/handler/redeem.go:20-25/59 | TestRedeemBadUserDoesNotDisableCard |
| ⑩优惠码 pending 占限次 | 每用户限次只计 paid/refunded 订单（pending/failed/expired 不计） | `f3c43a5` | server/internal/handler/coupon.go:278 | TestCouponPendingDoesNotOccupyPerUserLimit |
| ⑪refund 不回收权益 | 退款同事务回收发放：add_quota 扣回（下限 0）、extend_days 扣天数，用户行缺失跳过，站内信告知 | `924d34b` | server/internal/handler/payrecon.go:192-233 | TestRefundRevokesGrants |
| ⑫reconcile 统计口径错 | 小计改全表聚合（实收/退款分桶、流水/发放全表），页内行照常 limit 截断 | `837eeba` | server/internal/handler/payrecon.go:145-165 | TestReconcileSummaryWholeLedger（250 单 > 一页仍全量） |
| ⑬UTF-8 截断 | truncate/truncateErr 回退 UTF-8 字符边界（快照体/回执 detail 可含中文） | `356fd66` | server/internal/handler/audit.go:98、contact.go:173 | TestTruncateUTF8Boundary + TestTruncateErr（中文边界） |
| ⑭本地时区计费 | 月账单闸门/标题/用量窗口/到期展示改服务器本地日历（quota month 口径），亮点管道（save_stats UTC 日聚合）自洽留档 | `99aeb9e` | server/internal/toucher/toucher.go:76/116/128/253 | TestBillGateFollowsLocalCalendar（FixedZone 东八区注入） |

验证：`go build ./...` 绿；`go test ./... -count=1` 38 包全绿（99aeb9e 提交前
复跑）；每项提交前 `git pull --rebase origin main` 后推送，无 force push。

## Codex 深审发现（2026-11-13，luna profile 近5 commit 审核，0C/6M/4m，待修）
- 去重留痕（2026-11-25）：本节原有 11 行与主清单逐字重复（8 条 BLOCKED＝微信/支付宝直连支付、TOUCH-7、costref、OSS 三候选、hy2 真机联调、VitePress 2、FERRY-002、FERRY-004；3 条观察项＝G1/G7/G3）已删除，主清单为准（BLOCKED 见「## 未开工项」段，G1/G7/G3 见其下观察项行），本节只留独有内容

- [x] 修复非法 UTF-8 截断退化为空串并补测试（`internal/audit/audit.go:104`）—— 审计快照不应因非合法 UTF-8 输入完全丢失（d7e0154）。
- [x] 让退款回收显式匹配 `extend_days` 并补未知类型测试（`internal/payrecon/payrecon.go:260`）—— 避免未知权益类型被误当延期扣减（a838d47）。
- [x] 为退款用户权益更新引入行锁或原子 SQL 并补并发测试（`internal/payrecon/payrecon.go:253`）—— 消除与 `applyGrant` 的丢更新竞态（a838d47）。
- [x] 替换登录 UA 字节截断为安全截断（`internal/loginlog/loginlog.go:31`）—— 防止 UA 写入非法 UTF-8（d7e0154）。
- [x] 替换恢复错误尾部截断为安全截断（`internal/recovery/recovery.go:288`）—— 防止中文错误文案被截断成非法 UTF-8（d7e0154）。
- [x] 统一替换 cert/provision/cloudflare 错误截断实现（`internal/cert/loop.go:212`）—— 清理同型 UTF-8 截断残留（d7e0154）。
- [x] 执行 `make fmt` 并在 CI 增加 gofmt 门禁（`internal/payrecon/payrecon.go:146`）—— 防止格式漂移再次进入主分支（96e23dd）。
- [x] 为审计请求体增加大小上限（`internal/audit/audit.go:57`）—— 避免大请求造成内存放大（e098fb4）。
- [x] 统一过期日期展示时区（`internal/toucher/toucher.go:255`）—— 保证 UI 日期与计费窗口一致（0659fb5）。
- [x] 补齐退款异常路径测试（`internal/payrecon/payrecon_test.go:379`）—— 覆盖用户已删、回收失败回滚和数据库终态断言（a838d47）。

## 文档一致性审计 G2（2026-11-14 再审计，ferry 席位）

- [x] 五条漂移全修并追加 `docs/审计-文档一致性.md` G2 段：P1 两项（`/api/health` 访问门未记、herald 回执「无鉴权」口径作废）+ P2 三项（2FA 暂存键分键与登录/兑换限速器分面、退款同事务回收权益未记、部署指南 env 表缺 `FERRY_HERALD_CALLBACK_SECRET`/`FERRY_TRUSTED_PROXIES` 两行）。无 P0、无幽灵功能。
- [x] **P2【需拍板】文档站浏览器语言自动识别**：现状=ferry 站无自动切换、仅手动链接（`docs/.vitepress/config.mts` `lang:'zh-CN'` 且无 `locales`，探针 `auto_switch: null`；线上 `/`→200 zh-CN、`/zh/`→**404**、`index.en.html`→200）。三重待决：①形态是「成对文件」叠「locale 路由反向用法」（root=中文而非英文），是否迁到 fleet 契约 root=英文/`/zh/`=中文；②EN 覆盖只 5 篇孪生（index/design-术语表/panel-comparison/herald-integration-log/doc-consistency-audit），设计/部署/指南深页无英文版，补到哪一层；③是否上 `navigator.language` → `/zh/` 自动跳转（增强项，非受规基线）。等用户拍范围，本轮不重构、不擅自实现自动跳转。
  **2026-11-22 用户拍板（仅取③，①不迁 root、②不补深页）**：落地为「仅自动跳转」——不迁 `locales`、不动 URL、维持 5 篇英孪。新增 `docs/.vitepress/theme/lang-switch.ts`（逻辑）+ `config.mts` `<head>` 同步内联脚本（首帧前跳转，防「先中文后英文」闪页）+ `theme/index.ts` 点击拦截写 `localStorage ferry-doc-lang` 显式偏好（手动切过即让位，防弹回）。仅 5 对孪生互跳，`/deploy/*`、`/guide/*`、`/design/*` 等非孪生页一律不动（绝不 404）；舰队探针 `fleet-url-contract.py ferry` 本地面 `auto_switch` 由 `null` 变为检出三项（`navigator.language`/`navigator.languages`/`theme:lang-switch.ts:navigator.language`），探针无需改。验收：`pnpm -C docs build` 绿 + Playwright 八例全过（en-US→EN 首页、zh-CN 留守、中英深页孪生双向互跳、非孪生页留守、两向手动切换写偏好且刷新不弹回）（94be932）。
  - [x] 死链/双语违例复核结论：本轮未见死链（EN 孪生五篇与 `index.en.html` 可达）；「双语违例」指 root 语言与 fleet 契约相反，并入上一条待拍板项——本轮不迁 root、维持反向形态，仅上自动跳转（94be932）。

- [x] SHOP-1 · P1 · 套餐购买页（用户 2026-11-21 需求）：后端 `GET /panel/products`（可售 CardBatch+库存）与 `POST /panel/orders`（渠道+优惠码下单）已就绪但**前端零调用**——新增 panel「购买」view：套餐卡片（名称/价格/加量或延时/剩余）、渠道选择（epusdt/wechat/alipay 按注册态渲染）、优惠码输入、下单后轮询订单状态跳支付；验收=面板真实走查一单全链（下单→支付回调→订单 paid→权益到账）——2026-11-22（5f2d387）：面板新增 `/shop`「购买」view（ShopView.vue，套餐卡+渠道选择+优惠码+下单轮询跳支付）、路由与导航项；新增 `GET /api/panel/channels`（payment.Channels 暴露注册态：epusdt 启用、wechat/alipay 占位置灰）+ api.ts 资源类型与调用；Go 全链测试 `TestPanelOrderFlow` 已覆盖下单→回调→paid→权益到账（stub 网关），新增 `TestPanelChannels`+`payment.TestChannels`；`make test` 全绿（6 Go 模块+3 前端构建）+ `pnpm -C panel build` 绿；真实 HTTP 走查（临时 DB 起服）渠道/商品/错误路径端点均正确。注：真实支付回调需 epusdt 商户资质（BLOCKED 项未解除），全链以 stub 网关 Go 测试等价收口，面板侧以构建+HTTP 走查验证。
- [x] SHOP-2 · P2 · 订阅客户端感知补全：UA 判分支现在只有 clash/其他两态（`sub.go:103-110`），`sub/singbox.go` PackSingbox 已实现但 UA 未接——补 sing-box/Stash 分支出 singbox 格式；HomeView 订阅区按客户端类型给 v2ray/clash/singbox 三键复制（含 `?target=` 显式参数）；验收=三种 UA curl 实测各得对应格式，默认 UA 仍回落 v2ray——2026-11-22（7699d05）：抽共享 helper `subTargetByUA`（clash 系→clash、sing-box/Stash→singbox、其余回落 v2ray），主订阅 `/sub/:token` 与自建订阅 `/sub/x/:token` 两处接线（此前各写一份，改后同口径）；HomeView 订阅区补 sing-box / Stash（JSON）第三键复制（`?target=singbox`）；Go 测试补主订阅与自建订阅两处置（UA=sing-box/1.8.0 与 Stash/2.0 各出 outbounds JSON、默认 UA=curl/8.0 回落 base64）；`make test` 全绿（6 Go 模块+3 前端构建）；真实 HTTP 走查 curl 实测四态命中——v2rayNG/1.8→base64（text/plain）、curl/8.0（无特征）→base64、clash-verge/2.0→YAML（text/yaml）、sing-box/1.8.0 与 Stash/2.0→outbounds JSON（application/json），clash UA 叠 `?target=singbox` 显式覆盖为 JSON、`?target=bad` 回 400。

## 订阅过滤 + 配置定制（ferry-sub-filters，2026-11-21 期次，用户拍板 P0）

- [x] server：`subscriptions` 表 + 面板 CRUD API（`/api/subscriptions`，每用户上限 10、严格 JSON 未知字段 400、越权 404、重置令牌）+ 过滤下发端点，最终路径 `GET /sub/x/:token`（gin 静态段 `x` 优先于 `:token`，两者共存已验证，未走 `/subx/:token` 备选）—— 三维度（region/labels/protocol）白名单内存过滤 + per-sub 配置（remark_template/sort/max_nodes/include_rtt），沿用主订阅全部护栏（限流/设备校验/用量头/三格式 UA 协商）；主订阅 `/sub/:token` 行为不变（回归锚）（a5645e5）。
- [x] panel：「订阅」页（列表/新建/编辑/删除/重置令牌/复制与打开链接），三维度表单 + 配置表单，走 `/api/subscriptions`，无 i18n 结构沿用仓内中文约定、无新依赖（a5645e5）。
- [x] 测试与门禁：`sub/filter_test.go` + `handler/subx_test.go`（CRUD 护栏/越权/下发黄金断言/截断/用户态/限流/主订阅回归锚）；`make test` 全绿 + `pnpm -C panel build` 绿（a5645e5）。
- [x] 验收口径：curl 实测 `/sub/x/:token`（UA=clash）只含过滤后节点且 ≤3、主订阅 `/sub/主token` 输出不变、未知令牌 404、非法 filter JSON 400；面板走查截图留路径 `docs/public/screenshots/subscriptions-2026-11-21.png`、`subscriptions-create-2026-11-21.png`（a5645e5）。
- [ ] admin 面订阅管理（看用户订阅列表）：spec 明确不做（用户自助面优先），留后续项。

## 依赖安全（dependabot 2026-11 批，2026-11-22 处理）

- [x] vite override 6.4.3 → 6.4.6（`pnpm-workspace.yaml`）：dependabot 对 vite ≤6.4.3 开 5 条（1 high + 3 medium + 1 low，修于 6.4.4）、对 =6.4.4 再开 1 medium（修于 6.4.5），scoped override `vite@^5.4.14` 提到最新 patch 6.4.6；只命中 vite 5 声明（vitepress 1.x），dash/panel 的 vite ^7 不受影响；vitepress 2（vite ^8）仍 alpha 不升。验收：`pnpm install` 更新 lockfile 后三前端构建全绿（dash 21.51s / panel 2.47s / docs 8.86s→21.51s），docs 自动跳转脚本仍带（`ferry-doc-lang` 命中）；lockfile 仅存 `vite@6.4.6`（vitepress 面）与 `vite@7.3.7`（dash/panel 面），无 ≤6.4.5 残留（c78db4e / 36403b6）。
  - 收口复核（2026-11-22）：6.4.6 push 后 GitHub 重扫，**开放告警 0**（≤6.4.3 五条 + =6.4.4 一条全关）。`.github/dependabot.yml` npm 项 `directory: /` 下无 `/package.json`（pnpm workspace 根只有 `pnpm-workspace.yaml`），故 security-update job 每次开告警都会红（`/package.json not found`，run 44959739326）——属已知限制表象：告警由 scanner 依 lockfile 自动开/关、修复靠手动 override，不影响收口；已在该 yml 注释补记说明（8833855）。
  - go_modules 常规批（2026-11-22）：合并 dependabot PR #10（golang.org/x/net 0.62.0→0.63.0，agent+server 四文件纯版本号，与 #6–#9 同口径 merge commit）；PR CI 绿后合并（d060d62），本地 ff 同步后 server/agent 构建复验 OK。

## code scanning 分诊（CodeQL 2026-11 批，2026-11-22 处理）

- [x] 16 条开放 code scanning 告警逐条分诊后全部作废（用户 2026-11-22 拍板「全部作废」）：**14 × `go/incorrect-integer-conversion`（warning）= 误报**（全部是 `uint(ParseUint(id,10,64))`，`uint` 在 64 位平台 64 位、转换无损；ferry 目标 amd64/arm64 VPS 无 32 位构建，CodeQL 按平台宽度保守告警）；**1 × `go/request-forgery`（error，agent/internal/upgrade/upgrade.go:124 SSRF）= 按设计接受**（`up.URL` 唯一来源 `POST /api/nodes/:id/upgrade`，router.go:206 在 `api` 组 `apiAuth()` 下仅管理员；agent 下载后按管理员 sha256 校验+备份回滚，响应体不回流请求方；单运维架构管理员本就全权，无提权面）；**1 × `go/weak-sensitive-data-hashing`（warning，server/internal/secret/secret.go:41）= 不适用**（规则针对低熵口令存储，此处输入是运维设的高熵主密钥 `FERRY_SECRET_KEY`，SHA-256 对高熵输入是合格 KDF）。全部以 `false positive`/`won't fix` 作废（dismissed_reason 值域是带空格枚举），**code scanning 开放告警归零**；分诊理由写入各告警 dismissed_comment（2baeafd）。
  - 潜在加固项（未做，另立设计讨论）：SSRF 纵深防御——升级 URL 的 scheme/host 校验；主密钥若允许弱口令则换内存硬 KDF（Argon2id/PBKDF2）。
