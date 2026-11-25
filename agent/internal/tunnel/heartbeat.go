package tunnel

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// ErrHeartbeatLost means the heartbeat timed out: the connection is gone or the peer is unresponsive, so the caller should reconnect.
var ErrHeartbeatLost = errors.New("tunnel heartbeat lost")

const (
	framePing = 0x01
	framePong = 0x02
	frameLen  = 9 // 1 byte type + 8 byte nonce
)

// PingPong runs a ping/pong heartbeat over the connection: a ping goes out every interval and the peer must answer
// every ping with a pong; three consecutive periods without any pong declare the connection dead. A cancelled ctx returns nil.
//
// The frame format is a fixed 9 bytes, and both sides can run this function at once (a received ping answers pong automatically),
// so both relay ends can use the same function with no master/slave distinction.
func PingPong(ctx context.Context, conn net.Conn, interval time.Duration) error {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	var wmu sync.Mutex
	write := func(kind byte, nonce uint64) error {
		var b [frameLen]byte
		b[0] = kind
		binary.BigEndian.PutUint64(b[1:], nonce)
		wmu.Lock()
		defer wmu.Unlock()
		_ = conn.SetWriteDeadline(time.Now().Add(interval))
		_, err := conn.Write(b[:])
		return err
	}

	pongs := make(chan struct{}, 16)
	errc := make(chan error, 1)
	go func() {
		var b [frameLen]byte
		for {
			_ = conn.SetReadDeadline(time.Now().Add(3 * interval))
			if _, err := io.ReadFull(conn, b[:]); err != nil {
				errc <- err
				return
			}
			nonce := binary.BigEndian.Uint64(b[1:])
			switch b[0] {
			case framePing:
				if err := write(framePong, nonce); err != nil {
					errc <- err
					return
				}
			case framePong:
				select {
				case pongs <- struct{}{}:
				default:
				}
			default:
				errc <- errors.New("tunnel: unknown heartbeat frame")
				return
			}
		}
	}()

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	var nonce uint64
	missed := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-errc:
			return err
		case <-pongs:
			missed = 0
		case <-ticker.C:
			nonce++
			if err := write(framePing, nonce); err != nil {
				return err
			}
			// no pong within the period counts one miss (pongs is buffered and the reader keeps draining it).
			select {
			case <-pongs:
				missed = 0
			case <-time.After(interval):
				missed++
				if missed >= 3 {
					return ErrHeartbeatLost
				}
			case <-ctx.Done():
				return nil
			}
		}
	}
}
