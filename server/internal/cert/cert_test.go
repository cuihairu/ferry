package cert

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// recRunner 记录每次执行（args 与 env），按序返回预设结果。
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

// dnsFix 入库一条启用的 Cloudflare 凭证，返回 ID。
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

// TestIssueCommand 覆盖命令构造：dns-01 映射 dns_cf 插件、http-01 带
// webroot、附加域名逐个 -d、未知方式/插件不冒称支持。
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

	// 不冒称支持：未知 DNS 插件、未知方式、缺 webroot。
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

// TestSweepIssuesPending 待签任务自动首签：凭证走环境变量、到期回填、
// 状态转 ok；dns-01 未配凭证失败留原因。
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

	// 未配凭证的任务：失败留原因不冒称。
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

// TestSweepRenewsDue 临期任务自动续：进 30 天窗口才续、续后刷新到期；
// 未临期不动。
func TestSweepRenewsDue(t *testing.T) {
	db := newDB(t)
	now := time.Now()
	rr := &recRunner{}
	m := newMgr(rr, now.Add(90*24*time.Hour))

	due := now.Add(10 * 24 * time.Hour) // 进 30 天续期窗口且未过期
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

	// 临期事件（HERALD-3）：进窗口的任务落 cert_expiring，未临期不落；
	// 同日第二轮不重复。
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

// TestSweepFailedBackoff 失败退避：刚失败不重试，退避点过后再签；
// 签发报错落 failed 留原因。
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
	// 退避窗口内：不动。
	if err := Sweep(context.Background(), db, m, secret.NewStore("cert-master"), now, nil); err != nil {
		t.Fatal(err)
	}
	if len(rr.calls) != 0 {
		t.Fatalf("must back off, calls = %v", rr.calls)
	}

	// 退避点过后：重签，这次 runner 报错 → failed 留尾段原因。
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
