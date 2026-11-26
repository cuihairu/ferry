// Package app wires up the agent business logic: the hello handshake, periodic heartbeats and message dispatch.
package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"time"

	"github.com/cuihairu/ferry/agent/internal/certwatch"
	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/agent/internal/configd"
	"github.com/cuihairu/ferry/agent/internal/host"
	"github.com/cuihairu/ferry/agent/internal/link"
	"github.com/cuihairu/ferry/agent/internal/probe"
	"github.com/cuihairu/ferry/agent/internal/procs"
	"github.com/cuihairu/ferry/agent/internal/roles/speedtest"
	"github.com/cuihairu/ferry/agent/internal/spool"
	"github.com/cuihairu/ferry/agent/internal/traffic"
	"github.com/cuihairu/ferry/agent/internal/upgrade"
	"github.com/cuihairu/ferry/packages/agentproto"
)

const (
	helloTimeout = 10 * time.Second
	sendTimeout  = 10 * time.Second

	highLoadCPU      = 90.0             // CPU usage alarm threshold
	highLoadStreak   = 2                // consecutive heartbeats above the threshold
	highLoadCooldown = 30 * time.Minute // minimum interval between alarms of the same kind
)

// App implements link.Handlers and holds runtime state.
type App struct {
	cfg     config.Config
	version string
	log     *log.Logger
	mgr     *procs.Manager
	cfgd    *configd.Deployer

	mu       sync.Mutex
	hbStop   chan struct{}
	interval time.Duration

	probe *probe.Runner
	traf  *traffic.Reporter

	// Alarm throttling state (A-7): a certificate keyed by "domain+validity" is reported once; high load is gated by a cooldown period.
	alarmMu    sync.Mutex
	certAlarms map[string]time.Time
	lw         loadWatch

	// OnHelloOK is called when the hello handshake succeeds (the A-23 upgrade verification commit point).
	OnHelloOK func()

	sendMu  sync.Mutex
	curSend func(agentproto.Envelope) error // sender of the current connection; cleared on disconnect

	// calibrated ensures speed-test calibration runs once per process (no retest on reconnect, E-8).
	calMu      sync.Mutex
	calibrated bool

	hbSeq int
	hello chan agentproto.Envelope

	// Procs returns a snapshot of process states (used by the heartbeat payload).
	Procs func() []agentproto.ProcStatus
}

// New creates the app and wires up the process manager per configuration.
func New(cfg config.Config, version string) *App {
	a := &App{
		cfg:        cfg,
		version:    version,
		log:        log.New(os.Stderr, "agent ", log.LstdFlags),
		interval:   cfg.HeartbeatInterval(),
		certAlarms: map[string]time.Time{},
	}
	a.mgr = procs.NewWithVersion(cfg.Procs, a.log, version)
	a.mgr.OnStatusChange = a.reportStatus
	a.mgr.OnAlarm = a.reportAlarm
	a.Procs = a.mgr.Statuses
	a.cfgd = configd.New(a.log)
	a.probe = probe.New(cfg.Probes, a.log)
	a.probe.SetSend(a.sendIfConnected)
	a.traf = traffic.New(cfg.Procs, cfg.TrafficInterval(), traffic.Dispatch(cfg.Procs), a.log)
	return a
}

// Run blocks until the ctx is cancelled: it starts process supervision and edge probing first, then maintains the connection to the panel.
func (a *App) Run(ctx context.Context) error {
	a.mgr.Start(ctx)
	if a.cfg.SpoolDir != "" {
		// Role mode: probing runs in the standalone -role probe process and reports through the spool;
		// the core no longer runs built-in probing, avoiding duplicate conclusions.
		go func() {
			_ = spool.Watch(ctx, a.cfg.SpoolDir, a.sendIfConnected)
		}()
	} else {
		go a.probe.Run(ctx)
	}
	go a.traf.Run(ctx)
	client := link.New(link.Options{
		URL:      a.cfg.PanelURL,
		CAFile:   a.cfg.TLS.CAFile,
		CertFile: a.cfg.TLS.CertFile,
		KeyFile:  a.cfg.TLS.KeyFile,
		Log:      a.log,
	})
	return client.Run(ctx, a)
}

