package cert

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/notify"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// recRunner records every execution (args and env) and returns the preset results in order.
type recRunner struct {
	calls   []string
	envs    []map[string]string
	results []error
}

func (r *recRunner) run(ctx context.Context, args []string, env map[string]string) (string, error) {
	r.calls = append(r.calls, strings.Join(args, " "))
	r.envs = append(r.envs, env)
	if len(r.results) == 0 {
		return "ok-out", nil
	}
	err := r.results[0]
	r.results = r.results[1:]
	if err != nil {
		return "boom: acme error detail", err
	}
	return "ok-out", nil
}

func newDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open("sqlite", ":memory:")
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
	return db
}

func newMgr(rr *recRunner, expiry time.Time) *Manager {
	return &Manager{
		Home: "/tmp/acme", Run: rr.run,
		ReadExpiry: func(string) (time.Time, error) { return expiry, nil },
	}
}

func mkTask(t *testing.T, db *gorm.DB, mutate func(*storage.CertTask)) storage.CertTask {
	t.Helper()
	task := storage.CertTask{Name: "主证书", Domain: "edge.example.com",
		Method: MethodDNS01, State: StatePending}
	if mutate != nil {
		mutate(&task)
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	return task
}

// dnsFix stores one enabled Cloudflare credential and returns its ID.
func dnsFix(t *testing.T, db *gorm.DB) uint {
	t.Helper()
	store := secret.NewStore("cert-master")
	sealed, err := store.Encrypt("tok-secret")
	if err != nil {
		t.Fatal(err)
	}
	prov := storage.DNSProvider{Name: "cf", Type: "cloudflare", APIKey: sealed, Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}
	return prov.ID
}

// TestIssueCommand covers command construction: dns-01 maps to the dns_cf plugin, http-01 carries a
// webroot, additional domains become one -d each, unknown methods/plugins must not be claimed as supported.
func TestIssueCommand(t *testing.T) {
	rr := &recRunner{}
	m := newMgr(rr, time.Now())

	if _, err := m.Issue(context.Background(), []string{"edge.example.com", "a.example.com"},
		MethodDNS01, "cloudflare", map[string]string{"CF_Token": "tok"}); err != nil {
		t.Fatal(err)
	}
	want := "--issue --server letsencrypt -d edge.example.com -d a.example.com --dns dns_cf"
	if rr.calls[0] != want {
		t.Fatalf("dns-01 args = %q", rr.calls[0])
	}
	if rr.envs[0]["CF_Token"] != "tok" {
		t.Fatalf("token must pass via env: %v", rr.envs[0])
	}

	m2 := newMgr(rr, time.Now())
	m2.Webroot = "/var/www/acme"
	if _, err := m2.Issue(context.Background(), []string{"edge.example.com"}, MethodHTTP01, "", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rr.calls[len(rr.calls)-1], "--webroot /var/www/acme") {
		t.Fatalf("http-01 args = %q", rr.calls[len(rr.calls)-1])
	}

	// Do not claim support: unknown DNS plugin, unknown method, missing webroot.
	if _, err := m.Issue(context.Background(), []string{"x.example.com"}, MethodDNS01, "alidns", nil); err == nil {
		t.Fatal("unknown dns plugin must fail")
	}
	if _, err := m.Issue(context.Background(), []string{"x.example.com"}, "tls-alpn-01", "", nil); err == nil {
		t.Fatal("unknown method must fail")
	}
	if _, err := m.Issue(context.Background(), []string{"x.example.com"}, MethodHTTP01, "", nil); err == nil {
		t.Fatal("missing webroot must fail")
	}
}

// TestSweepIssuesPending a pending task is auto-issued: the credential goes through env vars, the expiry is
// backfilled, and the state turns ok; a dns-01 failure without a configured credential leaves the reason behind.
func TestSweepIssuesPending(t *testing.T) {
	db := newDB(t)
	now := time.Now()
	expiry := now.Add(90 * 24 * time.Hour)
	rr := &recRunner{}
	m := newMgr(rr, expiry)
	provID := dnsFix(t, db)
	task := mkTask(t, db, func(tk *storage.CertTask) { tk.DNSProviderID = provID })

	if err := Sweep(context.Background(), db, m, secret.NewStore("cert-master"), now, nil); err != nil {
		t.Fatal(err)
	}
	var got storage.CertTask
	if err := db.First(&got, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.State != StateOK || got.NotAfter == nil || !got.NotAfter.Equal(expiry) {
		t.Fatalf("after issue = %+v", got)
	}
	if len(rr.envs) != 1 || rr.envs[0]["CF_Token"] != "tok-secret" {
		t.Fatalf("env = %v", rr.envs)
	}

	// Task without a configured credential: failure records the reason without claiming support.
	mkTask(t, db, nil)
	if err := Sweep(context.Background(), db, m, secret.NewStore("cert-master"), now, nil); err != nil {
		t.Fatal(err)
	}
	var bad storage.CertTask
	if err := db.Where("dns_provider_id = ?", 0).First(&bad).Error; err != nil {
		t.Fatal(err)
	}
	if bad.State != StateFailed || !strings.Contains(bad.LastError, "dns provider not configured") {
		t.Fatalf("no-provider = %+v", bad)
	}
}

// TestSweepRenewsDue due tasks auto-renew: renew only inside the 30-day window, refresh the expiry afterwards;
// not yet due stays untouched.
func TestSweepRenewsDue(t *testing.T) {
	db := newDB(t)
	now := time.Now()
	rr := &recRunner{}
	m := newMgr(rr, now.Add(90*24*time.Hour))

	due := now.Add(10 * 24 * time.Hour) // inside the 30-day renewal window and not expired
	mkTask(t, db, func(tk *storage.CertTask) {
		tk.State, tk.NotAfter = StateOK, &due
	})
	fresh := now.Add(200 * 24 * time.Hour)
	mkTask(t, db, func(tk *storage.CertTask) {
		tk.State, tk.NotAfter = StateOK, &fresh
	})

	if err := Sweep(context.Background(), db, m, secret.NewStore("x"), now, nil); err != nil {
		t.Fatal(err)
	}
	if len(rr.calls) != 1 || !strings.HasPrefix(rr.calls[0], "--renew -d edge.example.com") {
		t.Fatalf("calls = %v", rr.calls)
	}
	var first storage.CertTask
	if err := db.First(&first, 1).Error; err != nil {
		t.Fatal(err)
	}
	if first.State != StateOK || first.NotAfter.Before(now.Add(89*24*time.Hour)) {
		t.Fatalf("after renew = %+v", first)
	}

	// Expiring event (HERALD-3): tasks inside the window drop cert_expiring, not-yet-due ones do not;
	// a same-day second round does not duplicate.
	var evs []storage.Event
	if err := db.Where("kind = ?", "cert_expiring").Find(&evs).Error; err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || evs[0].Severity != "warning" ||
		evs[0].DedupKey != fmt.Sprintf("cert:%s:cert_expiring:%s", first.Domain, now.Format("20060102")) {
		t.Fatalf("cert_expiring events = %+v", evs)
	}
	if !strings.Contains(evs[0].Title, "10 天后到期") {
		t.Fatalf("title = %q", evs[0].Title)
	}
	if err := Sweep(context.Background(), db, m, secret.NewStore("x"), now, nil); err != nil {
		t.Fatal(err)
	}
	var n int64
	db.Model(&storage.Event{}).Where("kind = ?", "cert_expiring").Count(&n)
	if n != 1 {
		t.Fatalf("second sweep events = %d, want 1", n)
	}
}

// TestSweepFailedBackoff failure backoff: a fresh failure does not retry, issuance retries after the backoff point;
// an issuance error lands as failed with the reason kept.
func TestSweepFailedBackoff(t *testing.T) {
	db := newDB(t)
	now := time.Now()
	provID := dnsFix(t, db)
	justFailed := now.Add(-10 * time.Minute)
	task := mkTask(t, db, func(tk *storage.CertTask) {
		tk.State, tk.LastAttempt, tk.DNSProviderID = StateFailed, &justFailed, provID
	})

	rr := &recRunner{}
	m := newMgr(rr, now)
	// Inside the backoff window: leave untouched.
	if err := Sweep(context.Background(), db, m, secret.NewStore("cert-master"), now, nil); err != nil {
		t.Fatal(err)
	}
	if len(rr.calls) != 0 {
		t.Fatalf("must back off, calls = %v", rr.calls)
	}

	// After the backoff point: reissue, this time the runner errors → failed with the tail reason.
	rr.results = []error{errors.New("acme fail")}
	if err := Sweep(context.Background(), db, m, secret.NewStore("cert-master"), now.Add(FailBackoff+time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	var got storage.CertTask
	if err := db.First(&got, task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if got.State != StateFailed || !strings.Contains(got.LastError, "acme error detail") {
		t.Fatalf("after retry = %+v", got)
	}
}

// TestLoopSweepsAndStopsOnCancel covers Loop start/stop: one round on start (pending gets issued and
// turns ok), exits after ctx is canceled; a nil logger falls back to the default logger.
func TestLoopSweepsAndStopsOnCancel(t *testing.T) {
	db := newDB(t)
	provID := dnsFix(t, db)
	task := mkTask(t, db, func(tk *storage.CertTask) { tk.DNSProviderID = provID })
	m := newMgr(&recRunner{}, time.Now().Add(90*24*time.Hour))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		Loop(ctx, db, m, secret.NewStore("cert-master"), 20*time.Millisecond, nil)
		close(done)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for {
		var got storage.CertTask
		if err := db.First(&got, task.ID).Error; err != nil {
			t.Fatal(err)
		}
		if got.State == StateOK {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("loop first sweep never issued: state=%s", got.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("loop did not return after cancel")
	}
}

// TestIssueNowIssuesAsync covers manual issuance: set issuing then run asynchronously, terminal state and
// expiry backfilled; an unknown task ID errors.
func TestIssueNowIssuesAsync(t *testing.T) {
	db := newDB(t)
	provID := dnsFix(t, db)
	task := mkTask(t, db, func(tk *storage.CertTask) { tk.DNSProviderID = provID })
	m := newMgr(&recRunner{}, time.Now().Add(90*24*time.Hour))

	if err := IssueNow(context.Background(), db, m, secret.NewStore("cert-master"), task.ID, nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		var got storage.CertTask
		if err := db.First(&got, task.ID).Error; err != nil {
			t.Fatal(err)
		}
		if got.State == StateOK && got.NotAfter != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("issue now never finished: state=%s", got.State)
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := IssueNow(context.Background(), db, m, nil, 9999, nil); err == nil {
		t.Fatal("issue now of unknown task should error")
	}
}

// TestRealRunExec covers the real execution slot: combined output, extra env merged into the environment, working
// directory effective; Bin defaults back to acme.sh (not installed here, so it must report not-found instead of hanging).
func TestRealRunExec(t *testing.T) {
	m := &Manager{Bin: "sh"}
	ctx := context.Background()
	out, err := m.realRun(ctx, []string{"-c", "echo hi"}, nil)
	if err != nil || !strings.Contains(out, "hi") {
		t.Fatalf("plain run = %q, %v", out, err)
	}

	home := t.TempDir()
	m.Home = home
	out, err = m.realRun(ctx, []string{"-c", "pwd"}, nil)
	if err != nil || !strings.Contains(out, filepath.Base(home)) {
		t.Fatalf("dir run = %q, %v; want pwd under %s", out, err, home)
	}

	m.Home = ""
	out, err = m.realRun(ctx, []string{"-c", "echo $FERRY_TEST_TOKEN"}, map[string]string{"FERRY_TEST_TOKEN": "bar"})
	if err != nil || !strings.Contains(out, "bar") {
		t.Fatalf("env run = %q, %v; want env merged", out, err)
	}

	out, err = (&Manager{}).realRun(ctx, []string{"--version"}, nil)
	if err == nil {
		t.Fatalf("default bin = %q, %v; want exec failure (acme.sh absent)", out, err)
	}
}

// testCertPEM is a self-signed test certificate (ecdsa P-256, notAfter 2027-01-12) for the real openssl
// parsing path of Expiry.
const testCertPEM = `-----BEGIN CERTIFICATE-----
MIIBizCCATGgAwIBAgIUB/Q1fjIl3XQ8L5471PCxXdT3ckQwCgYIKoZIzj0EAwIw
GzEZMBcGA1UEAwwQZWRnZS5leGFtcGxlLmNvbTAeFw0yNjEwMTQxMDU5MjdaFw0y
NzAxMTIxMDU5MjdaMBsxGTAXBgNVBAMMEGVkZ2UuZXhhbXBsZS5jb20wWTATBgcq
hkjOPQIBBggqhkjOPQMBBwNCAATv7jPoD+55EDqAMGfj+tXHrWIjXFJuPnq9tzVJ
F7ARabk0yTHOdaS0C6uObMGwUnz7BO/YPIziOXK+BWcXfXduo1MwUTAdBgNVHQ4E
FgQUVQlct50FeeAeN/NyBQwT38vjYDQwHwYDVR0jBBgwFoAUVQlct50FeeAeN/Ny
BQwT38vjYDQwDwYDVR0TAQH/BAUwAwEB/zAKBggqhkjOPQQDAgNIADBFAiBOnb/5
gmoLn8H5qlOSBG6+KRsr9qcLcmyQShoJQFl0tAIhAP/WGk0JHRywOXx3ZZVlu4MM
g0+wOSS8PYVT5WVmhJbL
-----END CERTIFICATE-----
`

// TestExpiryReadsRealCert covers Expiry's default openssl path: a real certificate parses notAfter,
// a missing file errors.
func TestExpiryReadsRealCert(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, "edge.example.com_ecc")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "fullchain.cer"), []byte(testCertPEM), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Manager{Home: home}
	got, err := m.Expiry("edge.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if want := time.Date(2027, 1, 12, 10, 59, 27, 0, time.UTC); !got.Equal(want) {
		t.Fatalf("expiry = %v; want %v", got, want)
	}
	if _, err := m.Expiry("missing.example.com"); err == nil {
		t.Fatal("missing cert must error")
	}
}

// TestSweepRenewFailureAlerts the renewal failure path: flips to failed with the reason kept, the webhook
// alert goes out (channel configured); an expired certificate's expiring title uses the "expired" text.
func TestSweepRenewFailureAlerts(t *testing.T) {
	db := newDB(t)
	now := time.Now()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	u := srv.URL
	srv.Close()
	if err := storage.SetSetting(db, notify.KeyURL, u); err != nil {
		t.Fatal(err)
	}
	expired := now.Add(-24 * time.Hour)
	mkTask(t, db, func(tk *storage.CertTask) { tk.State, tk.NotAfter = StateOK, &expired })

	rr := &recRunner{results: []error{errors.New("acme fail")}}
	m := newMgr(rr, now)
	if err := Sweep(context.Background(), db, m, nil, now, nil); err != nil {
		t.Fatal(err)
	}
	var got storage.CertTask
	if err := db.First(&got, 1).Error; err != nil {
		t.Fatal(err)
	}
	if got.State != StateFailed || !strings.Contains(got.LastError, "acme error detail") {
		t.Fatalf("after failed renew = %+v", got)
	}
	var evs []storage.Event
	if err := db.Where("kind = ?", "cert_expiring").Find(&evs).Error; err != nil {
		t.Fatal(err)
	}
	if len(evs) != 1 || !strings.Contains(evs[0].Title, "已到期") {
		t.Fatalf("expired events = %+v", evs)
	}
}

// TestSweepIssueCredentialFailures the four dns-01 credential failure branches: provider missing,
// provider disabled, master key unconfigured, decrypt failure — the runner is never called.
func TestSweepIssueCredentialFailures(t *testing.T) {
	db := newDB(t)
	now := time.Now()
	// Disabled provider (GORM default:true swallows zero values: change it explicitly after creating).
	disabled := storage.DNSProvider{Name: "cf-off", Type: "cloudflare", APIKey: "x", Enabled: true}
	if err := db.Create(&disabled).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&storage.DNSProvider{}).Where("id = ?", disabled.ID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	// Enabled but sealed with the k1 key.
	sealed, err := secret.NewStore("k1").Encrypt("tok-secret")
	if err != nil {
		t.Fatal(err)
	}
	prov := storage.DNSProvider{Name: "cf", Type: "cloudflare", APIKey: sealed, Enabled: true}
	if err := db.Create(&prov).Error; err != nil {
		t.Fatal(err)
	}

	noProv := mkTask(t, db, func(tk *storage.CertTask) { tk.DNSProviderID = 999 })
	offProv := mkTask(t, db, func(tk *storage.CertTask) { tk.DNSProviderID = disabled.ID })
	noKey := mkTask(t, db, func(tk *storage.CertTask) { tk.DNSProviderID = prov.ID })
	badKey := mkTask(t, db, func(tk *storage.CertTask) { tk.DNSProviderID = prov.ID })

	rr := &recRunner{}
	m := newMgr(rr, now)
	// noKey runs one round with store=nil; badKey decrypts with the wrong master (a failed task must pass the
	// backoff point before retrying, so the second round takes now+FailBackoff; that round also re-sweeps
	// the first three, hence assertions split per round).
	if err := Sweep(context.Background(), db, m, nil, now, nil); err != nil {
		t.Fatal(err)
	}
	if len(rr.calls) != 0 {
		t.Fatalf("runner must not be called on credential failures: %v", rr.calls)
	}
	for task, want := range map[storage.CertTask]string{
		noProv: "dns provider missing", offProv: "dns provider disabled",
		noKey: "secret master key not configured",
	} {
		var got storage.CertTask
		if err := db.First(&got, task.ID).Error; err != nil {
			t.Fatal(err)
		}
		if got.State != StateFailed || !strings.Contains(got.LastError, want) {
			t.Fatalf("task=%d state=%s err=%q; want %q", task.ID, got.State, got.LastError, want)
		}
	}
	if err := Sweep(context.Background(), db, m, secret.NewStore("k2"), now.Add(FailBackoff+time.Minute), nil); err != nil {
		t.Fatal(err)
	}
	if len(rr.calls) != 0 {
		t.Fatalf("runner must not be called on credential failures: %v", rr.calls)
	}
	var bad storage.CertTask
	if err := db.First(&bad, badKey.ID).Error; err != nil {
		t.Fatal(err)
	}
	if bad.State != StateFailed || !strings.Contains(bad.LastError, "dns token decrypt failed") {
		t.Fatalf("badKey state=%s err=%q", bad.State, bad.LastError)
	}
}

// TestIssueAndRenewEdgeArgs argument boundaries: empty domains rejected, a custom CA reaches the command line,
// with Run not injected it falls back to real exec (acme.sh missing errors out).
func TestIssueAndRenewEdgeArgs(t *testing.T) {
	rr := &recRunner{}
	m := newMgr(rr, time.Now())
	if _, err := m.Issue(context.Background(), nil, MethodDNS01, "cloudflare", nil); err == nil {
		t.Fatal("empty domains must fail")
	}
	if _, err := m.Renew(context.Background(), ""); err == nil {
		t.Fatal("empty domain must fail")
	}
	m.CA = "custom-ca"
	if _, err := m.Issue(context.Background(), []string{"d.example.com"}, MethodHTTP01, "", nil); err == nil {
		t.Fatal("missing webroot must fail")
	}
	m.Webroot = "/var/www"
	if _, err := m.Issue(context.Background(), []string{"d.example.com"}, MethodHTTP01, "", nil); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(rr.calls[len(rr.calls)-1], "--server custom-ca") {
		t.Fatalf("ca args = %q", rr.calls[len(rr.calls)-1])
	}
	// Run not injected: runner() falls back to realRun, and acme.sh being absent makes exec error out.
	if _, err := (&Manager{}).Renew(context.Background(), "d.example.com"); err == nil {
		t.Fatal("default runner must exec and fail")
	}
}
