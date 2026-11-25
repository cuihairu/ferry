package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Thirty-second batch of fill-in: panel web certificate (P1-7) error terms --
// db failures in each stage of the cert/key read, startup-load pairing failure,
// pair error echoed in the status view, upload bad bind / missing PEM /
// oversize / each of the two saves failing, self-sign bad bind / missing host /
// default days / IP host branch / each of the two saves failing, and the clear
// db failure.
// Left white: crypto/rand entropy failure, CreateCertificate and
// MarshalECPrivateKey failures after key generation (unreachable in the normal
// flow), and ParseCertificate failing after X509KeyPair already parsed the
// leaf (one parse cannot both succeed and fail) -- not forced.

// isSettingModel reports whether the statement lands on the settings KV table.
func isSettingModel(v any) bool {
	switch v.(type) {
	case *storage.Setting, []storage.Setting, *[]storage.Setting:
		return true
	}
	return false
}

// failSettingAt makes the nth (1-based) write statement hitting the settings
// table fail: SetSetting goes through Save (UPDATE, then INSERT if it misses),
// counting across the Update and Create chains.
func failSettingAt(t *testing.T, db *gorm.DB, name string, n int) {
	t.Helper()
	seen := 0
	bump := func(tx *gorm.DB) {
		if isSettingModel(tx.Statement.Model) || isSettingModel(tx.Statement.Dest) {
			seen++
			if seen == n {
				tx.AddError(errors.New("db down"))
			}
		}
	}
	db.Callback().Update().Before("gorm:update").Register(name+"_u", bump)
	db.Callback().Create().Before("gorm:create").Register(name+"_c", bump)
}

// TestLoadWebCertPEMErrors covers the certificate loading helper: cert-stage
// read failure, key-stage read failure, and configured-but-mismatched
// (startup-abort terms).
func TestLoadWebCertPEMErrors(t *testing.T) {
	_, db := newTestRouterWithDB(t)

	// Cert-stage read failure (closing the db makes the first query error).
	closeSQL(t, db)
	if _, _, _, err := loadWebCertPEM(db); err == nil {
		t.Fatal("closed db: want err")
	}

	// Key-stage read failure: the cert stage (1st query) passes, the key stage
	// (2nd query) is injected with an error.
	_, db = newTestRouterWithDB(t)
	seen := 0
	db.Callback().Query().Before("gorm:query").Register("test:setting_q_at", func(tx *gorm.DB) {
		if isSettingModel(tx.Statement.Model) || isSettingModel(tx.Statement.Dest) {
			seen++
			if seen == 2 {
				tx.AddError(errors.New("db down"))
			}
		}
	})
	if _, _, _, err := loadWebCertPEM(db); err == nil {
		t.Fatal("key read: want err")
	}

	// Configured but mismatched: WebTLSCert errors and returns configured=true.
	_, db = newTestRouterWithDB(t)
	_, keyPEM, err := generateSelfSignedPEM("a.example.com", 30)
	if err != nil {
		t.Fatal(err)
	}
	other, _, err := generateSelfSignedPEM("b.example.com", 30)
	if err != nil {
		t.Fatal(err)
	}
	for _, kv := range []struct{ k, v string }{
		{settingKeyWebTLSCert, other}, {settingKeyWebTLSKey, keyPEM},
	} {
		if err := db.Create(&storage.Setting{Key: kv.k, Value: kv.v}).Error; err != nil {
			t.Fatal(err)
		}
	}
	if cert, configured, err := WebTLSCert(db); err == nil || !configured || cert != nil {
		t.Fatalf("mismatch pair: cert=%v configured=%v err=%v", cert, configured, err)
	}
}

// TestGetWebCertError covers the status view: read failure 500 and the pair
// error echoed back (200+Error).
func TestGetWebCertError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedAdminForJWT(t, db)
	// Settings first read injected with an error -> 500 (the re-check First only
	// reads users, so the targeted hook does not intercept it).
	failNthSetting("t:wcerr", db, 1)
	if w := doAdmin(t, r, http.MethodGet, "/admin/web-cert", nil); w.Code != http.StatusInternalServerError {
		t.Fatalf("db fail: %d %s", w.Code, w.Body)
	}

	r, db = newTestRouterWithDB(t)
	seedAdminForJWT(t, db)
	if err := db.Create(&storage.Setting{Key: settingKeyWebTLSCert, Value: "garbage"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.Setting{Key: settingKeyWebTLSKey, Value: "garbage"}).Error; err != nil {
		t.Fatal(err)
	}
	w := doAdmin(t, r, http.MethodGet, "/admin/web-cert", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("pair error view: %d %s", w.Code, w.Body)
	}
	var st webCertStatus
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if st.Valid || st.Error == "" || !st.Configured {
		t.Fatalf("pair error status: %+v", st)
	}
}

