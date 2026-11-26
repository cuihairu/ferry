// The ferry command is the service entrypoint: after wiring config, database and HTTP routes it serves externally.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	_ "github.com/cuihairu/ferry/payments/alipay" // PAY-10: placeholder channel (no enabled copy)
	_ "github.com/cuihairu/ferry/payments/epusdt" // PAY-8: init self-registers the payment channel
	_ "github.com/cuihairu/ferry/payments/wechat" // PAY-10: placeholder channel (no enabled copy)
	"github.com/cuihairu/ferry/server/internal/alloc"
	"github.com/cuihairu/ferry/server/internal/audit"
	"github.com/cuihairu/ferry/server/internal/backup"
	"github.com/cuihairu/ferry/server/internal/cert"
	"github.com/cuihairu/ferry/server/internal/cloudlink"
	"github.com/cuihairu/ferry/server/internal/cloudsync"
	"github.com/cuihairu/ferry/server/internal/config"
	"github.com/cuihairu/ferry/server/internal/cost"
	"github.com/cuihairu/ferry/server/internal/geodns"
	"github.com/cuihairu/ferry/server/internal/handler"
	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/keyinit"
	"github.com/cuihairu/ferry/server/internal/monitor"
	"github.com/cuihairu/ferry/server/internal/notifyscan"
	"github.com/cuihairu/ferry/server/internal/oper"
	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/provision"
	"github.com/cuihairu/ferry/server/internal/quota"
	"github.com/cuihairu/ferry/server/internal/recovery"
	"github.com/cuihairu/ferry/server/internal/review"
	"github.com/cuihairu/ferry/server/internal/ringlog"
	"github.com/cuihairu/ferry/server/internal/save"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/standby"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/cuihairu/ferry/server/internal/toucher"
	"github.com/cuihairu/ferry/server/internal/xray"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func main() {
	// Backup-decrypt subcommand (panel availability §3 restore ②, called by deploy/restore.sh): the .enc
	// file is the compact binary of AES-256-GCM under a master-key-derived key, and openssl enc has no GCM support,
	// so restoration borrows this binary's own secret package to restore the plaintext; without the subcommand the service starts as usual.
	if len(os.Args) > 1 && os.Args[1] == "backup-decrypt" {
		os.Exit(backupDecrypt(os.Args[2:]))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, config.Load()); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("ferry: %v", err)
	}
}

