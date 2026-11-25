// Package traffic periodically collects process traffic and reports it (A-19/A-21).
// Collection implementations register by kind: xray uses gRPC stats to collect real per-user traffic (P1-3),
// other kinds stay Noop (marked unimplemented; when nothing can be collected nothing is reported, keeping zero-value noise out of accounting).
package traffic

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/agent/internal/xraystats"
	"github.com/cuihairu/ferry/packages/agentproto"
)

// errNotImplemented marks a kind whose collection is not implemented yet.
var errNotImplemented = errors.New("traffic collection not implemented for this kind")

// Collector returns one process's traffic delta since the last query and its online connection count.
type Collector interface {
	Collect(proc string) (rx, tx uint64, conns int, err error)
}

// UserCollector is a collector able to split traffic deltas per user (P1-3 xray gRPC stats);
// when an implementation exists the Reporter prefers the per-user path, with the node-level figure summed from per-user values.
type UserCollector interface {
	Collector
	CollectUsers(proc string) ([]agentproto.UserTraffic, error)
}

// OutboundCollector is a collector able to collect routed outbound byte deltas (SAVE-4 blocked /
// SAVE-7 direct, both fetched in one xray outbound counter query); when an implementation exists the Reporter
// attaches both delta sets in the same cycle, and an error is treated as missing (routed counters never disturb the main traffic accounting flow).
type OutboundCollector interface {
	CollectOutbound(proc string) (direct, blocked uint64, err error)
}

// Noop is an empty implementation that does nothing, matching the P0 boundary pattern of the panel-side xray.NoopHandler.
type Noop struct{}

func (Noop) Collect(string) (uint64, uint64, int, error) {
	return 0, 0, 0, errNotImplemented
}

// CollectorFor returns a collector by kind; an unconnected kind gets a Noop.
func CollectorFor(kind string) Collector {
	return Noop{}
}

// the xray collector satisfies the per-user rule (a compile-time assertion).
var _ UserCollector = (*xraystats.Collector)(nil)

// Dispatch routes a collection request to the collector of the right kind per the process spec.
func Dispatch(specs []config.ProcSpec) Collector {
	byProc := map[string]Collector{}
	for _, s := range specs {
		if s.Kind == "xray" && s.StatsAPI != "" {
			byProc[s.Name] = xraystats.NewCollector(s.StatsAPI, nil)
			continue
		}
		byProc[s.Name] = CollectorFor(s.Kind)
	}
	return dispatchCollector(byProc)
}

type dispatchCollector map[string]Collector

func (d dispatchCollector) Collect(proc string) (uint64, uint64, int, error) {
	c, ok := d[proc]
	if !ok {
		return 0, 0, 0, fmt.Errorf("no collector for proc %q", proc)
	}
	return c.Collect(proc)
}

// CollectUsers routes per-user collection to the internal UserCollector;
// when the proc has no real collection attached (Noop) it returns not-implemented and the caller skips it.
func (d dispatchCollector) CollectUsers(proc string) ([]agentproto.UserTraffic, error) {
	uc, ok := d[proc].(UserCollector)
	if !ok {
		return nil, errNotImplemented
	}
	return uc.CollectUsers(proc)
}

var _ UserCollector = dispatchCollector{}

// Reporter collects every process periodically and reports in batches.
type Reporter struct {
	log       *log.Logger
	interval  time.Duration
	procs     []string
	collector Collector

	sendMu sync.Mutex
	send   func(agentproto.Envelope) error // sender of the current connection; cleared on disconnect

	// collectErr remembers each process's already-reported collection errors, so the same kind of error is logged once.
	collectErr map[string]bool
}

// New creates a reporter. procs is the list of managed process names; interval <=0 disables periodic collection.
func New(specs []config.ProcSpec, interval time.Duration, collector Collector, logger *log.Logger) *Reporter {
	if logger == nil {
		logger = log.Default()
	}
	names := make([]string, 0, len(specs))
	for _, s := range specs {
		names = append(names, s.Name)
	}
	return &Reporter{
		log:        logger,
		interval:   interval,
		procs:      names,
		collector:  collector,
		collectErr: map[string]bool{},
	}
}

