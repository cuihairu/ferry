package storage

import "time"

// User is a subscription user; SubToken is the subscription link token.
type User struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	Username   string     `gorm:"size:64;uniqueIndex" json:"username"`
	SubToken   string     `gorm:"size:64;uniqueIndex" json:"sub_token"`
	QuotaBytes int64      `gorm:"default:0" json:"quota_bytes"`
	ResetCycle string     `gorm:"size:8;default:none" json:"reset_cycle"` // traffic reset cycle (P1-4): none/day/week/month
	ExpiresAt  *time.Time `json:"expires_at"`
	Enabled    bool       `gorm:"default:true" json:"enabled"`
	IsAdmin    bool       `gorm:"default:false" json:"is_admin"`
	// Multi-level admins (P2-2): empty=regular user; super=super admin (created by bootstrap,
	// can manage admins); operator=operator admin (constrained by max_users user-creation quota). The two-tier
	// policy aligns with Marzban is_sudo; legacy admins with an empty role are treated as super (normalized at login).
	AdminRole string `gorm:"size:16;default:''" json:"admin_role"`
	// MaxUsers is the operator admin's user-creation quota (0=unlimited): it constrains only the createUser action,
	// counting by created_by attribution, and over quota returns 403.
	MaxUsers  int    `gorm:"default:0" json:"max_users"`
	CreatedBy string `gorm:"size:64;default:''" json:"created_by"`
	// User-level bandwidth cap (P2-3): 0=unlimited. ferry is the authoritative source (metadata); actual enforcement
	// is in the node-side kernel (the config generator reads this value to write the kernel's per-client rate limit) - ferry does
	// not render node kernel config (opaque payload delivery), so it only provides authoritative metadata and a visible surface.
	BwUpMbps   int `gorm:"default:0" json:"bw_up_mbps"`
	BwDownMbps int `gorm:"default:0" json:"bw_down_mbps"`
	// Device count / IP count caps (P2-4, 0=unlimited): device_limit counts devices by the
	// hwid the subscription end reports (it depends on the client sending X-Client-Hwid; that face is inert when unreported);
	// ip_limit counts by the subscription end's independent IPs (no client dependency, always effective).
	DeviceLimit int    `gorm:"default:0" json:"device_limit"`
	IPLimit     int    `gorm:"default:0" json:"ip_limit"`
	Password    string `gorm:"size:128" json:"-"`
	ApiToken    string `gorm:"size:64" json:"api_token"`
	// Two-factor authentication (security design §1, P1): the TOTP secret is encrypted via secret_store into the DB
	// (v1:nonce:ct ciphertext, the master key never enters the DB）; after binding, login requires a 6-digit TOTP or a one-time
	// recovery code, and unbound login behavior is unchanged.
	TOTPSecret  string `gorm:"size:256" json:"-"`
	TOTPEnabled bool   `gorm:"default:false" json:"-"`
	// Notification preferences (NT-2): per-type toggles within the in-app channel; expand the channel dimension when the second channel (email/TG) lands.
	NotifyExpiry       bool       `gorm:"default:true" json:"notify_expiry"`      // expiry reminder
	NotifyTraffic      bool       `gorm:"default:true" json:"notify_traffic"`     // traffic warning
	TrafficWarnPercent int        `gorm:"default:80" json:"traffic_warn_percent"` // traffic warning threshold (1-100)
	LastNodeID         *uint      `gorm:"index" json:"last_node_id,omitempty"`
	LastSeenAt         *time.Time `json:"last_seen_at,omitempty"`
	// PasswordChangedAt is the time of the most recent admin password change: admin JWTs issued earlier are all void
	// (an audit fix on 2026-11-12; after a change the old token no longer survives to natural expiry).
	// Legacy behavior migration: old rows NULL / zero value are not judged, and existing sessions wind down at their own expiry.
	PasswordChangedAt *time.Time `json:"password_changed_at,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// UserAlias retains a user's old name (P2 fix): xray traffic is attributed by email (email ==
// users.username convention); during the period after the rename when the node-side config has not been re-pushed, the old email still reports -
// the old name is retained as an alias, and saveUserTraffic still maps it to the original user, so renamed traffic is not missed.
// Generations of renames are all retained (one row per generation); when a name is taken back (renamed to the old name again), the old alias still points at this user.
type UserAlias struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"index;not null" json:"user_id"`
	Username  string    `gorm:"size:64;uniqueIndex;not null" json:"username"`
	CreatedAt time.Time `json:"created_at"`
}

// SubDevice is one record of a device / IP observed by the subscription end (P2-4). Kind=hwid rows record devices by the
// client-reported X-Client-Hwid (no row is written when unreported); kind=ip rows record by the subscription source IP
// (always writable). (user_id, kind, key) is unique to guard against double-insert on concurrent first visits.
type SubDevice struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"uniqueIndex:idx_subdev_key" json:"user_id"`
	Kind      string    `gorm:"size:8;uniqueIndex:idx_subdev_key" json:"kind"` // hwid/ip
	Key       string    `gorm:"size:64;uniqueIndex:idx_subdev_key" json:"key"`
	UserAgent string    `gorm:"size:128" json:"user_agent,omitempty"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
}

