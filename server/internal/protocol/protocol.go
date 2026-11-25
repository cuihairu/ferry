// Package protocol is the protocol plugin registry (2026-11-24 protocol
// pluginization refactor): all the knowledge of a protocol (display name /
// default port / field declarations / four render hooks) lives in one
// declarative descriptor, one file per protocol registering in init; the three
// subscription formatters and the inbound kernel config are fully data-driven,
// no more switch. Adding a protocol = one new definition file + one register
// line, the core package untouched -- that is the acceptance line.
//
// A render hook returning ErrNotSupported means the protocol does not support
// that format (e.g. wireguard has no share link); callers handle it per
// surface: subscription packing skips the entry, single-point sharing / kernel
// config generation report the error explicitly. A registry.Lookup miss
// (unregistered protocol) is a different matter: a legacy dirty-data scenario,
// reported explicitly rather than silenced (same convention as the pre-refactor
// "unsupported protocol").
package protocol

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// ErrNotSupported means the protocol does not support that render format (subscription packing handles it by skipping).
var ErrNotSupported = errors.New("protocol: format not supported")

// FieldKind declares the type of a field value (for frontend form rendering and documentation; subscription rendering does not force-convert on it).
type FieldKind string

// Supported field kinds: string / bool / int / enum.
const (
	KindString FieldKind = "string"
	KindBool   FieldKind = "bool"
	KindInt    FieldKind = "int"
	KindEnum   FieldKind = "enum"
)

// Field declares one config field of a protocol: name / kind / required /
// enum / default / description. Required-field validation is executed uniformly
// by registry.Validate (intercepted before rendering); the frontend form is
// rendered dynamically from GET /api/protocols, no more scattered hand-writing.
type Field struct {
	Name     string    `json:"name"`
	Kind     FieldKind `json:"kind"`
	Required bool      `json:"required"`
	// Group binds several required fields into a joint validation: any missing
	// one reports a single joint error "<proto> requires config.a and
	// config.b" (shadowsocks puts method+password in one group). An empty group
	// means independent validation.
	Group string   `json:"group,omitempty"`
	Enum  []string `json:"enum,omitempty"`
	// Default is the documented default value of the field (for example configs / form prefill); nil means no default.
	Default any    `json:"default,omitempty"`
	Desc    string `json:"desc,omitempty"`
}

// Protocol describes all the knowledge of one protocol. A nil render hook is
// equivalent to returning ErrNotSupported; hooks are set at registration (init)
// and read-only afterwards.
type Protocol struct {
	Name        string  `json:"name"`         // "vless" / "tuic" / ...
	DisplayName string  `json:"display_name"` // frontend display name
	DefaultPort int     `json:"default_port"`
	Fields      []Field `json:"fields"`

	// KernelProc/KernelKind is the managed kernel process mapping (the
	// data-driven replacement for the provisioning pipeline's procForProtocol):
	// vless family -> xray, hy2 -> hysteria2, the new QUIC-family protocols ->
	// sing-box; empty means fall back to xray (legacy defensive convention).
	KernelProc string `json:"kernel_proc,omitempty"`
	KernelKind string `json:"kernel_kind,omitempty"`

	// RenderLink produces the v2ray-family share link (data source of shareLink / PackV2Ray).
	RenderLink func(rc *RenderCtx) (string, error)
	// RenderClash produces the mihomo/clash proxy entry: the five legacy
	// protocols return a ClashProxy (struct order guarantees YAML byte
	// compatibility), new protocols may return map[string]any.
	RenderClash func(rc *RenderCtx) (any, error)
	// RenderSingbox produces the sing-box outbound object (subscription singbox
	// format); a protocol only available in endpoint form (wireguard, whose
	// outbound was removed in 1.14) returns ErrNotSupported, and the kernel
	// config surface is taken over by RenderSingboxEndpoint.
	RenderSingbox func(rc *RenderCtx) (map[string]any, error)
	// RenderSingboxEndpoint produces a sing-box endpoints entry (the new wireguard form).
	RenderSingboxEndpoint func(rc *RenderCtx) (map[string]any, error)
	// RenderXray produces an Xray-core outbound object (subscription xray-json
	// format, G1): the protocol's own outbound schema plus the shared stream
	// settings. Protocols Xray has no outbound for (hysteria2, tuic, wireguard)
	// return ErrNotSupported, and the subscription packing surface skips them.
	RenderXray func(rc *RenderCtx) (map[string]any, error)
	// RenderSingboxInbound produces the server-side sing-box inbound object
	// (agent kernel config surface): TLS-family protocols need
	// rc.CertPath/KeyPath, and a missing path is reported rather than silenced.
	RenderSingboxInbound func(rc *RenderCtx) (map[string]any, error)
}

