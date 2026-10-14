// ferry-quic 是 QUIC 传输的独立 sidecar（E-18b，拍板：独立 plugin 二进制）：
// quic-go（MIT）只落本二进制，agent 主程序不携带（E-4 10MB 门禁不动）。
//
// 两种模式：
//
//	ferry-quic client --listen 127.0.0.1:7300 --ca ca.pem
//	  本地 TCP 收 CONNECT 桥（agent 内置 quic 插件拨这里），按目标拨 QUIC。
//	ferry-quic server --listen :443 --target 127.0.0.1:8000 --cert c.pem --key k.pem
//	  QUIC（UDP）监听，逐流转发给本机 target 端口。
//
// 诚实口径：raw QUIC（ALPN ferry-quic），不是 hysteria2，不冒称任何现有协议。
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/cuihairu/ferry/agent/internal/quictunnel"
	"github.com/quic-go/quic-go"
)

func main() {
	log.SetFlags(0)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		if errors.Is(err, context.Canceled) {
			return // 正常停机
		}
		if errors.Is(err, ErrUsage) {
			os.Exit(2) // 用法错误沿用 2
		}
		log.Fatalf("ferry-quic: %v", err)
	}
}

// ErrUsage 表示缺模式或未知模式（main 据此按用法错误退出 2）。
var ErrUsage = errors.New("需要模式 client|server")

// run 分发模式：client 起本地桥，server 起 QUIC 监听。用法错误返回 ErrUsage。
func run(ctx context.Context, args []string) error {
	if len(args) < 1 {
		usage()
		return ErrUsage
	}
	switch args[0] {
	case "client":
		return runClient(ctx, args[1:])
	case "server":
		return runServer(ctx, args[1:])
	}
	usage()
	return ErrUsage
}

func usage() {
	fmt.Fprint(os.Stderr, "usage: ferry-quic client|server [flags]\n"+
		"  client --listen 127.0.0.1:7300 [--ca ca.pem] [--insecure]\n"+
		"  server --listen :443 --target 127.0.0.1:8000 --cert c.pem --key k.pem\n")
}

func runClient(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("client", flag.ExitOnError)
	listen := fs.String("listen", "127.0.0.1:7300", "本地桥监听地址（agent quic 插件拨这里）")
	caFile := fs.String("ca", "", "私有 CA 证书（PEM，空则系统根证书）")
	insecure := fs.Bool("insecure", false, "跳过服务端证书校验（仅引导期调试，生产勿用）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	tlsCfg := &tls.Config{InsecureSkipVerify: *insecure} //nolint:gosec // 引导调试开关，显式 --insecure
	if *caFile != "" {
		pem, err := os.ReadFile(*caFile)
		if err != nil {
			return fmt.Errorf("read ca: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return fmt.Errorf("no certificates in %s", *caFile)
		}
		tlsCfg.RootCAs = pool
	}
	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		return err
	}
	log.Printf("ferry-quic client bridging on %s", *listen)
	return quictunnel.NewClient(tlsCfg).Serve(ctx, ln, log.Default())
}

func runServer(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	listen := fs.String("listen", ":443", "QUIC（UDP）监听地址")
	target := fs.String("target", "127.0.0.1:8000", "本机转发目标（TCP）")
	certFile := fs.String("cert", "", "TLS 证书（PEM，必填）")
	keyFile := fs.String("key", "", "TLS 私钥（PEM，必填）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *certFile == "" || *keyFile == "" {
		return fmt.Errorf("--cert/--key 必填（可用面板 BR-4 签发的证书）")
	}
	cert, err := tls.LoadX509KeyPair(*certFile, *keyFile)
	if err != nil {
		return fmt.Errorf("load cert: %w", err)
	}
	tlsCfg := &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{quictunnel.ALPNIdent},
	}
	udpConn, err := net.ListenPacket("udp", *listen)
	if err != nil {
		return err
	}
	tr := &quic.Transport{Conn: udpConn}
	defer tr.Close()
	ln, err := tr.Listen(tlsCfg, quictunnel.DefaultQUICConfig())
	if err != nil {
		return err
	}
	log.Printf("ferry-quic server quic://%s -> %s", *listen, *target)
	return quictunnel.ServeServer(ctx, ln, *target, log.Default())
}
