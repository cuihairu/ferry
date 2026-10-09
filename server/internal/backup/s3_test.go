package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// s3Record 记录假端点收到的一次请求（SigV4 结构断言用）。
type s3Record struct {
	method, path, host, auth, amzDate, contentSHA string
	body                                          []byte
}

// fakeS3 是 httptest 假 S3 端点：记录请求，status=0/2xx 回 200，其余回
// 该状态码 + 错误体。
type fakeS3 struct {
	status int
	mu     sync.Mutex
	reqs   []s3Record
}

func (f *fakeS3) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	rec := s3Record{
		method: r.Method, path: r.URL.Path, host: r.Host,
		auth: r.Header.Get("Authorization"), amzDate: r.Header.Get("x-amz-date"),
		contentSHA: r.Header.Get("x-amz-content-sha256"), body: body,
	}
	f.mu.Lock()
	f.reqs = append(f.reqs, rec)
	f.mu.Unlock()
	if f.status >= 300 {
		http.Error(w, "upstream unavailable", f.status)
		return
	}
	w.WriteHeader(http.StatusOK)
}

func (f *fakeS3) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.reqs)
}

func (f *fakeS3) all() []s3Record {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]s3Record(nil), f.reqs...)
}

// newFakeS3 起假端点并返回其 URL 与客户端（同进程回环，不上真实网络）。
func newFakeS3(t *testing.T, status int) (*fakeS3, *httptest.Server) {
	t.Helper()
	fake := &fakeS3{status: status}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return fake, srv
}

// quietLogger 吞掉运行日志（三态测试不污染输出）。
func quietLogger() *log.Logger { return log.New(io.Discard, "", 0) }

// TestRunS3DisabledNoNetwork 覆盖硬要求三态之一：未显式开启时行为与关闭
// 态零变化——注入了外发实现也零网络请求、零外发日志，快照落档照常。
func TestRunS3DisabledNoNetwork(t *testing.T) {
	db := openDB(t)
	dir := t.TempDir()
	fake, srv := newFakeS3(t, 0)
	up, err := NewS3Uploader(srv.URL, "ferry-bk", "ak", "sk", srv.Client())
	if err != nil {
		t.Fatalf("uploader: %v", err)
	}
	var logs bytes.Buffer
	logger := log.New(&logs, "", 0)
	if err := run(context.Background(), db, Options{Dir: dir, S3Enabled: false, S3: up, Logger: logger}, secret.NewStore("")); err != nil {
		t.Fatalf("run: %v", err)
	}
	if n := fake.count(); n != 0 {
		t.Fatalf("s3 requests = %d, want 0 (disabled must not touch network)", n)
	}
	if s := logs.String(); strings.Contains(s, "s3") {
		t.Fatalf("log mentions s3 while disabled: %q", s)
	}
	var row storage.Backup
	if err := db.Where("kind = ?", KindScheduled).First(&row).Error; err != nil {
		t.Fatalf("scheduled row: %v", err)
	}
	if row.Uploaded {
		t.Fatal("uploaded must stay false when disabled")
	}
}

