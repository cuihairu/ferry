package speedtest

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMeasureDefaultBytes n<=0 取缺省字节数（与面板 speedtestDefault 对齐）。
func TestMeasureDefaultBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(make([]byte, DefaultBytes))
	}))
	defer srv.Close()
	res, err := Measure(context.Background(), srv.URL, 0)
	if err != nil {
		t.Fatalf("measure: %v", err)
	}
	if res.Bytes != DefaultBytes {
		t.Fatalf("bytes = %d; want default %d", res.Bytes, DefaultBytes)
	}
	if res.Mbps <= 0 {
		t.Fatalf("mbps = %d; want positive", res.Mbps)
	}
}

// TestMeasureBadBaseURL 面板根地址非法：构造请求即失败。
func TestMeasureBadBaseURL(t *testing.T) {
	if _, err := Measure(context.Background(), "http://bad\nurl.example", 100); err == nil {
		t.Fatal("malformed base URL must error")
	}
}

// TestMeasureConnRefused 面板不可达：上报错误。
func TestMeasureConnRefused(t *testing.T) {
	if _, err := Measure(context.Background(), "http://127.0.0.1:1", 100); err == nil {
		t.Fatal("refused endpoint must error")
	}
}

// TestMeasureTruncatedBody 声明长度与实际不符：下载中断报错。
func TestMeasureTruncatedBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("short")) // 只给一部分即断开
	}))
	defer srv.Close()
	if _, err := Measure(context.Background(), srv.URL, 1000); err == nil {
		t.Fatal("truncated body must error")
	}
}

// TestMeasureNoData 空响应体：判无效结果而不是除零/0 Mbps。
func TestMeasureNoData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	_, err := Measure(context.Background(), srv.URL, 100)
	if err == nil || !strings.Contains(err.Error(), "no data") {
		t.Fatalf("err = %v; want no data error", err)
	}
}
