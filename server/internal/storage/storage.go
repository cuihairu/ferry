// Package storage 是存储层：GORM 方言选择、连接与 AutoMigrate。
// 三方言定稿：sqlite（默认，小内存 VPS 零依赖）/ postgres / mysql。
package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// 数据库方言取值。
const (
	DriverSQLite   = "sqlite"
	DriverPostgres = "postgres"
	DriverMySQL    = "mysql"
)

// Open 按方言打开数据库并执行全部 AutoMigrate。
// sqlite 的 dsn 为文件路径（默认 ferry.db），postgres/mysql 为连接串。
func Open(driver, dsn string) (*gorm.DB, error) {
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Warn)}
	var dialector gorm.Dialector
	switch driver {
	case "", DriverSQLite:
		path, err := sqliteDSN(dsn)
		if err != nil {
			return nil, err
		}
		dialector = sqlite.Open(path)
	case DriverPostgres:
		if dsn == "" {
			return nil, fmt.Errorf("postgres dsn is required")
		}
		dialector = postgres.Open(dsn)
	case DriverMySQL:
		if dsn == "" {
			return nil, fmt.Errorf("mysql dsn is required")
		}
		dialector = mysql.Open(dsn)
	default:
		return nil, fmt.Errorf("unsupported db driver %q", driver)
	}
	db, err := gorm.Open(dialector, cfg)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", driver, err)
	}
	if driver == "" || driver == DriverSQLite {
		// SQLite 单文件，多连接加剧锁竞争，限单连接由 WAL 兜底读并发。
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.SetMaxOpenConns(1)
		}
	}
	if err := AutoMigrate(db); err != nil {
		return nil, err
	}
	return db, nil
}

// AutoMigrate 建齐全部表与索引，幂等可重复执行。
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&User{},
		&Node{},
		&TrafficLog{},
		&CardBatch{},
		&CardCode{},
		&PaymentOrder{},
		&PaymentTransaction{},
		&Grant{},
		&LandingAssignment{},
		&ProbeReport{},
		&DimensionStatus{},
		&NodeConfig{},
		&NodeTrafficLog{},
		&Alert{},
		&Setting{},
		&Provider{},
		&ProvisionTemplate{},
		&ProvisionJob{},
		&Recovery{},
	)
}

// sqliteDSN 补齐 SQLite 连接参数（WAL/busy_timeout/外键）并保证目录存在。
func sqliteDSN(dsn string) (string, error) {
	if dsn == "" {
		dsn = "ferry.db"
	}
	if dsn == ":memory:" || strings.Contains(dsn, "mode=memory") {
		return dsn, nil
	}
	path := strings.TrimPrefix(dsn, "file:")
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}
	if path != "" && path != ":memory:" {
		if dir := filepath.Dir(path); dir != "." && dir != "" {
			if err := os.MkdirAll(dir, 0o755); err != nil {
				return "", fmt.Errorf("create db dir: %w", err)
			}
		}
	}
	sep := "?"
	if strings.Contains(dsn, "?") {
		sep = "&"
	}
	pragmas := "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	if strings.HasPrefix(dsn, "file:") {
		return dsn + sep + pragmas, nil
	}
	return "file:" + dsn + sep + pragmas, nil
}
