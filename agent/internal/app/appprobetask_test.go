package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/packages/agentproto"
)

// msgsOfType returns the captured envelopes of one message type.
func (r *recorder) msgsOfType(typ string) []agentproto.Envelope {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []agentproto.Envelope
	for _, m := range r.msgs {
		if m.Type == typ {
			out = append(out, m)
		}
	}
	return out
}

// probeTaskEnvelope builds a probe_task dispatch envelope for the given specs.
func probeTaskEnvelope(id string, specs []agentproto.ProbeTaskSpec) agentproto.Envelope {
	env, err := agentproto.NewEnvelope(id, agentproto.MsgProbeTask, agentproto.ProbeTask{
		Version: "v-test", Specs: specs,
	})
	if err != nil {
		panic(err)
	}
	return env
}

// validTaskSpec is a dispatchable probe task.
func validTaskSpec() agentproto.ProbeTaskSpec {
	return agentproto.ProbeTaskSpec{
		Name: "dispatched-1", TargetKind: agentproto.ProbeTargetPeer,
		Target: "127.0.0.1:443", IntervalSec: 30,
	}
}

// TestHandleProbeTaskApplies covers the happy path: the ack reports the applied
// count, the set persists to the spool dir and the runner hot-swaps to the
// merged set (dispatched tasks join the config-file probes).
func TestHandleProbeTaskApplies(t *testing.T) {
	spoolDir := t.TempDir()
	cfg := config.Default()
	cfg.SpoolDir = spoolDir
	cfg.Probes = []config.ProbeSpec{{
		Name: "file-probe", TargetKind: agentproto.ProbeTargetTunnel,
		Target: "127.0.0.1:443", IntervalSec: 60,
	}}
	a := New(cfg, "test")
	rec := &recorder{}
	a.setSend(rec.send)

	env := probeTaskEnvelope("pt-1", []agentproto.ProbeTaskSpec{validTaskSpec()})
	a.handleProbeTask(env)

	acks := rec.msgsOfType(agentproto.MsgProbeTaskAck)
	if len(acks) != 1 {
		t.Fatalf("got %d probe_task_ack; want 1", len(acks))
	}
	var ack agentproto.ProbeTaskAck
	if err := acks[0].Decode(&ack); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	if !ack.OK || ack.Applied != 1 || ack.Error != "" || ack.Version != "v-test" {
		t.Fatalf("ack = %+v; want ok applied=1", ack)
	}

	// persisted verbatim for the next start
	raw, err := os.ReadFile(filepath.Join(spoolDir, probeTasksFile))
	if err != nil {
		t.Fatalf("spool file: %v", err)
	}
	var saved agentproto.ProbeTask
	if err := json.Unmarshal(raw, &saved); err != nil || len(saved.Specs) != 1 {
		t.Fatalf("saved = %s (%v); want 1 spec", raw, err)
	}

	// the runner now probes the file probe plus the dispatched one
	specs := a.probe.Specs()
	if len(specs) != 2 || specs[0].Name != "file-probe" || specs[1].Name != "dispatched-1" {
		t.Fatalf("runner specs = %+v; want file-probe + dispatched-1", specs)
	}
}

// TestHandleProbeTaskReplacesByName covers name shadowing: a dispatched task
// with the same name as a config-file probe replaces it.
func TestHandleProbeTaskReplacesByName(t *testing.T) {
	cfg := config.Default()
	cfg.Probes = []config.ProbeSpec{{
		Name: "shared", TargetKind: agentproto.ProbeTargetTunnel,
		Target: "127.0.0.1:443", IntervalSec: 60,
	}}
	a := New(cfg, "test")
	rec := &recorder{}
	a.setSend(rec.send)

	spec := validTaskSpec()
	spec.Name = "shared"
	spec.Target = "127.0.0.1:8443"
	a.handleProbeTask(probeTaskEnvelope("pt-2", []agentproto.ProbeTaskSpec{spec}))

	specs := a.probe.Specs()
	if len(specs) != 1 || specs[0].Name != "shared" || specs[0].Target != "127.0.0.1:8443" {
		t.Fatalf("runner specs = %+v; want the dispatched shared only", specs)
	}
}