// Node is one proxy node. Token is used for agent outbound connection authentication.
type Node struct {
	ID       uint   `gorm:"primaryKey" json:"id"`
	Name     string `gorm:"size:64" json:"name"`
	Address  string `gorm:"size:255" json:"address"`
	Port     int    `gorm:"not null" json:"port"`
	Protocol string `gorm:"size:16" json:"protocol"` // vless/vmess/trojan/shadowsocks/hysteria2
	Config   string `gorm:"type:text" json:"config"` // protocol config template JSON
	Enabled  bool   `gorm:"default:true" json:"enabled"`
	Token    string `gorm:"size:64;uniqueIndex" json:"token"`
	// Token rotation grace: an agent replaces local config and reconnects.
	OldToken       string     `gorm:"size:64;index" json:"old_token,omitempty"`
	TokenRotatedAt *time.Time `json:"token_rotated_at,omitempty"`
	LastSeen       *time.Time `json:"last_seen,omitempty"`
	Status         string     `gorm:"size:16;default:unknown" json:"status"` // online/offline/unknown
	// Registration metadata (entries and load balancing design §C.3/C.6): the initial values the agent reports at registration,
	// the panel may change them and is authoritative (after meta_init is set, registration no longer overwrites).
	Role                     string     `gorm:"size:16;default:landing" json:"role"` // entry/landing/both
	Direction                string     `gorm:"size:8;default:out" json:"direction"` // out/in/both
	LineType                 string     `gorm:"size:16;default:普通" json:"line_type"` // cn2_gia/cu_vip/cmi/iplc/163/normal
	Region                   string     `gorm:"size:64;default:未知" json:"region"`    // region, the fault-aggregation dimension
	City                     string     `gorm:"size:64" json:"city"`
	Datacenter               string     `gorm:"size:128" json:"datacenter"`
	ISP                      string     `gorm:"size:32;default:未知" json:"isp"`          // ISP, the fault-aggregation dimension
	Labels                   string     `gorm:"type:text" json:"labels"`                // JSON array
	Transport                string     `gorm:"size:16;default:tls" json:"transport"`   // tls/quic/ws-tls/ssh
	BillingType              string     `gorm:"size:16;default:包月" json:"billing_type"` // traffic/monthly/fixed bandwidth
	TrafficPriceCents        int64      `gorm:"default:0" json:"traffic_price_cents"`   // per-traffic unit price (cents/GB)
	MonthlyCostCents         int64      `gorm:"default:0" json:"monthly_cost_cents"`    // fixed monthly cost (cents/month)
	Currency                 string     `gorm:"size:8;default:CNY" json:"currency"`
	CostNote                 string     `gorm:"size:255" json:"cost_note"`
	BwUpMbps                 int        `gorm:"default:0" json:"bw_up_mbps"`                  // plan upload Mbps
	BwDownMbps               int        `gorm:"default:0" json:"bw_down_mbps"`                // plan download Mbps
	MonthlyTrafficQuotaBytes int64      `gorm:"default:0" json:"monthly_traffic_quota_bytes"` // monthly traffic quota (bytes, 0=unlimited)
	RateLimited              bool       `gorm:"default:true" json:"rate_limited"`
	Burst                    bool       `gorm:"default:false" json:"burst"`
	SpeedMeasuredMbps        int        `gorm:"default:0" json:"speed_measured_mbps"` // speed-calibrated measured capacity
	SpeedCalibratedAt        *time.Time `json:"speed_calibrated_at"`
	MetaInit                 bool       `gorm:"default:false" json:"-"`                  // whether the metadata is initialized (the panel has taken over)
	AgentVersion             string     `gorm:"size:64;default:''" json:"agent_version"` // agent self-reported version (A-23)
	// Entry-pool suspend state (E-16): suspended = removed on consecutive sick, and the subscription entry pool drops it
	// immediately; enabled is left alone (the agent connection and probing keep running, and reset judgment needs that basis).
	PoolState     string     `gorm:"size:16;default:active;index" json:"pool_state"`
	PoolChangedAt *time.Time `json:"pool_changed_at"`
	// Supply-into-pool pipeline (OS-4): a note of provisioning nodes' progress / failure reasons, cleared
	// when they turn online; online/offline transitions do not write this column.
	ProvisionNote string    `gorm:"size:255" json:"provision_note"`
	PoolReason    string    `gorm:"size:64" json:"pool_reason"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// TrafficLog is one traffic accounting record (aggregated per user per node per cycle).
// Accounting detail is never deleted or modified (policy in quota.go): deleting a user does not cascade-clear history - the User foreign key uses
// SET NULL (same policy as Node), user_id is nullable, and after user deletion the row is kept with attribution nulled.
// It previously misused CASCADE, where deleting a user also cleared billing history (an audit fix on 2026-11-12).
type TrafficLog struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	UserID     *uint     `gorm:"index:idx_traffic_logs_user,priority:1" json:"user_id"`
	NodeID     *uint     `json:"node_id"`
	RxBytes    int64     `gorm:"default:0" json:"rx_bytes"`
	TxBytes    int64     `gorm:"default:0" json:"tx_bytes"`
	RecordedAt time.Time `gorm:"index:idx_traffic_logs_user,priority:2" json:"recorded_at"`
	User       *User     `gorm:"foreignKey:UserID;references:ID;constraint:OnDelete:SET NULL" json:"-"`
	Node       *Node     `gorm:"foreignKey:NodeID;references:ID;constraint:OnDelete:SET NULL" json:"-"`
}

// Setting is a panel setting key-value pair (a lightweight KV, Value holds JSON text).
type Setting struct {
	Key       string    `gorm:"primaryKey;size:64" json:"key"`
	Value     string    `gorm:"type:text" json:"value"`
	UpdatedAt time.Time `json:"updated_at"`
}

// CardBatch is the entitlement definition of a batch of card codes.
type CardBatch struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	Name       string     `gorm:"size:128" json:"name"`
	GrantType  string     `gorm:"size:16" json:"grant_type"`    // add_quota / extend_days
	GrantValue int64      `gorm:"not null" json:"grant_value"`  // bytes or days
	PriceCents int64      `gorm:"default:0" json:"price_cents"` // online sale price (cents), 0 = redeem only, not for sale
	Total      int        `gorm:"not null" json:"total"`
	ExpiredAt  *time.Time `json:"expired_at"` // card-code validity period, empty means permanent
	// Distributor attribution (DS-1): 0=the panel self-operates; >0=a batch under the distributor, brought into the order and accounts at redemption.
	DistributorID uint      `gorm:"default:0;index" json:"distributor_id"`
	CreatedBy     string    `gorm:"size:64" json:"created_by"`
	CreatedAt     time.Time `json:"created_at"`
	// Deleting a batch cascades to its card codes（《payment design》§3.1), so a batch created by mistake before export can be reclaimed whole.
	Codes []CardCode `gorm:"foreignKey:BatchID;references:ID;constraint:OnDelete:CASCADE" json:"-"`
}

// CardCode is one card code; Code is stored in plaintext so it can be exported and delivered.
type CardCode struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	BatchID   uint       `gorm:"index;not null" json:"batch_id"`
	Code      string     `gorm:"size:32;uniqueIndex" json:"code"`
	Status    string     `gorm:"size:16;default:unused" json:"status"` // unused/used/disabled
	FailCount int        `gorm:"default:0" json:"fail_count"`
	UsedBy    *uint      `json:"used_by"`
	UsedAt    *time.Time `json:"used_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// LandingAssignment is one landing assignment record (the entry->landing mapping and its policy trail).
// The entry pool / landing pool is just a nodes.role grouping; no separate pool table is created.
type LandingAssignment struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	EntryNodeID   *uint      `gorm:"index" json:"entry_node_id"` // entry node (empty = region-level assignment)
	Region        string     `gorm:"size:64" json:"region"`      // the region name for a region-level assignment (left empty for an entry-level assignment)
	LandingNodeID uint       `gorm:"index;not null" json:"landing_node_id"`
	Direction     string     `gorm:"size:8;not null" json:"direction"` // out/in, direction-split trail
	Strategy      string     `gorm:"size:16;not null" json:"strategy"` // manual/least_conn/cost_first/perf_first/balanced
	Weight        int        `gorm:"default:0" json:"weight"`
	Reason        string     `gorm:"size:64" json:"reason"` // region_fault/failover/manual...
	AssignedAt    time.Time  `gorm:"not null" json:"assigned_at"`
	ReleasedAt    *time.Time `json:"released_at"` // empty = in effect
	ReleaseReason string     `gorm:"size:64" json:"release_reason"`
}

