package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

// doAdmin 带管理员令牌发请求。
func doAdmin(t *testing.T, r *gin.Engine, method, path string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+adminJWT(t))
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

// TestWebCertLifecycle 覆盖证书生命周期（P1-7）：初始未配置 →
// 自签 → 状态可用 → 上传配对替换 → 错配拒绝 → 清除回落 HTTP。
func TestWebCertLifecycle(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// 初始未配置
	w := doAdmin(t, r, "GET", "/admin/web-cert", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("get initial: %d %s", w.Code, w.Body)
	}
	var st webCertStatus
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if st.Configured {
		t.Fatalf("initial cert should be unconfigured: %+v", st)
	}
	// 启动加载：未配置 ok=false
	if _, ok, err := WebTLSCert(db); err != nil || ok {
		t.Fatalf("WebTLSCert unconfigured: ok=%v err=%v", ok, err)
	}

	// 自签：30 天
	w = doAdmin(t, r, "POST", "/admin/web-cert/selfsign", []byte(`{"host":"panel.example.com","days":30}`))
	if w.Code != http.StatusOK {
		t.Fatalf("selfsign: %d %s", w.Code, w.Body)
	}
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !st.Configured || !st.Valid || !st.SelfSigned {
		t.Fatalf("selfsign status mismatch: %+v", st)
	}
	if len(st.DNSNames) != 1 || st.DNSNames[0] != "panel.example.com" {
		t.Fatalf("dns names mismatch: %v", st.DNSNames)
	}
	if want := time.Now().AddDate(0, 0, 30); st.NotAfter == nil || st.NotAfter.Sub(want) > time.Hour || want.Sub(*st.NotAfter) > time.Hour {
		t.Fatalf("not_after = %v, want ~%v", st.NotAfter, want)
	}

	// 启动加载：配对成功拿到可用证书
	cert, ok, err := WebTLSCert(db)
	if err != nil || !ok || cert == nil || len(cert.Certificate) == 0 {
		t.Fatalf("WebTLSCert after selfsign: ok=%v err=%v", ok, err)
	}

	// 上传替换：另一对自签证书
	certPEM, keyPEM, err := generateSelfSignedPEM("upload.example.com", 90)
	if err != nil {
		t.Fatalf("generate pair: %v", err)
	}
	body := `{"cert_pem":` + mustQuote(t, certPEM) + `,"key_pem":` + mustQuote(t, keyPEM) + `}`
	if w = doAdmin(t, r, "PUT", "/admin/web-cert", []byte(body)); w.Code != http.StatusOK {
		t.Fatalf("put cert: %d %s", w.Code, w.Body)
	}
	w = doAdmin(t, r, "GET", "/admin/web-cert", nil)
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !st.Valid || !st.SelfSigned || !strings.Contains(st.Subject, "upload.example.com") {
		t.Fatalf("uploaded cert status mismatch: %+v", st)
	}

	// 错配拒绝：A 证书配 B 私钥
	otherCert, _, err := generateSelfSignedPEM("other.example.com", 90)
	if err != nil {
		t.Fatalf("generate other pair: %v", err)
	}
	badBody := `{"cert_pem":` + mustQuote(t, otherCert) + `,"key_pem":` + mustQuote(t, keyPEM) + `}`
	if w = doAdmin(t, r, "PUT", "/admin/web-cert", []byte(badBody)); w.Code != http.StatusBadRequest {
		t.Fatalf("mismatched pair should 400: %d %s", w.Code, w.Body)
	}

	// 清除：回落未配置
	if w = doAdmin(t, r, "DELETE", "/admin/web-cert", nil); w.Code != http.StatusOK {
		t.Fatalf("delete cert: %d %s", w.Code, w.Body)
	}
	if _, ok, err := WebTLSCert(db); err != nil || ok {
		t.Fatalf("WebTLSCert after delete: ok=%v err=%v", ok, err)
	}

	// 自签参数校验
	if w = doAdmin(t, r, "POST", "/admin/web-cert/selfsign", []byte(`{"host":"x.com","days":9999}`)); w.Code != http.StatusBadRequest {
		t.Fatalf("days out of range should 400: %d", w.Code)
	}
	if w = doAdmin(t, r, "POST", "/admin/web-cert/selfsign", []byte(`{"days":30}`)); w.Code != http.StatusBadRequest {
		t.Fatalf("missing host should 400: %d", w.Code)
	}

	// 无令牌 401
	if w := doJSON(t, r, "GET", "/admin/web-cert", nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("without token should 401: %d", w.Code)
	}
}

func mustQuote(t *testing.T, s string) string {
	t.Helper()
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}
