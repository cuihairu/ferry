package keyrotate

import (
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

// TestKeyringRotation covers F-1723 step 1 at the store level: the transition
// keyring "v1:old,v2:new" keeps decrypting old v1 ciphertexts while new ones
// carry v2; the retired ring ("v2:new" only) opens v2 and refuses v1.
func TestKeyringRotation(t *testing.T) {
	old := secret.NewStore("old-key")
	v1ct, err := old.Encrypt("sk-live")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(v1ct, "v1:") {
		t.Fatalf("plain key must encrypt as v1: %s", v1ct)
	}

	// Transition ring: old values open, new values use the higher version.
	ring := secret.NewStore("v1:old-key,v2:new-key")
	if plain, err := ring.Decrypt(v1ct); err != nil || plain != "sk-live" {
		t.Fatalf("ring must decrypt v1: %q err=%v", plain, err)
	}
	if ring.CurrentVersion() != 2 {
		t.Fatalf("ring current version = %d", ring.CurrentVersion())
	}
	v2ct, err := ring.Encrypt("sk-live")
	if err != nil || !strings.HasPrefix(v2ct, "v2:") {
		t.Fatalf("ring must encrypt as v2: %s err=%v", v2ct, err)
	}
	// Term order is irrelevant; whitespace tolerated.
	ring2 := secret.NewStore(" v2:new-key , v1:old-key ")
	if plain, err := ring2.Decrypt(v1ct); err != nil || plain != "sk-live" {
		t.Fatalf("reordered ring must decrypt v1: %q err=%v", plain, err)
	}

	// Retired ring: v2 opens, v1 refuses with the no-key error.
	retired := secret.NewStore("v2:new-key")
	if plain, err := retired.Decrypt(v2ct); err != nil || plain != "sk-live" {
		t.Fatalf("retired ring must decrypt v2: %q err=%v", plain, err)
	}
	if _, err := retired.Decrypt(v1ct); err == nil || !strings.Contains(err.Error(), "no key for cipher version v1") {
		t.Fatalf("retired ring must refuse v1: %v", err)
	}

	// Binary forms ride the same ring: EncryptBytes headers the current version.
	bct, err := ring.EncryptBytes([]byte("blob"))
	if err != nil || int(bct[0]) != 2 {
		t.Fatalf("EncryptBytes version byte = %v err=%v", bct[0], err)
	}
	if plain, err := retired.DecryptBytes(bct); err != nil || string(plain) != "blob" {
		t.Fatalf("retired ring DecryptBytes: %q err=%v", plain, err)
	}
}

// TestRun covers F-1723 step 1's walk: sealed rows under an old version are
// re-encrypted to the ring's current version in one pass, the pass is
// idempotent, and a keyring missing the old key leaves rows untouched with an
// error reported (never fails the boot).
func TestRun(t *testing.T) {
	db := testDB(t)
	old := secret.NewStore("old-key")
	seal := map[string]string{
		"prov": mustSeal(t, old, "sk-prov"),
		"dns":  mustSeal(t, old, "sk-dns"),
		"totp": mustSeal(t, old, "JBSWY3DPEHPK3PXP"),
	}
	prov := storage.Provider{Name: "kr-prov", Type: "vultr", AccessKey: seal["prov"]}
	dns := storage.DNSProvider{Name: "kr-dns", Type: "cloudflare", APIKey: seal["dns"]}
	user := storage.User{Username: "kr-user", SubToken: "st", Password: "x", TOTPSecret: seal["totp"]}
	for _, m := range []any{&prov, &dns, &user} {
		if err := db.Create(m).Error; err != nil {
			t.Fatal(err)
		}
	}
	// A plaintext (never-sealed) column value must not be touched.
	plainProv := storage.Provider{Name: "kr-plain", Type: "vultr", AccessKey: "raw-key"}
	if err := db.Create(&plainProv).Error; err != nil {
		t.Fatal(err)
	}

	ring := secret.NewStore("v1:old-key,v2:new-key")
	n, err := Run(db, ring)
	if err != nil || n != 3 {
		t.Fatalf("walk = %d err=%v", n, err)
	}

	// Every sealed value now decrypts under the ring and carries v2; the
	// plaintext row is unchanged.
	var freshP, freshPlain storage.Provider
	var freshD storage.DNSProvider
	var freshU storage.User
	if err := db.First(&freshP, prov.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&freshD, dns.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&freshU, user.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&freshPlain, plainProv.ID).Error; err != nil {
		t.Fatal(err)
	}
	for name, ct := range map[string]string{"prov": freshP.AccessKey, "dns": freshD.APIKey, "totp": freshU.TOTPSecret} {
		if !strings.HasPrefix(ct, "v2:") {
			t.Fatalf("%s cipher prefix = %s", name, ct)
		}
	}
	if plain, err := ring.Decrypt(freshP.AccessKey); err != nil || plain != "sk-prov" {
		t.Fatalf("prov decrypt: %q err=%v", plain, err)
	}
	if plain, err := ring.Decrypt(freshD.APIKey); err != nil || plain != "sk-dns" {
		t.Fatalf("dns decrypt: %q err=%v", plain, err)
	}
	if plain, err := ring.Decrypt(freshU.TOTPSecret); err != nil || plain != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("totp decrypt: %q err=%v", plain, err)
	}
	if freshPlain.AccessKey != "raw-key" {
		t.Fatalf("plaintext column touched: %s", freshPlain.AccessKey)
	}

	// Idempotent: the migrated rows are at the current version already.
	if n, err := Run(db, ring); err != nil || n != 0 {
		t.Fatalf("second walk = %d err=%v", n, err)
	}

	// Retired ring: a row still on the old version (the walk missed it, or the
	// key term was retired too early) surfaces as an error and keeps its
	// ciphertext; rows already at v2 are untouched.
	if err := db.Model(&storage.Provider{}).Where("id = ?", prov.ID).
		Update("access_key", seal["prov"]).Error; err != nil {
		t.Fatal(err)
	}
	retired := secret.NewStore("v2:new-key")
	n, err = Run(db, retired)
	if err == nil || !strings.Contains(err.Error(), "no key for cipher version v1") {
		t.Fatalf("retired walk err = %v (n=%d)", err, n)
	}
	var kept storage.Provider
	if err := db.First(&kept, prov.ID).Error; err != nil || kept.AccessKey != seal["prov"] {
		t.Fatalf("retired walk must keep the old cipher: %s err=%v", kept.AccessKey, err)
	}
}

// TestRunNoop covers the no-rotation guards: a plain (single-version) keyring
// and a disabled store are no-ops even with sealed rows present.
func TestRunNoop(t *testing.T) {
	db := testDB(t)
	old := secret.NewStore("old-key")
	if err := db.Create(&storage.Provider{Name: "noop", Type: "vultr",
		AccessKey: mustSeal(t, old, "sk")}).Error; err != nil {
		t.Fatal(err)
	}
	if n, err := Run(db, secret.NewStore("old-key")); err != nil || n != 0 {
		t.Fatalf("plain keyring walk = %d err=%v", n, err)
	}
	if n, err := Run(db, secret.NewStore("")); err != nil || n != 0 {
		t.Fatalf("disabled store walk = %d err=%v", n, err)
	}
}

func mustSeal(t *testing.T, s *secret.Store, plain string) string {
	t.Helper()
	ct, err := s.Encrypt(plain)
	if err != nil {
		t.Fatal(err)
	}
	return ct
}