// ProbeReport stores one edge probe conclusion: probing runs locally on the node,
// and the panel only receives the conclusion for aggregate judgment and historical backtracking.
type ProbeReport struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	NodeID       uint      `gorm:"index;not null" json:"node_id"`                                         // the prober (entry agent)
	TargetKind   string    `gorm:"size:16;not null;index:idx_probe_window,priority:1" json:"target_kind"` // tunnel/exit/peer
	TargetNodeID *uint     `gorm:"index:idx_probe_window,priority:2" json:"target_node_id"`
	TargetHost   string    `gorm:"size:255" json:"target_host"`
	Direction    string    `gorm:"size:8;not null;default:out" json:"direction"`
	RttMs        int       `gorm:"default:0" json:"rtt_ms"`
	LossPct      int       `gorm:"default:0" json:"loss_pct"` // 0-100
	Reachable    bool      `gorm:"default:false" json:"reachable"`
	Blocked      bool      `gorm:"default:false" json:"blocked"`
	Verdict      string    `gorm:"size:16;not null" json:"verdict"`           // healthy/sick
	Region       string    `gorm:"size:64;not null;default:未知" json:"region"` // target region snapshot
	ISP          string    `gorm:"size:32;not null;default:未知" json:"isp"`    // target ISP snapshot
	ProbedAt     time.Time `gorm:"not null;index:idx_probe_window,priority:3" json:"probed_at"`
	CreatedAt    time.Time `json:"created_at"`
}

// DimensionStatus is the status-light data for a region / ISP dimension: the persisted outcome of aggregate judgment.
type DimensionStatus struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Scope     string    `gorm:"size:16;not null;uniqueIndex:idx_dimension_status" json:"scope"` // region/isp
	Key       string    `gorm:"size:64;not null;uniqueIndex:idx_dimension_status" json:"key"`   // region name or ISP name
	State     string    `gorm:"size:16;not null;default:healthy" json:"state"`                  // healthy/degraded/failed
	Reason    string    `gorm:"size:255" json:"reason"`
	Since     time.Time `gorm:"not null" json:"since"` // when the current state was entered
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

