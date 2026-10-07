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

// ErrHeartbeatLost 表示心跳超时：连接已断或对端无响应，调用方应重连。
var ErrHeartbeatLost = errors.New("tunnel heartbeat lost")

const (
	framePing = 0x01
	framePong = 0x02
	frameLen  = 9 // 1 字节类型 + 8 字节随机数
)

// PingPong 在连接上跑 ping/pong 心跳：每 interval 发 ping，对端 ping 必须回
// pong；连续 3 个周期无任何 pong 即判连接已死。ctx 取消返回 nil。
//
// 帧格式固定 9 字节，双方可同时跑本函数（收 ping 自动回 pong），因此 relay
// 两端用同一函数即可，无主从之分。
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
			// 本周期内无 pong 即记一次 miss（pongs 带缓冲，读端持续消费）。
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