// TestRunS3UploadSuccess 覆盖硬要求三态之二：显式开启 + 端点可用 → 每次外发
// 日志明示数据范围，上传成功才置 uploaded=1；假端点同时校验 SigV4 请求
// 结构（方法/path-style 路径/host 签名头/payload 哈希与实体一致/日期与
// 凭证 scope 格式）。
func TestRunS3UploadSuccess(t *testing.T) {
	db := openDB(t)
	dir := t.TempDir()
	fake, srv := newFakeS3(t, 0)
	up, err := NewS3Uploader(srv.URL, "ferry-bk", "AKIDEXAMPLE", "secret", srv.Client())
	if err != nil {
		t.Fatalf("uploader: %v", err)
	}
	var logs bytes.Buffer
	logger := log.New(&logs, "", 0)
	if err := run(context.Background(), db, Options{Dir: dir, S3Enabled: true, S3: up, Logger: logger}, secret.NewStore("")); err != nil {
		t.Fatalf("run: %v", err)
	}
	if n := fake.count(); n != 1 {
		t.Fatalf("s3 requests = %d, want 1", n)
	}
	var row storage.Backup
	if err := db.Where("kind = ?", KindScheduled).First(&row).Error; err != nil {
		t.Fatalf("scheduled row: %v", err)
	}
	if !row.Uploaded {
		t.Fatal("uploaded = false, want true after successful upload")
	}
	if _, err := os.Stat(row.Path); err != nil {
		t.Fatalf("local archive kept: %v", err)
	}
	if !strings.Contains(logs.String(), "data scope = all files in backup dir") {
		t.Fatalf("scope disclosure missing in logs: %q", logs.String())
	}
	rec := fake.all()[0]
	wantPath := "/ferry-bk/" + filepath.Base(row.Path)
	if rec.method != http.MethodPut || rec.path != wantPath {
		t.Fatalf("request = %s %s, want PUT %s", rec.method, rec.path, wantPath)
	}
	if rec.host != strings.TrimPrefix(srv.URL, "http://") {
		t.Fatalf("host = %s, want %s (signed host must match)", rec.host, srv.URL)
	}
	local, err := os.ReadFile(row.Path)
	if err != nil {
		t.Fatalf("read local archive: %v", err)
	}
	if !bytes.Equal(rec.body, local) {
		t.Fatalf("uploaded body mismatch: got %d bytes, want %d", len(rec.body), len(local))
	}
	sum := sha256.Sum256(local)
	if rec.contentSHA != hex.EncodeToString(sum[:]) {
		t.Fatalf("x-amz-content-sha256 = %s, want payload hash %s", rec.contentSHA, hex.EncodeToString(sum[:]))
	}
	if !regexp.MustCompile(`^\d{8}T\d{6}Z$`).MatchString(rec.amzDate) {
		t.Fatalf("x-amz-date = %s, want basic-format ISO8601", rec.amzDate)
	}
	wantAuthPrefix := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/"
	if !strings.HasPrefix(rec.auth, wantAuthPrefix) ||
		!strings.Contains(rec.auth, "/us-east-1/s3/aws4_request, SignedHeaders=host;x-amz-content-sha256;x-amz-date, Signature=") {
		t.Fatalf("authorization = %q, want SigV4 scope us-east-1/s3 with three signed headers", rec.auth)
	}
}

// TestRunS3UploadFailureAlerts 覆盖硬要求三态之三：端点失败 → uploaded=0、
// backup_failed 告警落 outbox（外发失败标题、本地档保留口径入文）、本地
// 档保留，且该轮整体不算失败（滚动清理照常）。
func TestRunS3UploadFailureAlerts(t *testing.T) {
	db := openDB(t)
	dir := t.TempDir()
	fake, srv := newFakeS3(t, http.StatusInternalServerError)
	up, err := NewS3Uploader(srv.URL, "ferry-bk", "ak", "sk", srv.Client())
	if err != nil {
		t.Fatalf("uploader: %v", err)
	}
	if err := run(context.Background(), db, Options{Dir: dir, S3Enabled: true, S3: up, Logger: quietLogger()}, secret.NewStore("")); err != nil {
		t.Fatalf("run: %v (upload failure must not fail the round)", err)
	}
	if n := fake.count(); n != 1 {
		t.Fatalf("s3 requests = %d, want 1", n)
	}
	var row storage.Backup
	if err := db.Where("kind = ?", KindScheduled).First(&row).Error; err != nil {
		t.Fatalf("scheduled row: %v", err)
	}
	if row.Uploaded {
		t.Fatal("uploaded = true, want false on upload failure")
	}
	if _, err := os.Stat(row.Path); err != nil {
		t.Fatalf("local archive kept: %v", err)
	}
	var events []storage.Event
	if err := db.Where("kind = ?", "backup_failed").Find(&events).Error; err != nil {
		t.Fatalf("list events: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %d, want 1 backup_failed alert", len(events))
	}
	if events[0].Title != "备份外发失败" || !strings.Contains(events[0].Body, "本地档已保留") {
		t.Fatalf("event = %q / %q, want upload-failure title and kept-local note", events[0].Title, events[0].Body)
	}
	if !strings.Contains(events[0].Body, "status 500") {
		t.Fatalf("body = %q, want endpoint status embedded", events[0].Body)
	}
}

