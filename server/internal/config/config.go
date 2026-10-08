// Package config 提供服务运行参数的读取，默认值面向小内存 VPS 场景。
package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/ratelimit"
)

// Config 汇总服务运行所需的全部参数。
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
	// MonitorIntervalSec 是区域/运营商聚合判定周期（秒）。
	MonitorIntervalSec int
	// ReviewIntervalSec 是到期/超限用户停用扫表周期（秒）。
	ReviewIntervalSec int
	// PoolIntervalSec 是入口池摘挂判定周期（秒）。
	PoolIntervalSec int
	// AllocIntervalSec 是落地自动分配判定周期（秒）。
	AllocIntervalSec int
	// CostIntervalSec 是高成本告警检查周期（秒）。
	CostIntervalSec int
	// RedeemLimiter 是兑换接口的 IP 限流选项（PAY-5）。
	RedeemLimiter *ratelimit.Options `json:"-"`
	// AdminSecret 是管理员 JWT 签名秘钥，默认 "ferry-admin-secret"，可通过 FERRY_ADMIN_SECRET 环境变量覆盖。
	AdminSecret string `json:"-"`
	// ApiTokenSecret 是 API Token 签名秘钥，默认 "ferry-api-secret"，可通过 FERRY_API_SECRET 环境变量覆盖。
	ApiTokenSecret string `json:"-"`
	// AdminUser/AdminPassword 是管理员首启引导（安全设计 §1）：库内无管理员时
	// 启动建号（FERRY_ADMIN_USER 缺省 admin），已有管理员则忽略不覆盖。
	AdminUser     string `json:"-"`
	AdminPassword string `json:"-"`
	// SecretKey 是机密加密主密钥（R24：不入库，部署环境变量/文件注入），
	// 未配置时云凭证等机密不可录入。可通过 FERRY_SECRET_KEY 覆盖。
	SecretKey string `json:"-"`
	// TofuBin 是 OpenTofu 可执行文件路径（供给引擎，OS-2）。
	TofuBin string `json:"-"`
	// TofuWorkdir 是供给工作目录根（state 集中存面板侧，每模板一目录）。
	TofuWorkdir string `json:"-"`
	// AgentDownloadBase 是供给节点下载 ferry-agent 的基址（OS-3 cloud-init），
	// 内网可指到自建文件服务。
	AgentDownloadBase string `json:"-"`
	// AgentVersion 是供给节点安装的 agent 版本（对应 release tag vV）。
	AgentVersion string `json:"-"`
	// RecoveryIntervalSec 是封禁恢复流水线状态机的扫表周期（BR-1）。
	RecoveryIntervalSec int
	// RecoveryTemplateID 是恢复 L3 一键开新机用的供给模板 ID（0=不启用 L3）。
	RecoveryTemplateID uint
	// ReplenishIntervalSec 是池空保底开服的扫表周期（BR-3）。
	ReplenishIntervalSec int
	// PoolTemplateID 是池空保底开服用的供给模板 ID（0=不启用，BR-3）。
	PoolTemplateID uint
	// AcmeBin/AcmeHome/AcmeWebroot 是证书编排的外部工具位口径（BR-4）：
	// acme.sh 可执行路径、工作目录（state 集中面板侧）、http-01 webroot。
	AcmeBin     string
	AcmeHome    string
	AcmeWebroot string
	// CertIntervalSec 是证书到期/续期扫表周期（BR-4）。
	CertIntervalSec int
	// NotifyScanIntervalSec 是站内信到期/流量预警扫表周期（NT-2），去重按日，建议小时级。
	NotifyScanIntervalSec int
	// PriceWatchIntervalSec 是价格关注扫描周期（秒，E-32）：从参考价表抓
	// 关注键快照比对降价/到位，建议小时级（价格不分钟级变）。
	PriceWatchIntervalSec int
	// QuotaLinkIntervalSec 是配额联动判定周期（秒，SAVE-6）；降档状态在订阅
	// 出口即时生效，周期只影响触发/释放留痕与通知的及时性。
	QuotaLinkIntervalSec int
	// BotToken 是 TG bot 后端调只读对接面的服务级令牌（触达批 TOUCH-6，
	// FERRY_BOT_TOKEN）；空=bot 对接面禁用（路由 404 防探测）。
	BotToken string
	// TouchDomainsDays 是域名例行邮件周期（天，触达批 TOUCH-4）；0=关。
	TouchDomainsDays int
	// TouchIntervalSec 是例行触达调度周期（秒，TOUCH-4）；月账单有月闸门，
	// 周期只影响发信时机精度。
	TouchIntervalSec int
	// SaveStatsIntervalSec 是节省报表聚合周期（秒，SAVE-7）；报表按日汇总，
	// 周期只影响当日数字的及时性。
	SaveStatsIntervalSec int
	// EventFlushIntervalSec 是事件 outbox 投递周期（秒，HERALD-1）；未配置
	// Herald 通道时事件只落库等投递。
	EventFlushIntervalSec int
	// HeraldURL/HeraldToken 是 Herald 投递基建的接入地址与 Bearer 令牌
	//（HERALD-2）；URL 为空=未接 Herald，事件只落本地 outbox。
	HeraldURL   string
	HeraldToken string
	// HeraldCallbackSecret 是 Herald 回调（§13.5 事件回调，HERALD-5）的
	// HMAC-SHA256 验签密钥，对应 herald 侧 PUT /api/v1/apps/ferry/callback
	// 设置的 32 字节 secret（FERRY_HERALD_CALLBACK_SECRET）；空=不验签
	//（沿用 HERALD-2 的网络边界隔离口径，配置后回执端点强制验签）。
	HeraldCallbackSecret string
	// BackupDir/BackupCron/BackupKeep 是本地周期备份（面板可用性 §2，P1）：
	// 落档目录、调度（cron 表达式或 "daily"/"HH:MM" 别名，缺省每日）、
	// 本地滚动保留份数（含手动落档，超窗连文件带行删）。
	BackupDir  string
	BackupCron string
	BackupKeep int
	// BackupS3* 是备份外发插件位的配置面（FERRY_BACKUP_S3_*）：默认关闭，
	// 显式 ENABLED=1 才视为开启；本批仅预留配置与接口位，未接真实外发
	//（开启时启动日志明示数据范围=备份目录全部落档文件）。
	BackupS3Enabled   bool
	BackupS3Endpoint  string
	BackupS3Bucket    string
	BackupS3AccessKey string
	BackupS3SecretKey string `json:"-"`
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
		MonitorIntervalSec:   envIntOr("FERRY_MONITOR_SEC", 30),
		ReviewIntervalSec:    envIntOr("FERRY_REVIEW_SEC", 60),
		PoolIntervalSec:      envIntOr("FERRY_POOL_SEC", 60),
		AllocIntervalSec:     envIntOr("FERRY_ALLOC_SEC", 300),
		CostIntervalSec:      envIntOr("FERRY_COST_SEC", 3600),
		RedeemLimiter:        &ratelimit.Options{Window: time.Minute, MaxAttempts: 10, FailLimit: 5, Lockout: 15 * time.Minute},
		AdminSecret:          envOr("FERRY_ADMIN_SECRET", ""),
		ApiTokenSecret:       envOr("FERRY_API_SECRET", ""),
		AdminUser:            envOr("FERRY_ADMIN_USER", "admin"),
		AdminPassword:        os.Getenv("FERRY_ADMIN_PASSWORD"),
		SecretKey:            envOr("FERRY_SECRET_KEY", ""),
		TofuBin:              envOr("FERRY_TOFU_BIN", "tofu"),
		TofuWorkdir:          envOr("FERRY_TOFU_DIR", "/var/lib/ferry/tofu"),
		AgentDownloadBase:    envOr("FERRY_AGENT_BASE", "https://github.com/cuihairu/ferry/releases/download"),
		AgentVersion:         envOr("FERRY_AGENT_VERSION", "latest"),
		RecoveryIntervalSec:  envIntOr("FERRY_RECOVERY_SEC", 60),
		RecoveryTemplateID:   uint(envIntOr("FERRY_RECOVERY_TEMPLATE_ID", 0)),
		ReplenishIntervalSec: envIntOr("FERRY_REPLENISH_SEC", 300),
		PoolTemplateID:       uint(envIntOr("FERRY_POOL_TEMPLATE_ID", 0)),
		AcmeBin:              envOr("FERRY_ACME_BIN", "acme.sh"),
		AcmeHome:             envOr("FERRY_ACME_HOME", "/var/lib/ferry/acme"),
		AcmeWebroot:          envOr("FERRY_ACME_WEBROOT", "/var/www/acme"),
		CertIntervalSec:      envIntOr("FERRY_CERT_SEC", 21600),

		NotifyScanIntervalSec: envIntOr("FERRY_NOTIFY_SCAN_SEC", 3600),
		PriceWatchIntervalSec: envIntOr("FERRY_PRICE_WATCH_SEC", 3600),
		QuotaLinkIntervalSec:  envIntOr("FERRY_QUOTA_LINK_SEC", 600),
		SaveStatsIntervalSec:  envIntOr("FERRY_SAVE_STATS_SEC", 600),

		// 本地周期备份（面板可用性 §2）：dir 落档目录，cron 缺省每日，
		// keep 滚动保留份数；S3_* 为外发插件位（默认关闭，本批未接真实外发）。
		BackupDir:             envOr("FERRY_BACKUP_DIR", "backups"),
		BackupCron:            envOr("FERRY_BACKUP_CRON", "daily"),
		BackupKeep:            envIntOr("FERRY_BACKUP_KEEP", 7),
		BackupS3Enabled:       envBoolOr("FERRY_BACKUP_S3_ENABLED", false),
		BackupS3Endpoint:      os.Getenv("FERRY_BACKUP_S3_ENDPOINT"),
		BackupS3Bucket:        os.Getenv("FERRY_BACKUP_S3_BUCKET"),
		BackupS3AccessKey:     os.Getenv("FERRY_BACKUP_S3_ACCESS_KEY"),
		BackupS3SecretKey:     os.Getenv("FERRY_BACKUP_S3_SECRET_KEY"),
		BotToken:              os.Getenv("FERRY_BOT_TOKEN"),
		TouchDomainsDays:      envIntOr("FERRY_TOUCH_DOMAINS_DAYS", 7),
		TouchIntervalSec:      envIntOr("FERRY_TOUCH_SEC", 3600),
		EventFlushIntervalSec: envIntOr("FERRY_EVENT_FLUSH_SEC", 30),
		HeraldURL:             os.Getenv("FERRY_HERALD_URL"),
		HeraldToken:           os.Getenv("FERRY_HERALD_TOKEN"),
		HeraldCallbackSecret:  os.Getenv("FERRY_HERALD_CALLBACK_SECRET"),
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
		MonitorIntervalSec:   30,
		ReviewIntervalSec:    60,
		PoolIntervalSec:      60,
		AllocIntervalSec:     300,
		CostIntervalSec:      3600,
		RedeemLimiter:        &ratelimit.Options{Window: time.Minute, MaxAttempts: 10, FailLimit: 5, Lockout: 15 * time.Minute},
		AdminSecret:          "",
		ApiTokenSecret:       "",
		AdminUser:            "admin",
		AdminPassword:        "",
		SecretKey:            "",
		TofuBin:              "tofu",
		TofuWorkdir:          "",
		AgentDownloadBase:    "https://github.com/cuihairu/ferry/releases/download",
		AgentVersion:         "latest",
		RecoveryIntervalSec:  60,
		RecoveryTemplateID:   0,
		ReplenishIntervalSec: 300,
		PoolTemplateID:       0,
		AcmeBin:              "acme.sh",
		AcmeHome:             "",
		AcmeWebroot:          "",
		CertIntervalSec:      21600,

		NotifyScanIntervalSec: 3600,
		PriceWatchIntervalSec: 3600,
		QuotaLinkIntervalSec:  600,
		EventFlushIntervalSec: 30,
		TouchDomainsDays:      7,
		TouchIntervalSec:      3600,
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

// envBoolOr 读取布尔环境变量（"1"/"true"/"yes"/"on" 为真，不区分大小写）。
func envBoolOr(key string, fallback bool) bool {
	switch strings.ToLower(os.Getenv(key)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off", "":
		return fallback
	}
	return fallback
}
