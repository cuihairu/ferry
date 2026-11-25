package storage

import (
	"errors"

	"gorm.io/gorm"
)

// GetSetting reads one setting key; ok=false when absent.
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

// SetSetting writes or updates a setting key (upsert by primary key).
func SetSetting(db *gorm.DB, key, value string) error {
	return db.Save(&Setting{Key: key, Value: value}).Error
}
