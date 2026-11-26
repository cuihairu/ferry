package app

import (
	"context"
	"errors"
	"log"
	"testing"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/packages/agentproto"
)

// Dispatch tests for the panel-triggered agent operations (F-1621 restart,
// F-1626 rollback, F-1627 reprobe): the wire ack goes out first, the destructive
// action runs through the injectable hooks so no test re-execs the binary.

// TestOnMessageAgentRestart covers F-1621: OK ack on the wire, then the
// restart hook fires.
func TestOnMessageAgentRestart(t *testing.T) {
	a := New(config.Default(), "test")
	rec := &recorder{}
	a.setSend(rec.send)

	restarted := make(chan struct{})
	a.restartFn = func() { close(restarted) }

	env, _ := agentproto.NewEnvelope("ar-1", agentproto.MsgAgentRestart, struct{}{})
	a.OnMessage(context.Background(), env, a.sendIfConnected)

	acks := rec.msgsOfType(agentproto.MsgAgentRestartAck)
	if len(acks) != 1 {
		t.Fatalf("got %d restart acks; want 1", len(acks))
	}
	var ack agentproto.AgentRestartAck
	if err := acks[0].Decode(&ack); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	if !ack.OK {
		t.Fatalf("ack not OK: %+v", ack)
	}
	select {
	case <-restarted:
	case <-time.After(2 * time.Second):
		t.Fatal("restartFn was not invoked")
	}
}

// TestOnMessageAgentRollback covers F-1626 both ways: a failure (the real
// RollbackNow refuses when no backup binary exists next to the test binary)
// answers not-OK and does not restart; success answers OK.
func TestOnMessageAgentRollback(t *testing.T) {
	// Refusal path: the default rollbackFn stats for <self>.bak, which does not
	// exist for a test binary, so the command must be refused.
	a := New(config.Default(), "test")
	rec := &recorder{}
	a.setSend(rec.send)

	env, _ := agentproto.NewEnvelope("rb-1", agentproto.MsgAgentRollback, struct{}{})
	a.OnMessage(context.Background(), env, a.sendIfConnected)

	acks := rec.msgsOfType(agentproto.MsgAgentRollbackAck)
	if len(acks) != 1 {
		t.Fatalf("got %d rollback acks; want 1", len(acks))
	}
	var nack agentproto.AgentRollbackAck
	if err := acks[0].Decode(&nack); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	if nack.OK || nack.Error == "" {
		t.Fatalf("want refused ack with reason, got %+v", nack)
	}

	// Success path: the injected hook restores the backup and re-execs.
	a2 := New(config.Default(), "test")
	rec2 := &recorder{}
	a2.setSend(rec2.send)
	rolled := make(chan struct{})
	a2.rollbackFn = func(_ *log.Logger) error {
		close(rolled)
		return nil
	}
	env2, _ := agentproto.NewEnvelope("rb-2", agentproto.MsgAgentRollback, struct{}{})
	a2.OnMessage(context.Background(), env2, a2.sendIfConnected)

	acks2 := rec2.msgsOfType(agentproto.MsgAgentRollbackAck)
	if len(acks2) != 1 {
		t.Fatalf("got %d rollback acks; want 1", len(acks2))
	}
	var okAck agentproto.AgentRollbackAck
	if err := acks2[0].Decode(&okAck); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	if !okAck.OK {
		t.Fatalf("ack not OK: %+v", okAck)
	}
	select {
	case <-rolled:
	case <-time.After(2 * time.Second):
		t.Fatal("rollbackFn was not invoked")
	}
}

// TestOnMessageAgentRollbackError keeps the refusal generic: whatever the
// rollback hook reports lands verbatim in the ack error.
func TestOnMessageAgentRollbackError(t *testing.T) {
	a := New(config.Default(), "test")
	rec := &recorder{}
	a.setSend(rec.send)
	a.rollbackFn = func(_ *log.Logger) error {
		return errors.New("rollback failed mid-restore")
	}
	env, _ := agentproto.NewEnvelope("rb-3", agentproto.MsgAgentRollback, struct{}{})
	a.OnMessage(context.Background(), env, a.sendIfConnected)

	acks := rec.msgsOfType(agentproto.MsgAgentRollbackAck)
	if len(acks) != 1 {
		t.Fatalf("got %d rollback acks; want 1", len(acks))
	}
	var ack agentproto.AgentRollbackAck
	if err := acks[0].Decode(&ack); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	if ack.OK || ack.Error != "rollback failed mid-restore" {
		t.Fatalf("want error ack, got %+v", ack)
	}
}

// TestOnMessageProbeNow covers F-1627: the ack reports the kicked loop count
// (0 for a runner that was never started, and the command still succeeds).
func TestOnMessageProbeNow(t *testing.T) {
	a := New(config.Default(), "test")
	rec := &recorder{}
	a.setSend(rec.send)

	env, _ := agentproto.NewEnvelope("pn-1", agentproto.MsgProbeNow, struct{}{})
	a.OnMessage(context.Background(), env, a.sendIfConnected)

	acks := rec.msgsOfType(agentproto.MsgProbeNowAck)
	if len(acks) != 1 {
		t.Fatalf("got %d probe_now acks; want 1", len(acks))
	}
	var ack agentproto.ProbeNowAck
	if err := acks[0].Decode(&ack); err != nil {
		t.Fatalf("decode ack: %v", err)
	}
	if !ack.OK || ack.Probed != 0 {
		t.Fatalf("want OK/probed=0 on a stopped runner, got %+v", ack)
	}
}