// TestPutWebCertErrors covers upload: bad bind, missing PEM, oversize and each
// of the two saves failing. SetSetting goes through Save (a non-primary-key row
// is UPDATEd then INSERTed), so the two saves are targeted by statement order:
// statement 1 lands on the cert key, statement 3 on the private key.
func TestPutWebCertErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedAdminForJWT(t, db)
	if w := doAdmin(t, r, http.MethodPut, "/admin/web-cert", []byte("not json")); w.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", w.Code, w.Body)
	}
	for _, tc := range []struct {
		name string
		body string
		msg  string
	}{
		{"empty", `{"cert_pem":"","key_pem":""}`, "required"},
		{"oversize", `{"cert_pem":"` + strings.Repeat("x", maxPEMBytes+1) + `","key_pem":"y"}`, "too large"},
	} {
		if w := doAdmin(t, r, http.MethodPut, "/admin/web-cert", []byte(tc.body)); w.Code != http.StatusBadRequest ||
			!strings.Contains(w.Body.String(), tc.msg) {
			t.Fatalf("%s: %d %s", tc.name, w.Code, w.Body)
		}
	}

	certPEM, keyPEM, err := generateSelfSignedPEM("put.example.com", 30)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"cert_pem":` + mustQuote(t, certPEM) + `,"key_pem":` + mustQuote(t, keyPEM) + `}`

	// Cert key save failure (statement 1 injected).
	r, db = newTestRouterWithDB(t)
	seedAdminForJWT(t, db)
	failSettingAt(t, db, "test:setting_at_1", 1)
	if w := doAdmin(t, r, http.MethodPut, "/admin/web-cert", []byte(body)); w.Code != http.StatusInternalServerError {
		t.Fatalf("cert save: %d %s", w.Code, w.Body)
	}

	// Private key save failure (statement 3 injected: the cert key's UPDATE and
	// INSERT pair passes first).
	r, db = newTestRouterWithDB(t)
	seedAdminForJWT(t, db)
	failSettingAt(t, db, "test:setting_at_3", 3)
	if w := doAdmin(t, r, http.MethodPut, "/admin/web-cert", []byte(body)); w.Code != http.StatusInternalServerError {
		t.Fatalf("key save: %d %s", w.Code, w.Body)
	}
}

// TestSelfSignWebCertErrors covers self-sign: bad bind, missing host, default
// days, the IP host branch and each of the two saves failing.
func TestSelfSignWebCertErrors(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedAdminForJWT(t, db)
	if w := doAdmin(t, r, http.MethodPost, "/admin/web-cert/selfsign", []byte("not json")); w.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", w.Code, w.Body)
	}
	if w := doAdmin(t, r, http.MethodPost, "/admin/web-cert/selfsign", []byte(`{"host":"  "}`)); w.Code != http.StatusBadRequest {
		t.Fatalf("empty host: %d %s", w.Code, w.Body)
	}

	// Default days 365: omitting the days field takes the default branch.
	r, db = newTestRouterWithDB(t)
	seedAdminForJWT(t, db)
	w := doAdmin(t, r, http.MethodPost, "/admin/web-cert/selfsign", []byte(`{"host":"def-days.example.com"}`))
	if w.Code != http.StatusOK {
		t.Fatalf("default days: %d %s", w.Code, w.Body)
	}
	var st webCertStatus
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Valid || !st.SelfSigned || st.NotAfter == nil {
		t.Fatalf("default days status: %+v", st)
	}

	// IP host: the SAN takes the IPAddresses branch, and the certificate is still issued.
	r, db = newTestRouterWithDB(t)
	seedAdminForJWT(t, db)
	if w := doAdmin(t, r, http.MethodPost, "/admin/web-cert/selfsign", []byte(`{"host":"1.2.3.4","days":30}`)); w.Code != http.StatusOK {
		t.Fatalf("ip host: %d %s", w.Code, w.Body)
	}

	// Cert key save failure (statement 1 injected).
	r, db = newTestRouterWithDB(t)
	seedAdminForJWT(t, db)
	failSettingAt(t, db, "test:ss_setting_at_1", 1)
	if w := doAdmin(t, r, http.MethodPost, "/admin/web-cert/selfsign", []byte(`{"host":"a.example.com"}`)); w.Code != http.StatusInternalServerError {
		t.Fatalf("cert save: %d %s", w.Code, w.Body)
	}

	// Private key save failure (statement 3 injected).
	r, db = newTestRouterWithDB(t)
	seedAdminForJWT(t, db)
	failSettingAt(t, db, "test:ss_setting_at_3", 3)
	if w := doAdmin(t, r, http.MethodPost, "/admin/web-cert/selfsign", []byte(`{"host":"b.example.com"}`)); w.Code != http.StatusInternalServerError {
		t.Fatalf("key save: %d %s", w.Code, w.Body)
	}
}

// TestDeleteWebCertError covers a clear with a db failure returning 500 (the
// fallback to unconfigured only takes effect after a restart).
func TestDeleteWebCertError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	seedAdminForJWT(t, db)
	if err := db.Create(&storage.Setting{Key: settingKeyWebTLSCert, Value: "x"}).Error; err != nil {
		t.Fatal(err)
	}
	// Settings delete injected with an error -> 500 (the re-check First only
	// reads users, so the targeted hook does not intercept it).
	db.Callback().Delete().Before("gorm:delete").Register("t:wcdel", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Model.(*storage.Setting); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if w := doAdmin(t, r, http.MethodDelete, "/admin/web-cert", nil); w.Code != http.StatusInternalServerError {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
}
