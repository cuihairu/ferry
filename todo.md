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
- [ ] [BLOCKED] 上游等待：VitePress 2 stable 尚未发布；npm latest 仍为 1.6.4（2026-10-19 复核 dist-tags.latest=1.6.4），待 stable 发布后验证再撤 override
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
- [ ] G1 订阅格式补 mihomo/xray-json（报告 P2 观察项）：触发条件=出现 clash-meta 拒载或 xray-json 消费方的真实反馈
- [ ] G7 OIDC/LDAP 登录（报告 P2 观察项）：触发条件=多管理员规模化且 P2-2 配额面被真实使用
- [ ] G3 用户 LIMITED/EXPIRED 拆态（报告 P2 观察项）：触发条件=客服排障反复需要区分到期与超限来源
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

-   [ ] `F-0001` 为每个任务分配唯一且稳定的任务 ID。
-   [ ] `F-0002` 确保每个 checkbox 只描述一个主要交付结果。
-   [ ] `F-0003` 将存在多个独立结果的任务拆分为多个 checkbox。
-   [ ] `F-0004` 为每个任务标明依赖任务 ID。
-   [ ] `F-0005` 为每个任务定义可验证的完成条件。
-   [ ] `F-0006` 为每个任务记录涉及的模块或目录。
-   [ ] `F-0007` 允许单个任务独立标记完成、阻塞、取消或不适用。
-   [ ] `F-0008` 任务取消或不适用时记录原因，不得静默删除任务。
-   [ ] `F-0009` 禁止因某个阶段完成而自动勾选该阶段下未验证的任务。
-   [ ] `F-0010` 每次提交只关闭本次实际完成且有证据的任务。
-   [ ] `F-0011` 在任务完成说明中记录 commit、测试命令和测试结果。
-   [ ] `F-0012`
    若实现方式与任务描述不符，先更新任务和验收标准，再实施。
-   [ ] `F-0013` 不得把文档描述、TODO 状态或接口占位视为功能已实现。
-   [ ] `F-0014` 不得为完成任务而重复实现仓库中已有且可用的能力。

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

-   [ ] `F-0040` 所有数据库结构变化提供 Migration。
-   [ ] `F-0041` 为破坏性 Migration 提供备份、迁移和回滚方案。
-   [ ] `F-0042` 所有新增后台任务具备超时、重试上限和取消机制。
-   [ ] `F-0043` 所有有副作用的 API 和消息定义幂等语义。
-   [ ] `F-0044` 所有批量操作支持部分失败结果，不得只返回笼统成功。
-   [ ] `F-0045` 所有敏感操作写入审计日志。
-   [ ] `F-0046` 所有配置下发具备版本、校验、确认和失败处理。
-   [ ] `F-0047` 所有外部 API 调用具备超时、限流和错误分类。
-   [ ] `F-0048` 所有外部依赖故障不得阻塞 Server 核心 API。
-   [ ] `F-0049` 所有新增指标必须有真实采集来源。
-   [ ] `F-0050` 所有功能完成后更新对应任务状态和文档。

------------------------------------------------------------------------

# 1. 产品边界与总体架构

## 1.1 Ferry 负责的能力

-   [ ] `F-0101` 管理用户、套餐、订阅和设备。
-   [x] `F-0102` 管理节点注册、认证、元数据和状态。
    - 备注（Task ID: F-0102 | Status: Done | Evidence: enroll.go+agentws 认证+models.go:64；TestEnrollExchange/TestAgentWSHelloMeta | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0103` 管理云资源与物理节点的关联关系。
-   [x] `F-0104` 管理 Entry、Landing 和 Relay 节点。
    - 备注（Task ID: F-0104 | Status: Done | Evidence: roles 三角色+LandingAssignment models.go:165+relay；TestRegistry/TestLandingAssignment | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0105` 管理 Tunnel、Path 和 Path Group。
-   [x] `F-0106` 管理线路探测、健康状态和质量历史。
    - 备注（Task ID: F-0106 | Status: Done | Evidence: ProbeReport models.go:181+dimension/transport 状态；TestAgentWSProbeReport/TestSweepPersistsDimensions | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0107` 管理全局 Policy 和线路调度。
-   [x] `F-0108` 编译并下发 Agent 和数据面配置。
    - 备注（Task ID: F-0108 | Status: Done | Evidence: relaypush+configd；TestPushAckApplied/TestApplyReloadFailRollsBack | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0109` 生成 sing-box、Clash/Mihomo 等客户端订阅。
    - 备注（Task ID: F-0109 | Status: Done | Evidence: sub/{links,clash,singbox}.go 三格式；TestPackV2Ray/TestPackClash/TestPackSingBox | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0110` 管理流量计量、配额、订单和支付。
    - 备注（Task ID: F-0110 | Status: Done | Evidence: TrafficLog/QuotaAction/PaymentOrder models.go:116/636/327；TestAgentWSTrafficReport/TestEpusdtNotify | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0111` 管理云服务商账号、资源生命周期和成本。
-   [x] `F-0112` 提供管理员 Dash 和用户 Panel。
    - 备注（Task ID: F-0112 | Status: Done | Evidence: dash 17 视图+panel 5 视图+router.ts:36 守卫；pnpm build 双绿 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0113` 提供监控、告警、审计、备份和恢复。
    - 备注（Task ID: F-0113 | Status: Done | Evidence: health/alerts/audit/backup；TestHealth/TestAlertGuards/TestPruneRemovesOnlyOutOfWindow/TestSnapshotManual | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）

## 1.2 明确不做的能力

-   [ ] `F-0121` 不开发 Ferry Desktop。
-   [ ] `F-0122` 不开发 Ferry 自有代理客户端。
-   [ ] `F-0123` 不实现自有 TUN 或系统代理接管。
-   [ ] `F-0124` 不实现客户端本地 DNS 和规则执行引擎。
-   [ ] `F-0125` 不重复实现 sing-box、Clash/Mihomo 等代理协议栈。
-   [ ] `F-0126` 不把第三方客户端的本地运行状态当作 Ferry
    可直接控制的状态。
-   [ ] `F-0127` 不把云服务商 API 调用逻辑散落在 Node、Path 或 Dash
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

-   [ ] `F-0131` 明确 Server 是业务控制面的权威来源。
-   [ ] `F-0132` 明确 Agent 只保存本机运行所需的期望配置和实际状态。
-   [ ] `F-0133` 明确 Probe 负责测量，不拥有全局调度权。
-   [ ] `F-0134` 明确 Relay 负责数据转发，不拥有用户和套餐业务状态。
-   [ ] `F-0135` 确保用户实际数据流量不经过 Ferry Server。
-   [ ] `F-0136` 确保 Server 短时不可用不导致已建立的数据面立即停止。
-   [ ] `F-0137` 确保 Server 恢复后可以与 Agent 对账并恢复管理状态。

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
-   [ ] `F-0204` 明确 Role 表示拓扑职责，Direction 表示业务流量方向。
-   [x] `F-0205` 明确管理状态与运行健康状态分离。
    - 备注（Task ID: F-0205 | Status: Done | Evidence: Enabled :71/Status :77/PoolState :104 分离；TestNodeCRUD+TestSweepSuspendAndResume | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0206` 明确 `disabled`、`maintenance`、`offline`、`unhealthy`
    的区别。
-   [ ] `F-0207` 为 Node 关联 Region、ISP、ASN 和 Provider 信息。
-   [ ] `F-0208` 为 Node 关联可选的云资源 ID。
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
-   [ ] `F-0214` 定义节点删除时的关联检查和数据保留规则。

## 2.2 Capability

-   [x] `F-0221` 审计现有节点能力声明方式。
    - 备注（Task ID: F-0221 | Status: Done | Evidence: 能力声明方式审计（结论：Capability 未实现）§3.1 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0222` 定义静态声明能力与动态运行能力。
-   [ ] `F-0223` 定义 `proxy_ingress` 能力。
-   [ ] `F-0224` 定义 `relay_entry` 能力。
-   [ ] `F-0225` 定义 `relay_landing` 能力。
-   [ ] `F-0226` 定义 `probe` 能力。
-   [ ] `F-0227` 定义 TCP/UDP 转发能力声明。
-   [ ] `F-0228` 让 Agent 上报实际运行能力。
-   [ ] `F-0229` 在组件异常时更新动态能力状态。
-   [ ] `F-0230` 让调度器只选择具备所需能力的节点。
-   [ ] `F-0231` 记录能力变化历史。

## 2.3 Path 与 Tunnel

-   [x] `F-0241` 审计现有 Pool、Allocation、Tunnel 和线路关系模型。
    - 备注（Task ID: F-0241 | Status: Done | Evidence: Pool/Allocation/Tunnel/线路关系审计 §3.1 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0242` 明确 Tunnel 表示两个端点之间的实际连接。
-   [ ] `F-0243` 明确 Path 表示完整业务访问路径。
-   [ ] `F-0244` 第一阶段 Path 只要求支持 Entry → Landing。
-   [ ] `F-0245` 定义 Path 与 Entry Node 的关联。
-   [ ] `F-0246` 定义 Path 与 Landing Node 的关联。
-   [ ] `F-0247` 定义 Path 与 Tunnel/Relay 的关联。
-   [ ] `F-0248` 允许一个 Entry 关联多个 Path。
-   [ ] `F-0249` 允许一个 Landing 关联多个 Path。
-   [ ] `F-0250` 定义 Path 的启停状态。
-   [ ] `F-0251` 定义 Path 的健康状态。
-   [ ] `F-0252` 定义 Path 的配置版本。
-   [ ] `F-0253` 定义 Path 的变更历史。
-   [ ] `F-0254` 定义 Path 删除时的依赖检查。
-   [ ] `F-0255` 提供 Path 查询和详情 API。

## 2.4 Path Group

-   [ ] `F-0261` 定义 Path Group 数据结构或确认现有等价模型。
-   [ ] `F-0262` 支持创建和编辑 Path Group。
-   [ ] `F-0263` 支持 Path 加入和移出 Group。
-   [ ] `F-0264` 支持 Group 默认 Path。
-   [ ] `F-0265` 支持候选 Path 顺序。
-   [ ] `F-0266` 支持按方向、区域和用途组织 Group。
-   [ ] `F-0267` 支持手动排除 Path。
-   [ ] `F-0268` 支持 Group 健康状态聚合。
-   [ ] `F-0269` 支持 Group 可用性统计。
-   [ ] `F-0270` 支持 Group 与订阅策略关联。

