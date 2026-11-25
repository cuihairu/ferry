package backup

// An S3-compatible endpoint outbound implementation (FERRY_BACKUP_S3_*, panel availability §2.2): a
// hand-written AWS SigV4 single-object PUT in the stdlib (path-style), with zero new dependencies. Off by
// default - when FERRY_BACKUP_S3_ENABLED=1 is not set explicitly, main does not inject this implementation
// and backup behavior is unchanged from the disabled state; once explicitly enabled, each round PUTs the
// new archive to {endpoint}/{bucket}/{key}, only success sets uploaded=1, and a failure alerts via backup_failed
// while keeping the local archive. The region is fixed at us-east-1 (S3-compatible endpoints rarely validate it; add a config slot if a real AWS cross-region bucket needs it).

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	// s3Region is the SigV4 signing region (a fixed value; the rationale is in the file header note).
	s3Region = "us-east-1"
	// s3UploadTimeout bounds a single outbound duration: there is no config slot, so a slow chain must not hang the backup round.
	s3UploadTimeout = 10 * time.Minute
)

// S3Uploader sends a local archive to an S3-compatible endpoint (an Uploader implementation). The zero
// value is unusable; construct via NewS3Uploader; the payload participates in the SigV4 signature (a strict endpoint accepts it equally).
type S3Uploader struct {
	scheme    string // http/https (defaults to https when the endpoint lacks a scheme)
	host      string // host[:port]
	basePath  string // a path prefix carried by the endpoint itself (e.g. a reverse proxy /s3), may be empty
	bucket    string
	accessKey string
	secretKey string
	client    *http.Client
}

// NewS3Uploader constructs and parses the endpoint (a missing scheme gets https; a path prefix and
// port are allowed). A missing / unparseable endpoint errors out immediately - enabled but misconfigured is not silent, and it is
// visible in the startup log; an empty bucket/key is left to error in Upload (routing via the backup_failed alert, visible in the panel).
func NewS3Uploader(endpoint, bucket, accessKey, secretKey string, client *http.Client) (*S3Uploader, error) {
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		return nil, errors.New("s3 endpoint not set")
	}
	if !strings.Contains(ep, "://") {
		ep = "https://" + ep
	}
	u, err := url.Parse(ep)
	if err != nil {
		return nil, fmt.Errorf("s3 endpoint %q: %w", endpoint, err)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("s3 endpoint %q: missing host", endpoint)
	}
	if client == nil {
		client = &http.Client{Timeout: s3UploadTimeout}
	}
	return &S3Uploader{
		scheme:    u.Scheme,
		host:      u.Host,
		basePath:  strings.Trim(u.Path, "/"),
		bucket:    bucket,
		accessKey: accessKey,
		secretKey: secretKey,
		client:    client,
	}, nil
}

// Upload PUTs the localPath archive to {endpoint}/{bucket}/{basename} (implementing
// Uploader). After streaming the hash, the original file becomes the request body, so the archive is never read fully into memory; no error
// touches the local archive (keeping the archive on outbound failure is run's policy). outbound logs, and snapshot archiving proceeds as usual.
func (u *S3Uploader) Upload(ctx context.Context, localPath string) error {
	if u.bucket == "" || u.accessKey == "" || u.secretKey == "" {
		return errors.New("s3 bucket/access key/secret key not set")
	}
	f, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("open backup: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("hash backup: %w", err)
	}
	payloadHash := hex.EncodeToString(h.Sum(nil))
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind backup: %w", err)
	}
	amzDate := time.Now().UTC().Format("20060102T150405Z")
	uri := s3CanonicalURI(u.basePath, u.bucket, filepath.Base(localPath))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.scheme+"://"+u.host+uri, f)
	if err != nil {
		return err
	}
	if info, err := f.Stat(); err == nil {
		req.ContentLength = info.Size() // explicit length, avoiding chunked triggering endpoint compatibility issues
	}
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	req.Header.Set("Authorization", s3SigV4(http.MethodPut, uri, u.host, s3Region, amzDate, payloadHash, u.accessKey, u.secretKey))
	resp, err := u.client.Do(req)
	if err != nil {
		return fmt.Errorf("s3 put %s: %w", uri, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("s3 put %s: status %d: %s", uri, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return nil
}

// s3CanonicalURI assembles the path-style object path escaping segment by segment (S3 rules: escape
// inside a segment, do not escape the separator /); a prefix path carried by the endpoint is handled the same way.
func s3CanonicalURI(basePath, bucket, key string) string {
	segments := strings.Split(strings.Trim(basePath+"/"+bucket+"/"+key, "/"), "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return "/" + strings.Join(segments, "/")
}

// s3SigV4 computes the Authorization header of a single-object PUT: the canonical request contains
// only the three signing headers host/x-amz-content-sha256/x-amz-date and no query string, and the derived key
// follows the four-level HMAC chain date->region->service->terminator.
func s3SigV4(method, canonicalURI, host, region, amzDate, payloadHash, accessKey, secretKey string) string {
	date := amzDate[:8]
	const signed = "host;x-amz-content-sha256;x-amz-date"
	canonical := method + "\n" + canonicalURI + "\n\n" +
		"host:" + host + "\n" +
		"x-amz-content-sha256:" + payloadHash + "\n" +
		"x-amz-date:" + amzDate + "\n" +
		"\n" + signed + "\n" + payloadHash
	scope := date + "/" + region + "/s3/aws4_request"
	sum := sha256.Sum256([]byte(canonical))
	stringToSign := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(sum[:])
	k := hmacChain([]byte("AWS4"+secretKey), date, region, "s3", "aws4_request")
	sig := hex.EncodeToString(hmacSHA256(k, stringToSign))
	return "AWS4-HMAC-SHA256 Credential=" + accessKey + "/" + scope +
		", SignedHeaders=" + signed + ", Signature=" + sig
}

func hmacSHA256(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func hmacChain(key []byte, parts ...string) []byte {
	for _, p := range parts {
		key = hmacSHA256(key, p)
	}
	return key
}
