// Package relay 是 relay 数据面组件（E-5）：入口角色的独立转发进程。
//
// 形态：本机监听 → 每条前端连接经传输插件（默认 tls-camo）拨落地 → 双向拷贝。
// 崩溃只断转发，核心心跳不受影响；被管方式是 procs 的一条普通规格
// （exec=ferry-agent，args=[-role, relay]），随面板 proc_ctl 启停。
//
// P0 口径：单流转发（传输插件 Dial 的连接即数据通道）；落地侧只需能终结
// 该传输（tls-camo 对端是任意 TLS 监听），无需 ferry 私有握手协议。
package relay

import (
	"context"
	"errors"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/agent/internal/tunnel"
)

// maxConns 是并发转发上限：入口小机器防爆，超限直接拒掉新连接。
const maxConns = 128

// dialTimeout 是单条转发连接的落地拨号超时。
const dialTimeout = 15 * time.Second

// Run 阻塞运行到 ctx 取消：监听 → 逐连接转发。
func Run(ctx context.Context, spec config.RelaySpec, logger *log.Logger) error {
	if spec.Listen == "" || spec.LandingAddr == "" {
		return errors.New("relay: listen and landing_addr are required")
	}
	plugin := spec.Tunnel
	if plugin == "" {
		plugin = tunnel.DefaultPlugin
	}
	tn, err := tunnel.Open(plugin, tunnel.Options{
		ServerName: spec.ServerName,
		CAFile:     spec.CAFile,
		Timeout:    dialTimeout,
	})
	if err != nil {
		return err
	}
	defer tn.Close()

	ln, err := net.Listen("tcp", spec.Listen)
	if err != nil {
		return err
	}
	defer ln.Close()
	logger.Printf("relay listening on %s -> %s via %s", spec.Listen, spec.LandingAddr, plugin)

	sem := make(chan struct{}, maxConns)
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		front, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
				logger.Printf("relay accept: %v", err)
				continue
			}
		}
		select {
		case sem <- struct{}{}:
		default:
			logger.Printf("relay overloaded, dropping %s", front.RemoteAddr())
			_ = front.Close()
			continue
		}
		go func(front net.Conn) {
			defer func() { <-sem }()
			defer front.Close()
			serveConn(ctx, tn, spec.LandingAddr, front, logger)
		}(front)
	}
}

// serveConn 为一条前端连接拨落地并双向拷贝；任一方向结束即断开整条。
func serveConn(ctx context.Context, tn tunnel.Tunnel, landing string, front net.Conn, logger *log.Logger) {
	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	back, err := tn.Dial(dialCtx, landing)
	if err != nil {
		logger.Printf("relay dial %s: %v", landing, err)
		return
	}
	defer back.Close()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(back, front)
	}()
	go func() {
		defer wg.Done()
		_, _ = io.Copy(front, back)
	}()
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-ctx.Done():
	case <-done:
	}
}
