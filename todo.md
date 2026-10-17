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

## 未开工项（需求一句话）

- [ ] agent 组件独立进程与 unix socket IPC（C-03）：当前组件为进程内角色调用；拍板维持原状不重开
  - 状态=维持不做｜原因：进程内角色调用已满足需求，独立 IPC 增加复杂度无收益｜触发条件：无（拍板关闭）
- [x] 节点令牌轮换窗口与吊销 CRL（C-06）：未实现；拍板维持原状不重开
  - 状态=完成｜落地=2026-10-17 随本批单提交；原因：轮换窗口、吊销 CRL、认证与 dash 操作均已实现｜证据=make test 全绿（6 Go 模块+dash/panel 构建）
- [ ] mTLS CA 编排：受 mTLS 默认关闭抑制，维持挂起；开启 CA 消费方后再评估
  - 状态=阻塞｜原因：mTLS 默认关闭，无 CA 消费方｜触发条件：mTLS 开启且有节点证书签发需求
- [ ] 微信/支付宝直连支付：等商户资质到位再开工
  - 状态=阻塞｜原因：无商户资质，无法对接直连支付｜触发条件：用户提供商户资质（appid/merchant_id/证书）
- [ ] 通道维度偏好：单项增补不构成解禁，维持不做
  - 状态=维持不做｜原因：Herald 单通道已满足，多通道偏好无实际需求｜触发条件：无（拍板关闭）
- [ ] TOUCH-4 触达周期 dash 化：已核实 env 配置口径更优，维持不做
  - 状态=维持不做｜原因：env 配置已满足可调需求，dash 化无增量价值｜触发条件：无（拍板关闭）
- [ ] TOUCH-7 边缘探测结论复用（判定源）：判定源=管理员手动标记已落地（GET/PUT /api/outage + 订阅常附注释行）；探测复用已代决留待
  - 状态=已代决留待（2026-10-16 FW1016D）｜原因：判定源手动标记已落地并测试（outage_mode settings 键+dash 断联容灾卡+订阅逃生注释）；探测复用需 probe 面扩 entry_domain target_kind——属跨组件协议面新能力（agentproto+agent 二进制发布面），设计 2026-10-08 落地口径已判定「不可执行（ProbeReport 只有 tunnel/exit/peer 目标，没有面板入口域名的探测面）」并改走手动标记，本轮代决不重开该裁决，仅补正本行状态与账目｜触发条件：用户点名扩 probe 面（新增 target_kind 与 agent 侧探测实现）后再评估复用
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

## 巡检结论（P1014B）

2026-10-14 核验轮：15 项未勾全部复核，触发条件逐条实证不成立（含外部依赖实测），无可推进项，按闸门维持挂起不代拍板。

实测证据：vitepress 上游 latest release 仍为 v2.0.0-alpha.20（2026-09-04 发布），npm stable 线 1.6.4——上游等待项（撤 pnpm override）触发条件未满足；gh 实测 open PR=0、open issue=0、dependabot open alerts=0、main 最近 4 次 test CI 全 success；全仓 todo 文件仅 ./todo.md 一个（find 零遗漏）；当日批次七~十新增 10 个引用哈希（72a6919..2055362）逐一 merge-base 核在 origin/main。

当日覆盖工作（已完成，完成行留痕）：七批 handler 缝隙 74.5%→75.2%（72a6919）、八批 alloc/cert →89.2%/89.0%（ddb919f）、九批 quota/standby/herald →87.4%/77.2%/86.6%（eb65bc9）、十批 storage/configd/monitor →88.0%/75.0%/89.9%（59f1fc8/0b7e0d9/648202a），各包 -race -count=3 全绿。

依据：外部触发条件未满足（上游未发布/无商户资质/无真机环境/拍板关闭项无解锁条件），无 open issues/PRs，零改动零 commit（纯核验轮）。

## 巡检结论（PATROL1015）

2026-10-15 巡检轮：15 项未勾逐条复核，触发条件仍全部不成立（上游未发布/无商户资质/无真机环境/拍板关闭），无可推进项，按闸门维持挂起不代拍板；覆盖批连推四批，涉 17 包，全部 -race 复核全绿。

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

依据：外部触发条件未满足（上游未发布/无商户资质/无真机环境/拍板关闭项无解锁条件），无可开工功能项；本轮单 commit 纯文档落账，main 与 origin/main 同步。

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
