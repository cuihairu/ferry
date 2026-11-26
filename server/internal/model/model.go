// Package model defines the data structures and request/response payloads shared across layers.
package model

import (
	"encoding/json"
	"time"

	"github.com/cuihairu/ferry/server/internal/protocol"
)

// Supported node protocols; the scope convention is in todo P2-7.
// hysteria2 (hy2) is the fifth one: the entry disguise rides QUIC/HTTP/3
// (UDP 443) and the managed kernel is the hysteria2-server binary (open-source
// selection: AGPL-3.0, hosted process-isolated only, its source is never imported).
const (
	ProtoVless       = "vless"
	ProtoVmess       = "vmess"
	ProtoTrojan      = "trojan"
	ProtoShadowsocks = "shadowsocks"
	ProtoHysteria2   = "hysteria2"
)

// ValidProtocol reports whether a protocol is in the supported range (since the
// protocol pluginization it delegates to the registry, so the support surface
// grows automatically with the protocol package's registrations -- no more
// whitelist switch).
func ValidProtocol(p string) bool {
	return protocol.Has(p)
}

// The node persistence structure lives in internal/storage (GORM model); only
// request payloads stay here.

// NodeInput is the request payload for creating/updating a node.
type NodeInput struct {
	Name     string `json:"name"`
	Address  string `json:"address"`
	Port     int    `json:"port"`
	Protocol string `json:"protocol"`
	Config   string `json:"config"`
	Enabled  *bool  `json:"enabled,omitempty"`
	// Maintenance marks a planned window (F-0206): the node stays out of
	// scheduling but keeps reporting; probes still run and alerts are hushed.
	Maintenance *bool `json:"maintenance,omitempty"`
	// Registration metadata (entry and load-balancing design §C.3/C.6): the
	// agent reports initial values at enrollment, the panel may change them and
	// wins; empty string = DB default on create, keep the old value on update.
	Role       string `json:"role,omitempty"`      // entry/landing/both
	Direction  string `json:"direction,omitempty"` // out/in/both
	LineType   string `json:"line_type,omitempty"` // line type, free text
	Region     string `json:"region,omitempty"`    // region, failure aggregation dimension
	City       string `json:"city,omitempty"`
	Datacenter string `json:"datacenter,omitempty"`
	ISP        string `json:"isp,omitempty"` // ISP, failure aggregation dimension
	ASN        string `json:"asn,omitempty"` // autonomous system number (F-0207)
	// ProviderID links the node to its owning cloud Provider (F-0207): nil keeps
	// the current value, 0 clears the link, >0 must reference an existing row.
	ProviderID *uint  `json:"provider_id,omitempty"`
	Transport  string `json:"transport,omitempty"` // tls/quic/ws-tls/ssh
	// Capabilities sets the statically declared capability list (F-0222): nil
	// keeps the current value, a non-nil list replaces it (an empty list falls
	// back to the role-derived default).
	Capabilities []string `json:"capabilities,omitempty"`
	// Business metadata (F-0743): Labels is a custom label list (nil keeps the
	// current value, non-nil replaces); Group is a free business grouping tag.
	Labels []string `json:"labels,omitempty"`
	Group  string   `json:"group,omitempty"`
	// SchedPriority sets the scheduling priority tier (F-1029): lower value =
	// higher priority, 0 = standard. nil keeps the current value (the pointer
	// distinguishes an explicit 0 reset from absent).
	SchedPriority *int `json:"sched_priority,omitempty"`
	// MaxConns caps the node's online connections (F-1065): 0 = unlimited;
	// the sweep skips candidates at or above the cap.
	MaxConns *int `json:"max_conns,omitempty"`
	// MaxStreams caps concurrent relay streams (F-1066): 0 = unlimited; the
	// sweep skips candidates at or above the cap.
	MaxStreams *int `json:"max_streams,omitempty"`
	// CapacityOverrideMbps is the admin capacity override (F-1070): it leads
	// the capacity chain ahead of measurements; 0 = no override.
	CapacityOverrideMbps *int `json:"capacity_override_mbps,omitempty"`
	// PlanID binds the node to one subscription plan (F-1047): 0 = serves
	// every plan. nil keeps the current value.
	PlanID *uint `json:"plan_id,omitempty"`
	// Cost fields (F-0743): editable by the admin, feeding the estimate engine
	// (billing.EstimateNode); pointers distinguish "absent" from "explicitly 0".
	BillingType       string `json:"billing_type,omitempty"`
	TrafficPriceCents *int64 `json:"traffic_price_cents,omitempty"`
	MonthlyCostCents  *int64 `json:"monthly_cost_cents,omitempty"`
	Currency          string `json:"currency,omitempty"`
	CostNote          string `json:"cost_note,omitempty"`
}

