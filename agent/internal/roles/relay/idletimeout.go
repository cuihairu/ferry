package relay

import (
	"net"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
)

// effectiveIdleTimeout normalizes the idle cleanup window (F-0814): 0 and
// negatives disable it.
func effectiveIdleTimeout(spec config.RelaySpec) time.Duration {
	if spec.IdleTimeoutSec > 0 {
		return time.Duration(spec.IdleTimeoutSec) * time.Second
	}
	return 0
}

// deadlineConn refreshes a sliding read deadline on every successful read so a
// connection with no traffic in either direction fails its next read once the
// idle window elapses. Transports whose conns ignore deadlines (the SSH
// channel returns an error from SetReadDeadline) degrade to no cleanup: the
// error is swallowed here and the connection keeps working.
type deadlineConn struct {
	net.Conn
	idle time.Duration
}

func (d *deadlineConn) Read(p []byte) (int, error) {
	_ = d.Conn.SetReadDeadline(time.Now().Add(d.idle))
	n, err := d.Conn.Read(p)
	return n, err
}

func (d *deadlineConn) Write(p []byte) (int, error) {
	// Refresh on the read side from writes too: activity in the write direction
	// means the connection is not idle (a download-only or upload-only stream
	// must not be killed while data flows one way).
	_ = d.Conn.SetReadDeadline(time.Now().Add(d.idle))
	return d.Conn.Write(p)
}
