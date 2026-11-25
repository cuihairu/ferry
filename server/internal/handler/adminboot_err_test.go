package handler

import (
	"errors"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// TestEnsureAdminDBErrors covers three first-boot bootstrap failures that return
// their error verbatim: the admin count query failing, a password over 72 bytes
// (rejected by bcrypt), and the admin row failing to be inserted.
// Left alone: the crypto/rand error in RandomToken is not forcibly provoked.
func TestEnsureAdminDBErrors(t *testing.T) {
	// Admin count fails -> err.
	_, db := newTestRouterWithDB(t)
	failNthCount("t:ab_count", db, 1)
	if _, err := EnsureAdmin(db, "admin", "first-pw"); err == nil {
		t.Fatal("count error should propagate")
	}

	// Password over 72 bytes: rejected by bcrypt -> err (no account created).
	_, db = newTestRouterWithDB(t)
	if created, err := EnsureAdmin(db, "admin", strings.Repeat("x", 73)); err == nil || created {
		t.Fatalf("long password: created=%v err=%v", created, err)
	}

	// Insert fails -> err.
	_, db = newTestRouterWithDB(t)
	db.Callback().Create().Before("gorm:create").Register("t:ab_create", func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*storage.User); ok {
			tx.AddError(errors.New("db down"))
		}
	})
	if created, err := EnsureAdmin(db, "admin", "first-pw"); err == nil || created {
		t.Fatalf("create error: created=%v err=%v", created, err)
	}
}
