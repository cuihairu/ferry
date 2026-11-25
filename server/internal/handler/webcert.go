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

// Panel web certificate management (P1-7, aligned with 3x-ui getWebCertFiles'
// upload/self-sign terms; ACME is left to the deployment layer). The cert and
// key PEM live in the settings KV and migrate with the SQLite backup; they are
// read at panel startup, so switching certificates takes effect on restart.

const (
	settingKeyWebTLSCert = "web_tls_cert"
	settingKeyWebTLSKey  = "web_tls_key"

	// maxPEMBytes caps a single uploaded PEM size, guarding against a fat file uploaded by mistake.
	maxPEMBytes = 64 << 10
)

// webCertStatus is the certificate status view (no private key).
type webCertStatus struct {
	Configured bool       `json:"configured"`
	Valid      bool       `json:"valid"` // cert and key pair up and are usable
	Subject    string     `json:"subject,omitempty"`
	DNSNames   []string   `json:"dns_names,omitempty"`
	NotAfter   *time.Time `json:"not_after,omitempty"`
	SelfSigned bool       `json:"self_signed,omitempty"`
	Error      string     `json:"error,omitempty"` // reason when configured but unusable
}

// loadWebCertPEM reads the cert and key PEM; ok=true when both are present.
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

// WebTLSCert loads the panel TLS certificate (startup use): ok=false when
// unconfigured; a configured pair that fails to match returns an error and
// aborts startup instead of silently degrading to HTTP.
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

// getWebCert views the certificate status (GET /admin/web-cert).
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

// fillWebCertStatus parses the certificate and fills the status view; a pairing
// failure writes Error and keeps Valid=false.
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

// putWebCert uploads the certificate and private key (PUT /admin/web-cert):
// saves after validating the pair; takes effect on restart.
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

// selfSignWebCert self-signs a certificate (POST /admin/web-cert/selfsign):
// ECC P-256; host accepts a domain name or an IP, days defaults to 365 with a
// cap of 3650.
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

// deleteWebCert clears the certificate (DELETE /admin/web-cert); the panel falls
// back to HTTP (takes effect on restart).
func (h *Handler) deleteWebCert(c *gin.Context) {
	for _, key := range []string{settingKeyWebTLSCert, settingKeyWebTLSKey} {
		if err := h.db.Where("key = ?", key).Delete(&storage.Setting{}).Error; err != nil {
			fail(c, http.StatusInternalServerError, err)
			return
		}
	}
	c.JSON(http.StatusOK, gin.H{"deleted": true})
}

// generateSelfSignedPEM produces a self-signed certificate and private key PEM
// (ECC P-256, server authentication use).
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
