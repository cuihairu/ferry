package tunnel

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

// The ssh transport plugin (E-28, the "alternative" row of the transport plugin matrix): a standard SSH2 outbound session to the landing
// host's sshd with publickey authentication, where a direct-tcpip channel dials the local ferry relay listener on the landing host --
// the traffic shape is just port forwarding inside an ordinary SSH session (the remote side of ssh -L),
// natively terminated by sshd. Its fingerprint is distinctive and easy to spot, so it is an alternative, never the default (design §transport plugin matrix).
// Credential rules: the key is referenced by file path (a local agent file, never in the database); the known_hosts
// file reference performs host key verification, and empty = skip it (bootstrap only; production must configure it).
const pluginSSH = "ssh"

func init() {
	// a plugin failing to register is a programming error, so panic directly (init-time registration has no concurrency).
	if err := Register(pluginSSH, func(o Options) (Tunnel, error) {
		return &sshTunnel{opts: o}, nil
	}); err != nil {
		panic(err)
	}
}

// sshTunnel holds one multiplexed SSH connection (one client, several channels, the same shape as a resident ssh -L):
// a Dial failure first declares the connection dead, reconnects once and retries; Close closes the reused connection.
type sshTunnel struct {
	opts    Options
	mu      sync.Mutex
	sshConn *ssh.Client
	warned  bool // the bootstrap warning for an empty known_hosts fires only once
}

func (t *sshTunnel) Name() string { return pluginSSH }

func (t *sshTunnel) getOrCreate(ctx context.Context) (*ssh.Client, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sshConn != nil {
		return t.sshConn, nil
	}
	c, err := t.dial(ctx)
	if err != nil {
		return nil, err
	}
	t.sshConn = c
	return c, nil
}

func (t *sshTunnel) reset(dead *ssh.Client) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sshConn == dead {
		_ = t.sshConn.Close()
		t.sshConn = nil
	}
}

// dial builds one SSH connection: the TCP dial honors the ctx timeout (aligned with the plugin's TimeoutOrDefault),
// followed by the SSH handshake and authentication (publickey as the only form; no password login surface).
func (t *sshTunnel) dial(ctx context.Context) (*ssh.Client, error) {
	addr := t.opts.ServerName
	if addr == "" {
		return nil, errors.New("tunnel: ssh requires server_name (sshd host:port)")
	}
	keyPath := t.opts.KeyFile
	if keyPath == "" {
		return nil, errors.New("tunnel: ssh requires key_file (private key path)")
	}
	user := t.opts.AuthUser
	if user == "" {
		return nil, errors.New("tunnel: ssh requires auth_user")
	}
	if t.opts.ForwardAddr == "" {
		return nil, errors.New("tunnel: ssh requires forward_addr (ferry relay listen on landing host)")
	}
	pemBytes, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("tunnel: read key file: %w", err)
	}
	signer, err := ssh.ParsePrivateKey(pemBytes)
	if err != nil {
		return nil, fmt.Errorf("tunnel: parse private key: %w", err)
	}
	hostKey := ssh.InsecureIgnoreHostKey() //nolint:gosec // bootstrap rule: skipped when known_hosts is unset, see the ssh_known_hosts note
	if t.opts.KnownHostsFile != "" {
		hk, err := knownHostsCallback(t.opts.KnownHostsFile)
		if err != nil {
			return nil, err
		}
		hostKey = hk
	} else if !t.warned {
		t.warned = true
		fmt.Fprintln(os.Stderr, "tunnel: ssh known_hosts not configured, host key check skipped (bootstrap only, configure ssh_known_hosts for production)")
	}
	cfg := &ssh.ClientConfig{
		User:            user,
		Auth:            []ssh.AuthMethod{ssh.PublicKeys(signer)},
		HostKeyCallback: hostKey,
		Timeout:         t.opts.TimeoutOrDefault(),
	}
	d := &net.Dialer{Timeout: t.opts.TimeoutOrDefault()}
	raw, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("tunnel: ssh dial %s: %w", addr, err)
	}
	conn, chans, reqs, err := ssh.NewClientConn(raw, addr, cfg)
	if err != nil {
		_ = raw.Close()
		return nil, fmt.Errorf("tunnel: ssh handshake %s: %w", addr, err)
	}
	return ssh.NewClient(conn, chans, reqs), nil
}

func (t *sshTunnel) Dial(ctx context.Context, addr string) (net.Conn, error) {
	if t.opts.ForwardAddr == "" {
		return nil, errors.New("tunnel: ssh requires forward_addr (ferry relay listen on landing host)")
	}
	c, err := t.getOrCreate(ctx)
	if err != nil {
		return nil, err
	}
	// Client.Dial returns a net.Conn directly (the stream semantics of a direct-tcpip channel).
	// Honest limitation note: x/crypto channel connections do not support the SetDeadline family
	// (they return "deadline not supported", which PingPong ignores with `_ =`), so heartbeat timeouts rely on the
	// application-layer 3-periods-without-pong verdict as the backstop, not on deadlines to speed it up.
	// opening a channel has no separate timeout; a dead connection errors immediately and the reconnect leg is the backstop.
	conn, err := c.Dial("tcp", t.opts.ForwardAddr)
	if err != nil {
		// the reused connection may already be dead (an sshd restart / network jitter): declare it dead, reconnect once and retry,
		// so a single jitter cannot take down every relay frontend connection.
		t.reset(c)
		c2, err2 := t.getOrCreate(ctx)
		if err2 != nil {
			return nil, err
		}
		conn, err = c2.Dial("tcp", t.opts.ForwardAddr)
		if err != nil {
			return nil, fmt.Errorf("tunnel: ssh open channel %s: %w", t.opts.ForwardAddr, err)
		}
	}
	return conn, nil
}

func (t *sshTunnel) ServeHeartbeat(ctx context.Context, conn net.Conn, interval time.Duration) error {
	return PingPong(ctx, conn, interval)
}

// Close releases the reused connection (tunnelCache holds the instance cache; everything closes when a hot reload swaps the landing).
func (t *sshTunnel) Close() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sshConn == nil {
		return nil
	}
	err := t.sshConn.Close()
	t.sshConn = nil
	return err
}

// knownHostsCallback verifies the host key against a known_hosts file (the OpenSSH format,
// hashed or plain entries both fine): a thin wrapper around the knownhosts library, from the same source as x/crypto.
func knownHostsCallback(path string) (ssh.HostKeyCallback, error) {
	return knownhosts.New(path)
}
