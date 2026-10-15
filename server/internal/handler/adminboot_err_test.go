package handler

import (
	"errors"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// TestEnsureAdminDBErrors 覆盖首启引导三处故障原样返回错误：管理员计数失败、
// 密码超 72 字节（bcrypt 拒收）、管理员行落库失败。
// 留白：RandomToken 的 crypto/rand 错误不硬凑。
func TestEnsureAdminDBErrors(t *testing.T) {
	// 管理员计数失败 → err。
	_, db := newTestRouterWithDB(t)
	failNthCount("t:ab_count", db, 1)
	if _, err := EnsureAdmin(db, "admin", "first-pw"); err == nil {
		t.Fatal("count error should propagate")
	}

	// 密码超 72 字节：bcrypt 拒收 → err（不建号）。
	_, db = newTestRouterWithDB(t)
	if created, err := EnsureAdmin(db, "admin", strings.Repeat("x", 73)); err == nil || created {
		t.Fatalf("long password: created=%v err=%v", created, err)
	}

	// 落库失败 → err。
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
