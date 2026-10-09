package backup

// S3 兼容端点外发实现（FERRY_BACKUP_S3_*，面板可用性 §2.2）：stdlib 手写
// AWS SigV4 单对象 PUT（path-style），零新依赖。默认关闭——未显式
// FERRY_BACKUP_S3_ENABLED=1 时 main 不注入本实现，备份行为与关闭态零变化；
// 显式开启后每轮把新落档 PUT 到 {endpoint}/{bucket}/{key}，成功才置
// uploaded=1，失败经 backup_failed 告警且本地档保留。region 固定
// us-east-1（S3 兼容端点普遍不校验，真 AWS 跨区桶需要时再开配置位）。

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
	// s3Region 是 SigV4 签名 region（固定值，理由见文件头注记）。
	s3Region = "us-east-1"
	// s3UploadTimeout 限制单次外发时长：无配置位，慢链路也不挂死备份轮次。
	s3UploadTimeout = 10 * time.Minute
)

// S3Uploader 把本地档外发到 S3 兼容端点（Uploader 实现）。零值不可用，
// 经 NewS3Uploader 构造；payload 参与 SigV4 签名（严格端点同样接受）。
type S3Uploader struct {
	scheme    string // http/https（endpoint 缺 scheme 时缺省 https）
	host      string // host[:port]
	basePath  string // endpoint 自带路径前缀（如反代 /s3），可空
	bucket    string
	accessKey string
	secretKey string
	client    *http.Client
}

// NewS3Uploader 构造并解析 endpoint（缺 scheme 补 https；可带路径前缀与
// 端口）。endpoint 缺失/不可解析即报错——开启但配置坏了不静默，启动日志
// 可见；bucket/密钥留空留到 Upload 报错（走 backup_failed 告警，面板可见）。
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

// Upload 把 localPath 档 PUT 到 {endpoint}/{bucket}/{basename}（实现
// Uploader）。流式哈希后原文件作请求体，档不整读进内存；任何错误都不动
// 本地档（外发失败档保留由 run 侧口径保证）。
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
		req.ContentLength = info.Size() // 显式长度，避免 chunked 触发端点兼容问题
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

// s3CanonicalURI 拼 path-style 对象路径并逐段转义（S3 规则：段内转义、
// 分隔符 / 不转义），endpoint 自带前缀路径同样处理。
func s3CanonicalURI(basePath, bucket, key string) string {
	segments := strings.Split(strings.Trim(basePath+"/"+bucket+"/"+key, "/"), "/")
	for i, s := range segments {
		segments[i] = url.PathEscape(s)
	}
	return "/" + strings.Join(segments, "/")
}

// s3SigV4 计算单对象 PUT 的 Authorization 头：canonical 请求只含
// host/x-amz-content-sha256/x-amz-date 三个签名头、无查询串，派生钥按
// date→region→service→terminator 四级 HMAC 链。
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
