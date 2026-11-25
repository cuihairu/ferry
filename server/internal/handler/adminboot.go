package handler

// Admin first-boot bootstrap (security design §1): ferry previously had no
// production path able to create an IsAdmin account (createUser does not accept
// is_admin), so once the dash login surface landed, deployers needed a clear
// account-creation path -- if FERRY_ADMIN_PASSWORD is non-empty and the database
// has no admin, one is created at startup; if an admin already exists this is
// ignored (single-admin policy, the password is not overwritten).

import (
	"strings"

	"github.com/cuihairu/ferry/server/internal/storage"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// EnsureAdmin creates an admin account when the database has no admin and
// password is non-empty, returning whether one was created. The password uses
// bcrypt's default cost; the subscription token comes from the same RandomToken
// used for regular users.
func EnsureAdmin(db *gorm.DB, username, password string) (bool, error) {
	if strings.TrimSpace(username) == "" || password == "" {
		return false, nil
	}
	var admins int64
	if err := db.Model(&storage.User{}).Where("is_admin = ?", true).Count(&admins).Error; err != nil {
		return false, err
	}
	if admins > 0 {
		return false, nil
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return false, err
	}
	token, err := RandomToken()
	if err != nil {
		return false, err
	}
	u := storage.User{
		Username: strings.TrimSpace(username), SubToken: token, Password: string(hash),
		Enabled: true, IsAdmin: true, AdminRole: "super",
	}
	if err := db.Create(&u).Error; err != nil {
		return false, err
	}
	return true, nil
}

// NormalizeAdminRoles normalizes legacy accounts for multi-tier admins (P2-2):
// admins from the bootstrap era have no role field, so they are uniformly given
// super (idempotent, runs at startup). Sub-admins (operator) are unaffected.
func NormalizeAdminRoles(db *gorm.DB) error {
	return db.Model(&storage.User{}).
		Where("is_admin = ? AND (admin_role = '' OR admin_role IS NULL)", true).
		Update("admin_role", "super").Error
}