// run wires up and serves one panel instance: opens the DB, bootstraps the admin, mounts all background loops, then
// listens until ctx is cancelled (graceful shutdown). Returned errors are handed to main for a uniform exit; context.
// Canceled means a normal shutdown.
func run(ctx context.Context, cfg config.Config) error {
	// Runtime logs (P1-11): std log and gin output are mirrored into the ring buffer for the admin UI to view.
	ring := ringlog.Default()
	log.SetOutput(io.MultiWriter(os.Stderr, ring))
	gin.DefaultWriter = io.MultiWriter(os.Stdout, ring)
	gin.DefaultErrorWriter = io.MultiWriter(os.Stderr, ring)

	// Key gate (FERRY-003): signing/encryption keys resolve as "explicit config > key file > generate and
	// persist"; in production (FERRY_ENV=production) a missing key with nowhere to persist, a weak key, or a
	// dev default constant refuses startup. Logs name keys but never print values.
	resolved, keys, err := keyinit.Resolve(cfg)
	if err != nil {
		return fmt.Errorf("key init: %w", err)
	}
	cfg = resolved
	for _, w := range keys.Warnings {
		log.Printf("keyinit: %s", w)
	}
	if len(keys.Generated) > 0 {
		log.Printf("keyinit: generated %s -> %s", strings.Join(keys.Generated, ","), keys.KeyFile)
	}

	db, err := storage.Open(cfg.DBDriver, cfg.DBDSN)
	if err != nil {
		return fmt.Errorf("init database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("db handle: %w", err)
	}
	defer sqlDB.Close()

	// After restart, online status is reset to offline, awaiting agent reconnects to refresh it.
	if err := db.Exec(`UPDATE nodes SET status='offline'`).Error; err != nil {
		return fmt.Errorf("reset node status: %w", err)
	}

	// Admin first-boot bootstrap (security design §1): when FERRY_ADMIN_PASSWORD is non-empty and the DB has no admin,
	// create one as FERRY_ADMIN_USER (default admin); if an admin already exists, ignore it and do not overwrite the password.
	if created, err := handler.EnsureAdmin(db, cfg.AdminUser, cfg.AdminPassword); err != nil {
		return fmt.Errorf("bootstrap admin: %w", err)
	} else if created {
		log.Printf("bootstrap admin: created admin user %q", cfg.AdminUser)
	}
	// Multi-level admins (P2-2) legacy normalization: bootstrap-era admins get the super role (idempotent).
	if err := handler.NormalizeAdminRoles(db); err != nil {
		return fmt.Errorf("normalize admin roles: %w", err)
	}

	// The master key was freshly generated this run but the DB already holds encrypted secrets (cloud credentials etc.):
	// they were most likely written under an earlier env key that was then removed - prompt to restore that key instead of letting decrypt errors surface only at use time.
	if slices.Contains(keys.Generated, "FERRY_SECRET_KEY") {
		var n int64
		if err := db.Model(&storage.Provider{}).Count(&n).Error; err == nil && n > 0 {
			log.Printf("keyinit: FERRY_SECRET_KEY was auto-generated but %d provider row(s) exist; "+
				"if their credentials were written under an earlier master key, restore that key via FERRY_SECRET_KEY", n)
		}
	}

	// Until the Xray core integration is enabled, use a no-op implementation so the interface stays stable.
	_ = xray.NoopHandler{}

	// Region/ISP aggregate determination: periodically aggregates probe conclusions, merging alerts on state transitions.
	go monitor.Run(ctx, db, time.Duration(cfg.MonitorIntervalSec)*time.Second, nil)

	// Node overload warning (F-1067): evaluates the unified bandwidth
	// utilization per node and keeps one active overload alert per node.
	go monitor.RunOverload(ctx, db, time.Duration(cfg.MonitorIntervalSec)*time.Second, nil)

	// Entry pool auto removal/restore (E-16): consecutive sick removals and restores reset, the subscription entry pool takes effect immediately;
	// pool state transitions trigger per-region reconciliation via the OnChange hook (E-27 §C.8: event-driven, no
	// polling built - removing/restoring the representative entry syncs the {region-slug}.{front-domain} A record).
	pool.OnChange = func(db *gorm.DB) {
		s := &geodns.Syncer{DB: db, Store: secret.NewStore(cfg.SecretKey)}
		if _, err := s.Sync(ctx); err != nil {
			log.Printf("geodns sync: %v", err)
		}
	}
	go pool.Loop(ctx, db, time.Duration(cfg.PoolIntervalSec)*time.Second, nil)

	// Router and config pusher (E-16b): automatic line switching re-points landings through the same config.push pipeline.
	r, pusher := handler.NewRouter(db, cfg)

	// Landing auto-assignment (E-21): weighted least connections, three configurable strategy tiers, switches audited and notified;
	// switching means re-pointing (E-16b): the change list pushes a per-entry relay landing override into the spec.
	go alloc.Loop(ctx, db, time.Duration(cfg.AllocIntervalSec)*time.Second, nil, pusher.OnSwitch)

	// High-cost alerting (E-23): fires once when a traffic node's monthly spend exceeds the threshold, auto-resolves when it falls back.
	go cost.Loop(ctx, db, time.Duration(cfg.CostIntervalSec)*time.Second, nil)

	// Ban-recovery pipeline (BR-1): a removal that never recovers is judged banned, advancing tier by tier L1→L2→L3,
	// each tier timing out into the next; L1 switches domain fronting to a backup IP (BR-2, fails forward if unconfigured),
	// L2 fills in with a standby machine (BR-3), L3 opens a new machine in one shot off the supply template (0 = disabled).
	{
		reg := recovery.Registry{}
		secrets := secret.NewStore(cfg.SecretKey)
		// L1 (BR-2): rotate the front-domain A record onto a backup IP, switching back to normal once probes recover.
		dnsAct := &recovery.DNSAction{DB: db, Store: secrets}
		reg[1] = dnsAct
		// L2 (BR-3): take one online standby entry from the standby list into the pool, subscription switching takes effect immediately.
		reg[2] = &recovery.StandbyAction{DB: db}
		opts := recovery.Options{
			OnDone: func(nodeID uint, nodeName string) {
				if err := dnsAct.Restore(context.Background()); err != nil {
					log.Printf("recovery dns restore: node=%d %s: %v", nodeID, nodeName, err)
				}
			},
		}
		if cfg.RecoveryTemplateID > 0 {
			reg[3] = &recovery.InstanceAction{
				Manager:    provision.Shared(db, cfg.TofuBin, cfg.TofuWorkdir, nil),
				Store:      secrets,
				TemplateID: cfg.RecoveryTemplateID,
				Launch: func(name string) provision.LaunchParams {
					return provision.LaunchParams{
						InstanceName: name,
						PanelWSURL:   provision.PanelWSURL(cfg.BaseURL),
						AgentBase:    cfg.AgentDownloadBase, AgentVersion: cfg.AgentVersion,
						NewToken: handler.RandomToken,
					}
				},
			}
		}
		go recovery.Loop(ctx, db, time.Duration(cfg.RecoveryIntervalSec)*time.Second, opts, reg, nil)
	}

	// Empty-pool fallback (BR-3): when the entry pool is entirely empty and no supply is in flight, auto-open a new machine from the template;
	// launched machines join the pool automatically via the OS-4 pipeline. Template 0 = disabled.
	{
		repl := &provision.Replenisher{
			Manager:    provision.Shared(db, cfg.TofuBin, cfg.TofuWorkdir, nil),
			Store:      secret.NewStore(cfg.SecretKey),
			TemplateID: cfg.PoolTemplateID,
			Launch: func(name string) provision.LaunchParams {
				return provision.LaunchParams{
					InstanceName: name,
					PanelWSURL:   provision.PanelWSURL(cfg.BaseURL),
					AgentBase:    cfg.AgentDownloadBase, AgentVersion: cfg.AgentVersion,
					NewToken: handler.RandomToken,
				}
			},
		}
		go provision.ReplenishLoop(ctx, repl, time.Duration(cfg.ReplenishIntervalSec)*time.Second, nil)
	}

	// Certificate orchestration (BR-4): drives pending/expiring/failed-with-backoff items one by one, issuance runs the acme.sh
	// tool slot (DNS-01 reuses the BR-2 credential channel), auto-renews 30 days before expiry, alerts on failure.
	{
		cm := &cert.Manager{Bin: cfg.AcmeBin, Home: cfg.AcmeHome, Webroot: cfg.AcmeWebroot}
		go cert.Loop(ctx, db, cm, secret.NewStore(cfg.SecretKey), time.Duration(cfg.CertIntervalSec)*time.Second, nil)
	}

	// Scan and disable expired / over-limit users.
	go review.Run(ctx, db, time.Duration(cfg.ReviewIntervalSec)*time.Second)

	// In-app message auto-trigger (NT-2): expiry reminders and traffic warnings scan tables, deduped per day;
	// the grant-credit event trigger lives in the applyGrant transaction, announcement fan-out lives in the admin API.
	go notifyscan.Loop(ctx, db, time.Duration(cfg.NotifyScanIntervalSec)*time.Second, nil)

	// Order timeout sweep (F-1303): pending orders past the 30-minute rule flip to expired;
	// settle-side already refuses non-pending orders, so the two ends close the same rule.
	go handler.OrderLoop(ctx, db, time.Duration(cfg.OrderSweepIntervalSec)*time.Second, nil)

	// Payment reconciliation task (F-1308): counts the ledger fault classes on schedule
	// and lands one deduped admin alert per day when anything is off.
	go handler.ReconLoop(ctx, db, time.Duration(cfg.ReconSweepIntervalSec)*time.Second, nil)

	// Quota linkage (SAVE-6): subscriptions of users over the traffic/cost threshold auto-downgrade (only low-cost tier
	// entries are emitted), trigger/release audit and in-app messages are scan-driven, the subscription egress reads the audit trail instantly.
	go quota.LinkLoop(ctx, db, time.Duration(cfg.QuotaLinkIntervalSec)*time.Second, nil)
	// Price-watch scan (E-32): price drops and targets reached go via Herald price_alert, or land in the local outbox when Herald is unconfigured.
	go cost.WatchLoop(ctx, db, time.Duration(cfg.PriceWatchIntervalSec)*time.Second, nil)

	// Savings report aggregation (SAVE-7): traffic rows accumulate daily into save_stats, report and cost dashboard
	// are shown on the same page; converted cost is computed on the fly API-side from each node's traffic unit price.
	go save.Loop(ctx, db, time.Duration(cfg.SaveStatsIntervalSec)*time.Second, nil)
	// Routine outreach (TOUCH-4): monthly bill plus domain routine emails, jobs land in touch_jobs and are delivered via Herald.
	go toucher.Loop(ctx, db, toucher.Config{
		DomainsDays: cfg.TouchDomainsDays, BaseURL: strings.TrimRight(cfg.BaseURL, "/"),
	}, time.Duration(cfg.TouchIntervalSec)*time.Second, nil)

	// Cloud resource sync (F-0347): each enabled provider account is pulled through
	// its cloudprov adapter (region scope and tag filter applied), results are
	// soft-synced into CloudResource rows and every run is logged.
	go cloudsync.Loop(ctx, db, secret.NewStore(cfg.SecretKey),
		time.Duration(cfg.CloudSyncIntervalSec)*time.Second, "scheduled")

	// Bound resources are refreshed on a controlled cadence (F-0398): the vendor
	// is queried before any Node scheduling dimension changes; a vendor outage
	// never changes the Node state.
	go cloudlink.Loop(ctx, db, secret.NewStore(cfg.SecretKey),
		time.Duration(cfg.CloudOpsIntervalSec)*time.Second, nil)

	// Cloud operation reconciliation (F-0373): tasks left Unknown by a vendor
	// timeout are settled by querying the vendor state; no blind re-execution.
	opService := oper.New(db, secret.NewStore(cfg.SecretKey))
	go func() {
		interval := time.Duration(cfg.CloudOpsIntervalSec) * time.Second
		if interval <= 0 {
			interval = time.Minute
		}
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := opService.Reconcile(ctx, time.Now()); err != nil {
					log.Printf("cloudops reconcile: %v", err)
				}
			}
		}
	}()

	// Event outbox (HERALD-1/2): events persist and return immediately, the delivery loop retries asynchronously;
	// setting FERRY_HERALD_URL hooks up the Herald delivery leg, when unset events only land in the DB, visible on the dash.
	var eventSender herald.Sender
	if cfg.HeraldURL != "" {
		eventSender = herald.HTTPSender(cfg.HeraldURL, cfg.HeraldToken)
	}
	go herald.Loop(ctx, db, time.Duration(cfg.EventFlushIntervalSec)*time.Second, eventSender, nil)

	// Local periodic backup (panel availability §2, P1): VACUUM INTO online snapshot packed and encrypted into
	// FERRY_BACKUP_DIR (master-key-derived key, the master key never enters the backup archive), keeping
	// FERRY_BACKUP_KEEP copies rolling, failures alert via Herald backup_failed; the offload slot
	// S3_* is off by default with zero change, only an explicit ENABLED=1 injects S3 offload (SigV4 PUT,
	// success sets uploaded=1, failure alerts and keeps the local archive).
	backupOpts := backup.Options{
		Dir: cfg.BackupDir, Cron: cfg.BackupCron, Keep: cfg.BackupKeep,
		S3Enabled: cfg.BackupS3Enabled,
	}
	if cfg.BackupS3Enabled {
		up, err := backup.NewS3Uploader(cfg.BackupS3Endpoint, cfg.BackupS3Bucket,
			cfg.BackupS3AccessKey, cfg.BackupS3SecretKey, nil)
		if err != nil {
			log.Printf("backup: s3 uploader not injected: %v", err)
		} else {
			backupOpts.S3 = up
		}
	}
	go backup.Loop(ctx, db, backupOpts, secret.NewStore(cfg.SecretKey))

	// Panel primary/standby sync (P2-1): a standby instance with FERRY_STANDBY_MASTER_URL set
	// periodically pulls the primary panel's encrypted snapshot and atomically replaces the local DB, then exits for systemd to restart and load
	// the new DB (RPO <= interval; if the primary is unreachable it keeps serving the stale DB without replacing). On the primary side
	// only FERRY_STANDBY_TOKEN is needed to open the snapshot endpoint (handler/standby.go).
	if cfg.StandbyMasterURL != "" {
		go standby.Loop(ctx, standby.Options{
			MasterURL: cfg.StandbyMasterURL, Token: cfg.StandbyToken,
			Sec: cfg.StandbySec, DBPath: standby.DBPathFromDSN(cfg.DBDSN),
		}, db, secret.NewStore(cfg.SecretKey), func() {
			log.Printf("standby: db replaced from master, exiting for restart")
			os.Exit(0)
		})
	}

	// Operation audit retention sweep (AU-3): retention<=0 keeps records forever and does not start.
	go audit.Loop(ctx, db, cfg.AuditRetentionDays, nil)

	// Panel web certificate (P1-7): if a certificate is configured in settings, serve HTTPS;
	// corrupted config aborts startup to avoid a silent downgrade to HTTP; switching certificates requires a restart.
	cert, hasCert, err := handler.WebTLSCert(db)
	if err != nil {
		return fmt.Errorf("load web cert: %w", err)
	}
	srv := &http.Server{Addr: cfg.Addr, Handler: r}
	if hasCert {
		log.Printf("ferry listening on %s (https)", cfg.Addr)
		srv.TLSConfig = &tls.Config{Certificates: []tls.Certificate{*cert}, MinVersion: tls.VersionTLS12}
	} else {
		log.Printf("ferry listening on %s", cfg.Addr)
	}
	return serve(ctx, srv)
}

