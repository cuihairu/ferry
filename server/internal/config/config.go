// Package config reads the service runtime settings; defaults target a small-memory VPS.
package config

import (
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/ratelimit"
)

// OidcConfig is the OIDC/OAuth2 login slot of the admin panel (G7): the
// authorization-code client whose endpoints and credentials come from env vars.
// Off unless FERRY_OIDC_ENABLED is set, so the local password login is untouched
// by default.
type OidcConfig struct {
	Enabled bool
	// ClientID/ClientSecret are the registered application credentials.
	ClientID     string
	ClientSecret string
	// AuthURL/TokenURL/UserInfoURL are the provider endpoints.
	AuthURL     string
	TokenURL    string
	UserInfoURL string
	// RedirectURL is the panel callback; empty defaults to
	// <BaseURL>/admin/oidc/callback.
	RedirectURL string
	// BaseURL is the externally visible panel address (filled from FERRY_BASE_URL),
	// used only to build the default RedirectURL.
	BaseURL string
	// Scope defaults to "openid email profile".
	Scope string
}

// LdapConfig is the LDAP login slot of the admin panel (G7): a per-user simple
// bind against a directory. Off unless FERRY_LDAP_ENABLED is set.
type LdapConfig struct {
	Enabled bool
	// URL is the directory address, e.g. ldaps://ldap.example.com:636.
	URL string
	// BindDN is the user DN template; %s is replaced by the username, so a login
	// attempt binds as that user with the supplied password.
	BindDN string
	// TimeoutSec bounds the dial and bind (default 5): an unreachable directory
	// must not hang a login attempt.
	TimeoutSec int
	// StartTLS upgrades a plain connection with a STARTTLS request.
	StartTLS bool
}