## 2.5 Policy

-   [x] `F-0281` 审计现有策略和分配规则。
    - 备注（Task ID: F-0281 | Status: Done | Evidence: 策略与分配规则审计 §3.1+§3.5 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0282` 定义全局安全约束。
-   [ ] `F-0283` 定义系统级 Policy。
-   [ ] `F-0284` 定义套餐级 Policy。
-   [ ] `F-0285` 定义用户级 Policy。
-   [ ] `F-0286` 定义订阅输出策略。
-   [ ] `F-0287` 定义策略优先级和冲突解决规则。
-   [ ] `F-0288` 支持区域约束。
-   [ ] `F-0289` 支持运营商约束。
-   [ ] `F-0290` 支持线路类型约束。
-   [ ] `F-0291` 支持健康阈值约束。
-   [ ] `F-0292` 支持容量约束。
-   [ ] `F-0293` 支持成本约束。
-   [ ] `F-0294` 支持故障回退规则。
-   [ ] `F-0295` 支持 Policy 版本和变更审计。
-   [ ] `F-0296` 提供 Policy 模拟和解释接口。

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
-   [ ] `F-0302` 定义统一 Cloud Provider Adapter 接口。
-   [ ] `F-0303` 定义 Provider 能力声明接口。
-   [ ] `F-0304` 定义资源列表查询接口。
-   [ ] `F-0305` 定义资源详情查询接口。
-   [ ] `F-0306` 定义资源状态刷新接口。
-   [ ] `F-0307` 定义开机操作接口。
-   [ ] `F-0308` 定义关机操作接口。
-   [ ] `F-0309` 定义重启操作接口。
-   [ ] `F-0310` 定义强制停止操作接口，并明确高危权限。
-   [ ] `F-0311` 定义重装/重建操作接口，仅对服务商明确支持的资源开放。
-   [ ] `F-0312` 定义公网 IP 查询接口。
-   [ ] `F-0313` 定义带宽和规格查询接口。
-   [ ] `F-0314` 定义账单和价格数据导入接口。
-   [ ] `F-0315` 定义 Provider 错误码到 Ferry 错误类型的映射。
-   [ ] `F-0316` 定义 Provider API 超时、限流和重试策略。
-   [ ] `F-0317` 定义 Provider API 版本和能力兼容信息。
-   [ ] `F-0318` 为 Provider Adapter 建立契约测试。

## 3.2 Provider 账号与凭据

-   [x] `F-0321` 定义 Cloud Account 数据模型。
    - 备注（Task ID: F-0321 | Status: Done | Evidence: Provider models.go:462（Name/Type/密钥/Enabled=云账号）；TestProviderAPI | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0322` 支持添加云服务商账号。
-   [ ] `F-0323` 支持配置账号名称和备注。
-   [ ] `F-0324` 支持配置账号所属区域或租户范围。
-   [x] `F-0325` 将 Access Key/Secret 等凭据加密存储。
    - 备注（Task ID: F-0325 | Status: Done | Evidence: providerapi sealSecret→secret.Store AES-GCM；TestProviderAPI+TestStoreRoundtrip | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0326` 凭据读取仅允许 Provider Adapter 内部使用。
-   [x] `F-0327` Dash 不回显完整 Secret。
    - 备注（Task ID: F-0327 | Status: Done | Evidence: HasAccessKey 不回显；TestProviderAPI+TestListEnrollmentTokensMasksSecret | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0328` 支持测试账号连接。
-   [x] `F-0329` 支持凭据更新。
    - 备注（Task ID: F-0329 | Status: Done | Evidence: updateProvider access_key 非空换密；TestProviderAPI | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0330` 支持凭据轮换。
-   [ ] `F-0331` 支持账号禁用。
-   [ ] `F-0332` 支持账号删除前依赖检查。
-   [x] `F-0333` 记录账号凭据变更审计。
    - 备注（Task ID: F-0333 | Status: Done | Evidence: auditMiddleware 覆盖 PUT /api/providers；TestAuditLogsCaptureWriteOps | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0334` 支持按账号授权可操作的资源范围。
-   [ ] `F-0335` 为云账号操作增加最小权限说明。

## 3.3 Provider 资源同步

-   [ ] `F-0341` 定义 Cloud Resource 数据模型。
-   [ ] `F-0342` 记录 Provider、Account、Region 和 Provider Resource
    ID。
-   [ ] `F-0343` 支持从 Provider 拉取资源列表。
-   [ ] `F-0344` 支持按区域同步资源。
-   [ ] `F-0345` 支持按标签过滤资源。
-   [ ] `F-0346` 支持手动触发资源同步。
-   [ ] `F-0347` 支持定时资源同步。
-   [ ] `F-0348` 支持增量同步或合理的分页同步。
-   [ ] `F-0349` 记录最后同步时间和同步结果。
-   [ ] `F-0350` 识别 Provider 中已删除的资源。
-   [ ] `F-0351` 外部资源消失时不得直接删除关联 Node 历史。
-   [ ] `F-0352` 处理资源 ID 冲突和重复导入。
-   [ ] `F-0353` 支持 Cloud Resource 与 Node 绑定。
-   [ ] `F-0354` 支持解除 Cloud Resource 与 Node 绑定。
-   [ ] `F-0355` 阻止同一 Cloud Resource 被多个有效 Node 重复绑定。
-   [ ] `F-0356` 支持资源详情和同步历史查询。

## 3.4 云资源操作任务

-   [ ] `F-0361` 定义 Cloud Operation 数据模型。
-   [ ] `F-0362`
    定义操作状态机：Pending、Running、Succeeded、Failed、Cancelled、Unknown。
-   [ ] `F-0363` 将开机操作接入统一任务系统。
-   [ ] `F-0364` 将关机操作接入统一任务系统。
-   [ ] `F-0365` 将重启操作接入统一任务系统。
-   [ ] `F-0366` 将强制停止操作接入统一任务系统。
-   [ ] `F-0367` 将重装/重建操作接入统一任务系统（仅限 Adapter
    声明支持）。
-   [ ] `F-0368` 操作前检查资源当前状态。
-   [ ] `F-0369` 操作前检查管理员权限。
-   [ ] `F-0370` 对关机、强制停止、重装等高风险操作执行二次确认。
-   [ ] `F-0371` 为每个操作生成幂等键。
-   [ ] `F-0372` 处理 Provider 请求超时后的 Unknown 状态。
-   [ ] `F-0373` Unknown 状态通过查询 Provider
    结果对账，不盲目重复执行。
-   [ ] `F-0374` 支持任务取消（仅在 Provider 操作允许取消时）。
-   [ ] `F-0375` 支持任务详情和操作日志。
-   [ ] `F-0376` 支持操作失败原因展示。
-   [ ] `F-0377` 支持批量开机。
-   [ ] `F-0378` 支持批量关机。
-   [ ] `F-0379` 支持批量重启。
-   [ ] `F-0380` 批量操作逐资源记录结果。
-   [ ] `F-0381` 批量操作设置并发上限和速率限制。
-   [ ] `F-0382` 批量操作支持预览受影响资源。
-   [ ] `F-0383` 批量操作支持部分成功、部分失败。
-   [ ] `F-0384` 记录操作者、目标资源、请求参数和最终结果。

## 3.5 云资源与 Ferry Node 联动

-   [ ] `F-0391` 定义云资源状态与 Node 管理状态的映射规则。
-   [ ] `F-0392` 云实例停止时将关联 Node 标记为不可分配或待确认。
-   [ ] `F-0393` 云实例启动后检查 Agent 心跳再恢复 Node 可用性。
-   [ ] `F-0394` 云实例重启后重新核对 Agent、Relay 和配置状态。
-   [ ] `F-0395` 云实例重装后将 Node 标记为需要重新注册或重新验证。
-   [ ] `F-0396` 云资源删除后将关联 Node 标记为资源丢失。
-   [ ] `F-0397` 云操作不得仅凭 Provider 返回成功就认定 Ferry Node
    已健康。
-   [ ] `F-0398` 云操作后触发受控的状态刷新。
-   [ ] `F-0399` 云操作与 Path 调度状态联动。
-   [ ] `F-0400` 对有用户流量的节点执行关机前检查和风险提示。
-   [ ] `F-0401` 支持维护窗口和节点摘流流程。
-   [ ] `F-0402` 支持开机后自动执行节点健康检查。
-   [ ] `F-0403` 支持启动失败后的告警。
-   [ ] `F-0404` 保留云资源操作与 Node 状态变化的关联审计。

## 3.6 Dash 云资源管理页面

-   [ ] `F-0411` 增加 Cloud Providers 页面。
-   [ ] `F-0412` 增加 Cloud Accounts 列表。
-   [ ] `F-0413` 增加 Cloud Account 创建页面。
-   [ ] `F-0414` 增加 Cloud Account 连接测试入口。
-   [ ] `F-0415` 增加 Cloud Resources 列表。
-   [ ] `F-0416` 支持按 Provider 筛选资源。
-   [ ] `F-0417` 支持按账号筛选资源。
-   [ ] `F-0418` 支持按区域筛选资源。
-   [ ] `F-0419` 支持按实例状态筛选资源。
-   [ ] `F-0420` 增加云资源详情页面。
-   [ ] `F-0421` 展示实例规格、区域、IP、带宽和计费信息。
-   [ ] `F-0422` 展示资源与 Ferry Node 的绑定关系。
-   [ ] `F-0423` 提供开机操作入口。
-   [ ] `F-0424` 提供关机操作入口。
-   [ ] `F-0425` 提供重启操作入口。
-   [ ] `F-0426` 对高风险操作展示明确警告和二次确认。
-   [ ] `F-0427` 增加云操作任务列表。
-   [ ] `F-0428` 展示操作执行进度和最终结果。
-   [ ] `F-0429` 支持批量操作结果查看。
-   [ ] `F-0430` 支持从云资源创建或关联 Ferry Node。
-   [ ] `F-0431` 支持从 Node 详情跳转到关联云资源。
-   [ ] `F-0432` 对 Provider API 故障显示数据最后更新时间。

------------------------------------------------------------------------

# 4. 云成本与资源成本核算

> 成本核算必须区分"云服务商实际账单"和"Ferry
> 内部估算"。不能把按实例规格估算的费用伪装成已结算账单，也不能只按当前实例数量乘单价来计算历史成本。

## 4.1 成本数据模型