// NodeConfig is one config delivery record and outcome (A-18): pending -> applied/failed.
type NodeConfig struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	NodeID    uint      `gorm:"index;not null" json:"node_id"`
	Proc      string    `gorm:"size:64;not null" json:"proc"`
	Kind      string    `gorm:"size:32" json:"kind"`
	Version   string    `gorm:"size:64;not null" json:"version"` // content hash, same value as sha256
	Sha256    string    `gorm:"size:64;not null" json:"sha256"`
	Payload   string    `gorm:"type:text" json:"payload"`
	Status    string    `gorm:"size:16;not null;default:pending" json:"status"` // pending/applied/failed
	Reverted  bool      `gorm:"default:false" json:"reverted"`
	Validated bool      `gorm:"default:false" json:"validated"`
	Error     string    `gorm:"size:512" json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Alert is one abnormal alert (A-22): an agent-reported process crash / certificate expiry / high load / config error,
// keeping only one active alert per node per category per process (a repeat report refreshes the message), auto-resolved when the process recovers.
type Alert struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	NodeID     uint       `gorm:"index;not null" json:"node_id"`
	Kind       string     `gorm:"size:32;not null;index" json:"kind"` // proc_crash/cert_expiry/high_load/config_error
	Severity   string     `gorm:"size:16;not null" json:"severity"`   // warning/critical
	Proc       string     `gorm:"size:64" json:"proc,omitempty"`
	Message    string     `gorm:"type:text" json:"message"`
	State      string     `gorm:"size:16;not null;default:active;index" json:"state"` // active/resolved
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	ResolvedAt *time.Time `json:"resolved_at,omitempty"`
}

// NodeTrafficLog is node-level process traffic accounting (A-20): the agent reports per-process increments each cycle.
// User-level accounting goes through traffic_logs (P0-9), needing a per-user mapping (P1-3).
type NodeTrafficLog struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	NodeID       uint      `gorm:"index:idx_node_traffic,priority:1;not null" json:"node_id"`
	Proc         string    `gorm:"size:64;not null" json:"proc"`
	RxBytes      int64     `gorm:"default:0" json:"rx_bytes"`
	TxBytes      int64     `gorm:"default:0" json:"tx_bytes"`
	DirectBytes  int64     `gorm:"default:0" json:"direct_bytes"`  // direct-route split outbound bytes (SAVE-1/7)
	BlockedBytes int64     `gorm:"default:0" json:"blocked_bytes"` // blocked outbound bytes (SAVE-4 ad/tracker blocking)
	Conns        int       `gorm:"default:0" json:"conns"`
	RecordedAt   time.Time `gorm:"not null;index:idx_node_traffic,priority:2" json:"recorded_at"`
	CreatedAt    time.Time `json:"created_at"`
}

// NodeProcStatus is the latest status snapshot of a managed process (SAVE-3): the ProcStatus carried by each heartbeat is upserted
// per node per process, and metrics (nginx cache / Squid hit statistics etc.) are stored in metrics as JSON.
type NodeProcStatus struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	NodeID    uint      `gorm:"uniqueIndex:idx_node_proc;not null" json:"node_id"`
	Proc      string    `gorm:"size:64;not null;uniqueIndex:idx_node_proc" json:"proc"`
	State     string    `gorm:"size:16;not null" json:"state"` // running/stopped/crashed
	PID       int       `gorm:"column:pid;default:0" json:"pid,omitempty"`
	Restarts  int       `gorm:"default:0" json:"restarts"`
	Metrics   string    `gorm:"type:text" json:"metrics,omitempty"` // JSON numeric object (key-value)
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
}

// UserContact is the user's contact-info binding and outreach preferences (outreach batch TOUCH-1): TG / email is
// at least one required (guided to complete by the panel), and stale marks delivery-failed awaiting rebind (TOUCH-5 receipt linkage).
// RoutineEmails only governs unsubscribing routine domain emails; billing types default to always-received with no toggle.
type UserContact struct {
	UserID        uint      `gorm:"primaryKey" json:"user_id"`
	TgChatID      string    `gorm:"size:64" json:"tg_chat_id,omitempty"`
	Email         string    `gorm:"size:255" json:"email,omitempty"`
	RoutineEmails bool      `json:"routine_emails"` // defaults to true, set by the handler (a gorm default tag swallows a Create zero value)
	BoundAt       time.Time `json:"bound_at"`
	Stale         bool      `gorm:"default:false" json:"stale"`   // invalidated, awaiting rebind
	FailStreak    int       `gorm:"default:0" json:"fail_streak"` // consecutive delivery-failure count (reset on success)
	UpdatedAt     time.Time `json:"updated_at"`
}

// TouchJob is one outreach task and its result (outreach batch TOUCH-4): routine outreach first lands a task then goes out via
// Herald (outbox semantics), with event_id linking the outbox row; once delivered it is sent (the ferry->
// Herald leg 2xx), and a channel dispatch failure is rewritten by the TOUCH-5 receipt and linked to stale.
type TouchJob struct {
	ID        int64      `gorm:"primaryKey" json:"id"`
	UserID    int64      `gorm:"index;not null" json:"user_id"`
	Channel   string     `gorm:"size:16;not null" json:"channel"`                                               // email (ferry runs no channel of its own, dispatch via Herald)
	Kind      string     `gorm:"size:16;not null;index:idx_touch_due,priority:2" json:"kind"`                   // bill/domains
	Payload   string     `gorm:"type:text;not null" json:"payload"`                                             // rendered content JSON {title,body}
	Status    string     `gorm:"size:16;not null;default:pending;index:idx_touch_due,priority:1" json:"status"` // pending/sent/failed
	EventID   int64      `gorm:"default:0" json:"event_id"`
	SentAt    *time.Time `json:"sent_at,omitempty"`
	Error     string     `gorm:"size:255" json:"error,omitempty"`
	CreatedAt time.Time  `gorm:"not null" json:"created_at"`
}

// EntryDomain is the panel's entry domains and backup addresses (outreach batch TOUCH-3): the common data source for
// routine domain emails and disconnection-disaster tolerance (subscription backup info and new-entry pushes all read this list).
type EntryDomain struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Domain    string    `gorm:"size:255;not null" json:"domain"`
	Role      string    `gorm:"size:16;not null" json:"role"`    // primary/backup
	Region    string    `gorm:"size:64" json:"region,omitempty"` // applicable region, empty = all regions
	Enabled   bool      `json:"enabled"`
	UpdatedAt time.Time `gorm:"not null" json:"updated_at"`
	CreatedAt time.Time `json:"created_at"`
}

// SaveStat is the daily traffic-savings summary (SAVE-7): aggregated from node_traffic_logs,
// direct / blocked bytes come from the agent's outbound counting, and cache hits are reserved (after the cache-layer byte metrics land under
// SAVE-3 metrics they feed in), with converted cost computed API-side from each node's traffic unit price.
type SaveStat struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	NodeID        uint      `gorm:"uniqueIndex:idx_save_stats;not null" json:"node_id"`
	Day           string    `gorm:"size:10;not null;uniqueIndex:idx_save_stats" json:"day"` // YYYY-MM-DD（UTC）
	DirectBytes   int64     `gorm:"default:0" json:"direct_bytes"`
	CacheHitBytes int64     `gorm:"default:0" json:"cache_hit_bytes"`
	BlockedBytes  int64     `gorm:"default:0" json:"blocked_bytes"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`
}

// PaymentOrder is one order (the first of three ledgers: who should pay how much).
type PaymentOrder struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	OrderNo     string `gorm:"size:32;uniqueIndex" json:"order_no"`
	UserID      uint   `gorm:"index;not null" json:"user_id"`
	Provider    string `gorm:"size:16" json:"provider"` // card / epusdt / wechat / alipay
	AmountCents int64  `gorm:"default:0" json:"amount_cents"`
	Product     string `gorm:"size:128" json:"product"`
	Status      string `gorm:"size:16;default:pending;index" json:"status"` // pending/paid/failed/expired/refunded
	// Distributor attribution (DS-1): brought in from the card batch or product at redemption/order time, 0=self-operated.
	DistributorID uint `gorm:"default:0;index" json:"distributor_id"`
	// Auto-grant policy on credit (used by online payments; card codes use the batch's own): add_quota/extend_days, empty = no auto grant.
	GrantType  string     `gorm:"size:16" json:"grant_type,omitempty"`
	GrantValue int64      `gorm:"default:0" json:"grant_value,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	PaidAt     *time.Time `json:"paid_at"`
	// Refund trail (OD-2): the panel only does state transitions and records, and money goes back via the channel's backend.
	RefundAt   *time.Time `json:"refund_at,omitempty"`
	RefundNote string     `gorm:"size:255" json:"refund_note,omitempty"` // channel refund number / reason
	// Promotional accounts (PROMO-1, operations design §5): list price / promo code / snapshot - the order records the list price + amount paid
	//(AmountCents) + the promo detail, so the three ledgers reconcile; with no promotion the three columns are zero values / empty.
	ListAmountCents int64  `gorm:"default:0" json:"list_amount_cents"` // list price (cents), no promotion = AmountCents
	PromoCode       string `gorm:"size:64;index;default:''" json:"promo_code,omitempty"`
	PromoSnapshot   string `gorm:"size:512" json:"promo_snapshot,omitempty"` // list price / discount / code / paid JSON
	// No User association: financial records do not follow user deletion (no foreign key).
}

