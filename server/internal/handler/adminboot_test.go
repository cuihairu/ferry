package handler

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// TestEnsureAdmin 覆盖首启引导三面：无管理员且配置密码时建号（IsAdmin、
// bcrypt 可验）；已有管理员时忽略（换名也不建，单管理员口径）；空密码不引导。
func TestEnsureAdmin(t *testing.T) {
	_, db := newTestRouterWithDB(t)

	created, err := EnsureAdmin(db, "admin", "first-pw")
	if err != nil || !created {
		t.Fatalf("first ensure: created=%v err=%v", created, err)
	}
	var u struct {
		Username string
		Password string
		IsAdmin  bool
		Enabled  bool
	}
	if err := db.Table("users").First(&u, "username = ?", "admin").Error; err != nil {
		t.Fatalf("query admin: %v", err)
	}
	if !u.IsAdmin || !u.Enabled || u.Password == "" {
		t.Fatalf("admin row = %+v", u)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.Password), []byte("first-pw")); err != nil {
		t.Fatalf("bcrypt verify: %v", err)
	}

	// 已有管理员：同名/异名、换密码都不再建。
	for _, tc := range [][2]string{{"admin", "second-pw"}, {"ops", "other-pw"}} {
		created, err := EnsureAdmin(db, tc[0], tc[1])
		if err != nil || created {
			t.Fatalf("ensure %q should skip: created=%v err=%v", tc[0], created, err)
		}
	}
	var n int64
	db.Table("users").Count(&n)
	if n != 1 {
		t.Fatalf("users = %d, want 1", n)
	}

	// 空密码不引导。
	if created, err := EnsureAdmin(db, "ghost", ""); err != nil || created {
		t.Fatalf("empty password should no-op: created=%v err=%v", created, err)
	}
}
