package tunnel

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"strings"
	"time"
)

// The quic sidecar plugin (E-18b, decision: a separate plugin binary) carries no QUIC implementation at all --
// it only hands connections to the ferry-quic sidecar through a local CONNECT bridge (quic-go lives only
// in that binary, leaving the agent's size gate E-4 untouched). Deployment: a ferry-quic client runs
// on the same host as the agent (systemd / a managed process slot), with the default bridge address 127.0.0.1:7300,
// overridable via the FERRY_QUIC_SIDECAR environment variable.
//
// Honest scope: transport through this plugin is raw QUIC (ALPN ferry-quic), not hysteria2,
// and it does not pose as any existing protocol.
const pluginQUIC = "quic"

// SidecarEnv is the environment variable name of the sidecar bridge address.
const SidecarEnv = "FERRY_QUIC_SIDECAR"

// DefaultSidecarAddr is the sidecar bridge's default listen address (aligned with the ferry-quic client default).
const DefaultSidecarAddr = "127.0.0.1:7300"

// SidecarAddr normalizes the sidecar bridge address.
func SidecarAddr() string {
	if v := strings.TrimSpace(os.Getenv(SidecarEnv)); v != "" {
		return v
	}
	return DefaultSidecarAddr
}

func init() {
	// a plugin failing to register is a programming error, so panic directly (init-time registration has no concurrency).
	if err := Register(pluginQUIC, func(o Options) (Tunnel, error) {
		return &quicSidecar{opts: o}, nil
	}); err != nil {
		panic(err)
	}
}

// quicSidecar is the CONNECT bridge dialer: stateless, with an empty Close.
type quicSidecar struct{ opts Options }

func (t *quicSidecar) Name() string { return pluginQUIC }

func (t *quicSidecar) Dial(ctx context.Context, addr string) (net.Conn, error) {
	d := &net.Dialer{Timeout: t.opts.TimeoutOrDefault()}
	conn, err := d.DialContext(ctx, "tcp", SidecarAddr())
	if err != nil {
		return nil, Wrap(ClassNetwork, "quic:dial", fmt.Errorf("sidecar %s: %w", SidecarAddr(), err))
	}
	sni := t.opts.ServerName
	if sni == "" {
		host, _, splitErr := net.SplitHostPort(addr)
		if splitErr != nil {
			host = addr
		}
		sni = host
	}
	// the request line and the reply both carry timeouts, so a missing sidecar or a version mismatch fails fast instead of hanging.
	_ = conn.SetDeadline(time.Now().Add(t.opts.TimeoutOrDefault()))
	if _, err := fmt.Fprintf(conn, "CONNECT %s sni=%s\n", addr, sni); err != nil {
		_ = conn.Close()
		return nil, Wrap(ClassNetwork, "quic:dial", fmt.Errorf("send connect: %w", err))
	}
	line, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		_ = conn.Close()
		return nil, Wrap(ClassNetwork, "quic:dial", fmt.Errorf("read connect reply: %w", err))
	}
	_ = conn.SetDeadline(time.Time{})
	switch {
	case strings.HasPrefix(line, "OK"):
		return conn, nil
	case strings.HasPrefix(line, "ERR"):
		_ = conn.Close()
		// the sidecar refuses the stream explicitly (peer unreachable etc.):
		// a peer-side refusal, not local load.
		return nil, Wrap(ClassAuth, "quic:dial", fmt.Errorf("%s: %s", addr, strings.TrimSpace(strings.TrimPrefix(line, "ERR"))))
	default:
		_ = conn.Close()
		return nil, Errorf(ClassAuth, "quic:dial", "unexpected sidecar reply %q", line)
	}
}

func (t *quicSidecar) ServeHeartbeat(ctx context.Context, conn net.Conn, interval time.Duration) error {
	return PingPong(ctx, conn, interval)
}

// Caps declares the plugin's features (F-0822): the agent leg to the sidecar
// is a plain TCP CONNECT bridge -- no camouflage on this hop -- plus the
// shared ping/pong; the QUIC leg is the sidecar's own concern.
func (t *quicSidecar) Caps() Caps {
	return Caps{Heartbeat: true}
}

func (t *quicSidecar) Close() error {
	// no global resources; the sidecar's lifecycle is independent of the agent (systemd / a managed process slot).
	return nil
}