// Config aggregates all parameters needed to run the service.
type Config struct {
	// Addr is the HTTP listen address, e.g. ":8080".
	Addr string
	// DBDriver is the database dialect: sqlite (default) / postgres / mysql.
	DBDriver string
	// DBDSN is the connection string: sqlite takes a file path (default ferry.db), postgres/mysql a connection string.
	DBDSN string
	// BaseURL assembles externally displayed subscription links, e.g. "https://panel.example.com".
	BaseURL string
	// HeartbeatIntervalSec is the agreed agent heartbeat period, delivered with hello_ack.
	HeartbeatIntervalSec int
	// MonitorIntervalSec is the region/ISP aggregation decision period (seconds).
	MonitorIntervalSec int
	// ReviewIntervalSec is the expiry/over-quota user deactivation sweep period (seconds).
	ReviewIntervalSec int
	// PoolIntervalSec is the entry-pool attach/detach decision period (seconds).
	PoolIntervalSec int
	// AllocIntervalSec is the landing auto-allocation decision period (seconds).
	AllocIntervalSec int
	// CostIntervalSec is the high-cost alert check period (seconds).
	CostIntervalSec int
	// RedeemLimiter is the IP rate-limit option of the redeem endpoint (PAY-5).
	RedeemLimiter *ratelimit.Options `json:"-"`
	// AdminSecret is the admin JWT signing secret, default "ferry-admin-secret", overridable via the FERRY_ADMIN_SECRET env var.
	AdminSecret string `json:"-"`
	// ApiTokenSecret is the API token signing secret, default "ferry-api-secret", overridable via the FERRY_API_SECRET env var.
	ApiTokenSecret string `json:"-"`
	// AdminUser/AdminPassword bootstrap the first admin (security design §1): when the DB has no admin, one is
	// created at startup (FERRY_ADMIN_USER defaults to admin); with an existing admin it is ignored, no overwrite.
	AdminUser     string `json:"-"`
	AdminPassword string `json:"-"`
	// SecretKey is the master key encrypting secrets (R24: never stored in the DB, injected via deploy env vars/files);
	// without it, secrets such as cloud credentials cannot be entered. Overridable via FERRY_SECRET_KEY.
	SecretKey string `json:"-"`
	// Env is the runtime environment mode (FERRY_ENV, FERRY-003): production=enforcing, key gate hard-checked
	// (missing/weak key/development default constants refuse to start, see keyinit); empty=development, a missing key
	// falls back to the historical development default with a warning.
	Env string `json:"-"`
	// KeyFile is the signing/encryption key persistence file (FERRY_KEYFILE, FERRY-003): empty defaults to
	// ferry-keys.env next to the sqlite DB file (non-sqlite and unset = no persistence).
	KeyFile string `json:"-"`
	// TofuBin is the OpenTofu executable path (provisioning engine, OS-2).
	TofuBin string `json:"-"`
	// TofuWorkdir is the provisioning working-directory root (state lives centrally on the panel side, one directory per template).
	TofuWorkdir string `json:"-"`
	// AgentDownloadBase is the base URL provisioned nodes download ferry-agent from (OS-3 cloud-init);
	// on an intranet it may point at a self-hosted file server.
	AgentDownloadBase string `json:"-"`
	// AgentVersion is the agent version installed on provisioned nodes (matching release tag vV).
	AgentVersion string `json:"-"`
	// ProvisionCleanupOnFail destroys a failed apply's partial workspace state
	// (F-1658): tofu state is per-template workdir, so the destroy only ever
	// touches instances this template manages, never other nodes. Default on:
	// a failed apply that leaves paid resources running is a cost leak.
	ProvisionCleanupOnFail bool `json:"-"`
	// RecoveryIntervalSec is the block-recovery pipeline state machine sweep period (BR-1).
	RecoveryIntervalSec int
	// RecoveryTemplateID is the provisioning template ID used by recovery L3 one-click relaunch (0 = L3 disabled).
	RecoveryTemplateID uint
	// ReplenishIntervalSec is the empty-pool fallback launch sweep period (BR-3).
	ReplenishIntervalSec int
	// PoolTemplateID is the provisioning template ID used for empty-pool fallback launches (0 = disabled, BR-3).
	PoolTemplateID uint
	// AcmeBin/AcmeHome/AcmeWebroot are the external tool slots of certificate orchestration (BR-4):
	// the acme.sh executable path, working directory (state lives on the panel side), and the http-01 webroot.
	AcmeBin     string
	AcmeHome    string
	AcmeWebroot string
	// CertIntervalSec is the certificate expiry/renewal sweep period (BR-4).
	CertIntervalSec int
	// NotifyScanIntervalSec is the in-site expiry/traffic-warning notification sweep period (NT-2); dedup is per day, hourly is advised.
	NotifyScanIntervalSec int
	// OrderSweepIntervalSec is the pending-order expiry sweep period (F-1303, 30-minute timeout rule).
	OrderSweepIntervalSec int
	// ReconSweepIntervalSec is the payment reconciliation sweep period (F-1308).
	ReconSweepIntervalSec int
	// PriceWatchIntervalSec is the price-watch scan period (seconds, E-32): snapshots of watched keys are taken
	// from the reference price table and compared for price drops / targets; hourly is advised (prices do not move by the minute).
	PriceWatchIntervalSec int
	// QuotaLinkIntervalSec is the quota-linkage decision period (seconds, SAVE-6); the downgrade state takes effect
	// immediately at the subscription outlet, the period only affects the timeliness of trigger/release trails and notifications.
	QuotaLinkIntervalSec int
	// BotToken is the service-level token the TG bot backend uses against the read-only integration face (touch batch TOUCH-6,
	// FERRY_BOT_TOKEN); empty = the bot integration face is disabled (routes 404, anti-probing).
	BotToken string
	// TouchDomainsDays is the domain routine-email period (days, touch batch TOUCH-4); 0 = off.
	TouchDomainsDays int
	// TouchIntervalSec is the routine-touch scheduling period (seconds, TOUCH-4); monthly bills have a monthly gate,
	// the period only affects the precision of send timing.
	TouchIntervalSec int
	// SaveStatsIntervalSec is the savings-report aggregation period (seconds, SAVE-7); reports summarize by day,
	// the period only affects the timeliness of the current day's numbers.
	SaveStatsIntervalSec int
	// CloudSyncIntervalSec is the cloud resource sync period (seconds, F-0347); one hour by default,
	// each enabled provider account is pulled by its registered adapter (if any).
	CloudSyncIntervalSec int
	// CloudOpsIntervalSec is the cloud operation reconciliation period (seconds, F-0373);
	// Unknown tasks are settled by querying the vendor instead of re-running.
	CloudOpsIntervalSec int
	// EventFlushIntervalSec is the event outbox delivery period (seconds, HERALD-1); without a configured
	// Herald channel, events are only stored awaiting delivery.
	EventFlushIntervalSec int
	// HeraldURL/HeraldToken are the access address and bearer token of the Herald delivery infrastructure
	// (HERALD-2); an empty URL = Herald not connected, events only land in the local outbox.
	HeraldURL   string
	HeraldToken string
	// HeraldCallbackSecret is the HMAC-SHA256 verification secret of the Herald callback (§13.5 event callback, HERALD-5),
	// matching the 32-byte secret configured on the herald side at PUT /api/v1/apps/ferry/callback (FERRY_HERALD_CALLBACK_SECRET);
	// empty = the receipt endpoint rejects all callbacks (P2 requires verification by default, so forged receipts are not accepted) -
	// connecting Herald requires configuring it and keeping it identical to the herald side, otherwise receipt trails and touch
	// linkage silently fail.
	HeraldCallbackSecret string
	// SupportURL/SupportServiceKey are the integration face of the external customer-service service (servify):
	// the base address of the panel's support entry and the hosting backend's service API key (X-API-Key).
	// An empty URL = support not connected, /api/panel/support always reports enabled=false, and the panel mounts
	// no component (staying zero-dependency); the key is used only server-side, browsers only hold a short-lived visitor token.
	SupportURL        string
	SupportServiceKey string
	// BackupDir/BackupCron/BackupKeep are local periodic backups (panel availability §2, P1): the archive
	// directory, the schedule (a cron expression or the "daily"/"HH:MM" alias, defaulting to daily), and the local
	// rolling retention count (manual archives included; out-of-window rows are deleted together with their files).
	BackupDir  string
	BackupCron string
	BackupKeep int

	// AuditRetentionDays is the operation audit (AU-3) retention in days: out-of-window rows are deleted by the sweep loop,
	// 0 = permanent retention (no sweep loop started).
	AuditRetentionDays int
	// BackupS3* is the config face of the backup outbound slot (FERRY_BACKUP_S3_*): off by default,
	// only an explicit ENABLED=1 turns it on and injects the S3-compatible endpoint outbound (stdlib SigV4 PUT);
	// while off, behavior is unchanged from the disabled state (never turned on proactively, env-example defaults untouched).
	BackupS3Enabled   bool
	BackupS3Endpoint  string
	BackupS3Bucket    string
	BackupS3AccessKey string
	BackupS3SecretKey string `json:"-"`

	// Standby* is panel primary/standby sync (P2-1): the primary panel only needs FERRY_STANDBY_TOKEN to
	// expose the /api/standby/snapshot endpoint; a standby instance configures MasterURL pointing at the primary
	// plus the same token, and periodically pulls the encrypted snapshot to atomically replace the local DB. Empty token =
	// the whole sync face is off (the primary endpoint 403s, standby does not start), zero change by default.
	// Oidc/Ldap are the external admin login slots (G7); both are off by default.
	Oidc             OidcConfig `json:"-"`
	Ldap             LdapConfig `json:"-"`
	StandbyMasterURL string
	StandbyToken     string `json:"-"`
	StandbySec       int

	// TrustedProxies is the trusted reverse-proxy CIDR whitelist (CIDRs or single IPs, comma-separated
	// FERRY_TRUSTED_PROXIES): only X-Forwarded-For/X-Real-IP from these sources participate in the ClientIP
	// decision. Empty (default) = trust no proxy, ClientIP always takes the direct address - so the IP basis of
	// login brute-force lockout / subscription locking / API rate limiting / audit logs cannot be forged via request headers.
	// When deployed behind a Caddy/Nginx reverse proxy, configure its actual CIDRs explicitly (e.g. the docker network).
	TrustedProxies []string
}

