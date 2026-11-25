package idp

import (
	"bytes"
	"net"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/config"
)

// bindResponse encodes a BER bindResponse carrying the given result code and
// echoing the request message id: the smallest fake directory answer go-ldap can
// decode.
func bindResponse(msgID byte, result byte) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x30, 0x0c})
	b.Write([]byte{0x02, 0x01, msgID})
	b.Write([]byte{0x61, 0x07, 0x0a, 0x01, result, 0x04, 0x00, 0x04, 0x00})
	return b.Bytes()
}

// requestMessageID reads the message id out of a bind request (the outer length
// may be a BER long form).
func requestMessageID(buf []byte) byte {
	idx := 2
	if buf[1]&0x80 != 0 {
		idx = 2 + int(buf[1]&0x7f)
	}
	return buf[idx+2]
}

// fakeLDAP listens on a loopback port and answers every bind request with the
// canned result code; answer=false accepts the socket and stays silent, which
// is how the bind timeout path is exercised.
func fakeLDAP(t *testing.T, result byte, answer bool) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				if !answer {
					// consume the bind request and then hold the socket open and
					// stay silent: the bind timeout has to fire instead of an
					// early EOF from our side
					buf := make([]byte, 512)
					_, _ = conn.Read(buf)
					_, _ = conn.Read(buf)
					return
				}
				buf := make([]byte, 512)
				n, err := conn.Read(buf)
				if err != nil || n < 5 {
					return
				}
				_, _ = conn.Write(bindResponse(requestMessageID(buf[:n]), result))
			}(conn)
		}
	}()
	return "ldap://" + ln.Addr().String()
}

func ldapCfg(url string, timeoutSec int) config.LdapConfig {
	return config.LdapConfig{
		Enabled:    true,
		URL:        url,
		BindDN:     "uid=%s,ou=people,dc=example,dc=com",
		TimeoutSec: timeoutSec,
	}
}

// TestLDAPAuthBind covers the bind outcomes a login surface can hit (G7): an
// accepted bind, a rejected one and a directory that never answers.
func TestLDAPAuthBind(t *testing.T) {
	if err := NewLDAP(ldapCfg(fakeLDAP(t, 0, true), 3)).Auth("alice", "pw"); err != nil {
		t.Fatalf("bind should succeed: %v", err)
	}
	err := NewLDAP(ldapCfg(fakeLDAP(t, 49, true), 3)).Auth("alice", "bad")
	if err == nil || !strings.Contains(err.Error(), "invalid credentials") {
		t.Fatalf("rejected bind error = %v", err)
	}
	err = NewLDAP(ldapCfg(fakeLDAP(t, 0, false), 1)).Auth("alice", "pw")
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("silent directory error = %v", err)
	}
}

// TestLDAPAuthConfigErrors covers the configuration mistakes that must fail
// before any connection is opened.
func TestLDAPAuthConfigErrors(t *testing.T) {
	if err := NewLDAP(ldapCfg(fakeLDAP(t, 0, true), 3)).Auth("alice", "pw"); err != nil {
		t.Fatalf("baseline bind: %v", err)
	}
	missing := ldapCfg("ldap://127.0.0.1:1", 1)
	missing.BindDN = "cn=admins"
	if err := NewLDAP(missing).Auth("alice", "pw"); err == nil || !strings.Contains(err.Error(), "%s") {
		t.Fatalf("bind dn template error = %v", err)
	}
	conflict := ldapCfg("ldaps://127.0.0.1:1", 1)
	conflict.StartTLS = true
	if err := NewLDAP(conflict).Auth("alice", "pw"); err == nil || !strings.Contains(err.Error(), "start_tls") {
		t.Fatalf("start tls conflict error = %v", err)
	}
}
