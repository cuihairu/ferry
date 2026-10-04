// Package database 负责打开 SQLite 连接并执行建表迁移。
// 使用 modernc.org/sqlite 纯 Go 实现，无需 CGO，便于交叉编译与低内存部署。
package database

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Open 打开（必要时创建）SQLite 数据库，并完成全部建表迁移。
func Open(path string) (*sql.DB, error) {
	if dir := filepath.Dir(path); dir != "." && dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db dir: %w", err)
		}
	}
	// _pragma 组合：WAL 提升并发读写，busy_timeout 规避锁冲突，foreign_keys 开启外键约束。
	dsn := "file:" + path + "?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	// SQLite 为单文件存储，多连接反而加剧锁竞争，限制为 1 写多读由 WAL 兜底。
	db.SetMaxOpenConns(1)
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

// migrate 按当前版本执行全部建表语句，语句均幂等，可重复执行。
func migrate(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id            INTEGER PRIMARY KEY AUTOINCREMENT,
			username      TEXT    NOT NULL UNIQUE,
			sub_token     TEXT    NOT NULL UNIQUE,
			quota_bytes   INTEGER NOT NULL DEFAULT 0,
			expires_at    DATETIME,
			enabled       INTEGER NOT NULL DEFAULT 1,
			created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS nodes (
			id           INTEGER PRIMARY KEY AUTOINCREMENT,
			name         TEXT    NOT NULL,
			address      TEXT    NOT NULL,
			port         INTEGER NOT NULL,
			protocol     TEXT    NOT NULL,
			config       TEXT    NOT NULL DEFAULT '{}',
			enabled      INTEGER NOT NULL DEFAULT 1,
			created_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
			updated_at   DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS traffic_logs (
			id         INTEGER PRIMARY KEY AUTOINCREMENT,
			user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			node_id    INTEGER REFERENCES nodes(id) ON DELETE SET NULL,
			rx_bytes   INTEGER NOT NULL DEFAULT 0,
			tx_bytes   INTEGER NOT NULL DEFAULT 0,
			recorded_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE INDEX IF NOT EXISTS idx_traffic_logs_user ON traffic_logs(user_id, recorded_at)`,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("migrate: %w", err)
		}
	}
	return nil
}