// serve listens until ctx is cancelled: graceful shutdown waits for in-flight requests to finish (http.ErrServerClosed
// is the normal-shutdown convention and not counted as an error); if TLSConfig is set, listen over TLS.
func serve(ctx context.Context, srv *http.Server) error {
	errCh := make(chan error, 1)
	go func() {
		if srv.TLSConfig != nil {
			errCh <- srv.ListenAndServeTLS("", "")
		} else {
			errCh <- srv.ListenAndServe()
		}
	}()
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			log.Printf("shutdown: %v", err)
		}
		err := <-errCh
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	}
}

// backupDecrypt decrypts a periodic backup .enc archive to plaintext (panel availability §3 restore ②) and returns
// an exit code: -key defaults to FERRY_SECRET_KEY, -out defaults to stripping the .enc suffix; missing args, an unconfigured
// master key, and decryption failure (wrong key / corrupt archive) all report errors clearly and exit non-zero, for restore.sh to judge.
func backupDecrypt(args []string) int {
	fs := flag.NewFlagSet("backup-decrypt", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	key := fs.String("key", os.Getenv("FERRY_SECRET_KEY"), "主密钥（缺省取 FERRY_SECRET_KEY）")
	in := fs.String("in", "", "待解密的 .enc 备份档路径")
	out := fs.String("out", "", "还原的明文输出路径（缺省去掉 .enc 后缀）")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *in == "" {
		fmt.Fprintln(os.Stderr, "backup-decrypt: 缺 -in（待解密的备份档路径）")
		return 2
	}
	store := secret.NewStore(*key)
	if !store.Enabled() {
		fmt.Fprintln(os.Stderr, "backup-decrypt: 未配置主密钥（-key 或 FERRY_SECRET_KEY）")
		return 1
	}
	raw, err := os.ReadFile(*in)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup-decrypt: 读档失败: %v\n", err)
		return 1
	}
	plain, err := store.DecryptBytes(raw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "backup-decrypt: 解密失败（主密钥不符或档损坏）: %v\n", err)
		return 1
	}
	target := *out
	if target == "" {
		target = strings.TrimSuffix(*in, ".enc")
		if target == *in {
			target = *in + ".plain"
		}
	}
	if err := os.WriteFile(target, plain, 0o600); err != nil {
		fmt.Fprintf(os.Stderr, "backup-decrypt: 写出失败: %v\n", err)
		return 1
	}
	fmt.Printf("backup-decrypt: 已还原 %s -> %s（%d 字节）\n", *in, target, len(plain))
	return 0
}
