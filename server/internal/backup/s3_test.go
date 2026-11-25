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

// s3Record records one request received by the fake endpoint (for SigV4 structure assertions).
type s3Record struct {
	method, path, host, auth, amzDate, contentSHA string
	body                                          []byte
}

// fakeS3 is an httptest fake S3 endpoint: it records requests, returning 200 for status=0/2xx and
// that status code plus an error body otherwise.
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

// newFakeS3 starts the fake endpoint and returns its URL and a client (same-process loopback, no real network).
func newFakeS3(t *testing.T, status int) (*fakeS3, *httptest.Server) {
	t.Helper()
	fake := &fakeS3{status: status}
	srv := httptest.NewServer(fake)
	t.Cleanup(srv.Close)
	return fake, srv
}

// quietLogger swallows runtime logs (so the three-state test does not pollute output).
func quietLogger() *log.Logger { return log.New(io.Discard, "", 0) }

// TestRunS3DisabledNoNetwork covers one of the hard-requirement three states: when not explicitly enabled, behavior is
// unchanged from the disabled state - even with an outbound implementation injected there are zero network requests and zero
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

// TestRunS3UploadSuccess covers the second hard-requirement state: explicitly enabled + a reachable endpoint -> every
// outbound log states the data scope, and uploaded=1 is set only on success; the fake endpoint also validates the SigV4 request
// structure (method / path-style path / host signing header / payload hash matching the entity / date and
// credential scope formats).
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

// TestRunS3UploadFailureAlerts covers the third hard-requirement state: an endpoint failure -> uploaded=0,
// a backup_failed alert lands in the outbox (outbound-failure title, the local-archive-kept policy in the body), the
// local archive is kept, and the round as a whole is not counted as a failure (rolling cleanup proceeds).
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

// TestS3SigV4KnownAnswer runs a known-answer check against a signature cross-checked with openssl: the same
// canonical request / key chain must produce the same signature in both implementations (stdlib and the openssl dgst HMAC chain).
// derive the same signature.
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

// TestS3CanonicalURI covers path-style path assembly and per-segment escaping (separators are not escaped).
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

// TestNewS3UploaderEndpoint covers endpoint parsing: a missing scheme gets https, a path prefix and
// port, and a missing / unparseable one errors (enabled but misconfigured is not silent).
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