// Load reads configuration from environment variables; unset items fall back to defaults.
// FERRY_DB still works as a compatibility alias for the sqlite file path.
func Load() Config {
	return Config{
		Addr:                   envOr("FERRY_ADDR", ":8080"),
		DBDriver:               envOr("FERRY_DB_DRIVER", "sqlite"),
		DBDSN:                  envOr("FERRY_DB_DSN", envOr("FERRY_DB", "ferry.db")),
		BaseURL:                envOr("FERRY_BASE_URL", "http://localhost:8080"),
		HeartbeatIntervalSec:   envIntOr("FERRY_HEARTBEAT_SEC", 30),
		MonitorIntervalSec:     envIntOr("FERRY_MONITOR_SEC", 30),
		ReviewIntervalSec:      envIntOr("FERRY_REVIEW_SEC", 60),
		PoolIntervalSec:        envIntOr("FERRY_POOL_SEC", 60),
		AllocIntervalSec:       envIntOr("FERRY_ALLOC_SEC", 300),
		CostIntervalSec:        envIntOr("FERRY_COST_SEC", 3600),
		RedeemLimiter:          &ratelimit.Options{Window: time.Minute, MaxAttempts: 10, FailLimit: 5, Lockout: 15 * time.Minute},
		AdminSecret:            envOr("FERRY_ADMIN_SECRET", ""),
		ApiTokenSecret:         envOr("FERRY_API_SECRET", ""),
		AdminUser:              envOr("FERRY_ADMIN_USER", "admin"),
		AdminPassword:          os.Getenv("FERRY_ADMIN_PASSWORD"),
		SecretKey:              envOr("FERRY_SECRET_KEY", ""),
		Env:                    os.Getenv("FERRY_ENV"),
		KeyFile:                envOr("FERRY_KEYFILE", ""),
		TofuBin:                envOr("FERRY_TOFU_BIN", "tofu"),
		TofuWorkdir:            envOr("FERRY_TOFU_DIR", "/var/lib/ferry/tofu"),
		AgentDownloadBase:      envOr("FERRY_AGENT_BASE", "https://github.com/cuihairu/ferry/releases/download"),
		AgentVersion:           envOr("FERRY_AGENT_VERSION", "latest"),
		ProvisionCleanupOnFail: envBoolOr("FERRY_TOFU_CLEANUP_ON_FAIL", true),
		RecoveryIntervalSec:    envIntOr("FERRY_RECOVERY_SEC", 60),
		RecoveryTemplateID:     uint(envIntOr("FERRY_RECOVERY_TEMPLATE_ID", 0)),
		ReplenishIntervalSec:   envIntOr("FERRY_REPLENISH_SEC", 300),
		PoolTemplateID:         uint(envIntOr("FERRY_POOL_TEMPLATE_ID", 0)),
		AcmeBin:                envOr("FERRY_ACME_BIN", "acme.sh"),
		AcmeHome:               envOr("FERRY_ACME_HOME", "/var/lib/ferry/acme"),
		AcmeWebroot:            envOr("FERRY_ACME_WEBROOT", "/var/www/acme"),
		CertIntervalSec:        envIntOr("FERRY_CERT_SEC", 21600),

		NotifyScanIntervalSec: envIntOr("FERRY_NOTIFY_SCAN_SEC", 3600),
		OrderSweepIntervalSec: envIntOr("FERRY_ORDER_SWEEP_SEC", 60),
		ReconSweepIntervalSec: envIntOr("FERRY_RECON_SEC", 3600),
		PriceWatchIntervalSec: envIntOr("FERRY_PRICE_WATCH_SEC", 3600),
		QuotaLinkIntervalSec:  envIntOr("FERRY_QUOTA_LINK_SEC", 600),
		SaveStatsIntervalSec:  envIntOr("FERRY_SAVE_STATS_SEC", 600),
		CloudSyncIntervalSec:  envIntOr("FERRY_CLOUD_SYNC_SEC", 3600),
		CloudOpsIntervalSec:   envIntOr("FERRY_CLOUD_OPS_SEC", 60),

		// Local periodic backups (panel availability §2): dir is the archive directory, cron defaults to daily,
		// keep is the rolling retention count; S3_* is the outbound slot (off by default, an explicit toggle wires in the S3 endpoint).
		BackupDir:          envOr("FERRY_BACKUP_DIR", "backups"),
		BackupCron:         envOr("FERRY_BACKUP_CRON", "daily"),
		BackupKeep:         envIntOr("FERRY_BACKUP_KEEP", 7),
		AuditRetentionDays: envIntOr("FERRY_AUDIT_RETENTION_DAYS", 90),
		BackupS3Enabled:    envBoolOr("FERRY_BACKUP_S3_ENABLED", false),
		Oidc: OidcConfig{
			Enabled:      envBoolOr("FERRY_OIDC_ENABLED", false),
			ClientID:     envOr("FERRY_OIDC_CLIENT_ID", ""),
			ClientSecret: envOr("FERRY_OIDC_CLIENT_SECRET", ""),
			AuthURL:      envOr("FERRY_OIDC_AUTH_URL", ""),
			TokenURL:     envOr("FERRY_OIDC_TOKEN_URL", ""),
			UserInfoURL:  envOr("FERRY_OIDC_USERINFO_URL", ""),
			RedirectURL:  envOr("FERRY_OIDC_REDIRECT_URL", ""),
			Scope:        envOr("FERRY_OIDC_SCOPE", "openid email profile"),
			BaseURL:      envOr("FERRY_BASE_URL", "http://localhost:8080"),
		},
		Ldap: LdapConfig{
			Enabled:    envBoolOr("FERRY_LDAP_ENABLED", false),
			URL:        envOr("FERRY_LDAP_URL", ""),
			BindDN:     envOr("FERRY_LDAP_BIND_DN", ""),
			TimeoutSec: envIntOr("FERRY_LDAP_TIMEOUT_SEC", 5),
			StartTLS:   envBoolOr("FERRY_LDAP_START_TLS", false),
		},
		StandbyMasterURL:      envOr("FERRY_STANDBY_MASTER_URL", ""),
		StandbyToken:          envOr("FERRY_STANDBY_TOKEN", ""),
		StandbySec:            envIntOr("FERRY_STANDBY_SEC", 600),
		TrustedProxies:        csvEnv("FERRY_TRUSTED_PROXIES"),
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
		SupportURL:            os.Getenv("FERRY_SUPPORT_URL"),
		SupportServiceKey:     os.Getenv("FERRY_SUPPORT_SERVICE_KEY"),
	}
}

// Default returns the default configuration for tests and local development.
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
		OrderSweepIntervalSec: 60,
		ReconSweepIntervalSec: 3600,
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

// csvEnv reads a comma-separated env var; an empty string yields nil (unconfigured), empty items are skipped.
func csvEnv(key string) []string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return nil
	}
	out := []string{}
	for _, part := range strings.Split(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// envBoolOr reads a boolean env var ("1"/"true"/"yes"/"on" are true, case-insensitive).
func envBoolOr(key string, fallback bool) bool {
	switch strings.ToLower(os.Getenv(key)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off", "":
		return fallback
	}
	return fallback
}