// Coupon is a promo code (PROMO-1, operations design §5 DDL blueprint): cut is a
// deduction amount (cents) and pct a discount (basis points, 10000 = no discount); scope=all or batch:<id> selects a batch;
// total=0 means unlimited, per_user caps each user; the redemption-condition update guards against overspend.
type Coupon struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	Code      string     `gorm:"size:64;uniqueIndex" json:"code"`
	Kind      string     `gorm:"size:8" json:"kind"` // cut / pct
	Value     int64      `json:"value"`              // cents (cut) or basis points (pct, 1-9999)
	Scope     string     `gorm:"size:32" json:"scope"`
	MinAmount int64      `gorm:"default:0" json:"min_amount"` // threshold (cents), counted against the list price
	StartsAt  *time.Time `json:"starts_at"`
	EndsAt    *time.Time `json:"ends_at"`
	Total     int64      `gorm:"default:0" json:"total"`    // 0 = unlimited
	PerUser   int64      `gorm:"default:1" json:"per_user"` // per-user cap
	Used      int64      `gorm:"default:0" json:"used"`
	CreatedAt time.Time  `json:"created_at"`
}

// Campaign is a limited-time campaign (PROMO-2, operations design §5 DDL blueprint): first_order for a first order /
// renew for a renewal / timed for a limited three types; rules is the discount-rule JSON (the same cut/pct
// policy as a promo code); applied automatically inside the window, on the same best-of policy as a promo code (one discount source per order).
type Campaign struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"size:64" json:"name"`
	Kind      string    `gorm:"size:16" json:"kind"` // first_order / renew / timed
	Rules     string    `gorm:"size:512" json:"rules"`
	StartsAt  time.Time `json:"starts_at"`
	EndsAt    time.Time `json:"ends_at"`
	Enabled   bool      `gorm:"default:true" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

// NodeTokenRevocation is a former node token that must not authenticate.
type NodeTokenRevocation struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	NodeID    uint      `gorm:"index" json:"node_id"`
	Token     string    `gorm:"size:64;uniqueIndex" json:"token"`
	RevokedAt time.Time `json:"revoked_at"`
}

// EnrollmentToken is a one-time short-lived node registration credential (FERRY-001): the /api/nodes management surface
// goes through apiAuth, and a fresh-install install script must not and should not hold admin credentials - an admin first signs this credential,
// and the node machine then exchanges it for a node token on the public surface /api/enroll. Single-use (used_at is set),
// revocable any time; the token plaintext appears only once in the signing response and is not returned in the list.
type EnrollmentToken struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	Token     string     `gorm:"size:64;uniqueIndex" json:"-"`
	Note      string     `gorm:"size:200" json:"note"`
	CreatedBy string     `gorm:"size:100" json:"created_by"`
	ExpiresAt time.Time  `json:"expires_at"`
	UsedAt    *time.Time `json:"used_at"`
	UsedBy    string     `gorm:"size:100" json:"used_by"`
	RevokedAt *time.Time `json:"revoked_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// PaymentTransaction is a payment ledger entry (the second of three ledgers: the payments that actually happened externally).
type PaymentTransaction struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	OrderNo     string    `gorm:"size:32;index" json:"order_no"`
	Provider    string    `gorm:"size:16;uniqueIndex:idx_provider_external" json:"provider"`
	ExternalID  string    `gorm:"size:128;uniqueIndex:idx_provider_external" json:"external_id"`
	AmountCents int64     `gorm:"not null" json:"amount_cents"`
	Direction   string    `gorm:"size:8;default:in" json:"direction"` // in/out
	Raw         string    `gorm:"type:text" json:"raw"`
	OccurredAt  time.Time `json:"occurred_at"`
	CreatedAt   time.Time `json:"created_at"`
}

// Grant is a grant record (the third of three ledgers: the entitlements the user actually received).
type Grant struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	OrderNo    string    `gorm:"size:32;index" json:"order_no"`
	UserID     uint      `gorm:"index;not null" json:"user_id"`
	GrantType  string    `gorm:"size:16" json:"grant_type"`   // add_quota / extend_days
	GrantValue int64     `gorm:"not null" json:"grant_value"` // bytes or days
	Snapshot   string    `gorm:"type:text" json:"snapshot"`   // pre- and post-change snapshot JSON
	CreatedAt  time.Time `json:"created_at"`
	// No User association: grant records do not follow user deletion (no foreign key).
}

// Distributor is an agent account (DS distribution, an independent login system, not users: no quota / subscription semantics).
// Disabled means login is barred, while the ledgers are kept. The commission ratio is the part the panel concedes to the agent.
type Distributor struct {
	ID              uint      `gorm:"primaryKey" json:"id"`
	Username        string    `gorm:"size:64;uniqueIndex;not null" json:"username"`
	PasswordHash    string    `gorm:"size:128;not null" json:"-"`
	DiscountPercent int       `gorm:"default:0" json:"discount_percent"` // commission ratio 0-100
	Note            string    `gorm:"size:255" json:"note"`
	Enabled         bool      `gorm:"default:true" json:"enabled"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

// DistributorLedger is an agent-ledger flow: sale (a card-sale trail that does not enter the balance) / commission (enters the balance) /
// payout (settlement deduction) / adjust (manual adjustment). Unsettled balance = Σcommission + Σadjust − Σpayout.
type DistributorLedger struct {
	ID            uint      `gorm:"primaryKey" json:"id"`
	DistributorID uint      `gorm:"index;not null" json:"distributor_id"`
	OrderNo       string    `gorm:"size:32;index" json:"order_no"` // sale/commission tied to the three-ledger order
	Kind          string    `gorm:"size:16;not null" json:"kind"`  // sale/commission/payout/adjust
	AmountCents   int64     `gorm:"not null" json:"amount_cents"`  // the interface layer distinguishes credit / debit by sign
	Note          string    `gorm:"size:255" json:"note"`
	CreatedAt     time.Time `json:"created_at"`
}

// Provider is a cloud provider credential (OS-1): the provider authentication secret for plugging into the OpenTofu
// supply engine. AccessKey is an AES-256-GCM ciphertext (encrypted via the unified secret entry point), the interface layer never
// echoes the plaintext back, and it is decrypted only when the server runs supply.
type Provider struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"size:64;not null" json:"name"`
	Type      string    `gorm:"size:32;not null" json:"type"`        // OpenTofu provider name (vultr/hetzner/…)
	AccessKey string    `gorm:"size:512;not null" json:"access_key"` // ciphertext, presented at the interface layer as has_access_key
	Enabled   bool      `gorm:"default:true" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// ProvisionTemplate is a machine template (OS-1): dash enters it, OS-2 renders HCL supply,