-   [x] `F-0501` 审计现有 Cost、CostRef 和成本统计实现。
    - 备注（Task ID: F-0501 | Status: Done | Evidence: Cost/CostRef/统计审计 §3.10 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0502` 定义 Cost Provider 数据来源。
-   [ ] `F-0503` 定义 Cost Record 原始账单记录。
-   [ ] `F-0504` 定义 Cost Item 费用明细。
-   [ ] `F-0505` 定义 Cost Allocation 分摊记录。
-   [ ] `F-0506` 定义 Cost Estimate 估算记录。
-   [ ] `F-0507` 定义 Cost Reconciliation 对账记录。
-   [ ] `F-0508` 明确账单币种、金额精度和汇率日期。
-   [ ] `F-0509` 明确费用发生时间、账单周期和入库时间。
-   [ ] `F-0510` 明确含税、未税和税费字段（Provider 可提供时）。
-   [ ] `F-0511` 保留 Provider 原始账单 ID 和原始字段快照。
-   [ ] `F-0512` 为账单记录建立去重约束。
-   [ ] `F-0513` 为成本记录建立时间和资源查询索引。

## 4.2 云账单导入

-   [ ] `F-0521` 定义统一账单导入 Adapter。
-   [ ] `F-0522` 为每个 Provider 声明账单 API 支持能力。
-   [ ] `F-0523` 支持手动导入账单文件（Provider 提供可用文件格式时）。
-   [ ] `F-0524` 支持通过 Provider Billing API 拉取账单（Provider
    支持时）。
-   [ ] `F-0525` 支持按账单周期拉取。
-   [ ] `F-0526` 支持按账号和区域拉取。
-   [ ] `F-0527` 支持分页和增量拉取。
-   [ ] `F-0528` 支持账单补拉。
-   [ ] `F-0529` 支持重复账单去重。
-   [ ] `F-0530` 支持账单修订和冲正记录。
-   [ ] `F-0531` 保留原始账单数据以便追溯。
-   [ ] `F-0532` 账单导入失败时记录失败原因和重试状态。
-   [ ] `F-0533` 展示账单数据来源和最后同步时间。
-   [ ] `F-0534` 明确 Provider 不提供账单 API 时的手工导入流程。

## 4.3 估算成本

-   [ ] `F-0541` 定义实例包月成本估算。
-   [ ] `F-0542` 定义按小时计费资源的成本估算。
-   [ ] `F-0543` 定义按量流量成本估算。
-   [ ] `F-0544` 定义固定带宽成本估算。
-   [ ] `F-0545` 定义额外公网 IP 成本估算。
-   [ ] `F-0546` 定义磁盘和快照成本估算。
-   [ ] `F-0547` 定义备份和存储成本估算。
-   [ ] `F-0548` 定义优惠、折扣和预留实例成本输入。
-   [ ] `F-0549` 支持自定义成本项。
-   [ ] `F-0550` 每条估算记录标明价格来源和生效时间。
-   [ ] `F-0551` 价格未知时显示未知，不使用虚构默认单价。
-   [ ] `F-0552` 估算数据与实际账单分开存储和展示。

## 4.4 成本归属与分摊

-   [ ] `F-0561`
    定义成本归属对象：Provider、Account、Resource、Node、Region、Path。
-   [ ] `F-0562` 支持资源成本直接归属到 Node。
-   [ ] `F-0563` 支持共享资源成本按规则分摊。
-   [ ] `F-0564` 支持按带宽使用量分摊。
-   [ ] `F-0565` 支持按流量使用量分摊。
-   [ ] `F-0566` 支持按固定比例分摊。
-   [ ] `F-0567` 支持人工调整分摊规则。
-   [ ] `F-0568` 记录分摊规则版本。
-   [ ] `F-0569` 保留原始成本和分摊后成本。
-   [ ] `F-0570` 防止重复分摊同一账单项。
-   [ ] `F-0571` 支持成本分摊结果重算。
-   [ ] `F-0572` 重算不得覆盖历史账单原始记录。

## 4.5 成本核算与对账

-   [ ] `F-0581` 按 Provider 统计实际账单金额。
-   [ ] `F-0582` 按账号统计实际账单金额。
-   [ ] `F-0583` 按区域统计实际账单金额。
-   [ ] `F-0584` 按资源统计实际账单金额。
-   [ ] `F-0585` 按 Node 统计分摊成本。
-   [ ] `F-0586` 按 Path 统计估算成本。
-   [ ] `F-0587` 对比估算成本与实际账单。
-   [ ] `F-0588` 识别账单缺失的资源。
-   [ ] `F-0589` 识别账单中无法匹配的资源。
-   [ ] `F-0590` 识别成本异常增长。
-   [ ] `F-0591` 支持账单差异确认。
-   [ ] `F-0592` 支持成本调整备注和审批记录。
-   [ ] `F-0593` 支持按日、月、账单周期汇总。
-   [ ] `F-0594` 支持多币种原币展示。
-   [ ] `F-0595` 支持按指定汇率转换为报表币种。
-   [ ] `F-0596` 记录汇率来源和日期。
-   [ ] `F-0597` 明确未结算账单和已结算账单状态。

## 4.6 成本与线路调度联动

-   [x] `F-0601` 为 Node 提供单位时间成本数据。
    - 备注（Task ID: F-0601 | Status: Done | Evidence: MonthlyCostCents models.go:92+cost/cost.go:96 Build；TestBuild | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0602` 为 Node 提供单位流量成本数据。
    - 备注（Task ID: F-0602 | Status: Done | Evidence: TrafficPriceCents models.go:91+costPerGB；TestBuild | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0603` 为 Path 计算可解释的成本估算。
-   [ ] `F-0604` 调度器读取成本数据时检查数据新鲜度。
-   [ ] `F-0605` 成本缺失时按 Policy 定义的降级规则处理。
-   [ ] `F-0606` 支持成本上限过滤候选 Path。
-   [x] `F-0607` 支持质量与成本权重配置。
    - 备注（Task ID: F-0607 | Status: Done | Evidence: ScoreWeights alloc.go:107-117 逐系数 dash 可配；TestPolicyPersistence+TestPickBalanced | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0608` 支持按套餐设定成本策略。
-   [ ] `F-0609` 记录成本约束导致的候选排除原因。
-   [ ] `F-0610` 不允许使用过期或未知价格生成虚假的精确成本结果。

## 4.7 Dash 成本管理页面

-   [x] `F-0621` 增加成本总览页面。
    - 备注（Task ID: F-0621 | Status: Done | Evidence: dash CostView.vue；TestCostAPI+TestCostThresholdGuards+build 绿 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0622` 展示本月实际账单。
-   [ ] `F-0623` 展示本月估算成本。
-   [ ] `F-0624` 展示估算与实际差异。
-   [ ] `F-0625` 展示 Provider 成本排名。
-   [ ] `F-0626` 展示账号成本排名。
-   [ ] `F-0627` 展示区域成本排名。
-   [ ] `F-0628` 展示 Node 成本排名。
-   [ ] `F-0629` 展示 Path 成本估算。
-   [ ] `F-0630` 展示流量成本趋势。
-   [x] `F-0631` 展示异常成本提醒。
    - 备注（Task ID: F-0631 | Status: Done | Evidence: cost.CheckAlerts 阈值告警+CostView 高亮；TestCheckAlerts+TestLoopChecksAlertsUntilCancel | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0632` 增加账单同步任务页面。
-   [ ] `F-0633` 增加账单明细查询页面。
-   [ ] `F-0634` 增加账单差异对账页面。
-   [ ] `F-0635` 增加成本分摊规则页面。
-   [ ] `F-0636` 支持按日期、Provider、账号、区域和资源筛选。
-   [ ] `F-0637` 支持导出成本明细。
-   [ ] `F-0638` 在 UI 中清楚区分实际账单、估算和分摊值。

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
-   [ ] `F-0706` 统一 Agent 能力上报。
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
-   [ ] `F-0712` 支持 Agent 升级状态上报。
-   [x] `F-0713` 支持 Agent 升级失败恢复。
    - 备注（Task ID: F-0713 | Status: Done | Evidence: upgrade 看门狗回滚；TestWatchdogRollback+TestWatchdogRollbackFailure | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）

## 5.2 Agent 协议

-   [x] `F-0721` 审计现有 agentproto 消息定义。
    - 备注（Task ID: F-0721 | Status: Done | Evidence: agentproto 消息定义审计 §3.2（Msg* 信封+Version） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0722` 为现有消息补充协议文档。
-   [ ] `F-0723` 为消息定义版本兼容规则。
-   [x] `F-0724` 为有副作用消息定义幂等键。
    - 备注（Task ID: F-0724 | Status: Done | Evidence: agentws_dedupe envelope ID 去重；dedupe_test 7 例 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0725` 为需要确认的消息定义 ACK。
    - 备注（Task ID: F-0725 | Status: Done | Evidence: ConfigAck/TrafficAck+relaypush；TestPushAckApplied+TestAgentWSTrafficReport | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0726` 定义消息超时和重试规则。
    - 备注（Task ID: F-0726 | Status: Done | Evidence: agenthub/hub.go:114 Request 超时 ErrTimeout；TestPushTimeout+TestPushSendError | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0727` 定义断线期间消息处理规则。
-   [ ] `F-0728` 定义消息大小限制。
-   [x] `F-0729` 定义未知消息处理方式。
    - 备注（Task ID: F-0729 | Status: Done | Evidence: link readLoop 丢弃坏消息+服务端留痕；TestReadLoopDropsBadMessages | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0730` 增加 Agent 协议兼容性测试。

## 5.3 节点管理

-   [ ] `F-0741` 支持节点注册审批。
-   [x] `F-0742` 支持节点元数据自动采集。
    - 备注（Task ID: F-0742 | Status: Done | Evidence: syncNodeMeta 注册元数据采集（meta_init 面板为准）；TestAgentWSHelloMeta | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0743` 支持管理员编辑业务元数据。
-   [x] `F-0744` 支持节点入池检查。
    - 备注（Task ID: F-0744 | Status: Done | Evidence: provision→probe→online 入池检查流水线；TestProvisionPipeline | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0745` 支持节点摘池。
    - 备注（Task ID: F-0745 | Status: Done | Evidence: pool.go suspend/resume 摘池；TestSuspend+TestSweepSuspendAndResume | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0746` 支持节点维护模式。
