package handler

// 管理员首启引导（安全设计 §1）：ferry 此前没有任何生产路径能造出
// IsAdmin 账号（createUser 不收 is_admin），dash 登录面落地后须给部署者
// 一条明确的建号路径——FERRY_ADMIN_PASSWORD 非空且库内无管理员时启动建号；
// 已有管理员则忽略（单管理员口径，不覆盖密码）。

import (
	"strings"

	"github.com/cuihairu/ferry/server/internal/storage"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// EnsureAdmin 在库内无管理员且 password 非空时创建管理员账号，返回是否新建。
// 密码走 bcrypt 默认成本；订阅令牌与业务用户同源 RandomToken。
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

// NormalizeAdminRoles 多级管理员（P2-2）旧号归一：bootstrap 时代的管理员
// 无角色字段，统一补 super（幂等，随启动跑）。子管理员（operator）不受影响。
func NormalizeAdminRoles(db *gorm.DB) error {
	return db.Model(&storage.User{}).
		Where("is_admin = ? AND (admin_role = '' OR admin_role IS NULL)", true).
		Update("admin_role", "super").Error
}
