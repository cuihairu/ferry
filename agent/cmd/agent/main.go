// agent 命令是 ferry-agent 入口：加载配置后维持与面板的出站长连接。
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/cuihairu/ferry/agent/internal/app"
	"github.com/cuihairu/ferry/agent/internal/config"
)

// version 由构建注入，默认 dev。
var version = "dev"

func main() {
	cfgPath := flag.String("config", envOr("FERRY_AGENT_CONFIG", "agent.json"), "配置文件路径")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	a := app.New(cfg, version)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("ferry-agent %s starting, panel=%s agent_id=%s", version, cfg.PanelURL, cfg.AgentID)
	if err := a.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("run: %v", err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
