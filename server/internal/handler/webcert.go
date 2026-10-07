package handler

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// 面板 Web 证书管理（P1-7，对齐 3x-ui getWebCertFiles 的上传/自签口径；
// ACME 留给部署层）。证书与私钥 PEM 存 settings KV，随 SQLite 备份迁移；
// 面板启动时读取，切换证书需重启生效。

const (
	settingKeyWebTLSCert = "web_tls_cert"
	settingKeyWebTLSKey  = "web_tls_key"

	// maxPEMBytes 限制单个 PEM 上传体积，防误传大文件。
	maxPEMBytes = 64 << 10
)

// webCertStatus 是证书状态视图（不含私钥）。
type webCertStatus struct {
	Configured bool       `json:"configured"`
	Valid      bool       `json:"valid"` // 证书与私钥配对可用
	Subject    string     `json:"subject,omitempty"`
	DNSNames   []string   `json:"dns_names,omitempty"`
	NotAfter   *time.Time `json:"not_after,omitempty"`
	SelfSigned bool       `json:"self_signed,omitempty"`
	Error      string     `json:"error,omitempty"` // 配置存在但不可用时的原因
}

// loadWebCertPEM 读取证书与私钥 PEM；两者齐备返回 ok=true。
func loadWebCertPEM(db *gorm.DB) (certPEM, keyPEM string, ok bool, err error) {
	certPEM, certOK, err := storage.GetSetting(db, settingKeyWebTLSCert)
	if err != nil {
		return "", "", false, err
	}
	keyPEM, keyOK, err := storage.GetSetting(db, settingKeyWebTLSKey)
	if err != nil {
		return "", "", false, err
	}
	return certPEM, keyPEM, certOK && keyOK, nil
}

// WebTLSCert 加载面板 TLS 证书（启动用）：未配置 ok=false；
// 已配置但配对失败报错，启动中止以免静默降级 HTTP。
func WebTLSCert(db *gorm.DB) (*tls.Certificate, bool, error) {
	certPEM, keyPEM, ok, err := loadWebCertPEM(db)
	if err != nil || !ok {
		return nil, false, err
	}
	pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return nil, true, err
	}
	return &pair, true, nil
}

// getWebCert 查看证书状态（GET /admin/web-cert）。
func (h *Handler) getWebCert(c *gin.Context) {
	certPEM, keyPEM, configured, err := loadWebCertPEM(h.db)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	st := webCertStatus{Configured: configured}
	if configured {
		fillWebCertStatus(&st, certPEM, keyPEM)
	}
	c.JSON(http.StatusOK, st)
}

// fillWebCertStatus 解析证书填充状态视图；配对失败写 Error 保持 Valid=false。
func fillWebCertStatus(st *webCertStatus, certPEM, keyPEM string) {
	pair, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		st.Error = err.Error()
		return
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		st.Error = err.Error()
		return
	}
	st.Valid = true
	st.Subject = leaf.Subject.String()
	st.DNSNames = leaf.DNSNames
	notAfter := leaf.NotAfter
	st.NotAfter = &notAfter
	st.SelfSigned = bytes.Equal(leaf.RawIssuer, leaf.RawSubject)
}

// putWebCert 上传证书与私钥（PUT /admin/web-cert）：校验配对后保存，重启生效。
func (h *Handler) putWebCert(c *gin.Context) {
	var in struct {
		CertPEM string `json:"cert_pem"`
		KeyPEM  string `json:"key_pem"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	if in.CertPEM == "" || in.KeyPEM == "" {
		fail(c, http.StatusBadRequest, errors.New("cert_pem and key_pem are required"))
		return
	}
	if len(in.CertPEM) > maxPEMBytes || len(in.KeyPEM) > maxPEMBytes {
		fail(c, http.StatusBadRequest, errors.New("pem too large"))
		return
	}
	if _, err := tls.X509KeyPair([]byte(in.CertPEM), []byte(in.KeyPEM)); err != nil {
		fail(c, http.StatusBadRequest, errors.New("cert/key pair invalid: "+err.Error()))
		return
	}
	if err := storage.SetSetting(h.db, settingKeyWebTLSCert, in.CertPEM); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if err := storage.SetSetting(h.db, settingKeyWebTLSKey, in.KeyPEM); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"saved": true})
}

// selfSignWebCert 自签证书（POST /admin/web-cert/selfsign）：ECC P-256，
// host 支持域名或 IP，days 缺省 365、上限 3650。
func (h *Handler) selfSignWebCert(c *gin.Context) {
	var in struct {
		Host string `json:"host"`
		Days int    `json:"days"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		fail(c, http.StatusBadRequest, err)
		return
	}
	host := strings.TrimSpace(in.Host)
	if host == "" {
		fail(c, http.StatusBadRequest, errors.New("host is required"))
		return
	}
	days := in.Days
	if days == 0 {
		days = 365
	}
	if days < 1 || days > 3650 {
		fail(c, http.StatusBadRequest, errors.New("days must be 1-3650"))
		return
	}
	certPEM, keyPEM, err := generateSelfSignedPEM(host, days)
	if err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if err := storage.SetSetting(h.db, settingKeyWebTLSCert, certPEM); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	if err := storage.SetSetting(h.db, settingKeyWebTLSKey, keyPEM); err != nil {
		fail(c, http.StatusInternalServerError, err)
		return
	}
	st := webCertStatus{Configured: true}
	fillWebCertStatus(&st, certPEM, keyPEM)
	c.JSON(http.StatusOK, st)
}

// deleteWebCert 清除证书（DELETE /admin/web-cert），面板回落 HTTP（重启生效）。
func (h *Handler) deleteWebCert(c *gin.Context) {
	for _, key := range []string{settingKeyWebTLSCert, settingKeyWebTLSKey} {
		if err := h.db.Where("key = ?", key).Delete(&storage.Setting{}).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// generateSelfSignedPEM 生成自签证书与私钥 PEM（ECC P-256，服务器认证用途）。
func generateSelfSignedPEM(host string, days int) (string, string, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return "", "", err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return "", "", err
	}
	now := time.Now()
	tmpl := x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: host},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.AddDate(0, 0, days),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	if ip := net.ParseIP(host); ip != nil {
		tmpl.IPAddresses = []net.IP{ip}
	} else {
		tmpl.DNSNames = []string{host}
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		return "", "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return "", "", err
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	return string(certPEM), string(keyPEM), nil
}