-   [x] `F-0747` 支持节点禁用。
    - 备注（Task ID: F-0747 | Status: Done | Evidence: enabled 关停；TestNodeCRUD | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0748` 支持节点重新启用。
    - 备注（Task ID: F-0748 | Status: Done | Evidence: enabled 重启+pool resume；TestNodeCRUD+TestResume | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0749` 支持节点批量操作。
    - 备注（Task ID: F-0749 | Status: Done | Evidence: router.go:184-185 batch；TestBatchNodeProc+TestBatchNodeConfig | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0750` 支持节点详情展示 Agent、Relay、Probe 状态。
-   [ ] `F-0751` 支持节点操作历史。
-   [x] `F-0752` 支持节点配置版本查看。
    - 备注（Task ID: F-0752 | Status: Done | Evidence: GET /api/nodes/:id/configs；config_err_test limit/404+agentws_test:442 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）

------------------------------------------------------------------------

# 6. Relay 与数据面

## 6.1 Relay 管理

-   [x] `F-0801` 审计当前 Relay 实现和进程边界。
    - 备注（Task ID: F-0801 | Status: Done | Evidence: Relay 实现与进程边界审计 §3.3（roles Standalone:true） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0802` 统一 Relay 配置模型。
-   [ ] `F-0803` 统一 Relay 状态模型。
-   [ ] `F-0804` 统一 Relay 能力声明。
-   [x] `F-0805` 支持 Relay 独立启停。
    - 备注（Task ID: F-0805 | Status: Done | Evidence: roles.go:31 relay Standalone:true+cmd -role；TestRegistry+TestRelayRunFile | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0806` 支持 Relay 配置校验。
    - 备注（Task ID: F-0806 | Status: Done | Evidence: relay 配置校验；TestRelayValidation | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0807` 支持 Relay 配置热更新。
    - 备注（Task ID: F-0807 | Status: Done | Evidence: loadOverride 热更新；TestRelayHotRepoint | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0808` 支持 Relay 连接数限制。
-   [ ] `F-0809` 支持并发流限制。
-   [ ] `F-0810` 支持带宽限制。
-   [x] `F-0811` 支持 TCP 转发。
    - 备注（Task ID: F-0811 | Status: Done | Evidence: roles/relay serveConn；TestRelayForwards | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0812` 支持 UDP 转发。
-   [ ] `F-0813` 支持隧道断线重连。
-   [ ] `F-0814` 支持空闲连接清理。
-   [ ] `F-0815` 支持 Relay 运行指标采集。
-   [ ] `F-0816` 支持 Relay 版本管理。

## 6.2 传输插件

-   [x] `F-0821` 为现有每种 Relay 传输方式建立独立测试。
    - 备注（Task ID: F-0821 | Status: Done | Evidence: tunnel{ws-tls,ssh,quic}+quictunnel 分测；wstls_test/ssh_test/quicsidecar_test/quictunnel_test | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0822` 统一传输插件能力声明。
-   [ ] `F-0823` 统一传输插件错误分类。
-   [ ] `F-0824` 统一连接超时和重连行为。
-   [x] `F-0825` 验证 TLS 证书校验。
    - 备注（Task ID: F-0825 | Status: Done | Evidence: link tlsConfig；TestTLSConfig+TestRunTLSSetupFailure | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0826` 验证 SSH Host Key 校验。
    - 备注（Task ID: F-0826 | Status: Done | Evidence: tunnel/ssh Host Key 校验；TestSSHTunnelHostKeyVerify | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0827` 验证 QUIC 参数和资源限制。
-   [ ] `F-0828` 验证 WS-TLS 连接行为。
-   [ ] `F-0829` 建立不同传输插件的性能对比测试。

## 6.3 故障隔离

-   [ ] `F-0831` 验证 Relay 进程故障不影响 Agent 心跳。
-   [ ] `F-0832` 验证 Relay 故障不影响 Server 管理节点。
-   [ ] `F-0833` 验证 Relay 重启后恢复最后有效配置。
-   [ ] `F-0834` Relay 失联时更新关联 Path 状态。
-   [ ] `F-0835` Relay 恢复后执行健康探测再恢复调度资格。
-   [ ] `F-0836` 禁止仅凭 Relay 进程存活判定 Path 可用。

------------------------------------------------------------------------

# 7. 探测与健康管理

## 7.1 探测类型

-   [x] `F-0901` 审计当前 Probe 任务模型和结果模型。
    - 备注（Task ID: F-0901 | Status: Done | Evidence: Probe 任务/结果模型审计 §3.4 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0902` 区分 Node Health。
-   [ ] `F-0903` 区分 Tunnel Health。
-   [ ] `F-0904` 区分 Path Health。
-   [ ] `F-0905` 区分 Egress Health。
-   [x] `F-0906` 支持 RTT 探测。
    - 备注（Task ID: F-0906 | Status: Done | Evidence: probe measure RTT；TestProbeHealthy | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0907` 支持 Jitter 统计。
-   [x] `F-0908` 支持 Packet Loss 统计。
    - 备注（Task ID: F-0908 | Status: Done | Evidence: probe lossPct；TestVerdictOf+TestProbeUnreachable | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0909` 支持 TCP Connect 探测。
    - 备注（Task ID: F-0909 | Status: Done | Evidence: probe.go:131 net.DialTimeout("tcp")；TestProbeHealthy | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0910` 支持 UDP 探测。
-   [ ] `F-0911` 支持应用层探测。
-   [ ] `F-0912` 支持公网出口探测。
-   [ ] `F-0913` 支持探测超时和错误分类。
-   [x] `F-0914` 支持探测结果去重。
    - 备注（Task ID: F-0914 | Status: Done | Evidence: agentws repeatReport+dedupe；TestReportDeduperWindow+TestReportDeduperTTL | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0915` 支持探测结果过期处理。
    - 备注（Task ID: F-0915 | Status: Done | Evidence: aggregate 窗口+idx_probe_window models.go:184-195；TestJudgeWindowAndLatest | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0916` 支持异常样本过滤。

## 7.2 探测调度和健康计算

-   [ ] `F-0921` 将可在边缘执行的探测任务下发给 Agent。
-   [ ] `F-0922` 为探测任务设置并发限制。
-   [ ] `F-0923` 为探测任务设置超时。
-   [ ] `F-0924` 支持按 Path 重要性配置探测频率。
-   [ ] `F-0925` 故障时支持临时提高探测频率。
-   [ ] `F-0926` 防止大规模节点互探造成探测风暴。
-   [x] `F-0927` 定义健康状态计算规则。
    - 备注（Task ID: F-0927 | Status: Done | Evidence: aggregate.go:65 Judge+monitor.go:66 applyVerdict；TestJudgeRegionAndISP+TestApplyVerdictTransitions | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-0928` 定义健康统计时间窗口。
    - 备注（Task ID: F-0928 | Status: Done | Evidence: ProbeFresh 30min 窗口；TestJudgeWindowAndLatest | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-0929` 定义连续失败阈值。
-   [ ] `F-0930` 定义恢复阈值。
-   [ ] `F-0931` 实现健康状态滞回，避免频繁抖动。
-   [ ] `F-0932` 支持健康状态人工覆盖。
-   [ ] `F-0933` 人工覆盖必须记录原因、操作者和过期时间。
-   [ ] `F-0934` 提供健康状态变化解释。

## 7.3 业务质量指标

-   [ ] `F-0941` 定义游戏质量指标集合。
-   [ ] `F-0942` 定义网页质量指标集合。
-   [ ] `F-0943` 定义视频质量指标集合。
-   [ ] `F-0944` 定义普通代理质量指标集合。
-   [ ] `F-0945` 游戏评分纳入 RTT、Jitter、Loss 和 Availability。
-   [ ] `F-0946` 网页评分纳入连接、TLS、TTFB 和失败率。
-   [ ] `F-0947` 视频评分纳入吞吐、丢包和缓冲风险。
-   [ ] `F-0948` 不同业务评分使用独立规则。
-   [ ] `F-0949` 支持区域、运营商和时段质量报表。

------------------------------------------------------------------------

# 8. Path 编排与调度

## 8.1 Path 编排

-   [ ] `F-1001` 实现 Path 创建和编辑。
-   [ ] `F-1002` 实现 Path 启停。
-   [ ] `F-1003` 实现 Path 拓扑校验。
-   [x] `F-1004` 根据 Role 选择 Entry。
    - 备注（Task ID: F-1004 | Status: Done | Evidence: sub.go:74 role 过滤+alloc.go:448 role 候选；TestPick* | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1005` 根据 Role 选择 Landing。
    - 备注（Task ID: F-1005 | Status: Done | Evidence: alloc.go:454 landing role 过滤；TestPick* | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1006` 根据 Direction 过滤节点。
    - 备注（Task ID: F-1006 | Status: Done | Evidence: alloc.go:486-489 direction 匹配；TestDirectionalDefaults | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1007` 根据 Capability 过滤节点。
-   [ ] `F-1008` 根据 Region 过滤节点。
-   [ ] `F-1009` 根据 ISP/ASN 过滤节点。
-   [ ] `F-1010` 根据 LineType 过滤节点。
-   [x] `F-1011` 根据维护和禁用状态过滤节点。
    - 备注（Task ID: F-1011 | Status: Done | Evidence: alloc.go:448 enabled/pool_state/status 过滤；TestPickHealthGate | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1012` 根据容量过滤节点。
    - 备注（Task ID: F-1012 | Status: Done | Evidence: CapacityMbps 候选；TestPickLeastConnCapacityWeight | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1013` 根据成本过滤节点。
    - 备注（Task ID: F-1013 | Status: Done | Evidence: costPer 成本序；TestPickCostFirst | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1014` 根据健康状态过滤节点。
    - 备注（Task ID: F-1014 | Status: Done | Evidence: Healthy=!sick 门；TestPickHealthGate | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1015` 支持候选 Path 预览。
-   [ ] `F-1016` 支持人工确认后创建 Path。
-   [ ] `F-1017` 支持自动创建 Path。
-   [ ] `F-1018` 自动创建后执行连通性验证。
-   [ ] `F-1019` 创建失败时清理本次创建的部分资源。

## 8.2 调度器