// maybeCalibrate runs a light speed test once after registration and reports it (E-8): skip when
// there is no plan download capacity (pure relay / unpriced nodes are not tested); a failed test is only logged and does not affect being online.
func (a *App) maybeCalibrate(send func(agentproto.Envelope) error) {
	a.calMu.Lock()
	if a.calibrated {
		a.calMu.Unlock()
		return
	}
	a.calibrated = true
	a.calMu.Unlock()

	plan := a.cfg.Meta.BwDownMbps
	if plan <= 0 {
		return
	}
	base, err := speedtest.BaseURL(a.cfg.PanelURL)
	if err != nil {
		a.log.Printf("calibrate: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), speedtest.Timeout)
	defer cancel()
	res, err := speedtest.Measure(ctx, base, speedtest.DefaultBytes)
	if err != nil {
		a.log.Printf("calibrate: measure: %v", err)
		return
	}
	env, err := agentproto.NewEnvelope(
		fmt.Sprintf("cal-%d", time.Now().UnixNano()),
		agentproto.MsgCalibrate,
		agentproto.Calibrate{
			MeasuredDownMbps: res.Mbps,
			Bytes:            res.Bytes,
			Seconds:          res.Seconds,
		},
	)
	if err != nil {
		return
	}
	if err := send(env); err != nil {
		a.log.Printf("calibrate: send: %v", err)
	}
}

// doUpgrade performs self-upgrade in the background (A-23): failures are only logged and the binary is left as it is.
// Progress is reported per state (F-0712) under an envelope id derived from the command id, so the panel
// can dedupe retransmissions per state.
func (a *App) doUpgrade(up agentproto.Upgrade, id string) {
	report := func(st agentproto.UpgradeStatus) {
		st.UpgradeID = id
		env, err := agentproto.NewEnvelope(id+"-"+st.State, agentproto.MsgUpgradeStatus, st)
		if err != nil {
			return
		}
		_ = a.sendIfConnected(env)
	}
	self, err := upgrade.Self()
	if err != nil {
		a.log.Printf("upgrade: locate self: %v", err)
		report(agentproto.UpgradeStatus{Version: up.Version, State: agentproto.UpgradeStateFailed, Error: err.Error()})
		return
	}
	if err := upgrade.DoWithProgress(context.Background(), self, up, a.log, report); err != nil {
		a.log.Printf("upgrade to %s failed: %v", up.Version, err)
	}
}

// reportStatus pushes process state changes to the panel (dropped silently when offline).
// a fresh envelope ID is generated per report: the panel makes reports idempotent by (node, type, ID) (FERRY-004),
// a new ID means a new frame; older versions sending an empty ID are not deduplicated by the panel, which stays compatible.
func (a *App) reportStatus(status agentproto.ProcStatus) {
	env, err := agentproto.NewEnvelope(fmt.Sprintf("proc-%d", time.Now().UnixNano()), agentproto.MsgProcReport, agentproto.ProcReport{
		Procs: []agentproto.ProcStatus{status},
	})
	if err != nil {
		return
	}
	_ = a.sendIfConnected(env)
}

// reportAlarm reports an abnormal alarm.
func (a *App) reportAlarm(al agentproto.Alarm) {
	env, err := agentproto.NewEnvelope(fmt.Sprintf("alarm-%d", time.Now().UnixNano()), agentproto.MsgAlarm, al)
	if err != nil {
		return
	}
	_ = a.sendIfConnected(env)
}

func (a *App) sendIfConnected(env agentproto.Envelope) error {
	a.sendMu.Lock()
	send := a.curSend
	a.sendMu.Unlock()
	if send == nil {
		return errors.New("not connected")
	}
	return send(env)
}

func (a *App) setSend(send func(agentproto.Envelope) error) {
	a.sendMu.Lock()
	a.curSend = send
	a.sendMu.Unlock()
}

// OnConnected sends hello and waits for the reply; a timeout counts as a handshake failure.
func (a *App) OnConnected(ctx context.Context, send func(agentproto.Envelope) error) error {
	a.stopHeartbeat()
	a.setSend(send)
	a.traf.SetSend(send)
	ch := make(chan agentproto.Envelope, 1)
	a.mu.Lock()
	a.hello = ch
	a.mu.Unlock()

	hostname, _ := os.Hostname()
	env, err := agentproto.NewEnvelope("hello", agentproto.MsgHello, agentproto.Hello{
		Token:     a.cfg.Token,
		AgentID:   a.cfg.AgentID,
		Version:   a.version,
		Hostname:  hostname,
		StartedAt: time.Now(),
		Meta:      a.cfg.Meta,
		Caps:      staticCaps(a.cfg),
	})
	if err != nil {
		return err
	}
	a.log.Printf("connected, sending hello (agent_id=%s)", a.cfg.AgentID)
	if err := send(env); err != nil {
		return err
	}
	select {
	case ack := <-ch:
		var ha agentproto.HelloAck
		if err := ack.Decode(&ha); err == nil {
			if ha.HeartbeatIntervalSec > 0 {
				a.setInterval(time.Duration(ha.HeartbeatIntervalSec) * time.Second)
			}
			// The panel is the authoritative source of metadata: values returned by hello_ack override the local defaults.
			if ha.Meta.Role != "" || ha.Meta.ISP != "" {
				ha.Meta.Normalize()
				a.cfg.Meta = ha.Meta
			}
		}
		a.startHeartbeat(ctx, send)
		go a.maybeCalibrate(send)
		if a.OnHelloOK != nil {
			a.OnHelloOK()
		}
		return nil
	case <-time.After(helloTimeout):
		return errors.New("hello ack timeout")
	case <-ctx.Done():
		return ctx.Err()
	}
}

// OnMessage dispatches panel messages.
func (a *App) OnMessage(_ context.Context, env agentproto.Envelope, send func(agentproto.Envelope) error) {
	switch env.Type {
	case agentproto.MsgHelloAck:
		a.mu.Lock()
		ch := a.hello
		a.mu.Unlock()
		if ch != nil {
			select {
			case ch <- env:
			default:
			}
		}
	case agentproto.MsgHeartbeatAck:
		var ha agentproto.HeartbeatAck
		if err := env.Decode(&ha); err == nil && ha.NextIntervalSec > 0 {
			a.setInterval(time.Duration(ha.NextIntervalSec) * time.Second)
		}
	case agentproto.MsgUpgrade:
		// Self-upgrade (A-23): reply with acceptance first, then download, replace and restart in the background; rejection reasons are sent back with the ack.
		var up agentproto.Upgrade
		ack := agentproto.UpgradeAck{}
		if err := env.Decode(&up); err != nil {
			ack.Error = err.Error()
		} else if up.Version == "" || up.URL == "" {
			ack.Error = "version and url are required"
		} else {
			ack.Version = up.Version
			ack.OK = true
			go a.doUpgrade(up, env.ID)
		}
		reply, _ := agentproto.NewEnvelope(env.ID, agentproto.MsgUpgradeAck, ack)
		_ = a.sendIfConnected(reply)
	case agentproto.MsgProcCtl:
		var ctl agentproto.ProcCtl
		ack := agentproto.ProcCtlAck{Proc: ctl.Proc, Action: ctl.Action}
		if err := env.Decode(&ctl); err != nil {
			ack.OK = false
			ack.Error = err.Error()
		} else {
			ack.Proc = ctl.Proc
			ack.Action = ctl.Action
			if err := a.mgr.Control(ctl.Proc, ctl.Action); err != nil {
				ack.OK = false
				ack.Error = err.Error()
			} else {
				ack.OK = true
			}
		}
		reply, _ := agentproto.NewEnvelope(env.ID, agentproto.MsgProcCtlAck, ack)
		_ = a.sendIfConnected(reply)
	case agentproto.MsgProcLogs:
		// Process log fetching (P1-11): return the most recent limit lines from the process ring buffer.
		var req agentproto.ProcLogsReq
		ack := agentproto.ProcLogsAck{}
		if err := env.Decode(&req); err != nil {
			ack.Error = err.Error()
		} else {
			ack.Proc = req.Proc
			if req.Limit <= 0 {
				req.Limit = 200
			}
			if req.Limit > 1000 {
				req.Limit = 1000
			}
			lines, err := a.mgr.Logs(req.Proc, req.Limit)
			if err != nil {
				ack.Error = err.Error()
			} else {
				ack.Lines = lines
			}
		}
		reply, _ := agentproto.NewEnvelope(env.ID, agentproto.MsgProcLogsAck, ack)
		_ = a.sendIfConnected(reply)
	case agentproto.MsgConfigPush:
		// Applying a push takes time (validation command + reload), so it runs in the background to avoid blocking the read loop; serialization is guaranteed by the Deployer.
		go a.handleConfigPush(env)
	case agentproto.MsgAlarmAck, agentproto.MsgTrafficAck:
		// The panel's acknowledgement of an agent report; nothing to do.
	case agentproto.MsgCalibrateAck:
		var ca agentproto.CalibrateAck
		if err := env.Decode(&ca); err == nil {
			a.log.Printf("calibrate ack: accepted=%v effective=%dMbps (%s)", ca.Accepted, ca.EffectiveDownMbps, ca.Note)
		}
	default:
		a.log.Printf("unknown message type %q", env.Type)
	}
}

// OnDisconnected stops this round of heartbeats and clears the sender.
func (a *App) OnDisconnected() {
	a.stopHeartbeat()
	a.setSend(nil)
	a.traf.SetSend(nil)
}

// handleConfigPush applies a config push and replies with config.ack (A-16/A-17).
func (a *App) handleConfigPush(env agentproto.Envelope) {
	var push agentproto.ConfigPush
	if err := env.Decode(&push); err != nil {
		a.log.Printf("config_push decode: %v", err)
		return // cannot locate proc/version, so no ack can be built; give up
	}
	ack := agentproto.ConfigAck{Proc: push.Proc, Version: push.Version}
	spec, ok := a.procSpec(push.Proc)
	if !ok {
		ack.Error = fmt.Sprintf("unknown proc %q", push.Proc)
	} else {
		ack = a.cfgd.Apply(spec, push, func() error {
			return a.mgr.Control(push.Proc, agentproto.ProcActionReload)
		})
	}
	reply, err := agentproto.NewEnvelope(env.ID, agentproto.MsgConfigAck, ack)
	if err != nil {
		return
	}
	if err := a.sendIfConnected(reply); err != nil {
		a.log.Printf("send config_ack: %v", err)
	}
}

// procSpec looks up a process spec by name.
func (a *App) procSpec(name string) (config.ProcSpec, bool) {
	for _, p := range a.cfg.Procs {
		if p.Name == name {
			return p, true
		}
	}
	return config.ProcSpec{}, false
}

func (a *App) setInterval(d time.Duration) {
	a.mu.Lock()
	a.interval = d
	a.mu.Unlock()
}

func (a *App) currentInterval() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.interval
}

func (a *App) startHeartbeat(ctx context.Context, send func(agentproto.Envelope) error) {
	stop := make(chan struct{})
	a.mu.Lock()
	a.hbStop = stop
	a.mu.Unlock()

	go func() {
		timer := time.NewTimer(a.currentInterval())
		defer timer.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-stop:
				return
			case <-timer.C:
			}
			a.mu.Lock()
			a.hbSeq++
			seq := a.hbSeq
			a.mu.Unlock()

			env, err := agentproto.NewEnvelope(fmt.Sprintf("hb-%d", seq), agentproto.MsgHeartbeat, a.buildHeartbeat())
			if err == nil {
				if err := send(env); err != nil {
					a.log.Printf("send heartbeat: %v", err)
					return
				}
			}
			timer.Reset(a.currentInterval())
		}
	}()
}

