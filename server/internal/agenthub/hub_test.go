package agenthub

import (
	"errors"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
)

type mockWriter struct {
	msgs   [][]byte
	closed bool
}

func (m *mockWriter) WriteMessage(_ int, data []byte) error {
	m.msgs = append(m.msgs, data)
	return nil
}

func TestHubRegisterUnregister(t *testing.T) {
	h := New()
	w := &mockWriter{}
	c1 := NewConn(1, w)
	h.Register(c1)
	if !h.IsOnline(1) {
		t.Fatal("node 1 should be online")
	}
	if h.OnlineCount() != 1 {
		t.Fatalf("online count = %d; want 1", h.OnlineCount())
	}
	h.Unregister(1, c1)
	if h.IsOnline(1) {
		t.Fatal("node 1 should be offline after unregister")
	}
	if h.OnlineCount() != 0 {
		t.Fatalf("online count = %d; want 0", h.OnlineCount())
	}
}

func TestHubRegisterReplace(t *testing.T) {
	h := New()
	w := &mockWriter{}
	c1 := NewConn(1, w)
	c2 := NewConn(1, w)
	h.Register(c1)
	replaced := h.Register(c2)
	if replaced != c1 {
		t.Fatalf("replaced = %v; want c1", replaced)
	}
	if !h.IsOnline(1) {
		t.Fatal("node 1 should be online")
	}
}

func TestHubUnregisterStale(t *testing.T) {
	h := New()
	w := &mockWriter{}
	c1 := NewConn(1, w)
	c2 := NewConn(1, w)
	h.Register(c1)
	h.Register(c2)
	h.Unregister(1, c1)
	if !h.IsOnline(1) {
		t.Fatal("node 1 should still be online (c2 is current)")
	}
}

func TestHubRequestOffline(t *testing.T) {
	h := New()
	_, err := h.Request(99, agentproto.Envelope{ID: "r1"}, time.Second)
	if !errors.Is(err, ErrOffline) {
		t.Fatalf("err = %v; want ErrOffline", err)
	}
}

func TestHubRequestNoID(t *testing.T) {
	h := New()
	w := &mockWriter{}
	h.Register(NewConn(1, w))
	_, err := h.Request(1, agentproto.Envelope{}, time.Second)
	if err == nil {
		t.Fatal("expected error for empty envelope ID")
	}
}

func TestHubRequestTimeout(t *testing.T) {
	h := New()
	w := &mockWriter{}
	h.Register(NewConn(1, w))
	_, err := h.Request(1, agentproto.Envelope{ID: "r1"}, 10*time.Millisecond)
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v; want ErrTimeout", err)
	}
}

func TestHubRequestDeliver(t *testing.T) {
	h := New()
	w := &mockWriter{}
	h.Register(NewConn(1, w))
	reply := agentproto.Envelope{ID: "r1", Type: agentproto.MsgConfigAck}
	go func() {
		time.Sleep(5 * time.Millisecond)
		h.Deliver(reply)
	}()
	got, err := h.Request(1, agentproto.Envelope{ID: "r1"}, time.Second)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if got.ID != "r1" {
		t.Fatalf("got.ID = %q; want r1", got.ID)
	}
}

func TestHubDeliverNoWaiter(t *testing.T) {
	h := New()
	if h.Deliver(agentproto.Envelope{ID: "r1"}) {
		t.Fatal("deliver with no waiter should return false")
	}
}

func TestHubDeliverNoID(t *testing.T) {
	h := New()
	if h.Deliver(agentproto.Envelope{}) {
		t.Fatal("deliver with empty ID should return false")
	}
}

func TestConnCloseIdempotent(t *testing.T) {
	w := &mockWriter{}
	c := NewConn(1, w)
	c.Close()
	c.Close()
}

func TestConnSendAfterClose(t *testing.T) {
	w := &mockWriter{}
	c := NewConn(1, w)
	c.Close()
	err := c.Send(agentproto.Envelope{ID: "x"})
	if !errors.Is(err, ErrOffline) {
		t.Fatalf("err = %v; want ErrOffline", err)
	}
}
