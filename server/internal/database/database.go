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
	if err := ensureColumns(db, "nodes", map[string]string{
		// 节点令牌供 agent 出站连接认证；SQLite 的 ALTER ADD COLUMN 不支持 UNIQUE，用唯一索引补齐。
		"token":     "TEXT NOT NULL DEFAULT ''",
		"last_seen": "DATETIME",
		"status":    "TEXT NOT NULL DEFAULT 'unknown'",
	}); err != nil {
		return err
	}
	if _, err := db.Exec(`CREATE UNIQUE INDEX IF NOT EXISTS idx_nodes_token ON nodes(token)`); err != nil {
		return fmt.Errorf("migrate nodes token index: %w", err)
	}
	return nil
}

// ensureColumns 为已存在的表补充缺失列，缺一条加一条。
func ensureColumns(db *sql.DB, table string, cols map[string]string) error {
	rows, err := db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return fmt.Errorf("table info %s: %w", table, err)
	}
	defer rows.Close()
	existing := map[string]bool{}
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull int
		var dflt any
		var pk int
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dflt, &pk); err != nil {
			return err
		}
		existing[name] = true
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for name, def := range cols {
		if existing[name] {
			continue
		}
		if _, err := db.Exec(fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", table, name, def)); err != nil {
			return fmt.Errorf("add column %s.%s: %w", table, name, err)
		}
	}
	return nil
}
