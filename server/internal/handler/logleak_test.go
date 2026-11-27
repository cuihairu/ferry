package handler

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gorilla/websocket"
)

// syncBuffer is a goroutine-safe sink for the standard logger (log.Output
// serializes per call, but concurrent writers still race on the buffer).
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// captureLog swaps the standard logger's sink for the duration of fn.
func captureLog(t *testing.T, fn func()) string {
	t.Helper()
	sink := &syncBuffer{}
	old := log.Writer()
	log.SetOutput(sink)
	defer log.SetOutput(old)
	fn()
	return sink.String()
}

// TestNoPlaintextSecretLogs covers F-1737: the credential-bearing admin flows
// must not spill plaintext secrets into the standard log. A canary sweep
// drives provider create/test/rotate, DNS provider create, an undeliverable
// config push and a forged agent token under a captured logger, then asserts
// none of the canaries escaped.
func TestNoPlaintextSecretLogs(t *testing.T) {
	canaryKey := "sk-canary-leak-42"
	canaryKey2 := "sk-canary-rot-43"
	canaryDNS := "dns-canary-77"
	canaryCfg := `{"tunnel_secret":"cfg-canary-99"}`
	canaryToken := "agt-canary-5150"

	logs := captureLog(t, func() {
		r, _ := newTestRouterCfg(t, func(cfg *config.Config) { cfg.SecretKey = "test-master-key" })

		// Provider lifecycle with canary credentials.
		rec := doJSON(t, r, "POST", "/api/providers", map[string]any{
			"name": "leak-prov", "type": "vultr", "access_key": canaryKey,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("create provider: %d %s", rec.Code, rec.Body)
		}
		var pv struct {
			ID uint `json:"id"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &pv); err != nil || pv.ID == 0 {
			t.Fatalf("no provider id: %s", rec.Body)
		}
		// Credential check runs the seal/unseal path (no adapter registered for
		// vultr: answers "credential readable", which is fine for the sweep).
		doJSON(t, r, "POST", "/api/providers/"+utoa(pv.ID)+"/test", nil)
		if rec := doJSON(t, r, "POST", "/api/providers/"+utoa(pv.ID)+"/rotate", map[string]any{
			"access_key": canaryKey2,
		}); rec.Code != http.StatusOK {
			t.Fatalf("rotate: %d %s", rec.Code, rec.Body)
		}

		// DNS provider carrying a canary key (unknown kind fails loudly; the
		// leak check is about the log, not the status).
		doJSON(t, r, "POST", "/api/dns-providers", map[string]any{
			"name": "leak-dns", "type": "alidns", "api_key": canaryDNS,
		})

		// Config push carrying a secret-shaped payload to an offline node.
		nid, _ := createNodeFor(t, r, "leak-off")
		if rec := doJSON(t, r, "POST", "/api/nodes/"+itoa64(int(nid))+"/config", map[string]any{
			"proc": "xray", "kind": "xray", "payload": canaryCfg,
		}); rec.Code != http.StatusBadGateway {
			t.Fatalf("offline push: %d %s", rec.Code, rec.Body)
		}

		// Forged agent token: the auth-failure log line must stay material-free.
		srv := httptest.NewServer(r)
		defer srv.Close()
		wsURL := "ws" + strings.TrimPrefix(srv.URL, "http") + "/agent/ws"
		c, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
		if err != nil {
			t.Fatalf("dial: %v", err)
		}
		hello, _ := agentproto.NewEnvelope("h1", agentproto.MsgHello, agentproto.Hello{
			Token: canaryToken, AgentID: "leak-agent", Version: "test",
		})
		if err := c.WriteJSON(hello); err != nil {
			t.Fatalf("write hello: %v", err)
		}
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		if _, _, err = c.ReadMessage(); err == nil {
			t.Fatal("forged token must be rejected")
		}
		c.Close()
	})

	for _, canary := range []string{canaryKey, canaryKey2, canaryDNS, canaryCfg, canaryToken} {
		if strings.Contains(logs, canary) {
			t.Fatalf("plaintext secret leaked to log: %s", canary)
		}
	}
	// The capture really rode along the sensitive paths.
	if !strings.Contains(logs, "agent auth failed") {
		t.Fatalf("auth failure log missing, capture did not cover the path: %q", logs)
	}
}

// TestKeyrotateBootLogNoSecrets covers F-1737's boot leg: when the key
// rotation walk cannot open a row (the old key term is missing from the
// keyring), the boot log names the column and row id — never the plaintext,
// never the stuck ciphertext.
func TestKeyrotateBootLogNoSecrets(t *testing.T) {
	old := secret.NewStore("foreign-old-key")
	ct, err := old.Encrypt("sk-canary-boot-88")
	if err != nil {
		t.Fatal(err)
	}

	db, err := storage.Open(storage.DriverSQLite, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.Provider{Name: "leak-kr", Type: "vultr", AccessKey: ct}).Error; err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.SecretKey = "v1:real-old,v2:real-new"
	logs := captureLog(t, func() {
		r, _ := NewRouter(db, cfg)
		_ = r
	})

	if !strings.Contains(logs, "keyrotate: re-encrypt incomplete") {
		t.Fatalf("boot log must surface the stuck walk: %q", logs)
	}
	if strings.Contains(logs, "sk-canary-boot-88") {
		t.Fatal("boot log leaked the plaintext secret")
	}
	if strings.Contains(logs, ct) {
		t.Fatal("boot log leaked the stuck ciphertext")
	}
}
