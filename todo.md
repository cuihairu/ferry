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

- [x] docs 双语化：docs/ 四篇顶层文档（index、herald 对接实录、面板横向对比、文档一致性审计）补英文版，两侧顶部互链切换器（首页用 hero 按钮，因 home 版式 frontmatter 必须在首行），命令/路径/专有名词原样保留——2026-10-15（c6003a9），docs vitepress 构建绿

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

## 巡检结论（P1014B）

2026-10-14 核验轮：15 项未勾全部复核，触发条件逐条实证不成立（含外部依赖实测），无可推进项，按闸门维持挂起不代拍板。

实测证据：vitepress 上游 latest release 仍为 v2.0.0-alpha.20（2026-09-04 发布），npm stable 线 1.6.4——上游等待项（撤 pnpm override）触发条件未满足；gh 实测 open PR=0、open issue=0、dependabot open alerts=0、main 最近 4 次 test CI 全 success；全仓 todo 文件仅 ./todo.md 一个（find 零遗漏）；当日批次七~十新增 10 个引用哈希（72a6919..2055362）逐一 merge-base 核在 origin/main。

当日覆盖工作（已完成，完成行留痕）：七批 handler 缝隙 74.5%→75.2%（72a6919）、八批 alloc/cert →89.2%/89.0%（ddb919f）、九批 quota/standby/herald →87.4%/77.2%/86.6%（eb65bc9）、十批 storage/configd/monitor →88.0%/75.0%/89.9%（59f1fc8/0b7e0d9/648202a），各包 -race -count=3 全绿。

依据：外部触发条件未满足（上游未发布/无商户资质/无真机环境/拍板关闭项无解锁条件），无 open issues/PRs，零改动零 commit（纯核验轮）。

## 巡检结论（PATROL1015）

2026-10-15 巡检轮：15 项未勾逐条复核，触发条件仍全部不成立（上游未发布/无商户资质/无真机环境/拍板关闭），无可推进项，按闸门维持挂起不代拍板；覆盖批连推四批，涉 17 包，全部 -race 复核全绿。

当日覆盖工作（已完成，完成行留痕）：十一批 relaypush/recovery/notifyscan/provision/replenish/config/spool/tunnel（b6f28f8）、十二批 audit/standby/dns/configd（45cc193）、十三批 handler/ratelimit/toucher/backup（14a0187）、十四批 app/handler 分页（720be9b）。十一批为工作区遗留未提交增量（10-14 15:22..15:53 落盘，无活跃会话），本次只读复核+门禁通过后收编提交。

依据：门禁按批跑受影响路径（go test + -race -count=2..3 全净，gofmt/vet 零输出），todo 增量即提即推，main 与 origin/main 同步；覆盖剩余洼地为 cmd 三入口 main() 装配体（覆盖需重构注入）与 DB 错误注入类深分支，不再有低成本批次可推。