// and when OS-4 fills in node metadata by the template it aligns with the node (direction / line / region / cost policy).
type ProvisionTemplate struct {
	ID                uint      `gorm:"primaryKey" json:"id"`
	Name              string    `gorm:"size:64;not null" json:"name"`
	ProviderID        uint      `gorm:"index;not null" json:"provider_id"`
	Plan              string    `gorm:"size:64" json:"plan"`                 // machine slug
	Region            string    `gorm:"size:64" json:"region"`               // region
	Image             string    `gorm:"size:64" json:"image"`                // system image (each provider's own policy)
	BwMbps            int       `json:"bw_mbps"`                             // bandwidth (Mbps)
	BillingType       string    `gorm:"size:16" json:"billing_type"`         // monthly / per-traffic
	MonthlyCostCents  int64     `json:"monthly_cost_cents"`                  // fixed monthly cost (cents)
	TrafficPriceCents int64     `json:"traffic_price_cents"`                 // per-traffic unit price (cents/GB)
	Direction         string    `gorm:"size:8;default:out" json:"direction"` // out/in/both
	LineType          string    `gorm:"size:32" json:"line_type"`            // 163/cn2_gia/cu_vip/cmi/iplc
	Role              string    `gorm:"size:16;default:entry" json:"role"`   // entry/landing/both/relay
	Transport         string    `gorm:"size:16" json:"transport"`            // tls/ws-tls/quic/ssh
	Config            string    `gorm:"type:text" json:"config"`             // protocol config template JSON (OS-4: a copy lands on the node row on server-up, for auto delivery)
	CreatedAt         time.Time `json:"created_at"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// ProvisionJob is one supply execution trail (OS-2): the result and log tail of the
// controlled run of plan/apply. A single instance does not apply concurrently; the log holds no secrets (keys go through TF_VAR env vars).
type ProvisionJob struct {
	ID           uint       `gorm:"primaryKey" json:"id"`
	TemplateID   uint       `gorm:"index;not null" json:"template_id"`
	TemplateName string     `gorm:"size:64" json:"template_name"`
	Action       string     `gorm:"size:8;not null" json:"action"` // plan / apply
	Status       string     `gorm:"size:8;not null" json:"status"` // running / ok / failed
	Log          string     `gorm:"type:text" json:"log"`
	CreatedAt    time.Time  `json:"created_at"`
	FinishedAt   *time.Time `json:"finished_at"`
}

// Recovery is the state row of the ban-recovery pipeline (BR-1): at most one in progress per node,
// advancing L1→L2→L3 in tiers, each tier timing out into the next, and probe recovery (attach/detach reset) completes it.
// Action-level trail and failure escalation to a human are carried by the later recovery_actions (BR-5).
type Recovery struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	NodeID         uint       `gorm:"uniqueIndex;not null" json:"node_id"`
	NodeName       string     `gorm:"size:64" json:"node_name"`
	Level          int        `json:"level"` // current advancement level 1-3
	State          string     `gorm:"size:16" json:"state"`
	Action         string     `gorm:"size:32" json:"action"`       // the current level's action name (empty = not started)
	ActionState    string     `gorm:"size:16" json:"action_state"` // running/ok/failed/skipped/empty
	LastErr        string     `gorm:"size:512" json:"last_error,omitempty"`
	LevelStartedAt time.Time  `json:"level_started_at"`
	StartedAt      time.Time  `json:"started_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	FinishedAt     *time.Time `json:"finished_at,omitempty"`
}

// RecoveryAction is each tier's action trail of the recovery pipeline (BR-5): one row per advancement,
// used for replay - from the ban to the terminal state, who was running each tier, whether it succeeded, and why it failed is all queryable.
type RecoveryAction struct {
	ID         uint       `gorm:"primaryKey" json:"id"`
	RecoveryID uint       `gorm:"index;not null" json:"recovery_id"`
	NodeID     uint       `json:"node_id"`
	NodeName   string     `gorm:"size:64" json:"node_name"`
	Level      int        `json:"level"`
	Action     string     `gorm:"size:32" json:"action"`            // action name (empty for an unregistered tier)
	State      string     `gorm:"size:16" json:"state"`             // running/ok/failed/skipped/timeout
	Detail     string     `gorm:"size:512" json:"detail,omitempty"` // failure / skip reason
	StartedAt  time.Time  `json:"started_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
	FinishedAt *time.Time `json:"finished_at,omitempty"`
}

// DNSProvider is a DNS vendor credential (the BR-2 plugin slot; BR-4 ACME DNS-01 reuses the same channel).
// The credential secret follows the R24 encrypted-surface policy, and the interface layer returns has_api_key without echoing the plaintext.
type DNSProvider struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Name      string    `gorm:"size:64;not null" json:"name"`
	Type      string    `gorm:"size:32;not null" json:"type"` // plugin-slot type: cloudflare/…
	APIKey    string    `gorm:"size:512;not null" json:"-"`   // ciphertext
	Enabled   bool      `gorm:"default:true" json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// DNSFront is a domain-fronting record (BR-2): normally points at PrimaryIP, switches to a
// backup IP rotation on a ban judgment at L1, and switches back when probes recover - the domain does not change, the IP does. BackupIPs is a JSON
// string array, and SwitchIndex is the rotation cursor.
type DNSFront struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Name        string    `gorm:"size:64" json:"name"`                   // description (e.g. main entry domain)
	Domain      string    `gorm:"size:191;not null;index" json:"domain"` // FQDN
	ProviderID  uint      `gorm:"index;not null" json:"provider_id"`
	PrimaryIP   string    `gorm:"size:45;not null" json:"primary_ip"`              // normal pointing (the switch-back target)
	BackupIPs   string    `gorm:"size:1024;not null;default:[]" json:"backup_ips"` // JSON array
	Switched    bool      `gorm:"default:false" json:"switched"`                   // switched off normal (switched back on recovery)
	CurrentIP   string    `gorm:"size:45" json:"current_ip"`
	SwitchIndex int       `gorm:"default:0" json:"switch_index"` // backup IP rotation cursor
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// GeoDNSRecord is the synchronized record face of smart-DNS per-region routing (E-27 §C.8.4): the diff
// baseline for reconciliation - Sync compares the expected set against existing rows, upserting on add/change and deleting on
// disappearance (full-outage record withdrawal). It is never written back to the entry_domains manual list (per-region routing is an addition, not a dependency).
type GeoDNSRecord struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	FrontID   uint      `gorm:"index;not null" json:"front_id"`      // the owning DNSFront
	Slug      string    `gorm:"size:64;not null" json:"slug"`        // normalized region vocabulary
	Name      string    `gorm:"size:253;not null;index" json:"name"` // full subdomain {slug}.{front domain}
	Value     string    `gorm:"size:255;not null" json:"value"`      // representative entry address
	UpdatedAt time.Time `json:"updated_at"`
}