// RenderCtx is the full input of one render: the node itself + the parsed core
// config + protocol extension fields + remark override. When Remark is empty the
// caller fills it in per the remark rules before passing it in.
type RenderCtx struct {
	Node   storage.Node
	Config NodeConfig
	// Extra holds the free-form fields of the node config JSON beyond the core
	// NodeConfig (tuic's congestion_control, wireguard's key pair, etc.), used
	// as each protocol needs.
	Extra  map[string]any
	Remark string
	// Inbound render parameters (RenderSingboxInbound): listen address and TLS
	// certificate paths, given by the deployment surface / validation tests;
	// an empty Listen means 127.0.0.1.
	Listen   string
	CertPath string
	KeyPath  string
}

// Str/Bool/Int read protocol extension fields from Extra; they default to the
// zero value (both a missing key and a wrong type are non-fatal here, the
// required-field validation of the field declarations is intercepted uniformly in
// registry.Validate).
func (rc *RenderCtx) Str(key string) string {
	if rc.Extra == nil {
		return ""
	}
	s, _ := rc.Extra[key].(string)
	return s
}

func (rc *RenderCtx) Bool(key string) bool {
	if rc.Extra == nil {
		return false
	}
	b, _ := rc.Extra[key].(bool)
	return b
}

func (rc *RenderCtx) Int(key string) int {
	if rc.Extra == nil {
		return 0
	}
	switch v := rc.Extra[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	}
	return 0
}