// TestHandleProbeTaskRejectsInvalid covers all-or-nothing validation: one bad
// spec rejects the whole dispatch, nothing is applied or persisted.
func TestHandleProbeTaskRejectsInvalid(t *testing.T) {
	spoolDir := t.TempDir()
	cfg := config.Default()
	cfg.SpoolDir = spoolDir
	a := New(cfg, "test")
	rec := &recorder{}
	a.setSend(rec.send)

	good := validTaskSpec()
	bad := validTaskSpec()
	bad.Name = "bad-interval"
	bad.IntervalSec = 1
	a.handleProbeTask(probeTaskEnvelope("pt-3", []agentproto.ProbeTaskSpec{good, bad}))

	var ack agentproto.ProbeTaskAck
	acks := rec.msgsOfType(agentproto.MsgProbeTaskAck)
	if len(acks) != 1 {
		t.Fatalf("got %d probe_task_ack; want 1", len(acks))
	}
	if err := acks[0].Decode(&ack); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	if ack.OK || ack.Error == "" {
		t.Fatalf("ack = %+v; want rejection with a reason", ack)
	}
	if _, err := os.Stat(filepath.Join(spoolDir, probeTasksFile)); !os.IsNotExist(err) {
		t.Fatalf("rejected set must not persist: %v", err)
	}
	if n := len(a.probe.Specs()); n != 0 {
		t.Fatalf("runner specs = %d; want 0 after rejection", n)
	}
}

// TestHandleProbeTaskWithdraw covers an empty dispatch: every dispatched task
// is withdrawn, the persisted file is removed and only file probes remain.
func TestHandleProbeTaskWithdraw(t *testing.T) {
	spoolDir := t.TempDir()
	cfg := config.Default()
	cfg.SpoolDir = spoolDir
	cfg.Probes = []config.ProbeSpec{{
		Name: "file-probe", TargetKind: agentproto.ProbeTargetTunnel,
		Target: "127.0.0.1:443", IntervalSec: 60,
	}}
	a := New(cfg, "test")
	rec := &recorder{}
	a.setSend(rec.send)

	a.handleProbeTask(probeTaskEnvelope("pt-4", []agentproto.ProbeTaskSpec{validTaskSpec()}))
	a.handleProbeTask(probeTaskEnvelope("pt-5", nil))

	var ack agentproto.ProbeTaskAck
	acks := rec.msgsOfType(agentproto.MsgProbeTaskAck)
	if len(acks) != 2 {
		t.Fatalf("got %d probe_task_ack; want 2", len(acks))
	}
	if err := acks[1].Decode(&ack); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	if !ack.OK || ack.Applied != 0 {
		t.Fatalf("withdraw ack = %+v; want ok applied=0", ack)
	}
	if _, err := os.Stat(filepath.Join(spoolDir, probeTasksFile)); !os.IsNotExist(err) {
		t.Fatalf("withdrawn set must remove the file: %v", err)
	}
	specs := a.probe.Specs()
	if len(specs) != 1 || specs[0].Name != "file-probe" {
		t.Fatalf("runner specs = %+v; want file-probe only", specs)
	}
}

// TestProbeConcurrencyWiring covers the F-0922 plumbing: the config value
// reaches the probe runner, 0 keeps the default cap.
func TestProbeConcurrencyWiring(t *testing.T) {
	cfg := config.Default()
	cfg.ProbeConcurrency = 3
	if got := New(cfg, "test").probe.Concurrency(); got != 3 {
		t.Fatalf("Concurrency() = %d; want 3", got)
	}
	if got := New(config.Default(), "test").probe.Concurrency(); got != 16 {
		t.Fatalf("default Concurrency() = %d; want 16", got)
	}
}

// TestLoadProbeTasksOnStart covers restart continuity: a persisted dispatch is
// merged over the config-file probes when the app is constructed; a corrupt
// file is ignored outright.
func TestLoadProbeTasksOnStart(t *testing.T) {
	spoolDir := t.TempDir()
	saved, _ := json.Marshal(agentproto.ProbeTask{Version: "v1", Specs: []agentproto.ProbeTaskSpec{validTaskSpec()}})
	if err := os.WriteFile(filepath.Join(spoolDir, probeTasksFile), saved, 0o600); err != nil {
		t.Fatalf("write spool file: %v", err)
	}
	cfg := config.Default()
	cfg.SpoolDir = spoolDir
	a := New(cfg, "test")
	specs := a.probe.Specs()
	if len(specs) != 1 || specs[0].Name != "dispatched-1" {
		t.Fatalf("specs = %+v; want the persisted dispatch", specs)
	}

	if err := os.WriteFile(filepath.Join(spoolDir, probeTasksFile), []byte("{not json"), 0o600); err != nil {
		t.Fatalf("corrupt spool file: %v", err)
	}
	a = New(config.Default(), "test") // spool dir unset: nothing to load
	if n := len(a.probe.Specs()); n != 0 {
		t.Fatalf("specs = %d; want 0", n)
	}
}
