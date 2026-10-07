// ferry 命令是服务入口：装配配置、数据库与 HTTP 路由后对外提供服务。
package main

import (
	"context"
	"crypto/tls"
	"log"
	"net/http"
	"time"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/handler"
	"github.com/cuihairu/ferry/server/internal/monitor"
	"github.com/cuihairu/ferry/server/internal/review"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/cuihairu/ferry/server/internal/xray"
)

func main() {
	cfg := config.Load()

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

	// 到期/超限用户停用扫表。
	go review.Run(ctx, db, time.Duration(cfg.ReviewIntervalSec)*time.Second)

	r := handler.NewRouter(db, cfg)
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
