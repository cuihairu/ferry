// ferry 命令是服务入口：装配配置、数据库与 HTTP 路由后对外提供服务。
package main

import (
	"context"
	"crypto/tls"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	_ "github.com/cuihairu/ferry/payments/alipay" // PAY-10：占位渠道（未启用文案）
	_ "github.com/cuihairu/ferry/payments/epusdt" // PAY-8：init 自注册收款渠道
	_ "github.com/cuihairu/ferry/payments/wechat" // PAY-10：占位渠道（未启用文案）
	"github.com/cuihairu/ferry/server/internal/alloc"
	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/cost"
	"github.com/cuihairu/ferry/server/internal/handler"
	"github.com/cuihairu/ferry/server/internal/monitor"
	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/provision"
	"github.com/cuihairu/ferry/server/internal/recovery"
	"github.com/cuihairu/ferry/server/internal/review"
	"github.com/cuihairu/ferry/server/internal/ringlog"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/cuihairu/ferry/server/internal/xray"
	"github.com/gin-gonic/gin"
)

func main() {
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

	// Xray 内核对接未启用前使用空实现，接口保持稳定。
	_ = xray.NoopHandler{}

	// 区域/运营商聚合判定：周期聚合探测结论，状态迁移时合并告警。
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go monitor.Run(ctx, db, time.Duration(cfg.MonitorIntervalSec)*time.Second, nil)

	// 入口池自动摘挂（E-16）：连续 sick 摘除/恢复复位，订阅入口池即时生效。
	go pool.Loop(ctx, db, time.Duration(cfg.PoolIntervalSec)*time.Second, nil)

	// 路由与配置推送器（E-16b）：自动换线经同一条 config.push 链路重指落地。
	r, pusher := handler.NewRouter(db, cfg)

	// 落地自动分配（E-21）：加权最小连接，策略三档可配，换线留痕并通知；
	// 换线即重指（E-16b）：变更列表逐入口下发 relay 落地覆盖 spec。
	go alloc.Loop(ctx, db, time.Duration(cfg.AllocIntervalSec)*time.Second, nil, pusher.OnSwitch)

	// 高成本告警（E-23）：按流量节点月花费超阈值单发，回落自动消解。
	go cost.Loop(ctx, db, time.Duration(cfg.CostIntervalSec)*time.Second, nil)

	// 封禁恢复流水线（BR-1）：摘除持续未恢复判封，L1→L2→L3 分级推进、
	// 每级超时进下一级；L3 一键开新机接供给模板（0=不启用，L1/L2 插件位
	// 分别由 BR-2/BR-3 注册后自动生效）。
	{
		reg := recovery.Registry{}
		if cfg.RecoveryTemplateID > 0 {
			reg[3] = &recovery.InstanceAction{
				Manager:    provision.New(db, cfg.TofuBin, cfg.TofuWorkdir, nil),
				Store:      secret.NewStore(cfg.SecretKey),
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
		go recovery.Loop(ctx, db, time.Duration(cfg.RecoveryIntervalSec)*time.Second, recovery.Options{}, reg, nil)
	}

	// 到期/超限用户停用扫表。
	go review.Run(ctx, db, time.Duration(cfg.ReviewIntervalSec)*time.Second)

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