// TestS3SigV4KnownAnswer 用 openssl 独立对拍的签名做已知答案校验：同一
// canonical 请求/密钥链，两套实现（stdlib 与 openssl dgst HMAC 链）必须
// 得出同一签名。
func TestS3SigV4KnownAnswer(t *testing.T) {
	const payload = "ferry backup payload"
	sum := sha256.Sum256([]byte(payload))
	got := s3SigV4(http.MethodPut, "/ferry-bk/ferry-backup-20261009-030000.db", "127.0.0.1:9000",
		"us-east-1", "20261009T030000Z", hex.EncodeToString(sum[:]),
		"AKIDEXAMPLE", "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY")
	want := "AWS4-HMAC-SHA256 Credential=AKIDEXAMPLE/20261009/us-east-1/s3/aws4_request, " +
		"SignedHeaders=host;x-amz-content-sha256;x-amz-date, " +
		"Signature=1120aa2f33ac2aaa51be3a7011dfe336367fa1e9e22a88aeb136b9dee000275a"
	if got != want {
		t.Fatalf("sigv4 =\n%s\nwant\n%s", got, want)
	}
}

// TestS3CanonicalURI 覆盖 path-style 路径拼接与逐段转义（分隔符不转义）。
func TestS3CanonicalURI(t *testing.T) {
	cases := []struct{ base, bucket, key, want string }{
		{"", "ferry-bk", "ferry-backup-20261009-030000.db", "/ferry-bk/ferry-backup-20261009-030000.db"},
		{"prefix", "bk", "a.db", "/prefix/bk/a.db"},
		{"", "my-bucket", "enc file.enc", "/my-bucket/enc%20file.enc"},
	}
	for _, c := range cases {
		if got := s3CanonicalURI(c.base, c.bucket, c.key); got != c.want {
			t.Fatalf("s3CanonicalURI(%q,%q,%q) = %s, want %s", c.base, c.bucket, c.key, got, c.want)
		}
	}
}

// TestNewS3UploaderEndpoint 覆盖 endpoint 解析：缺 scheme 补 https、带路径
// 前缀与端口、缺失/不可解析报错（开启但配置坏了不静默）。
func TestNewS3UploaderEndpoint(t *testing.T) {
	up, err := NewS3Uploader("s3.example.com", "bk", "ak", "sk", http.DefaultClient)
	if err != nil {
		t.Fatalf("bare host: %v", err)
	}
	if up.scheme != "https" || up.host != "s3.example.com" || up.basePath != "" {
		t.Fatalf("bare host parse = %+v", up)
	}
	up, err = NewS3Uploader("http://127.0.0.1:9000/s3/", "bk", "ak", "sk", http.DefaultClient)
	if err != nil {
		t.Fatalf("prefixed endpoint: %v", err)
	}
	if up.scheme != "http" || up.host != "127.0.0.1:9000" || up.basePath != "s3" {
		t.Fatalf("prefixed parse = %+v", up)
	}
	if _, err := NewS3Uploader("", "bk", "ak", "sk", nil); err == nil {
		t.Fatal("empty endpoint should error")
	}
	if _, err := NewS3Uploader("://bad", "bk", "ak", "sk", nil); err == nil {
		t.Fatal("unparseable endpoint should error")
	}
}
