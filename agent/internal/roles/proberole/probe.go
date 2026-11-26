// Package proberole is the standalone-process form of the edge prober component (E-7).
//
// Run: ferry-agent -role probe. It reads only the probes section of the config and reports conclusions
// through the spool (no direct panel connection; the single connection belongs to the core). On crash the procs supervision restarts it with backoff,
// the worst case being lost probe conclusions, while the heartbeat and traffic reporting are unaffected.
package proberole

import (
	"context"
	"log"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/agent/internal/probe"
	"github.com/cuihairu/ferry/agent/internal/spool"
	"github.com/cuihairu/ferry/packages/agentproto"
)

// Run blocks until the ctx is cancelled.
func Run(ctx context.Context, cfg config.Config, logger *log.Logger) error {
	if cfg.SpoolDir == "" {
		logger.Print("probe role: no spool_dir, reports go nowhere; set spool_dir to forward via core")
	}
	r := probe.New(cfg.Probes, logger)
	r.SetConcurrency(cfg.ProbeConcurrency) // F-0922
	// The standalone prober skips the F-0926 start jitter: its spec set is the
	// operator's local config (no panel dispatch herd), and it is supervised --
	// after a crash restart, delaying the first round leaves a blind window.
	r.SetStartJitter(0)
	r.SetSend(func(env agentproto.Envelope) error {
		if cfg.SpoolDir == "" {
			return nil // with no spool it is a black hole (debugging form), never asserting failure
		}
		return spool.Write(cfg.SpoolDir, "probe", env)
	})
	logger.Printf("probe role started (%d specs)", len(cfg.Probes))
	r.Run(ctx)
	return ctx.Err()
}