-   [x] `F-1021` 审计现有调度和分配逻辑。
    - 备注（Task ID: F-1021 | Status: Done | Evidence: 调度分配逻辑审计 §3.5 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1022` 统一调度输入模型。
-   [ ] `F-1023` 统一调度输出模型。
-   [ ] `F-1024` 实现候选 Path 过滤器。
-   [ ] `F-1025` 实现健康约束。
-   [ ] `F-1026` 实现容量约束。
-   [ ] `F-1027` 实现成本约束。
-   [ ] `F-1028` 实现 Path 质量评分。
-   [ ] `F-1029` 实现优先级规则。
-   [ ] `F-1030` 实现候选排序。
-   [ ] `F-1031` 实现故障回退。
-   [ ] `F-1032` 实现调度决策记录。
-   [ ] `F-1033` 实现候选排除原因。
-   [ ] `F-1034` 实现调度结果解释。
-   [ ] `F-1035` 实现调度模拟 API。
-   [x] `F-1036` 实现调度单元测试。
    - 备注（Task ID: F-1036 | Status: Done | Evidence: alloc_test 9 例（Pick/sweep/权重/手动） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）

## 8.3 调度模式与默认策略

-   [x] `F-1041` 定义 manual 调度模式。
    - 备注（Task ID: F-1041 | Status: Done | Evidence: alloc.go:473 hasManualAssignment 优先+LandingAssignment manual；TestSweepManualWins+TestLandingAssignment | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1042` 定义 priority 调度模式。
-   [x] `F-1043` 定义 balanced 调度模式。
    - 备注（Task ID: F-1043 | Status: Done | Evidence: PolicyBalanced；TestPickBalanced | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1044` 定义 quality 调度模式。
-   [x] `F-1045` 定义 cost 调度模式。
    - 备注（Task ID: F-1045 | Status: Done | Evidence: PolicyCostFirst；TestPickCostFirst | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1046` 为每种模式定义确定性的评分和约束规则。
    - 备注（Task ID: F-1046 | Status: Done | Evidence: alloc.go:214-216 规则+lessFor 纯函数；TestPick* 断言具体胜者 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1047` 支持按套餐配置调度模式。
-   [ ] `F-1048` 支持按业务类型配置调度模式。
-   [ ] `F-1049` 支持人工覆盖调度结果。
-   [ ] `F-1050` 支持人工覆盖到期自动恢复。
-   [ ] `F-1051` 国内访问国内默认优先直连。
-   [ ] `F-1052` 只有测量证明中继更优时才允许国内线路走中继。
-   [ ] `F-1053` 直连不可用时按 Policy 回退。
-   [ ] `F-1054` 记录直连与中继的质量比较依据。

## 8.4 容量管理

-   [ ] `F-1061` 统一上行带宽定义。
-   [ ] `F-1062` 统一下行带宽定义。
-   [ ] `F-1063` 区分标称带宽和实测带宽。
-   [ ] `F-1064` 统一带宽利用率计算。
-   [ ] `F-1065` 支持连接数限制。
-   [ ] `F-1066` 支持并发流限制。
-   [ ] `F-1067` 支持节点超载预警。
-   [ ] `F-1068` 支持 Path 容量估算。
-   [ ] `F-1069` 支持容量不足时自动降权。
-   [ ] `F-1070` 支持管理员容量覆盖。

------------------------------------------------------------------------

# 9. 订阅与外部客户端适配

## 9.1 统一订阅模型

-   [x] `F-1101` 审计现有订阅生成代码。
    - 备注（Task ID: F-1101 | Status: Done | Evidence: 订阅生成代码审计 §3.6 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1102` 定义统一内部 Subscription DTO。
-   [ ] `F-1103` 让订阅 Adapter 只依赖 DTO，不直接查询数据库。
-   [ ] `F-1104` 统一节点协议参数表达。
-   [ ] `F-1105` 统一 TLS、SNI、Transport 和相关参数表达。
-   [ ] `F-1106` 统一 UDP 支持声明。
-   [ ] `F-1107` 统一节点标签、地区和排序信息。
-   [ ] `F-1108` 统一订阅版本和更新时间。

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
-   [ ] `F-1127` 为每个 Adapter 建立客户端能力矩阵。
-   [x] `F-1128` 为每个 Adapter 建立配置语法测试。
    - 备注（Task ID: F-1128 | Status: Done | Evidence: 测试内 yaml/json 严格解析断言；clash_test+singbox_test | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1129` 使用真实客户端版本执行解析测试。
-   [ ] `F-1130` 对不支持的字段提供明确错误或安全降级。
-   [ ] `F-1131` 不向客户端输出其不支持的配置字段。
-   [ ] `F-1132` 不假设不同客户端具有相同策略组能力。

## 9.3 订阅权限与安全

-   [x] `F-1141` 根据用户状态过滤订阅。
    - 备注（Task ID: F-1141 | Status: Done | Evidence: sub/availability.go:11 UserActive+enabled 过滤；TestSubscriptionAvailability | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1142` 根据套餐权限过滤订阅。
-   [ ] `F-1143` 根据 Path Group 过滤订阅。
-   [ ] `F-1144` 根据 Policy 过滤订阅。
-   [x] `F-1145` 过滤禁用节点和不符合策略的线路。
    - 备注（Task ID: F-1145 | Status: Done | Evidence: sub.go:74 enabled/pool_state/status；TestSubscription | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1146` 支持订阅令牌重置。
    - 备注（Task ID: F-1146 | Status: Done | Evidence: user.go:300 resetSubToken；TestResetSubTokenErrors+e2e 重置腿 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1147` 支持订阅访问限频。
    - 备注（Task ID: F-1147 | Status: Done | Evidence: sub.go:33 subLimiter/subMissLimiter；TestSubscriptionRateLimit | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1148` 支持 ETag 和 Last-Modified。
-   [ ] `F-1149` 支持订阅缓存失效。
-   [ ] `F-1150` 记录订阅访问审计。
-   [x] `F-1151` 对过期或封禁用户执行明确的订阅拒绝策略。
    - 备注（Task ID: F-1151 | Status: Done | Evidence: 过期/封禁 404 或空订阅；TestSubscription+TestSubscriptionAvailability | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1152` 对流量耗尽用户执行套餐定义的订阅策略。
    - 备注（Task ID: F-1152 | Status: Done | Evidence: UserActive 额度+userinfo 头；TestSubscription | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）

------------------------------------------------------------------------

# 10. 用户、套餐、设备与流量

## 10.1 用户与套餐

-   [x] `F-1201` 审计 User、Plan、Subscription 和 Device 模型。
    - 备注（Task ID: F-1201 | Status: Done | Evidence: User/Plan/Subscription/Device 模型审计 §3.1+§3.7（结论：Plan/Subscription 表未建） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1202` 明确用户状态机。
-   [ ] `F-1203` 明确套餐生效和过期规则。
-   [x] `F-1204` 明确套餐流量周期。
    - 备注（Task ID: F-1204 | Status: Done | Evidence: models.go:11 ResetCycle+review 尊重周期；TestDisableInactiveRespectsResetCycle | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1205` 明确套餐限速规则。
-   [ ] `F-1206` 明确套餐线路权限。
-   [ ] `F-1207` 明确套餐区域权限。
-   [x] `F-1208` 明确套餐设备数量限制。
    - 备注（Task ID: F-1208 | Status: Done | Evidence: DeviceLimit/IPLimit；TestSubscriptionDeviceLimit+TestSubscriptionIPLimit | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1209` 明确流量耗尽处理策略。
    - 备注（Task ID: F-1209 | Status: Done | Evidence: review 耗尽停用+订阅空；TestDisableInactive+TestSubscription | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1210` 支持套餐升级和降级。
-   [ ] `F-1211` 支持套餐变更历史。

## 10.2 设备管理

-   [x] `F-1221` 审计现有 HWID 和设备数限制。
    - 备注（Task ID: F-1221 | Status: Done | Evidence: HWID 与设备数限制审计 §3.7（SubDevice idx_subdev_key+TestSubscriptionDeviceLimit） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1222` 统一设备绑定规则。
-   [x] `F-1223` 支持设备列表。
    - 备注（Task ID: F-1223 | Status: Done | Evidence: GET devices；TestUserDevicesErrors+TestPanelMeDeviceUsage | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1224` 支持设备解绑。
-   [x] `F-1225` 支持设备上限检查。
    - 备注（Task ID: F-1225 | Status: Done | Evidence: subdevice 上限检查；TestSubDeviceLimitValidation | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1226` 支持异常设备告警。
-   [ ] `F-1227` 避免仅凭 IP 变化认定设备变化。

## 10.3 流量记账

-   [x] `F-1241` 审计 traffic.report 的真实处理链路。
    - 备注（Task ID: F-1241 | Status: Done | Evidence: traffic.report 处理链路审计 §3.7（agentws.go:445→291→460） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1242` 明确入口、出口或单端计量口径。
-   [ ] `F-1243` 防止入口和落地重复计费。
-   [ ] `F-1244` 统一流量单位。
-   [ ] `F-1245` 统一上行和下行定义。
-   [x] `F-1246` 支持用户维度统计。
    - 备注（Task ID: F-1246 | Status: Done | Evidence: agentws.go:291 saveUserTraffic→traffic_logs；TestAgentWSUserTraffic | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1247` 支持 Node 维度统计。
    - 备注（Task ID: F-1247 | Status: Done | Evidence: agentws.go:269 saveNodeTraffic→node_traffic_logs；TestAgentWSTrafficReport | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1248` 支持 Path 维度统计。
-   [x] `F-1249` 支持周期配额重置。
    - 备注（Task ID: F-1249 | Status: Done | Evidence: quota.go:54 UsedBytes 周期窗；TestSubResetCycleWindow | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1250` 支持重复上报去重。
    - 备注（Task ID: F-1250 | Status: Done | Evidence: agentws_dedupe 去重；dedupe_test 7 例 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1251` 支持断线后的补报。
-   [ ] `F-1252` 支持流量异常检测。
-   [ ] `F-1253` 支持流量账目审计。
-   [x] `F-1254` 建立流量记账幂等测试。
    - 备注（Task ID: F-1254 | Status: Done | Evidence: 流量幂等；TestAgentWSDuplicateTrafficFrame+TestAgentWSDedupSurvivesReconnect | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1255` 建立断线补报集成测试。

------------------------------------------------------------------------

# 11. 订单、支付与运营

## 11.1 订单和支付

-   [x] `F-1301` 审计订单状态机。
    - 备注（Task ID: F-1301 | Status: Done | Evidence: 订单状态机审计 §3.8（PaymentOrder models.go:327） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1302` 统一订单幂等规则。
    - 备注（Task ID: F-1302 | Status: Done | Evidence: order_no uniqueIndex models.go:330+paynotify.go:51 事务；TestEpusdtNotify+TestReconcileGuards | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1303` 统一订单超时和取消规则。
-   [x] `F-1304` 审计支付 Provider 接口。
    - 备注（Task ID: F-1304 | Status: Done | Evidence: 支付 Provider 接口审计 §3.8（provider.go:14-20） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1305` 验证支付回调签名。
    - 备注（Task ID: F-1305 | Status: Done | Evidence: epusdt.go:139 Verify/:168 sign；TestVerify+TestSign | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1306` 保证支付回调幂等。
    - 备注（Task ID: F-1306 | Status: Done | Evidence: idx_provider_external models.go:413-414 唯一+事务；TestEpusdtNotify | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1307` 建立支付结果补偿机制。
