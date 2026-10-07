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
	"github.com/cuihairu/ferry/agent/internal/roles"
	"github.com/cuihairu/ferry/agent/internal/roles/proberole"
	"github.com/cuihairu/ferry/agent/internal/roles/relay"
	"github.com/cuihairu/ferry/agent/internal/upgrade"
)

// version 由构建注入，默认 dev。
var version = "dev"

func main() {
	cfgPath := flag.String("config", envOr("FERRY_AGENT_CONFIG", "agent.json"), "配置文件路径")
	role := flag.String("role", "", "角色组件独立进程：probe / relay（空=核心）")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	if *role != "" {
		runRole(*role, cfg)
		return
	}

	a := app.New(cfg, version)

	// 自升级验证（A-23）：存在待验证标记说明上次升级后未完成启动验证，
	// 起看门狗——超时未连上面板则回滚备份；hello 成功即提交。
	if self, err := upgrade.Self(); err == nil {
		if upgrade.Pending(self) {
			log.Printf("pending upgrade verify, watchdog %s", upgrade.RollbackTimeout)
			go upgrade.Watchdog(self, upgrade.RollbackTimeout, log.Default())
		}
		a.OnHelloOK = func() { upgrade.Commit(self) }
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Printf("ferry-agent %s starting, panel=%s agent_id=%s", version, cfg.PanelURL, cfg.AgentID)
	if err := a.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("run: %v", err)
	}
}

// runRole 以独立进程跑角色组件（E-5/E-7）：与核心只经 spool/配置文件交互，
// 不直连面板（同节点第二条面板连接会把核心踢下线）。
func runRole(name string, cfg config.Config) {
	if !roles.Standalone(name) {
		log.Fatalf("unknown standalone role %q (known: probe, relay)", name)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	logger := log.New(os.Stderr, "agent-"+name+" ", log.LstdFlags)
	var err error
	switch name {
	case "probe":
		log.Printf("ferry-agent %s role=probe starting (%d specs)", version, len(cfg.Probes))
		err = proberole.Run(ctx, cfg, logger)
	case "relay":
		log.Printf("ferry-agent %s role=relay starting", version)
		err = relay.Run(ctx, cfg.Relay, logger)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("role %s: %v", name, err)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
