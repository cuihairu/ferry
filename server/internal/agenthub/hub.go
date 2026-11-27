// Package agenthub maintains agents' online connections and request/answer correlation.
package agenthub

import (
	"errors"
	"sync"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/gorilla/websocket"
)

// writer abstracts the write entry point of an underlying connection, easy to substitute in tests.
type writer interface {
	WriteMessage(messageType int, data []byte) error
}

// ErrOffline means the target node is not currently online.
var ErrOffline = errors.New("agent offline")

// ErrTimeout means the wait for an answer timed out.
var ErrTimeout = errors.New("agent reply timeout")

// Conn is one authenticated agent connection.
type Conn struct {
	NodeID int64
	ws     writer

	mu     sync.Mutex
	closed bool
}

// NewConn creates the connection wrapper.
func NewConn(nodeID int64, ws writer) *Conn {
	return &Conn{NodeID: nodeID, ws: ws}
}

// Close closes the underlying connection (idempotent).
func (c *Conn) Close() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.closed = true
	if ws, ok := c.ws.(*websocket.Conn); ok {
		_ = ws.Close()
	}
}

// Send serializes writing out one message.
func (c *Conn) Send(env agentproto.Envelope) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return ErrOffline
	}
	raw, err := env.Marshal()
	if err != nil {
		return err
	}
	return c.ws.WriteMessage(websocket.TextMessage, raw)
}

// waiterKey is the key of a waiting request (P2 node-ownership validation): the same request ID hung under
// different nodes does not cross-talk; a fake answer injected from another node finds no waiter and cannot impersonate.
type waiterKey struct {
	nodeID int64
	id     string
}

// Hub holds all online connections and the waiting requests.
type Hub struct {
	mu      sync.RWMutex
	conns   map[int64]*Conn
	waiters map[waiterKey]chan agentproto.Envelope
}

// New creates a Hub.
func New() *Hub {
	return &Hub{conns: map[int64]*Conn{}, waiters: map[waiterKey]chan agentproto.Envelope{}}
}

// Register records a connection; an older connection from the same node is displaced and returned (the caller closes it).
func (h *Hub) Register(conn *Conn) (replaced *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	old := h.conns[conn.NodeID]
	h.conns[conn.NodeID] = conn
	if old == conn {
		return nil
	}
	return old
}

// Unregister unregisters a connection, only taking effect if it is still this connection.
func (h *Hub) Unregister(nodeID int64, conn *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conns[nodeID] == conn {
		delete(h.conns, nodeID)
	}
}

// IsOnline queries whether a node is online.
func (h *Hub) IsOnline(nodeID int64) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.conns[nodeID]
	return ok
}

// OnlineCount returns the current number of online agent connections (health self-check policy).
func (h *Hub) OnlineCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns)
}

// OnlineIDs returns the ids of the nodes with a live connection (metrics scrape).
func (h *Hub) OnlineIDs() []int64 {
	h.mu.RLock()
	defer h.mu.RUnlock()
	ids := make([]int64, 0, len(h.conns))
	for id := range h.conns {
		ids = append(ids, id)
	}
	return ids
}

// Request sends a request to a node and waits for the same-ID answer.
func (h *Hub) Request(nodeID int64, env agentproto.Envelope, timeout time.Duration) (agentproto.Envelope, error) {
	h.mu.RLock()
	conn := h.conns[nodeID]
	h.mu.RUnlock()
	if conn == nil {
		return agentproto.Envelope{}, ErrOffline
	}
	if env.ID == "" {
		return agentproto.Envelope{}, errors.New("request envelope requires an id")
	}
	ch := make(chan agentproto.Envelope, 1)
	key := waiterKey{nodeID: nodeID, id: env.ID}
	h.mu.Lock()
	h.waiters[key] = ch
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.waiters, key)
		h.mu.Unlock()
	}()

	if err := conn.Send(env); err != nil {
		return agentproto.Envelope{}, err
	}
	select {
	case reply := <-ch:
		return reply, nil
	case <-time.After(timeout):
		return agentproto.Envelope{}, ErrTimeout
	}
}

// Deliver passes an answer on a node's connection to that node's waiting request; returns whether someone claimed it.
// Looked up by the (nodeID, ID) key (P2 node-ownership validation): a same-ID message from another node finds no
// waiter on this node and cannot impersonate - before the fix, waiters keyed only by ID, so cross-node injection was possible.
func (h *Hub) Deliver(nodeID int64, env agentproto.Envelope) bool {
	if env.ID == "" {
		return false
	}
	h.mu.RLock()
	ch := h.waiters[waiterKey{nodeID: nodeID, id: env.ID}]
	h.mu.RUnlock()
	if ch == nil {
		return false
	}
	select {
	case ch <- env:
	default:
	}
	return true
}
