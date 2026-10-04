// Package config 提供服务运行参数的读取，默认值面向小内存 VPS 场景。
package config

import "os"

// Config 汇总服务启动所需的全部参数。
type Config struct {
	// Addr 是 HTTP 监听地址，例如 ":8080"。
	Addr string
	// DBPath 是 SQLite 数据库文件路径。
	DBPath string
	// BaseURL 用于拼装对外展示的订阅链接，例如 "https://panel.example.com"。
	BaseURL string
}

// Load 从环境变量读取配置，未设置的项回退到默认值。
func Load() Config {
	return Config{
		Addr:    envOr("FERRY_ADDR", ":8080"),
		DBPath:  envOr("FERRY_DB", "ferry.db"),
		BaseURL: envOr("FERRY_BASE_URL", "http://localhost:8080"),
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
