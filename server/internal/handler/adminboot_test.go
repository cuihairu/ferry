package handler

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

// TestEnsureAdmin covers three first-boot bootstrap sides: with no admin and a
// configured password it creates the account (IsAdmin, bcrypt-verifiable); with an
// existing admin it is ignored (a different name still creates nothing, single-
// admin policy); an empty password does not bootstrap.
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

	// Existing admin: neither the same nor another name, nor a changed password, creates one.
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

	// An empty password does not bootstrap.
	if created, err := EnsureAdmin(db, "ghost", ""); err != nil || created {
		t.Fatalf("empty password should no-op: created=%v err=%v", created, err)
	}
}