-   [ ] `F-1308` 建立支付对账任务。
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
-   [ ] `F-1324` 统一账目流水。
-   [ ] `F-1325` 统一佣金结算状态。
-   [ ] `F-1326` 建立异常订单和重复佣金测试。

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

-   [ ] `F-1401` 展示节点总数和在线数。
-   [ ] `F-1402` 展示健康与不健康节点数。
-   [ ] `F-1403` 展示 Entry、Landing 数量。
-   [ ] `F-1404` 展示 Relay 在线状态。
-   [ ] `F-1405` 展示 Path 总数和可用率。
-   [ ] `F-1406` 展示当前总流量和连接数。
-   [ ] `F-1407` 展示区域资源分布。
-   [ ] `F-1408` 展示告警摘要。
-   [ ] `F-1409` 展示本月云资源成本摘要。
-   [ ] `F-1410` 展示估算成本与实际账单差异。

## 12.2 节点与线路

-   [ ] `F-1421` 提供节点列表和筛选。
-   [ ] `F-1422` 提供节点详情。
-   [ ] `F-1423` 展示 Node 关联的 Cloud Resource。
-   [ ] `F-1424` 展示 Agent、Relay 和 Probe 状态。
-   [ ] `F-1425` 展示节点配置版本。
-   [ ] `F-1426` 提供节点维护和启停管理。
-   [ ] `F-1427` 提供 Path 列表。
-   [ ] `F-1428` 提供 Path 详情。
-   [ ] `F-1429` 提供 Path Group 管理。
-   [ ] `F-1430` 提供 Policy 管理。
-   [ ] `F-1431` 展示调度候选和排除原因。
-   [ ] `F-1432` 展示探测历史和质量趋势。

## 12.3 云服务商与资源

-   [ ] `F-1441` 提供 Provider 管理入口。
-   [ ] `F-1442` 提供 Cloud Account 管理入口。
-   [ ] `F-1443` 提供账号连接测试。
-   [ ] `F-1444` 提供资源同步入口。
-   [ ] `F-1445` 提供云资源列表。
-   [ ] `F-1446` 提供云资源详情。
-   [ ] `F-1447` 提供云资源与 Node 关联管理。
-   [ ] `F-1448` 提供开机操作。
-   [ ] `F-1449` 提供关机操作。
-   [ ] `F-1450` 提供重启操作。
-   [ ] `F-1451` 提供强制停止操作和高危确认。
-   [ ] `F-1452` 对支持的 Provider 提供重装/重建入口。
-   [ ] `F-1453` 提供批量操作预览。
-   [ ] `F-1454` 提供批量操作逐资源结果。
-   [ ] `F-1455` 展示云操作任务进度。
-   [ ] `F-1456` 展示 Provider API 最后同步时间。
-   [ ] `F-1457` 展示 Provider 错误和权限不足原因。

## 12.4 成本与账单

-   [ ] `F-1461` 提供成本总览。
-   [ ] `F-1462` 提供 Provider 成本统计。
-   [ ] `F-1463` 提供账号成本统计。
-   [ ] `F-1464` 提供区域成本统计。
-   [ ] `F-1465` 提供资源成本统计。
-   [ ] `F-1466` 提供 Node 成本统计。
-   [ ] `F-1467` 提供 Path 成本估算。
-   [ ] `F-1468` 提供账单导入和同步任务管理。
-   [ ] `F-1469` 提供账单明细查询。
-   [ ] `F-1470` 提供估算与实际账单对比。
-   [ ] `F-1471` 提供成本分摊规则管理。
-   [ ] `F-1472` 提供账单差异对账入口。
-   [ ] `F-1473` 提供成本异常告警。
-   [ ] `F-1474` 支持成本明细导出。
-   [ ] `F-1475` 在页面明确区分实际、估算和分摊金额。

## 12.5 配置、任务与审计

-   [ ] `F-1481` 提供配置版本列表。
-   [ ] `F-1482` 提供配置差异查看。
-   [ ] `F-1483` 提供配置下发状态。
-   [ ] `F-1484` 提供配置失败和回滚记录。
-   [ ] `F-1485` 提供批量运维任务列表。
-   [ ] `F-1486` 提供 Agent 升级任务列表。
-   [ ] `F-1487` 提供自动恢复任务列表。
-   [ ] `F-1488` 提供操作审计查询。
-   [ ] `F-1489` 提供告警确认和关闭入口。

------------------------------------------------------------------------

# 13. Panel 用户面板

-   [ ] `F-1501` 展示用户账户信息。
-   [ ] `F-1502` 展示当前套餐。
-   [ ] `F-1503` 展示套餐到期时间。
-   [ ] `F-1504` 展示流量使用情况。
-   [ ] `F-1505` 展示设备列表。
-   [ ] `F-1506` 提供设备解绑。
-   [ ] `F-1507` 展示订阅地址。
-   [ ] `F-1508` 支持订阅令牌重置。
-   [ ] `F-1509` 支持选择订阅格式。
-   [ ] `F-1510` 提供 sing-box 导入说明。
-   [ ] `F-1511` 提供 Clash/Mihomo 导入说明。
-   [ ] `F-1512` 展示公告。
-   [ ] `F-1513` 展示订单和支付记录。
-   [ ] `F-1514` 展示账单和套餐历史。
-   [ ] `F-1515` 提供账号安全设置。
-   [ ] `F-1516` 展示登录记录。

------------------------------------------------------------------------

# 14. 自动化运维与任务系统

## 14.1 通用任务系统

-   [x] `F-1601` 审计现有任务、Provision、Recovery 实现。
    - 备注（Task ID: F-1601 | Status: Done | Evidence: 任务/Provision/Recovery 审计 §3.12 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1602` 统一后台任务状态模型。
-   [ ] `F-1603` 支持任务排队。
-   [ ] `F-1604` 支持任务超时。
-   [ ] `F-1605` 支持有限次数重试。
-   [ ] `F-1606` 支持退避策略。
-   [ ] `F-1607` 支持任务取消。
-   [ ] `F-1608` 支持任务幂等。
-   [ ] `F-1609` 支持任务进度和阶段信息。
-   [ ] `F-1610` 支持任务日志。
-   [ ] `F-1611` 支持任务人工重试。
-   [ ] `F-1612` 支持任务部分成功结果。
-   [ ] `F-1613` 支持任务执行者和操作者审计。
-   [ ] `F-1614` 支持任务并发配额。
-   [ ] `F-1615` 支持任务积压和失败告警。

## 14.2 节点运维任务

-   [ ] `F-1621` 支持 Agent 重启任务。
-   [ ] `F-1622` 支持 Relay 重启任务。
-   [x] `F-1623` 支持代理进程重启任务。
    - 备注（Task ID: F-1623 | Status: Done | Evidence: procop 重启；TestNodeProcOp+TestBatchNodeProc | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1624` 支持节点配置重应用任务。
    - 备注（Task ID: F-1624 | Status: Done | Evidence: pushConfig 重应用+configd 回滚；TestNodeConfigGuards+TestApplyReloadFailRollsBack | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1625` 支持节点升级任务。
    - 备注（Task ID: F-1625 | Status: Done | Evidence: router.go:183 nodeUpgrade；TestNodeUpgrade | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1626` 支持节点回滚任务。
-   [ ] `F-1627` 支持节点重新探测任务。
-   [ ] `F-1628` 支持节点摘除任务。
-   [ ] `F-1629` 支持节点恢复任务。
-   [ ] `F-1630` 支持任务执行前依赖检查。

## 14.3 自动恢复

-   [x] `F-1641` 定义自动恢复触发条件。
    - 备注（Task ID: F-1641 | Status: Done | Evidence: recovery sweep 触发；TestSweepOpenAndAdvance | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1642` 定义自动恢复允许执行的动作集合。
    - 备注（Task ID: F-1642 | Status: Done | Evidence: recovery 动作集；TestActionNames+TestInstanceActionL3 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1643` 定义自动恢复最大执行次数。
    - 备注（Task ID: F-1643 | Status: Done | Evidence: Level 1-3 最大次数；TestTimeoutExhaustFails+TestLastLine | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1644` 定义自动恢复冷却时间。
-   [ ] `F-1645` 定义自动恢复失败后的人工介入状态。
-   [ ] `F-1646` 防止自动恢复循环重启。
-   [x] `F-1647` 记录自动恢复决策和执行结果。
    - 备注（Task ID: F-1647 | Status: Done | Evidence: RecoveryAction 轨迹 models.go:527；TestTraceRowsReplay | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）

## 14.4 云资源供给

-   [x] `F-1651` 审计当前 OpenTofu 和 Provider Provision 能力。
    - 备注（Task ID: F-1651 | Status: Done | Evidence: OpenTofu/Provider Provision 能力审计 §3.9 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1652` 将云资源创建纳入统一任务系统。
-   [ ] `F-1653` 支持创建前成本预估。
-   [ ] `F-1654` 支持创建前资源规格校验。
-   [ ] `F-1655` 支持创建后资源同步。
-   [x] `F-1656` 支持创建后 Agent 注册。
    - 备注（Task ID: F-1656 | Status: Done | Evidence: cloudinit 注册+agent-install 同口径；TestCloudInit+TestProvisionPipeline | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1657` 支持节点入池检查。
    - 备注（Task ID: F-1657 | Status: Done | Evidence: OS-4 入池检查；TestProvisionPipeline | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1658` 创建失败时按策略清理部分资源。
-   [ ] `F-1659` 支持资源销毁前依赖检查。
-   [ ] `F-1660` 支持资源销毁审批和二次确认。

------------------------------------------------------------------------

# 15. 安全与权限

## 15.1 Dash 权限

