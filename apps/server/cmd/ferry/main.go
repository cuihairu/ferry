// ferry 命令是服务入口：装配配置、数据库与 HTTP 路由后对外提供服务。
package main

import (
	"log"

	"github.com/cuihairu/ferry/apps/server/internal/config"
	"github.com/cuihairu/ferry/apps/server/internal/database"
	"github.com/cuihairu/ferry/apps/server/internal/handler"
	"github.com/cuihairu/ferry/apps/server/internal/xray"
)

func main() {
	cfg := config.Load()

	db, err := database.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("init database: %v", err)
	}
	defer db.Close()

	// Xray 内核对接未启用前使用空实现，接口保持稳定。
	_ = xray.NoopHandler{}

	r := handler.NewRouter(db)
	log.Printf("ferry listening on %s", cfg.Addr)
	if err := r.Run(cfg.Addr); err != nil {
		log.Fatalf("serve: %v", err)
	}
}
