// ferry-quic is the standalone sidecar for QUIC transport (E-18b, decision: a separate plugin binary):
// quic-go (MIT) lands only in this binary, the agent proper carries none of it (the E-4 10MB gate is untouched).
//
// Two modes:
//
//	ferry-quic client --listen 127.0.0.1:7300 --ca ca.pem
//	  Local TCP CONNECT bridge (the agent's built-in quic plugin dials here); dials QUIC per target.
//	ferry-quic server --listen :443 --target 127.0.0.1:8000 --cert c.pem --key k.pem
//	  Listens on QUIC (UDP) and forwards each stream to the local target port.
//
// Honest scope: raw QUIC (ALPN ferry-quic), not hysteria2, and not claimed as any existing protocol.
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
			return // normal shutdown
		}
		if errors.Is(err, ErrUsage) {
			os.Exit(2) // usage error keeps exit code 2
		}
		log.Fatalf("ferry-quic: %v", err)
	}
}

// ErrUsage means a missing or unknown mode (main exits 2 as a usage error on it).
var ErrUsage = errors.New("需要模式 client|server")

// run dispatches modes: client starts the local bridge, server starts the QUIC listener. Usage errors return ErrUsage.
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
	tlsCfg := &tls.Config{InsecureSkipVerify: *insecure} //nolint:gosec // bootstrap debugging switch, requires an explicit --insecure
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
	quicCfg := quictunnel.DefaultQUICConfig()
	if err := quictunnel.ValidateQUICConfig(quicCfg); err != nil {
		return err
	}
	ln, err := tr.Listen(tlsCfg, quicCfg)
	if err != nil {
		return err
	}
	log.Printf("ferry-quic server quic://%s -> %s", *listen, *target)
	return quictunnel.ServeServer(ctx, ln, *target, log.Default())
}