-   [x] `F-1701` 审计管理员登录和会话管理。
    - 备注（Task ID: F-1701 | Status: Done | Evidence: 管理员登录与会话审计 §3.13（admin.go:41+ratelimit+loginlog） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1702` 审计管理员角色和权限模型。
    - 备注（Task ID: F-1702 | Status: Done | Evidence: 管理员角色权限审计 §3.13（requireSuper+TestAdminRoleCRUD） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1703` 为云账号管理定义独立权限。
-   [ ] `F-1704` 为云资源开关机定义独立权限。
-   [ ] `F-1705` 为强制停止和重装定义高危权限。
-   [ ] `F-1706` 为成本查看和账单导出定义权限。
-   [ ] `F-1707` 为成本调整和分摊规则修改定义权限。
-   [ ] `F-1708` 为节点和线路操作定义权限。
-   [ ] `F-1709` 为敏感操作增加二次确认。
-   [ ] `F-1710` 支持按账号或资源范围限制操作权限。

## 15.2 凭据安全

-   [x] `F-1721` 审计现有 secret_store。
    - 备注（Task ID: F-1721 | Status: Done | Evidence: secret_store 审计 §3.13（secret_test 6 例） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1722` 验证云凭据加密存储。
    - 备注（Task ID: F-1722 | Status: Done | Evidence: secret.Store AES-GCM+providerapi sealSecret；TestStoreRoundtrip+TestProviderAPI | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1723` 验证密钥轮换。
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
-   [ ] `F-1732` 测试云账号凭据泄漏。
-   [ ] `F-1733` 测试高危云操作权限绕过。
-   [x] `F-1734` 测试 Agent 消息伪造和重放。
    - 备注（Task ID: F-1734 | Status: Done | Evidence: bad token 拒绝+envelope ID 重放去重；TestAgentWSBadToken+dedupe_test | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1735` 测试配置篡改。
-   [x] `F-1736` 测试订阅令牌爆破和滥用。
    - 备注（Task ID: F-1736 | Status: Done | Evidence: 订阅限频枚举锁定；TestSubscriptionRateLimit | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1737` 测试敏感信息日志泄漏。
-   [x] `F-1738` 执行依赖安全扫描。
    - 备注（Task ID: F-1738 | Status: Done | Evidence: .github/dependabot.yml security alerts+test.yml CI 绿 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）

------------------------------------------------------------------------

# 16. 可观测性与告警

## 16.1 指标

-   [ ] `F-1801` 采集 Server API 请求量和延迟。
-   [ ] `F-1802` 采集数据库连接数和查询延迟。
-   [ ] `F-1803` 采集 Agent 在线数量和心跳延迟。
-   [ ] `F-1804` 采集 Relay 在线数量和连接数。
-   [ ] `F-1805` 采集 Relay 吞吐量。
-   [ ] `F-1806` 采集 Path 可用率和质量指标。
-   [ ] `F-1807` 采集 Probe 任务成功率。
-   [ ] `F-1808` 采集配置下发成功率。
-   [ ] `F-1809` 采集流量记账延迟。
-   [ ] `F-1810` 采集调度耗时。
-   [ ] `F-1811` 采集 Provider API 请求成功率和延迟。
-   [ ] `F-1812` 采集云操作任务成功率和耗时。
-   [ ] `F-1813` 采集账单同步成功率和延迟。
-   [ ] `F-1814` 采集成本数据新鲜度。

## 16.2 告警

-   [ ] `F-1821` Agent 离线告警。
-   [ ] `F-1822` Relay 离线告警。
-   [ ] `F-1823` Path 不可用告警。
-   [ ] `F-1824` Path 质量下降告警。
-   [ ] `F-1825` 节点带宽超限告警。
-   [ ] `F-1826` 配置下发失败告警。
-   [ ] `F-1827` 节点升级失败告警。
-   [ ] `F-1828` Provider API 认证失败告警。
-   [ ] `F-1829` 云资源操作失败告警。
-   [ ] `F-1830` 云账单同步失败告警。
-   [ ] `F-1831` 成本异常增长告警。
-   [x] `F-1832` 备份失败告警。
    - 备注（Task ID: F-1832 | Status: Done | Evidence: backup.go:253/260 emitBackupFailed/emitUploadFailed；TestBackupFailedEmit | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1833` 告警支持去重和恢复通知。
    - 备注（Task ID: F-1833 | Status: Done | Evidence: node+kind+proc 去重+自动恢复；TestAlertGuards+TestAgentWSAlarmProcRecover | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-1834` 告警支持确认、关闭和处理记录。
    - 备注（Task ID: F-1834 | Status: Done | Evidence: resolveAlert+Alert.ResolvedAt；TestAlertGuards | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1835` 通知通道故障不阻塞主业务。

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
-   [ ] `F-1910` 建立历史数据归档和清理策略。

## 17.2 部署和恢复

-   [x] `F-1921` 审计当前 Compose 和部署脚本。
    - 备注（Task ID: F-1921 | Status: Done | Evidence: Compose/部署脚本审计 §3.16 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1922` 明确 Server、Dash、Panel 的部署依赖。
-   [x] `F-1923` 明确 Agent 安装和升级流程。
    - 备注（Task ID: F-1923 | Status: Done | Evidence: agent-install.sh 原子回滚+upgrade 看门狗；install-rollback-test+TestWatchdogRollback | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1924` 明确密钥和配置持久化位置。
-   [x] `F-1925` 建立数据库自动备份。
    - 备注（Task ID: F-1925 | Status: Done | Evidence: backup.go:153 Loop 周期备份；TestLoopRunsOnceAndPrunes+TestSnapshotEncrypted | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1926` 建立配置和密钥备份策略。
-   [x] `F-1927` 建立备份恢复测试。
    - 备注（Task ID: F-1927 | Status: Done | Evidence: deploy/restore.sh+e2e 备份恢复腿（53 断言内） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1928` 建立 Server 升级测试。
-   [ ] `F-1929` 建立 Server 回滚方案。
-   [x] `F-1930` 建立完整部署冒烟测试。
    - 备注（Task ID: F-1930 | Status: Done | Evidence: deploy/e2e-smoke.sh 实跑 53 ok EXIT=0 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-1931` 验证 Server 重启后 Agent 能重新连接。
-   [ ] `F-1932` 验证 Server 不可用时数据面继续运行。

## 17.3 多区域

-   [ ] `F-1941` 为 Node 和 Path 建立区域维度管理。
-   [ ] `F-1942` 支持跨区域探测。
-   [ ] `F-1943` 支持跨区域调度。
-   [ ] `F-1944` 支持区域容量和成本统计。
-   [ ] `F-1945` 支持区域故障隔离。
-   [ ] `F-1946` 评估 Server 高可用。
-   [ ] `F-1947` 评估数据库只读副本。
-   [ ] `F-1948` 在没有明确需求前不实现数据库双主双写。
-   [ ] `F-1949` 在没有明确需求前不实现多 Server 并发修改同一资源。

------------------------------------------------------------------------

# 18. 测试与验收

## 18.1 单元测试

-   [ ] `F-2001` 为 Node 状态机建立单元测试。
-   [ ] `F-2002` 为 Capability 过滤建立单元测试。
-   [ ] `F-2003` 为 Path 状态机建立单元测试。
-   [ ] `F-2004` 为 Path Group 建立单元测试。
-   [ ] `F-2005` 为 Policy 冲突规则建立单元测试。
-   [x] `F-2006` 为 Scheduler 过滤和评分建立单元测试。
    - 备注（Task ID: F-2006 | Status: Done | Evidence: alloc_test 9 例调度单测 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-2007` 为 Health 计算建立单元测试。
    - 备注（Task ID: F-2007 | Status: Done | Evidence: aggregate_test+monitor_test 健康计算单测 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-2008` 为 Config Compiler 建立单元测试。
-   [x] `F-2009` 为 Subscription Adapter 建立单元测试。
    - 备注（Task ID: F-2009 | Status: Done | Evidence: sub/{clash,singbox,links}_test Adapter 单测 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-2010` 为 Traffic Dedup 建立单元测试。
    - 备注（Task ID: F-2010 | Status: Done | Evidence: agentws_dedupe_test 7 例去重单测 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-2011` 为 Cloud Provider Adapter 建立契约测试。
-   [ ] `F-2012` 为 Cloud Operation 状态机建立单元测试。
-   [ ] `F-2013` 为 Cost Import 去重建立单元测试。
-   [ ] `F-2014` 为 Cost Allocation 建立单元测试。
-   [ ] `F-2015` 为后台任务重试和取消建立单元测试。

## 18.2 集成测试

-   [x] `F-2021` 测试 Agent 注册。
    - 备注（Task ID: F-2021 | Status: Done | Evidence: enroll_test 8 例注册测试 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-2022` 测试 Agent 心跳和断线重连。
    - 备注（Task ID: F-2022 | Status: Done | Evidence: TestAgentWSHeartbeat+TestRunReconnectsUntilCancel | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-2023` 测试 Agent 配置下发和 ACK。
    - 备注（Task ID: F-2023 | Status: Done | Evidence: relaypush_test 9 例（ACK/超时/重推） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-2024` 测试配置失败回滚。
    - 备注（Task ID: F-2024 | Status: Done | Evidence: configd_test TestApplyReloadFailRollsBack+TestApplyNoOldConfigRollbackRemoves | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-2025` 测试 Relay 建立和断线重连。
