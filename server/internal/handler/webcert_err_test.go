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

// 三十二批补口：面板 Web 证书（P1-7）错误口径——证书/私钥读取各段库故障、
// 启动加载配对失败、状态视图配对错误回显、上传坏绑定/缺 PEM/超限/两次落库
// 各自失败、自签坏绑定/缺 host/默认天数/IP host 分支/两次落库各自失败、
// 清除库故障。
// 留白：crypto/rand 取熵失败、密钥生成后 CreateCertificate 与
// MarshalECPrivateKey 失败（正常流程不可达）、X509KeyPair 已解析叶证书后
// ParseCertificate 再失败（同一次解析不可能成败各一）不硬凑。

// isSettingModel 判断语句是否落在设置 KV 表上。
func isSettingModel(v any) bool {
	switch v.(type) {
	case *storage.Setting, []storage.Setting, *[]storage.Setting:
		return true
	}
	return false
}

// failSettingAt 让第 n 次（1 起）命中设置表的写语句失败：SetSetting 走
// Save（UPDATE 未中再 INSERT），跨 Update/Create 两条链计数。
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

// TestLoadWebCertPEMErrors 证书加载助手：证书段读取失败、私钥段读取失败与
// 已配置但配对失败（启动中止口径）。
func TestLoadWebCertPEMErrors(t *testing.T) {
	_, db := newTestRouterWithDB(t)

	// 证书段读取失败（关库即首查报错）。
	closeSQL(t, db)
	if _, _, _, err := loadWebCertPEM(db); err == nil {
		t.Fatal("closed db: want err")
	}

	// 私钥段读取失败：证书段（第 1 查）放行，私钥段（第 2 查）注错。
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

	// 已配置但配对失败：WebTLSCert 报错并回 configured=true。
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

// TestGetWebCertError 状态视图：读取失败 500 与配对错误回显（200+Error）。
func TestGetWebCertError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	closeSQL(t, db)
	if w := doAdmin(t, r, http.MethodGet, "/admin/web-cert", nil); w.Code != http.StatusInternalServerError {
		t.Fatalf("db fail: %d %s", w.Code, w.Body)
	}

	r, db = newTestRouterWithDB(t)
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

// TestPutWebCertErrors 上传：坏绑定、缺 PEM、超限与两次落库各自失败。
// SetSetting 走 Save（非主键行先 UPDATE 后 INSERT），两次保存按语句次序
// 定向：第 1 句落在证书键，第 3 句落在私钥键。
func TestPutWebCertErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
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

	// 证书键落库失败（第 1 句注错）。
	r, db := newTestRouterWithDB(t)
	failSettingAt(t, db, "test:setting_at_1", 1)
	if w := doAdmin(t, r, http.MethodPut, "/admin/web-cert", []byte(body)); w.Code != http.StatusInternalServerError {
		t.Fatalf("cert save: %d %s", w.Code, w.Body)
	}

	// 私钥键落库失败（第 3 句注错：证书键 UPDATE+INSERT 两句先过）。
	r, db = newTestRouterWithDB(t)
	failSettingAt(t, db, "test:setting_at_3", 3)
	if w := doAdmin(t, r, http.MethodPut, "/admin/web-cert", []byte(body)); w.Code != http.StatusInternalServerError {
		t.Fatalf("key save: %d %s", w.Code, w.Body)
	}
}

// TestSelfSignWebCertErrors 自签：坏绑定、缺 host、默认天数、IP host 分支
// 与两次落库各自失败。
func TestSelfSignWebCertErrors(t *testing.T) {
	r, _ := newTestRouterWithDB(t)
	if w := doAdmin(t, r, http.MethodPost, "/admin/web-cert/selfsign", []byte("not json")); w.Code != http.StatusBadRequest {
		t.Fatalf("bind: %d %s", w.Code, w.Body)
	}
	if w := doAdmin(t, r, http.MethodPost, "/admin/web-cert/selfsign", []byte(`{"host":"  "}`)); w.Code != http.StatusBadRequest {
		t.Fatalf("empty host: %d %s", w.Code, w.Body)
	}

	// 缺省天数 365：days 段不传走默认分支。
	r, _ = newTestRouterWithDB(t)
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

	// IP host：SAN 走 IPAddresses 分支，证书照发。
	r, _ = newTestRouterWithDB(t)
	if w := doAdmin(t, r, http.MethodPost, "/admin/web-cert/selfsign", []byte(`{"host":"1.2.3.4","days":30}`)); w.Code != http.StatusOK {
		t.Fatalf("ip host: %d %s", w.Code, w.Body)
	}

	// 证书键落库失败（第 1 句注错）。
	r, db := newTestRouterWithDB(t)
	failSettingAt(t, db, "test:ss_setting_at_1", 1)
	if w := doAdmin(t, r, http.MethodPost, "/admin/web-cert/selfsign", []byte(`{"host":"a.example.com"}`)); w.Code != http.StatusInternalServerError {
		t.Fatalf("cert save: %d %s", w.Code, w.Body)
	}

	// 私钥键落库失败（第 3 句注错）。
	r, db = newTestRouterWithDB(t)
	failSettingAt(t, db, "test:ss_setting_at_3", 3)
	if w := doAdmin(t, r, http.MethodPost, "/admin/web-cert/selfsign", []byte(`{"host":"b.example.com"}`)); w.Code != http.StatusInternalServerError {
		t.Fatalf("key save: %d %s", w.Code, w.Body)
	}
}

// TestDeleteWebCertError 清除证书库故障 500（回落未配置需在重启后生效）。
func TestDeleteWebCertError(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	if err := db.Create(&storage.Setting{Key: settingKeyWebTLSCert, Value: "x"}).Error; err != nil {
		t.Fatal(err)
	}
	closeSQL(t, db)
	if w := doAdmin(t, r, http.MethodDelete, "/admin/web-cert", nil); w.Code != http.StatusInternalServerError {
		t.Fatalf("delete: %d %s", w.Code, w.Body)
	}
}
