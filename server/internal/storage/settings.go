package storage

import (
	"errors"

	"gorm.io/gorm"
)

// GetSetting 读取一个设置键；不存在返回 ok=false。
func GetSetting(db *gorm.DB, key string) (string, bool, error) {
	var s Setting
	err := db.First(&s, "key = ?", key).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return s.Value, true, nil
}

// SetSetting 写入或更新一个设置键（按主键 upsert）。
func SetSetting(db *gorm.DB, key, value string) error {
	return db.Save(&Setting{Key: key, Value: value}).Error
}
