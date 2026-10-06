package certwatch

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// genCert 生成自签证书并写 PEM 文件，返回路径与到期时间。
func genCert(t *testing.T, dir, commonName string, notAfter time.Time) (string, time.Time) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("gen key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: commonName},
		NotBefore:    notAfter.Add(-365 * 24 * time.Hour),
		NotAfter:     notAfter,
		IsCA:         true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	p := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	path := filepath.Join(dir, commonName+".pem")
	if err := os.WriteFile(path, p, 0o644); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	return path, notAfter
}

func TestFromFileFilePaths(t *testing.T) {
	dir := t.TempDir()
	certPath, notAfter := genCert(t, dir, "hk.example.com", time.Now().Add(90*24*time.Hour))
	cfg := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfg, []byte(`{"inbounds":[{"tls":{"certificates":[{"certificateFile":"`+certPath+`"}]}}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := FromFile(cfg)
	if len(got) != 1 {
		t.Fatalf("certs = %d, want 1", len(got))
	}
	// x509 会把时间截断到秒，按秒比对。
	if got[0].Domain != "hk.example.com" || got[0].NotAfter.Unix() != notAfter.Unix() {
		t.Fatalf("status = %+v want NotAfter %v", got[0], notAfter)
	}
}

func TestFromFileInlinePEM(t *testing.T) {
	dir := t.TempDir()
	certPath, _ := genCert(t, dir, "inline.example.com", time.Now().Add(24*time.Hour))
	raw, err := os.ReadFile(certPath)
	if err != nil {
		t.Fatal(err)
	}
	// PEM 含换行，须走 json.Marshal 生成合法 JSON 字符串。
	cfgRaw, err := json.Marshal(map[string]any{"cert": string(raw)})
	if err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfg, cfgRaw, 0o644); err != nil {
		t.Fatal(err)
	}
	got := FromFile(cfg)
	if len(got) != 1 || got[0].Domain != "inline.example.com" {
		t.Fatalf("certs = %+v", got)
	}
}

func TestFromFileSkipsNonCerts(t *testing.T) {
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.json")
	// 无 cert 键 / 指向缺失文件 / 非 JSON 文件：均静默返回空
	if err := os.WriteFile(cfg, []byte(`{"address":"a","tls":{"certificateFile":"/no/such/file.pem"}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := FromFile(cfg); len(got) != 0 {
		t.Fatalf("missing file should skip: %+v", got)
	}
	if err := os.WriteFile(cfg, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := FromFile(cfg); len(got) != 0 {
		t.Fatalf("non-json should skip: %+v", got)
	}
	if got := FromFile(filepath.Join(dir, "absent.json")); len(got) != 0 {
		t.Fatalf("absent file should skip: %+v", got)
	}
}

func TestExpiring(t *testing.T) {
	now := time.Now()
	// 三张状态：已过期 / 10 天后 / 40 天后。
	certs := []agentproto.CertStatus{
		{Domain: "expired", NotAfter: now.Add(-24 * time.Hour)},
		{Domain: "soon", NotAfter: now.Add(10 * 24 * time.Hour)},
		{Domain: "far", NotAfter: now.Add(40 * 24 * time.Hour)},
	}
	got := Expiring(certs, now, ExpiryWindow)
	if len(got) != 2 {
		t.Fatalf("expiring = %d, want 2", len(got))
	}
}
