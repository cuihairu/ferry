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

// doAdmin sends the request with an admin token.
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

// TestWebCertLifecycle covers the certificate lifecycle (P1-7): initially
// unconfigured -> self-sign -> status usable -> upload a pair as replacement
// -> mismatched pair rejected -> clear and fall back to HTTP.
func TestWebCertLifecycle(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedAdminForJWT(t, db)

	// Initially unconfigured
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
	// Startup load: unconfigured, ok=false
	if _, ok, err := WebTLSCert(db); err != nil || ok {
		t.Fatalf("WebTLSCert unconfigured: ok=%v err=%v", ok, err)
	}

	// Self-sign: 30 days
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

	// Startup load: pairing succeeds and yields a usable certificate
	cert, ok, err := WebTLSCert(db)
	if err != nil || !ok || cert == nil || len(cert.Certificate) == 0 {
		t.Fatalf("WebTLSCert after selfsign: ok=%v err=%v", ok, err)
	}

	// Upload replacement: another self-signed pair
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

	// Mismatch rejected: cert A with key B
	otherCert, _, err := generateSelfSignedPEM("other.example.com", 90)
	if err != nil {
		t.Fatalf("generate other pair: %v", err)
	}
	badBody := `{"cert_pem":` + mustQuote(t, otherCert) + `,"key_pem":` + mustQuote(t, keyPEM) + `}`
	if w = doAdmin(t, r, "PUT", "/admin/web-cert", []byte(badBody)); w.Code != http.StatusBadRequest {
		t.Fatalf("mismatched pair should 400: %d %s", w.Code, w.Body)
	}

	// Clear: falls back to unconfigured
	if w = doAdmin(t, r, "DELETE", "/admin/web-cert", nil); w.Code != http.StatusOK {
		t.Fatalf("delete cert: %d %s", w.Code, w.Body)
	}
	if _, ok, err := WebTLSCert(db); err != nil || ok {
		t.Fatalf("WebTLSCert after delete: ok=%v err=%v", ok, err)
	}

	// Self-sign parameter validation
	if w = doAdmin(t, r, "POST", "/admin/web-cert/selfsign", []byte(`{"host":"x.com","days":9999}`)); w.Code != http.StatusBadRequest {
		t.Fatalf("days out of range should 400: %d", w.Code)
	}
	if w = doAdmin(t, r, "POST", "/admin/web-cert/selfsign", []byte(`{"days":30}`)); w.Code != http.StatusBadRequest {
		t.Fatalf("missing host should 400: %d", w.Code)
	}

	// No token 401
	if w := doBare(t, r, "GET", "/admin/web-cert"); w.Code != http.StatusUnauthorized {
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