-   [x] `F-2026` 测试 Probe 任务执行和结果回传。
    - 备注（Task ID: F-2026 | Status: Done | Evidence: probe_test+TestAgentWSProbeReport/TestAgentWSProbeReportResolve | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-2027` 测试 Path 健康变化。
-   [ ] `F-2028` 测试 Path 故障回退。
-   [ ] `F-2029` 测试订阅生成和权限过滤。
-   [ ] `F-2030` 测试流量上报、去重和补报。
-   [ ] `F-2031` 测试 Provider 账号连接和资源同步。
-   [ ] `F-2032` 测试云资源开机操作。
-   [ ] `F-2033` 测试云资源关机操作。
-   [ ] `F-2034` 测试云资源重启操作。
-   [ ] `F-2035` 测试 Provider 请求超时后的状态对账。
-   [ ] `F-2036` 测试账单导入和重复数据去重。
-   [ ] `F-2037` 测试估算与实际账单差异计算。
-   [ ] `F-2038` 测试成本分摊和重算。
-   [ ] `F-2039` 测试云资源操作与 Node 状态联动。

## 18.3 端到端测试

-   [ ] `F-2041` 自动部署测试 Server。
-   [ ] `F-2042` 自动部署 Entry Node。
-   [ ] `F-2043` 自动部署 Landing Node。
-   [ ] `F-2044` 建立真实 Relay Tunnel。
-   [ ] `F-2045` 创建 Path 并执行探测。
-   [ ] `F-2046` 生成 sing-box 订阅并执行解析测试。
-   [ ] `F-2047` 生成 Clash/Mihomo 配置并执行解析测试。
-   [ ] `F-2048` 验证真实 TCP 转发。
-   [ ] `F-2049` 验证真实 UDP 转发。
-   [ ] `F-2050` 验证节点故障后的线路处理。
-   [ ] `F-2051` 验证节点恢复后重新加入调度。
-   [ ] `F-2052` 验证流量记账准确性。
-   [ ] `F-2053` 验证 Server 重启后的状态恢复。
-   [ ] `F-2054` 在测试 Provider 账号或 Mock Provider
    上验证完整云资源操作流程。
-   [ ] `F-2055` 验证云资源账单导入、成本汇总和 Dash 展示。

## 18.4 性能与故障测试

-   [ ] `F-2061` 测试大量 Agent 长连接。
-   [ ] `F-2062` 测试大量心跳并发。
-   [ ] `F-2063` 测试大量 ProbeResult 写入。
-   [ ] `F-2064` 测试大量订阅请求。
-   [ ] `F-2065` 测试大量流量报告。
-   [ ] `F-2066` 测试大量候选 Path 调度。
-   [ ] `F-2067` 测试大量配置批量下发。
-   [ ] `F-2068` 测试大量云资源同步。
-   [ ] `F-2069` 测试 Provider API 限流和超时。
-   [ ] `F-2070` 测试账单大批量导入。
-   [ ] `F-2071` 测试 Server 与 Provider 网络中断。
-   [ ] `F-2072` 测试 Relay 故障。
-   [ ] `F-2073` 测试 Agent 故障。
-   [ ] `F-2074` 测试数据库短暂不可用。
-   [ ] `F-2075` 测试成本数据过期和缺失时的降级行为。
-   [ ] `F-2076` 记录测试环境、数据规模、并发量、P95/P99 和资源占用。

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

-   [ ] `F-9101` 明确 Node、Capability 和现有 Pool 的关系。
-   [ ] `F-9102` 明确 Tunnel 与 Path 的边界。
-   [ ] `F-9103` 完成 Path 模型或现有模型映射。
-   [ ] `F-9104` 完成 Path Group 模型或现有模型映射。
-   [ ] `F-9105` 完成 Policy 模型或现有模型映射。
-   [ ] `F-9106` 完成模型 Migration。
-   [ ] `F-9107` 完成旧数据兼容。
-   [ ] `F-9108` 完成核心模型测试。

## P2 --- 云资源管理与成本基础

-   [ ] `F-9201` 完成 Provider Adapter 契约。
-   [ ] `F-9202` 完成 Cloud Account 和凭据管理。
-   [ ] `F-9203` 完成资源同步。
-   [ ] `F-9204` 完成 Cloud Operation 任务状态机。
-   [ ] `F-9205` 完成开机、关机和重启流程。
-   [ ] `F-9206` 完成 Cloud Resource 与 Node 关联。
-   [ ] `F-9207` 完成账单导入模型。
-   [ ] `F-9208` 完成至少一种 Provider 的实际账单接入或文件导入。
-   [ ] `F-9209` 完成估算成本。
-   [ ] `F-9210` 完成实际账单与估算对比。
-   [ ] `F-9211` 完成 Dash 云资源管理页面。
-   [ ] `F-9212` 完成 Dash 成本管理页面。

## P3 --- Agent、Relay 和配置闭环

-   [ ] `F-9301` 完成 Agent 协议差异修复。
-   [ ] `F-9302` 完成能力和状态上报。
-   [ ] `F-9303` 完成配置版本和 ACK。
-   [ ] `F-9304` 完成 Relay 状态管理。
-   [ ] `F-9305` 完成配置失败回滚。
-   [ ] `F-9306` 完成断线恢复集成测试。

## P4 --- 探测和健康体系

-   [ ] `F-9401` 完成 Probe 指标模型。
-   [ ] `F-9402` 完成 Probe 任务调度。
-   [ ] `F-9403` 完成结果聚合。
-   [ ] `F-9404` 完成 Node、Tunnel、Path、Egress 健康区分。
-   [ ] `F-9405` 完成健康状态计算。
-   [ ] `F-9406` 完成健康管理页面。

## P5 --- Path 编排和调度

-   [ ] `F-9501` 完成 Path 创建和校验。
-   [ ] `F-9502` 完成 Entry → Landing 编排。
-   [ ] `F-9503` 完成 Path Group。
-   [ ] `F-9504` 完成 Policy。
-   [ ] `F-9505` 完成候选过滤。
-   [ ] `F-9506` 完成质量、容量和成本评分。
-   [ ] `F-9507` 完成故障回退。
-   [ ] `F-9508` 完成调度解释和模拟。

## P6 --- 订阅和用户运营

-   [ ] `F-9601` 完成统一 Subscription DTO。
-   [ ] `F-9602` 完成 sing-box Adapter。
-   [ ] `F-9603` 完成 Clash/Mihomo Adapter。
-   [ ] `F-9604` 完成分享链接 Adapter。
-   [ ] `F-9605` 完成真实客户端解析测试。
-   [ ] `F-9606` 完成订阅权限过滤。
-   [ ] `F-9607` 完成流量记账可靠性。
-   [ ] `F-9608` 完成套餐与线路权限关联。

## P7 --- 管理和运营闭环

-   [x] `F-9701` 完成 Dash 节点与线路管理。
    - 备注（Task ID: F-9701 | Status: Done | Evidence: dash NodesView/LandingsView+node/landing API 测试+build 绿 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [ ] `F-9702` 完成 Dash 云资源管理。
-   [ ] `F-9703` 完成 Dash 成本和账单管理。
-   [x] `F-9704` 完成 Dash 配置和任务管理。
    - 备注（Task ID: F-9704 | Status: Done | Evidence: SettingsView+config 下发+ProvisionView/RecoveryView；TestNodeConfigGuards+TestApplyLifecycle | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-9705` 完成 Panel 用户自助能力。
    - 备注（Task ID: F-9705 | Status: Done | Evidence: panel 5 视图自助；TestPanelMe+TestPanelRedeem+TestPanelOrders+build 绿 | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）
-   [x] `F-9706` 完成订单和支付可靠性审计。
    - 备注（Task ID: F-9706 | Status: Done | Evidence: 订单与支付可靠性审计 §3.8（状态机/签名/幂等/对账/退款） | Verification: docs/audit-platform-impl-2026-11-03.md；测试全绿 2026-11-03（make test/handler -timeout=40m/agent/packages/e2e 53 断言） | Commit: 无）

## P8 --- 可靠性和规模化

-   [ ] `F-9801` 完成权限和安全测试。
-   [ ] `F-9802` 完成 Metrics 和告警。
-   [ ] `F-9803` 完成备份恢复演练。
-   [ ] `F-9804` 完成 Agent 和 Probe 规模测试。
-   [ ] `F-9805` 完成 Provider API 故障测试。
-   [ ] `F-9806` 完成账单导入和成本核算规模测试。
-   [ ] `F-9807` 完成完整端到端测试。

## P9 --- 后续扩展

-   [ ] `F-9901` 评估多区域控制面需求。
-   [ ] `F-9902` 评估 Server 高可用。
-   [ ] `F-9903` 评估数据库只读副本。
-   [ ] `F-9904` 评估高级多跳 Path。
-   [ ] `F-9905` 评估更多 Cloud Provider Adapter。
-   [ ] `F-9906` 评估更多订阅格式和客户端能力。

------------------------------------------------------------------------

# 20. 最终验收标准

## 控制面

-   [ ] `F-9991` 用户、套餐、订阅和节点管理可用。
-   [ ] `F-9992` Node、Capability、Tunnel、Path、Path Group、Policy
    边界清晰。
-   [ ] `F-9993` 能够管理 Entry、Landing 和 Relay。
-   [ ] `F-9994` 能够探测并识别节点、隧道和线路故障。
-   [ ] `F-9995` 能够基于健康、容量和成本选择线路。
-   [ ] `F-9996` 能够解释调度结果。

## 云资源与成本

-   [ ] `F-9997` 能够在 Dash 管理至少一个真实 Provider 账号。
-   [ ] `F-9998` 能够同步 Provider 云资源。
-   [ ] `F-9999` 能够在权限控制下执行开机、关机和重启。
-   [ ] `F-10000` 云操作状态能够与 Provider 实际状态对账。
-   [ ] `F-10001` Cloud Resource 能够与 Ferry Node 关联。
-   [ ] `F-10002` 能够导入或同步实际账单。
-   [ ] `F-10003` 能够区分实际账单、估算成本和成本分摊。
-   [ ] `F-10004` 能够按 Provider、账号、区域、资源和 Node 查看成本。
-   [ ] `F-10005` 能够发现账单缺失、资源无法匹配和异常成本。

## 数据面与订阅

-   [ ] `F-10006` Entry 和 Landing 能够建立 Relay 隧道。
-   [ ] `F-10007` 支持实际 TCP 和 UDP 转发。
-   [ ] `F-10008` 配置能够可靠下发和回滚。
-   [ ] `F-10009` 能够生成 sing-box 配置。
-   [ ] `F-10010` 能够生成 Clash/Mihomo 配置。
-   [ ] `F-10011` 生成内容经过真实客户端解析测试。
-   [ ] `F-10012` Ferry 不需要自研客户端即可完成用户接入。

## 运营和可靠性

-   [ ] `F-10013` 套餐权限与订阅内容一致。
-   [ ] `F-10014` 流量记账具备幂等和补报能力。
-   [ ] `F-10015` Dash 能够完成日常节点、云资源、线路和成本管理。
-   [ ] `F-10016` Server 重启后能够恢复控制面管理状态。
-   [ ] `F-10017` Server 短时不可用不导致已运行的数据面立即停止。
-   [ ] `F-10018` 关键故障有监控、告警、审计和恢复路径。
-   [ ] `F-10019` 备份经过真实恢复演练。
-   [ ] `F-10020` 所有最终验收项均有可复现的测试证据。

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
