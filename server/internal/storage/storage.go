// Package storage is the storage layer: GORM dialect selection, connection, and AutoMigrate.
// Three dialects settled: sqlite (default, zero dependency on a small-memory VPS) / postgres / mysql.
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

// Database dialect values.
const (
	DriverSQLite   = "sqlite"
	DriverPostgres = "postgres"
	DriverMySQL    = "mysql"
)

// Open opens the database by dialect and runs all AutoMigrate.
// For sqlite the dsn is a file path (default ferry.db); postgres/mysql take a connection string.
func Open(driver, dsn string) (*gorm.DB, error) {
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Warn), TranslateError: true}
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
		// SQLite is a single file, where multiple connections worsen lock contention; a single connection is capped, with WAL backing read concurrency.
		if sqlDB, err := db.DB(); err == nil {
			sqlDB.SetMaxOpenConns(1)
		}
	}
	if err := AutoMigrate(db); err != nil {
		return nil, err
	}
	return db, nil
}

// AutoMigrate creates all tables and indexes, idempotently re-runnable.
func AutoMigrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&User{},
		&UserAlias{},
		&Subscription{},
		&SubDevice{},
		&Plan{},
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
		&NodeProcStatus{},
		&UserContact{},
		&EntryDomain{},
		&TouchJob{},
		&SaveStat{},
		&Alert{},
		&Setting{},
		&Provider{},
		&ProvisionTemplate{},
		&ProvisionJob{},
		&Recovery{},
		&RecoveryAction{},
		&DNSProvider{},
		&DNSFront{},
		&CertTask{},
		&Notification{},
		&QuotaAction{},
		&Event{},
		&EventDelivery{},
		&Backup{},
		&RecoveryCode{},
		&LoginLog{},
		&AuditLog{},
		&PriceWatch{},
		&PriceSnapshot{},
		&Distributor{},
		&DistributorLedger{},
		&GeoDNSRecord{},
		&Coupon{},
		&Campaign{},
		&NodeTokenRevocation{},
		&EnrollmentToken{},
	)
}

// sqliteDSN completes the SQLite connection parameters (WAL/busy_timeout/foreign keys) and makes sure the directory exists.
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
