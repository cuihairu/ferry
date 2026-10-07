// Package proberole 是边缘探测器组件的独立进程形态（E-7）。
//
// 运行：ferry-agent -role probe。只读配置里的 probes 段，经 spool 上报
// 结论（不直连面板，单连接归核心）。崩溃时 procs 监管按退避拉起，
// 最坏情况丢探测结论，心跳与流量上报不受影响。
package proberole

import (
	"context"
	"log"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/agent/internal/probe"
	"github.com/cuihairu/ferry/agent/internal/spool"
	"github.com/cuihairu/ferry/packages/agentproto"
)

// Run 阻塞运行到 ctx 取消。
func Run(ctx context.Context, cfg config.Config, logger *log.Logger) error {
	if cfg.SpoolDir == "" {
		logger.Print("probe role: no spool_dir, reports go nowhere; set spool_dir to forward via core")
	}
	r := probe.New(cfg.Probes, logger)
	r.SetSend(func(env agentproto.Envelope) error {
		if cfg.SpoolDir == "" {
			return nil // 无 spool 即黑洞（调试形态），不断言失败
		}
		return spool.Write(cfg.SpoolDir, "probe", env)
	})
	logger.Printf("probe role started (%d specs)", len(cfg.Probes))
	r.Run(ctx)
	return ctx.Err()
}
