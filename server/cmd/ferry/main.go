// ferry 命令是服务入口：装配配置、数据库与 HTTP 路由后对外提供服务。
package main

import (
	"context"
	"log"
	"time"

	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/handler"
	"github.com/cuihairu/ferry/server/internal/monitor"
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

	r := handler.NewRouter(db, cfg)
	log.Printf("ferry listening on %s", cfg.Addr)
	if err := r.Run(cfg.Addr); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