// CertTask is an orchestration task for one certificate (BR-4): the panel manages orchestration and expiry, and issuance reuses the
// external tool (acme.sh) - DNS-01 reuses BR-2's DNS vendor credential channel, the artifact
// stays in the tool working directory, and node distribution is taken up by a later batch.
type CertTask struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	Name          string     `gorm:"size:64" json:"name"`                           // description
	Domain        string     `gorm:"size:191;not null;index" json:"domain"`         // main domain (FQDN)
	Sans          string     `gorm:"size:512" json:"sans"`                          // extra domains (comma-separated, may be empty)
	Method        string     `gorm:"size:16;not null;default:dns-01" json:"method"` // dns-01/http-01
	DNSProviderID uint       `json:"provider_id"`                                   // DNS vendor credential used by dns-01 (BR-2 channel)
	CA            string     `gorm:"size:32;default:letsencrypt" json:"ca"`
	State         string     `gorm:"size:16;default:pending" json:"state"` // pending/issuing/ok/failed
	NotAfter      *time.Time `json:"not_after"`                            // certificate expiry (backfilled after issuance)
	LastError     string     `gorm:"size:512" json:"last_error,omitempty"`
	LastAttempt   *time.Time `json:"last_attempt"`
	Attempts      int        `json:"attempts"` // issuance/renewal attempt count (capped by cert.MaxAttempts; reset on success)
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

