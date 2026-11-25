// The agent command is the ferry-agent entry point: load config, then keep an outbound long-lived connection to the panel.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
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

// version is injected by the build; defaults to dev.
var version = "dev"

func main() {
	cfgPath := flag.String("config", envOr("FERRY_AGENT_CONFIG", "agent.json"), "配置文件路径")
	role := flag.String("role", "", "角色组件独立进程：probe / relay（空=核心）")
	relaySpec := flag.String("relay-spec", "", "relay 落地覆盖文件（E-16b）：面板 config.push 写入，SIGHUP 热重指；空=只用本地配置")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *role != "" {
		if err := runRole(ctx, *role, cfg, *relaySpec); err != nil && !errors.Is(err, context.Canceled) {
			log.Fatalf("role %s: %v", *role, err)
		}
		return
	}

	if err := runCore(ctx, cfg); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("run: %v", err)
	}
}

// runCore runs the core component: wire the app, then keep the outbound long-lived
// connection to the panel, including self-upgrade verification (A-23). Errors go to main for a uniform exit (context.Canceled = normal stop).
func runCore(ctx context.Context, cfg config.Config) error {
	a := app.New(cfg, version)

	// Self-upgrade verification (A-23): a pending marker means the post-upgrade startup
	// check never completed, so start a watchdog -- roll back if the panel is not reached in time; hello commits.
	if self, err := upgrade.Self(); err == nil {
		if upgrade.Pending(self) {
			log.Printf("pending upgrade verify, watchdog %s", upgrade.RollbackTimeout)
			go upgrade.Watchdog(self, upgrade.RollbackTimeout, log.Default())
		}
		a.OnHelloOK = func() { upgrade.Commit(self) }
	}

	log.Printf("ferry-agent %s starting, panel=%s agent_id=%s", version, cfg.PanelURL, cfg.AgentID)
	return a.Run(ctx)
}

// runRole runs a role component as a standalone process (E-5/E-7): it interacts with the core only via
// spool/config files and never connects to the panel directly (a second panel connection on the same node
// would kick the core offline). Errors go to main (a uniform exit: context.Canceled is a normal stop, not Fatal).
func runRole(ctx context.Context, name string, cfg config.Config, relaySpecPath string) error {
	if !roles.Standalone(name) {
		return fmt.Errorf("unknown standalone role %q (known: probe, relay)", name)
	}
	logger := log.New(os.Stderr, "agent-"+name+" ", log.LstdFlags)
	switch name {
	case "probe":
		log.Printf("ferry-agent %s role=probe starting (%d specs)", version, len(cfg.Probes))
		return proberole.Run(ctx, cfg, logger)
	case "relay":
		if relaySpecPath != "" {
			log.Printf("ferry-agent %s role=relay starting (override %s)", version, relaySpecPath)
			return relay.RunFile(ctx, cfg.Relay, relaySpecPath, logger)
		}
		log.Printf("ferry-agent %s role=relay starting", version)
		return relay.Run(ctx, cfg.Relay, logger)
	}
	return nil // unreachable: Standalone only allows probe/relay
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