func (a *App) stopHeartbeat() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.hbStop != nil {
		close(a.hbStop)
		a.hbStop = nil
	}
}

func (a *App) buildHeartbeat() agentproto.Heartbeat {
	s := host.Sample()
	hb := agentproto.Heartbeat{
		UptimeSec:     s.UptimeSec,
		Load1:         s.Load1,
		CPUUtil:       s.CPUUtil,
		MemUsedBytes:  s.MemUsedBytes,
		MemTotalBytes: s.MemTotalBytes,
		NetRxBytes:    s.NetRxBytes,
		NetTxBytes:    s.NetTxBytes,
		Conns:         s.TCPConns,
		Certs:         a.collectCerts(),
		Procs:         []agentproto.ProcStatus{},
		At:            time.Now(),
	}
	if a.Procs != nil {
		hb.Procs = a.Procs()
	}
	// Runtime capability state rides every heartbeat (F-0229): derived from the
	// same component snapshot the panel persists as proc statuses.
	hb.Caps = dynamicCaps(a.cfg, hb.Procs)
	hb.RelayCaps = relayCaps(a.cfg)
	a.watchAlarms(hb)
	return hb
}

// collectCerts aggregates certificate state from managed process configs (A-3).
func (a *App) collectCerts() []agentproto.CertStatus {
	out := []agentproto.CertStatus{}
	for _, p := range a.cfg.Procs {
		if p.ConfigPath == "" {
			continue
		}
		out = append(out, certwatch.FromFile(p.ConfigPath)...)
	}
	return out
}