// Notification is one in-app message (NT-1): four user-facing notification types (announcement / expiry / traffic
// warning / system), one row per user (the fan-out cost is negligible at low user counts), with the read state hanging off the row.
type Notification struct {
	ID        int64      `gorm:"primaryKey" json:"id"`
	UserID    int64      `gorm:"index;not null" json:"user_id"`      // recipient (the announcement anchor row = 0)
	Type      string     `gorm:"size:16;index;not null" json:"type"` // announcement/expiry/traffic/system
	Title     string     `gorm:"size:128;not null" json:"title"`
	Body      string     `gorm:"size:512" json:"body,omitempty"`
	ReadAt    *time.Time `json:"read_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
	// Announcement window and outreach (P2-5): all ordinary notification rows are zero values. The anchor row = one site-level
	// entry (user_id=0, the RSS data source); user rows link to the anchor via AnchorID for statistics backflow.
	StartsAt *time.Time `json:"starts_at,omitempty"`              // scheduled publish (anchor row; no fan-out before the time, no RSS either)
	EndsAt   *time.Time `json:"ends_at,omitempty"`                // scheduled takedown (after the time the RSS and the panel list no longer show it, but the row is kept)
	FanoutAt *time.Time `json:"fanout_at,omitempty"`              // user fan-out completion time (empty while a scheduled row awaits fan-out)
	AnchorID int64      `gorm:"index" json:"anchor_id,omitempty"` // the id of the owning announcement anchor row (user rows > 0)
	ResendAt *time.Time `json:"resend_at,omitempty"`              // last external-channel resend (anchor row, the rate-limiting basis)
	// Audience (operations design §1.1 "all / groups"): the anchor row records the audience string chosen at publish, and
	// both fan-out and external-channel resend filter enabled users by it (all=every enabled user; expiring:N=N days
	// until expiry; the list is as of the fan-out time - scheduled announcements are resolved only when due); ordinary rows are zero values.
	Audience string `gorm:"size:32;default:'all'" json:"audience,omitempty"`
}

// Notification types (NT-1/NT-2): announcement fans out; expiry/traffic are triggered by the
// scheduled scan of expiry and the traffic threshold; system is event-triggered (e.g. a grant landing).
const (
	NotifAnnouncement = "announcement"
	NotifExpiry       = "expiry"
	NotifTraffic      = "traffic"
	NotifSystem       = "system"
)

// QuotaAction is one quota-linkage trail (SAVE-6): after a user's traffic / cost exceeds the threshold the subscription
// auto-downgrades (only low-cost tier entries are emitted), released_at empty = the downgrade is in effect; the threshold fields are a snapshot
// of the trigger moment, and a downgraded subscription filters by the snapshot tier lines, so a later settings change does not affect rows already in effect.
type QuotaAction struct {
	ID            uint       `gorm:"primaryKey" json:"id"`
	UserID        uint       `gorm:"index;not null" json:"user_id"`
	Trigger       string     `gorm:"size:16;not null" json:"trigger"`  // traffic/cost
	UsedBytes     int64      `gorm:"default:0" json:"used_bytes"`      // usage-snapshot at trigger time
	QuotaBytes    int64      `gorm:"default:0" json:"quota_bytes"`     // personal quota snapshot at trigger time (traffic tier)
	CostCents     int64      `gorm:"default:0" json:"cost_cents"`      // converted-cost snapshot at trigger time (cents, cost tier)
	MaxPriceCents int64      `gorm:"default:0" json:"max_price_cents"` // downgrade cost-tier line snapshot (cents/GB)
	Reason        string     `gorm:"size:255" json:"reason"`
	CreatedAt     time.Time  `gorm:"not null" json:"created_at"`
	ReleasedAt    *time.Time `json:"released_at"`
	ReleaseReason string     `gorm:"size:64" json:"release_reason"`
}

// Event is one event outbox row (HERALD-1, "alert channel design" §4/§5): the event-producing side returns immediately on
// persisting, and delivery is retried asynchronously by herald.Loop, with channel failures never dropped silently.
type Event struct {
	ID            int64      `gorm:"primaryKey" json:"id"`
	Kind          string     `gorm:"size:32;index;not null" json:"kind"` // region_fault/node_blocked/cert_expiring/... (§2 list)
	Severity      string     `gorm:"size:16;not null" json:"severity"`   // critical/warning/info
	Title         string     `gorm:"size:255;not null" json:"title"`
	Body          string     `gorm:"size:1024" json:"body,omitempty"`
	Target        string     `gorm:"size:32;not null" json:"target"` // admin / user:<id>
	DedupKey      string     `gorm:"size:128" json:"dedup_key,omitempty"`
	Meta          string     `gorm:"type:text" json:"meta,omitempty"`                      // JSON
	Status        string     `gorm:"size:16;index;not null;default:pending" json:"status"` // pending/sent/failed
	Attempts      int        `gorm:"default:0" json:"attempts"`
	NextAttemptAt *time.Time `json:"next_attempt_at,omitempty"` // empty = deliverable immediately
	OccurredAt    time.Time  `gorm:"not null" json:"occurred_at"`
	CreatedAt     time.Time  `gorm:"not null" json:"created_at"`
}

// EventDelivery is one delivery trail: the result of every ferry->Herald delivery attempt, and the async channel-dispatch
// receipts from Herald (channel dimension backfilled from HERALD-2 on).
type EventDelivery struct {
	ID      int64     `gorm:"primaryKey" json:"id"`
	EventID int64     `gorm:"index;not null" json:"event_id"`
	Channel string    `gorm:"size:16;not null" json:"channel"` // herald (the ferry->Herald delivery leg) /tg/email/wechat/webhook
	Status  string    `gorm:"size:16;not null" json:"status"`  // sent/failed
	Detail  string    `gorm:"size:512" json:"detail,omitempty"`
	At      time.Time `gorm:"not null" json:"at"`
}

// Backup is one backup trail (panel availability §6, P1): kind=manual is a manual-download
// endpoint archive and scheduled is a periodic-backup Loop archive; path points at the archive file inside the backup directory (an encrypted archive .enc when
// a master key is configured). uploaded is the outbound-slot success mark - only a successful periodic S3
// offload (FERRY_BACKUP_S3_*, off by default) sets it to 1, while a manual archive is always 0.
type Backup struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Kind      string    `gorm:"size:16;index;not null" json:"kind"` // manual / scheduled
	Path      string    `gorm:"size:255" json:"path"`
	SizeBytes int64     `json:"size_bytes"`
	Uploaded  bool      `gorm:"default:false" json:"uploaded"`
	CreatedAt time.Time `gorm:"not null" json:"created_at"`
}

// RecoveryCode is a one-time recovery code for two-factor authentication (security design §1, P1): it stores only the
// sha256 hash, not the plaintext (the plaintext is shown once at bind time), and it is one code one use - a non-empty
// used_at means consumed, and comparison and destruction go in one transaction against replay.
type RecoveryCode struct {
	ID        uint       `gorm:"primaryKey" json:"id"`
	UserID    uint       `gorm:"index;not null" json:"user_id"`
	CodeHash  string     `gorm:"size:64;not null" json:"-"`
	UsedAt    *time.Time `json:"used_at"`
	CreatedAt time.Time  `json:"created_at"`
}

// LoginLog is the admin login audit (security design §1, P1, aligned with the design DDL): time/IP /
// UA / result, queryable in paged form in dash; consecutive failures and new-subnet logins alert via Herald login_alert.
type LoginLog struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Username  string    `gorm:"size:64;index" json:"username"`
	IP        string    `gorm:"size:64" json:"ip"`
	UA        string    `gorm:"size:255" json:"ua"`
	OK        bool      `json:"ok"`
	CreatedAt time.Time `gorm:"index" json:"created_at"` // design DDL idx_login_logs_time
}

// AuditLog is the admin write-operation audit (AU-1, promoted to production by P2-6): who did what to which object when
// (method + path / request-body snapshot sanitized / response success or failure / IP), queryable in paged form in dash;
// retention and rotation are in AU-3 (FERRY_AUDIT_RETENTION_DAYS).
type AuditLog struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	Actor     string    `gorm:"size:128;index" json:"actor"`  // operator name (admin username / API Token subject)
	ActorKind string    `gorm:"size:16" json:"actor_kind"`    // admin | api | distributor | panel
	Action    string    `gorm:"size:255;index" json:"action"` // "PUT /api/users/:id"
	Target    string    `gorm:"size:64" json:"target"`        // object id (the path :id parameter)
	Body      string    `gorm:"size:2048" json:"body"`        // request-body snapshot (sensitive fields sanitized, truncated to 2KB)
	Success   bool      `json:"success"`                      // response <400
	IP        string    `gorm:"size:64" json:"ip"`
	CreatedAt time.Time `gorm:"index" json:"created_at"`
}

// PriceWatch is a price-watch condition (E-32, the plans-and-cost design §4.2 DDL blueprint): it watches one
// price-key of "vendor + region + config tier", and a price drop or a reached target price (target_price>0) alerts via
// Herald price_alert; conditions share the reference-price vocabulary (costref price-table keys).
type PriceWatch struct {
	ID          uint   `gorm:"primaryKey" json:"id"`
	Provider    string `gorm:"size:128;not null;uniqueIndex:idx_price_watch" json:"provider"`
	Region      string `gorm:"size:128;not null;default:'';uniqueIndex:idx_price_watch" json:"region"`
	Spec        string `gorm:"size:128;not null;uniqueIndex:idx_price_watch" json:"spec"`
	TargetPrice int64  `gorm:"default:0" json:"target_price"` // target price (cents/month), 0 = watch price drops only
	// Enabled carries no default tag: default:true would make Create skip zero-value fields,
	// take a column default value, and passing enabled:false at watch creation would be silently flipped back to true.
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// PriceSnapshot is one price-key snapshot of a watch (fetched each scan period from the reference-price table); comparison
// of price drops and reaches reuses it; the design DDL REFERENCES cascade is changed to the application layer: deleting the watch deletes the snapshot too.
type PriceSnapshot struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	WatchID      uint      `gorm:"index;not null" json:"watch_id"`
	MonthlyCents int64     `gorm:"not null" json:"monthly_cents"`
	Source       string    `gorm:"size:64;not null" json:"source"`
	URL          string    `gorm:"size:255" json:"url,omitempty"`
	CapturedAt   time.Time `gorm:"not null;index" json:"captured_at"`
}