// EnrollmentTokenInput is the request payload for issuing a one-time enrollment credential (FERRY-001).
type EnrollmentTokenInput struct {
	Note       string `json:"note,omitempty"`
	TTLMinutes *int   `json:"ttl_minutes,omitempty"` // 1..1440, default 60; the pointer distinguishes an explicit 0 from absent
	// RequireApproval (F-0741): nodes enrolled with this credential start
	// disabled with status "pending" and wait for POST /api/nodes/:id/approve.
	RequireApproval *bool `json:"require_approval,omitempty"`
}

// User is a subscription user.
type User struct {
	ID         int64      `json:"id"`
	Username   string     `json:"username"`
	SubToken   string     `json:"sub_token"` // subscription link token
	QuotaBytes int64      `json:"quota_bytes"`
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	Enabled    bool       `json:"enabled"`
	IsAdmin    bool       `json:"is_admin"`
	Password   string     `json:"password,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

// UserInput is the request payload for creating/updating a user.
type UserInput struct {
	Username   string     `json:"username"`
	QuotaBytes *int64     `json:"quota_bytes,omitempty"`
	ResetCycle *string    `json:"reset_cycle,omitempty"` // traffic reset cycle (P1-4): none/day/week/month
	ExpiresAt  *time.Time `json:"expires_at,omitempty"`
	Enabled    *bool      `json:"enabled,omitempty"`
	// BwUpMbps/BwDownMbps are per-user bandwidth caps (P2-3, 0 = unlimited).
	BwUpMbps   *int `json:"bw_up_mbps,omitempty"`
	BwDownMbps *int `json:"bw_down_mbps,omitempty"`
	// DeviceLimit/IPLimit cap device/IP counts (P2-4, 0 = unlimited). Devices
	// are counted from the X-Client-Hwid the subscription side reports (if the
	// client does not send it, that surface is not enforced).
	DeviceLimit *int `json:"device_limit,omitempty"`
	IPLimit     *int `json:"ip_limit,omitempty"`
	// UseTemplate true applies the default user template at create time (P1-5) to fill absent fields (only effective on create).
	UseTemplate *bool `json:"use_template,omitempty"`
	// ClearExpires true clears the expiry (JSON null and an absent field cannot be told apart, hence the explicit semantics).
	ClearExpires *bool `json:"clear_expire,omitempty"`
}

// TrafficLogInput is one user traffic accounting entry (P0-9); RecordedAt defaults to the server's current time.
type TrafficLogInput struct {
	UserID     uint       `json:"user_id"`
	NodeID     *uint      `json:"node_id,omitempty"`
	RxBytes    int64      `json:"rx_bytes"`
	TxBytes    int64      `json:"tx_bytes"`
	RecordedAt *time.Time `json:"recorded_at,omitempty"`
}

// TrafficLogBatchInput is the batch traffic accounting payload; empty or oversized items are rejected outright.
type TrafficLogBatchInput struct {
	Items []TrafficLogInput `json:"items"`
}

// ---- self-built subscriptions (ferry-sub-filters, 2026-11-21) ----

// SubCreateInput is the request payload for creating a self-built subscription.
// Filter/Config pass through as raw JSON; semantic validation (strict mode:
// unknown field 400) happens on the handler side via sub.ParseFilter/ParseConfig.
type SubCreateInput struct {
	Name   string          `json:"name"`
	Filter json.RawMessage `json:"filter,omitempty"`
	Config json.RawMessage `json:"config,omitempty"`
}

// SubUpdateInput is the request payload for updating a self-built subscription (PATCH; omitted optional fields keep their old value).
type SubUpdateInput struct {
	Name    *string          `json:"name,omitempty"`
	Filter  *json.RawMessage `json:"filter,omitempty"`
	Config  *json.RawMessage `json:"config,omitempty"`
	Enabled *bool            `json:"enabled,omitempty"`
}
