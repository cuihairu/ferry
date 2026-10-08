// Package agenthub 维护 agent 的在线连接与请求应答关联。
package agenthub

import (
	"errors"
	"sync"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/gorilla/websocket"
)

// writer 抽象底层连接的写入口，便于测试替换。
type writer interface {
	WriteMessage(messageType int, data []byte) error
}

// ErrOffline 表示目标节点当前不在线。
var ErrOffline = errors.New("agent offline")

// ErrTimeout 表示等待应答超时。
var ErrTimeout = errors.New("agent reply timeout")

// Conn 是一条已认证的 agent 连接。
type Conn struct {
	NodeID int64
	ws     writer

	mu     sync.Mutex
	closed bool
}

// NewConn 创建连接包装。
func NewConn(nodeID int64, ws writer) *Conn {
	return &Conn{NodeID: nodeID, ws: ws}
}

// Close 关闭底层连接（幂等）。
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

// Send 串行化写出一条消息。
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

// Hub 持有全部在线连接与等待中的请求。
type Hub struct {
	mu      sync.RWMutex
	conns   map[int64]*Conn
	waiters map[string]chan agentproto.Envelope
}

// New 创建 Hub。
func New() *Hub {
	return &Hub{conns: map[int64]*Conn{}, waiters: map[string]chan agentproto.Envelope{}}
}

// Register 登记连接；同节点旧连接被顶替并返回（调用方负责关闭）。
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

// Unregister 注销连接，仅当仍是这条连接时生效。
func (h *Hub) Unregister(nodeID int64, conn *Conn) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.conns[nodeID] == conn {
		delete(h.conns, nodeID)
	}
}

// IsOnline 查询节点是否在线。
func (h *Hub) IsOnline(nodeID int64) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	_, ok := h.conns[nodeID]
	return ok
}

// OnlineCount 返回当前在线 agent 连接数（health 自检口径）。
func (h *Hub) OnlineCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.conns)
}

// Request 向节点发送请求并等待同 ID 应答。
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
	h.mu.Lock()
	h.waiters[env.ID] = ch
	h.mu.Unlock()
	defer func() {
		h.mu.Lock()
		delete(h.waiters, env.ID)
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

// Deliver 把 agent 的应答投递给等待中的请求；返回是否有人认领。
func (h *Hub) Deliver(env agentproto.Envelope) bool {
	if env.ID == "" {
		return false
	}
	h.mu.RLock()
	ch := h.waiters[env.ID]
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