// Strs reads a string array from Extra (alpn, the wireguard address list, etc.).
func (rc *RenderCtx) Strs(key string) []string {
	if rc.Extra == nil {
		return nil
	}
	switch v := rc.Extra[key].(type) {
	case []string:
		return v
	case []any:
		out := make([]string, 0, len(v))
		for _, it := range v {
			if s, ok := it.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// NodeConfig is the core shape of the node config template (moved in from sub,
// the protocol-agnostic part): {"uuid","password","method","tls","sni","host",
// "path","net","aid","scy","flow"}; hy2 appends {"obfs","obfs_password",
// "insecure","up","down"}. Protocol extension fields go through RenderCtx.Extra
// and are no longer added to the core struct.
type NodeConfig struct {
	UUID         string `json:"uuid"`
	Password     string `json:"password"`
	Method       string `json:"method"`
	TLS          bool   `json:"tls"`
	SNI          string `json:"sni"`
	Host         string `json:"host"`
	Path         string `json:"path"`
	Net          string `json:"net"`
	AID          int    `json:"aid"`
	Scy          string `json:"scy"`
	Flow         string `json:"flow"`
	Obfs         string `json:"obfs"`
	ObfsPassword string `json:"obfs_password"`
	Insecure     bool   `json:"insecure"`
	Up           int    `json:"up"`
	Down         int    `json:"down"`
}

// ParseConfig parses the core part of the node config template; empty / {}
// yields the zero value. A wrong type or non-JSON input is reported explicitly
// (same convention as the pre-refactor sub.parseConfig, and the error string
// keeps the "parse node config" prefix for tests and the upstream surface to match).
func ParseConfig(raw string) (NodeConfig, error) {
	var cfg NodeConfig
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "{}" {
		return cfg, nil
	}
	if err := json.Unmarshal([]byte(trimmed), &cfg); err != nil {
		return cfg, fmt.Errorf("parse node config: %w", err)
	}
	return cfg, nil
}

// ParseExtra parses the free-field surface of the same config JSON (the data
// source of protocol extension fields); core field errors are already
// intercepted in ParseConfig, and a failure here also reports a parse-prefixed
// error.
func ParseExtra(raw string) (map[string]any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" || trimmed == "{}" {
		return nil, nil
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(trimmed), &m); err != nil {
		return nil, fmt.Errorf("parse node config: %w", err)
	}
	return m, nil
}

// fieldMissing decides the "missing" state of a required field: an empty string,
// a false bool and a zero int all count as missing (a required bool/int is only
// declared when the protocol genuinely needs a non-zero default, e.g. shadowtls version).
func fieldMissing(rc *RenderCtx, f Field) bool {
	var val any
	switch f.Name {
	case "uuid":
		val = rc.Config.UUID
	case "password":
		val = rc.Config.Password
	case "method":
		val = rc.Config.Method
	case "tls":
		val = rc.Config.TLS
	case "sni":
		val = rc.Config.SNI
	default:
		if rc.Extra == nil {
			return true
		}
		v, ok := rc.Extra[f.Name]
		if !ok {
			return true
		}
		val = v
	}
	switch v := val.(type) {
	case string:
		return v == ""
	case bool:
		return !v
	case int:
		return v == 0
	case float64:
		return v == 0
	case nil:
		return true
	}
	return false
}

// fieldGroup validates one required group (a single-field group = independent
// validation): any missing field reports a joint error whose message lists them
// in declaration order, "a and b" -- the shadowsocks-side test matches with
// contains on "requires config.method", which the joint message's leading part
// satisfies, so the compatibility holds.
func fieldGroup(p *Protocol, rc *RenderCtx, fields []Field) error {
	missing := make([]string, 0, len(fields))
	for _, f := range fields {
		if fieldMissing(rc, f) {
			missing = append(missing, f.Name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	if len(missing) == 1 {
		return fmt.Errorf("%s requires config.%s", p.Name, missing[0])
	}
	// A joint miss and a partial miss both report the whole group (the same
	// wording as the pre-refactor shadowsocks message
	// "requires config.method and config.password").
	names := make([]string, 0, len(fields))
	for _, f := range fields {
		names = append(names, "config."+f.Name)
	}
	return fmt.Errorf("%s requires %s", p.Name, strings.Join(names, " and "))
}

// Validate runs the required-field validation uniformly from the field
// declarations (intercepted before any render hook is called, shared by the
// four formats): fields not declared Required are not validated; Required
// fields with the same Group are merged into one joint validation.
func Validate(p *Protocol, rc *RenderCtx) error {
	groups := map[string][]Field{}
	var order []string
	for _, f := range p.Fields {
		if !f.Required {
			continue
		}
		g := f.Group
		if _, ok := groups[g]; !ok {
			order = append(order, g)
		}
		groups[g] = append(groups[g], f)
	}
	for _, g := range order {
		if err := fieldGroup(p, rc, groups[g]); err != nil {
			return err
		}
	}
	return nil
}

// ExampleConfig builds an example config JSON from the field default values (for
// frontend form prefill and documentation); a protocol with no default fields
// returns "{}".
func (p *Protocol) ExampleConfig() string {
	m := map[string]any{}
	for _, f := range p.Fields {
		if f.Default != nil {
			m[f.Name] = f.Default
		}
	}
	if len(m) == 0 {
		return "{}"
	}
	b, err := json.Marshal(m)
	if err != nil {
		return "{}"
	}
	return string(b)
}