// watchAlarms raises certificate-expiry and sustained high-load alarms from heartbeat samples (A-7), with throttling built in.
func (a *App) watchAlarms(hb agentproto.Heartbeat) {
	now := time.Now()
	a.alarmMu.Lock()
	defer a.alarmMu.Unlock()
	for _, c := range certwatch.Expiring(hb.Certs, now, certwatch.ExpiryWindow) {
		// the same certificate with the same validity is reported once; after renewal (a NotAfter change) it becomes eligible to alarm again.
		if last, ok := a.certAlarms[c.Domain]; ok && last.Equal(c.NotAfter) {
			continue
		}
		a.certAlarms[c.Domain] = c.NotAfter
		msg := fmt.Sprintf("证书 %s 将于 %s 到期", c.Domain, c.NotAfter.Format("2006-01-02"))
		if ttl := time.Until(c.NotAfter); ttl < 0 {
			msg = fmt.Sprintf("证书 %s 已于 %s 过期", c.Domain, c.NotAfter.Format("2006-01-02"))
		} else {
			msg += fmt.Sprintf("（剩 %d 天）", int(ttl.Hours()/24))
		}
		a.reportAlarm(agentproto.Alarm{
			Kind: agentproto.AlarmKindCertExpiry, Severity: "warn", Message: msg, At: now,
		})
	}
	if a.lw.observe(hb.CPUUtil, now) {
		a.reportAlarm(agentproto.Alarm{
			Kind: agentproto.AlarmKindHighLoad, Severity: "warn",
			Message: fmt.Sprintf("CPU 使用率持续 ≥ %.0f%%", highLoadCPU), At: now,
		})
	}
}

// loadWatch decides sustained high load: alarm once after streak consecutive readings above the threshold, and do not repeat inside the cooldown period.
type loadWatch struct {
	streak int
	last   time.Time
}

func (w *loadWatch) observe(cpu float64, now time.Time) bool {
	if cpu < highLoadCPU {
		w.streak = 0
		return false
	}
	w.streak++
	if w.streak < highLoadStreak {
		return false
	}
	if now.Sub(w.last) < highLoadCooldown {
		return false
	}
	w.last = now
	w.streak = 0
	return true
}

var _ link.Handlers = (*App)(nil)
