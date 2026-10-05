// Package config 提供服务运行参数的读取，默认值面向小内存 VPS 场景。
package config

import (
	"os"
	"strconv"
)

// Config 汇总服务启动所需的全部参数。
type Config struct {
	// Addr 是 HTTP 监听地址，例如 ":8080"。
	Addr string
	// DBDriver 是数据库方言：sqlite（默认）/postgres/mysql。
	DBDriver string
	// DBDSN 是连接串：sqlite 为文件路径（默认 ferry.db），postgres/mysql 为连接串。
	DBDSN string
	// BaseURL 用于拼装对外展示的订阅链接，例如 "https://panel.example.com"。
	BaseURL string
	// HeartbeatIntervalSec 是约定的 agent 心跳周期，随 hello_ack 下发。
	HeartbeatIntervalSec int
}

// Load 从环境变量读取配置，未设置的项回退到默认值。
// FERRY_DB 仍可用作 sqlite 文件路径的兼容别名。
func Load() Config {
	return Config{
		Addr:                 envOr("FERRY_ADDR", ":8080"),
		DBDriver:             envOr("FERRY_DB_DRIVER", "sqlite"),
		DBDSN:                envOr("FERRY_DB_DSN", envOr("FERRY_DB", "ferry.db")),
		BaseURL:              envOr("FERRY_BASE_URL", "http://localhost:8080"),
		HeartbeatIntervalSec: envIntOr("FERRY_HEARTBEAT_SEC", 30),
	}
}

// Default 返回测试与本地开发用的默认配置。
func Default() Config {
	return Config{
		Addr:                 ":8080",
		DBDriver:             "sqlite",
		DBDSN:                "ferry.db",
		BaseURL:              "http://localhost:8080",
		HeartbeatIntervalSec: 30,
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envIntOr(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}