// SetSend binds the sender of the current connection.
func (r *Reporter) SetSend(send func(agentproto.Envelope) error) {
	r.sendMu.Lock()
	r.send = send
	r.sendMu.Unlock()
}

// Run collects and reports periodically until the ctx is cancelled.
func (r *Reporter) Run(ctx context.Context) {
	if r.interval <= 0 {
		return
	}
	timer := time.NewTimer(r.interval)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
		if items := r.collectAll(); len(items) > 0 {
			r.report(items)
		}
		timer.Reset(r.interval)
	}
}

// collectAll collects every process; a process whose collection fails is skipped (the same kind of error is logged once).
// when the collector supports the per-user rule it prefers CollectUsers and the node-level delta is summed from per-user values
// (avoiding a second reset-stat query for the node level, which would zero out the delta).
func (r *Reporter) collectAll() []agentproto.ProcTraffic {
	now := time.Now()
	items := make([]agentproto.ProcTraffic, 0, len(r.procs))
	for _, name := range r.procs {
		if uc, ok := r.collector.(UserCollector); ok {
			item, err := collectUsers(uc, name, now)
			if err != nil {
				r.noteCollectErr(name, err)
				continue
			}
			r.attachOutbound(&item)
			delete(r.collectErr, name)
			items = append(items, item)
			continue
		}
		rx, tx, conns, err := r.collector.Collect(name)
		if err != nil {
			r.noteCollectErr(name, err)
			continue
		}
		delete(r.collectErr, name)
		item := agentproto.ProcTraffic{
			Proc: name, Rx: rx, Tx: tx, Conns: conns, At: now,
		}
		r.attachOutbound(&item)
		items = append(items, item)
	}
	return items
}

// attachOutbound attaches this cycle's routed deltas to a traffic line (SAVE-4 blocked / SAVE-7 direct):
// when the collector does not support it or the query errors, the defaults are kept and traffic accounting is never dragged down.
func (r *Reporter) attachOutbound(item *agentproto.ProcTraffic) {
	oc, ok := r.collector.(OutboundCollector)
	if !ok {
		return
	}
	if direct, blocked, err := oc.CollectOutbound(item.Proc); err == nil {
		item.DirectBytes = direct
		item.BlockedBytes = blocked
	}
}

// collectUsers collects per-user deltas and sums the node-level delta.
func collectUsers(uc UserCollector, name string, now time.Time) (agentproto.ProcTraffic, error) {
	users, err := uc.CollectUsers(name)
	if err != nil {
		return agentproto.ProcTraffic{}, err
	}
	item := agentproto.ProcTraffic{Proc: name, At: now}
	for _, u := range users {
		item.Rx += u.Rx
		item.Tx += u.Tx
	}
	item.Users = users
	return item, nil
}

// noteCollectErr records a collection error: the same kind is logged only once.
func (r *Reporter) noteCollectErr(name string, err error) {
	if !r.collectErr[name] {
		r.collectErr[name] = true
		r.log.Printf("collect traffic proc=%s: %v (won't repeat)", name, err)
	}
}

// report reports in batch; dropped silently while offline (periodic data, reported again next round).
func (r *Reporter) report(items []agentproto.ProcTraffic) {
	r.sendMu.Lock()
	send := r.send
	r.sendMu.Unlock()
	if send == nil {
		return
	}
	env, err := agentproto.NewEnvelope(
		fmt.Sprintf("traffic-%d", time.Now().UnixNano()),
		agentproto.MsgTraffic,
		agentproto.TrafficReport{Items: items},
	)
	if err != nil {
		return
	}
	if err := send(env); err != nil {
		r.log.Printf("send traffic report: %v", err)
	}
}
