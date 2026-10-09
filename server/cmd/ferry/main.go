// ferry 命令是服务入口：装配配置、数据库与 HTTP 路由后对外提供服务。
package main

import (
	"context"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	_ "github.com/cuihairu/ferry/payments/alipay" // PAY-10：占位渠道（未启用文案）
	_ "github.com/cuihairu/ferry/payments/epusdt" // PAY-8：init 自注册收款渠道
	_ "github.com/cuihairu/ferry/payments/wechat" // PAY-10：占位渠道（未启用文案）
	"github.com/cuihairu/ferry/server/internal/alloc"
	"github.com/cuihairu/ferry/server/internal/backup"
	"github.com/cuihairu/ferry/server/internal/cert"
	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/cost"
	"github.com/cuihairu/ferry/server/internal/geodns"
	"github.com/cuihairu/ferry/server/internal/handler"
	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/monitor"
	"github.com/cuihairu/ferry/server/internal/notifyscan"
	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/provision"
	"github.com/cuihairu/ferry/server/internal/quota"
	"github.com/cuihairu/ferry/server/internal/recovery"
	"github.com/cuihairu/ferry/server/internal/review"
	"github.com/cuihairu/ferry/server/internal/ringlog"
	"github.com/cuihairu/ferry/server/internal/save"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/cuihairu/ferry/server/internal/toucher"
	"github.com/cuihairu/ferry/server/internal/xray"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func main() {
	// 备份解包子命令（面板可用性 §3 恢复②，deploy/restore.sh 调用）：.enc
	// 档是主密钥派生钥 AES-256-GCM 的紧凑二进制，openssl enc 无 GCM 能力，
	// 恢复时借本二进制自身的 secret 包还原明文；不带子命令时照常起服务。
	if len(os.Args) > 1 && os.Args[1] == "backup-decrypt" {
		os.Exit(backupDecrypt(os.Args[2:]))
	}
	cfg := config.Load()

	// 运行日志（P1-11）：std log 与 gin 输出镜像进环形缓冲，供管理端查看。
	ring := ringlog.Default()
	log.SetOutput(io.MultiWriter(os.Stderr, ring))
	gin.DefaultWriter = io.MultiWriter(os.Stdout, ring)
	gin.DefaultErrorWriter = io.MultiWriter(os.Stderr, ring)

	db, err := storage.Open(cfg.DBDriver, cfg.DBDSN)
	if err != nil {
		log.Fatalf("init database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("db handle: %v", err)
	}
	defer sqlDB.Close()

	// 重启后在线状态一律置离线，等 agent 重连刷新。
	if err := db.Exec(`UPDATE nodes SET status='offline'`).Error; err != nil {
		log.Fatalf("reset node status: %v", err)
	}

	// 管理员首启引导（安全设计 §1）：FERRY_ADMIN_PASSWORD 非空且库内无管理员
	// 时按 FERRY_ADMIN_USER（缺省 admin）建号；已有管理员则忽略，不覆盖密码。
	if created, err := handler.EnsureAdmin(db, cfg.AdminUser, cfg.AdminPassword); err != nil {
		log.Fatalf("bootstrap admin: %v", err)
	} else if created {
		log.Printf("bootstrap admin: created admin user %q", cfg.AdminUser)
	}

	// Xray 内核对接未启用前使用空实现，接口保持稳定。
	_ = xray.NoopHandler{}

	// 区域/运营商聚合判定：周期聚合探测结论，状态迁移时合并告警。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go monitor.Run(ctx, db, time.Duration(cfg.MonitorIntervalSec)*time.Second, nil)

	// 入口池自动摘挂（E-16）：连续 sick 摘除/恢复复位，订阅入口池即时生效；
	// 池态迁移经 OnChange 钩子触发分地域对账（E-27 §C.8：事件驱动不建
	// 轮询，摘挂换代表入口即同步 {区域slug}.{前置域名} A 记录）。
	pool.OnChange = func(db *gorm.DB) {
		s := &geodns.Syncer{DB: db, Store: secret.NewStore(cfg.SecretKey)}
		if _, err := s.Sync(ctx); err != nil {
			log.Printf("geodns sync: %v", err)
		}
	}
	go pool.Loop(ctx, db, time.Duration(cfg.PoolIntervalSec)*time.Second, nil)

	// 路由与配置推送器（E-16b）：自动换线经同一条 config.push 链路重指落地。
	r, pusher := handler.NewRouter(db, cfg)

	// 落地自动分配（E-21）：加权最小连接，策略三档可配，换线留痕并通知；
	// 换线即重指（E-16b）：变更列表逐入口下发 relay 落地覆盖 spec。
	go alloc.Loop(ctx, db, time.Duration(cfg.AllocIntervalSec)*time.Second, nil, pusher.OnSwitch)

	// 高成本告警（E-23）：按流量节点月花费超阈值单发，回落自动消解。
	go cost.Loop(ctx, db, time.Duration(cfg.CostIntervalSec)*time.Second, nil)

	// 封禁恢复流水线（BR-1）：摘除持续未恢复判封，L1→L2→L3 分级推进、
	// 每级超时进下一级；L1 域名前置切备用 IP（BR-2，未配置则失败推进），
	// L2 预备机补位（BR-3），L3 一键开新机接供给模板（0=不启用）。
	{
		reg := recovery.Registry{}
		secrets := secret.NewStore(cfg.SecretKey)
		// L1（BR-2）：前置域名 A 记录切备用 IP 轮换，探测恢复回切常态。
		dnsAct := &recovery.DNSAction{DB: db, Store: secrets}
		reg[1] = dnsAct
		// L2（BR-3）：预备清单取一台在线备用入口升池，订阅换线即时生效。
		reg[2] = &recovery.StandbyAction{DB: db}
		reg[1] = dnsAct
		opts := recovery.Options{
			OnDone: func(nodeID uint, nodeName string) {
				if err := dnsAct.Restore(context.Background()); err != nil {
					log.Printf("recovery dns restore: node=%d %s: %v", nodeID, nodeName, err)
				}
			},
		}
		if cfg.RecoveryTemplateID > 0 {
			reg[3] = &recovery.InstanceAction{
				Manager:    provision.Shared(db, cfg.TofuBin, cfg.TofuWorkdir, nil),
				Store:      secrets,
				TemplateID: cfg.RecoveryTemplateID,
				Launch: func(name string) provision.LaunchParams {
					return provision.LaunchParams{
						InstanceName: name,
						PanelWSURL:   provision.PanelWSURL(cfg.BaseURL),
						AgentBase:    cfg.AgentDownloadBase, AgentVersion: cfg.AgentVersion,
						NewToken: handler.RandomToken,
					}
				},
			}
		}
		go recovery.Loop(ctx, db, time.Duration(cfg.RecoveryIntervalSec)*time.Second, opts, reg, nil)
	}

	// 池空保底（BR-3）：入口池全空且无在途供给时按模板自动开新机，
	// 开出的机器走 OS-4 流水线自动入池。模板 0=不启用。
	{
		repl := &provision.Replenisher{
			Manager:    provision.Shared(db, cfg.TofuBin, cfg.TofuWorkdir, nil),
			Store:      secret.NewStore(cfg.SecretKey),
			TemplateID: cfg.PoolTemplateID,
			Launch: func(name string) provision.LaunchParams {
				return provision.LaunchParams{
					InstanceName: name,
					PanelWSURL:   provision.PanelWSURL(cfg.BaseURL),
					AgentBase:    cfg.AgentDownloadBase, AgentVersion: cfg.AgentVersion,
					NewToken: handler.RandomToken,
				}
			},
		}
		go provision.ReplenishLoop(ctx, repl, time.Duration(cfg.ReplenishIntervalSec)*time.Second, nil)
	}

	// 证书编排（BR-4）：待签/临期/失败退避逐个驱动，签发执行 acme.sh
	// 工具位（DNS-01 复用 BR-2 凭证通道），到期前 30 天自动续，失败告警。
	{
		cm := &cert.Manager{Bin: cfg.AcmeBin, Home: cfg.AcmeHome, Webroot: cfg.AcmeWebroot}
		go cert.Loop(ctx, db, cm, secret.NewStore(cfg.SecretKey), time.Duration(cfg.CertIntervalSec)*time.Second, nil)
	}

	// 到期/超限用户停用扫表。
	go review.Run(ctx, db, time.Duration(cfg.ReviewIntervalSec)*time.Second)

	// 站内信自动触发（NT-2）：到期提醒与流量预警扫表，按日去重；
	// 发放到账的事件触发在 applyGrant 事务内，公告扇出在管理 API。
	go notifyscan.Loop(ctx, db, time.Duration(cfg.NotifyScanIntervalSec)*time.Second, nil)

	// 配额联动（SAVE-6）：流量/费用超阈值用户订阅自动降档（只出低成本档
	// 入口），触发/释放留痕与站内信由扫表驱动，订阅出口读留痕即时生效。
	go quota.LinkLoop(ctx, db, time.Duration(cfg.QuotaLinkIntervalSec)*time.Second, nil)
	// 价格关注扫描（E-32）：降价/到位经 Herald price_alert，未配 Herald 落本地 outbox。
	go cost.WatchLoop(ctx, db, time.Duration(cfg.PriceWatchIntervalSec)*time.Second, nil)

	// 节省报表聚合（SAVE-7）：流量行按日累计进 save_stats，报表与成本看板
	// 同页呈现；折算费用在 API 侧按节点流量单价现算。
	go save.Loop(ctx, db, time.Duration(cfg.SaveStatsIntervalSec)*time.Second, nil)
	// 例行触达（TOUCH-4）：月账单+域名例行邮件，任务落 touch_jobs 经 Herald 投递。
	go toucher.Loop(ctx, db, toucher.Config{
		DomainsDays: cfg.TouchDomainsDays, BaseURL: strings.TrimRight(cfg.BaseURL, "/"),
	}, time.Duration(cfg.TouchIntervalSec)*time.Second, nil)

	// 事件 outbox（HERALD-1/2）：事件落库即返回，投递循环异步重试；
	// 配置 FERRY_HERALD_URL 即接 Herald 投递腿，未配置时只落库 dash 可见。
	var eventSender herald.Sender
	if cfg.HeraldURL != "" {
		eventSender = herald.HTTPSender(cfg.HeraldURL, cfg.HeraldToken)
	}
	go herald.Loop(ctx, db, time.Duration(cfg.EventFlushIntervalSec)*time.Second, eventSender, nil)

	// 本地周期备份（面板可用性 §2，P1）：VACUUM INTO 在线快照打包加密落
	// FERRY_BACKUP_DIR（主密钥派生钥，主密钥不进备份包），滚动保留
	// FERRY_BACKUP_KEEP 份，失败经 Herald backup_failed 告警；外发位
	// S3_* 默认关闭零变化，显式 ENABLED=1 才注入 S3 外发（SigV4 PUT，
	// 成功置 uploaded=1，失败告警且本地档保留）。
	backupOpts := backup.Options{
		Dir: cfg.BackupDir, Cron: cfg.BackupCron, Keep: cfg.BackupKeep,
		S3Enabled: cfg.BackupS3Enabled,
	}
	if cfg.BackupS3Enabled {
		up, err := backup.NewS3Uploader(cfg.BackupS3Endpoint, cfg.BackupS3Bucket,
			cfg.BackupS3AccessKey, cfg.BackupS3SecretKey, nil)
		if err != nil {
			log.Printf("backup: s3 uploader not injected: %v", err)
		} else {
			backupOpts.S3 = up
		}
	}
	go backup.Loop(ctx, db, backupOpts, secret.NewStore(cfg.SecretKey))

	// 面板 Web 证书（P1-7）：settings 里配置了证书即走 HTTPS，
	// 配置损坏启动中止以免静默降级 HTTP；切换证书需重启生效。
	cert, hasCert, err := handler.WebTLSCert(db)
	if err != nil {
		log.Fatalf("load web cert: %v", err)
	}
	if hasCert {
		log.Printf("ferry listening on %s (https)", cfg.Addr)
		srv := &http.Server{
			Addr:      cfg.Addr,
			Handler:   r,
			TLSConfig: &tls.Config{Certificates: []tls.Certificate{*cert}, MinVersion: tls.VersionTLS12},
		}
		if err := srv.ListenAndServeTLS("", ""); err != nil {
			log.Fatalf("serve: %v", err)
		}
		return
	}
	log.Printf("ferry listening on %s", cfg.Addr)
	if err := r.Run(cfg.Addr); err != nil {
		log.Fatalf("serve: %v", err)
	}
}

// backupDecrypt 解密周期备份的 .enc 档为明文（面板可用性 §3 恢复②），返回
// 退出码：-key 缺省取 FERRY_SECRET_KEY，-out 缺省去掉 .enc 后缀；缺参、未配
// 主密钥、解密失败（错钥/档损坏）都明确报错非 0 退出，供 restore.sh 判定。
func backupDecrypt(args []string) int {
	fs := flag.NewFlagSet("backup-decrypt", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	key := fs.String("key", os.Getenv("FERRY_SECRET_KEY"), "主密钥（缺省取 FERRY_SECRET_KEY）")
	in := fs.String("in", "", "待解密的 .enc 备份档路径")
	out := fs.String("out", "", "还原的明文输出路径（缺省去掉 .enc 后缀）")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *in == "" {
		fmt.Fprintln(os.Stderr, "backup-decrypt: 缺 -in（待解密的备份档路径）")
		return 2
	}
	store := secret.NewStore(*key)
	if !store.Enabled() {
		fmt.Fprintln(os.Stderr, "backup-decrypt: 未配置主密钥（-key 或 FERRY_SECRET_KEY）")
		return 1
	}
	raw, err := os.ReadFile(*in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup-decrypt: 读档失败: %v\n", err)
		return 1
	}
	plain, err := store.DecryptBytes(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup-decrypt: 解密失败（主密钥不符或档损坏）: %v\n", err)
		return 1
	}
	target := *out
	if target == "" {
		target = strings.TrimSuffix(*in, ".enc")
		if target == *in {
			target = *in + ".plain"
		}
	}
	if err := os.WriteFile(target, plain, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "backup-decrypt: 写出失败: %v\n", err)
		return 1
	}
	fmt.Printf("backup-decrypt: 已还原 %s -> %s（%d 字节）\n", *in, target, len(plain))
	return 0
}
