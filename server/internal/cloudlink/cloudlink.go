// Package cloudlink keeps Ferry Node management state in step with the bound
// cloud resource (F-0391~F-0404). Vendor state is mapped onto the Node's own
// scheduling dimensions; a vendor success never claims the Node healthy -- the
// Agent heartbeat, relay config and crash alerts decide that. Every state
// change is written to the audit trail (F-0404).
package cloudlink

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strconv"
	"time"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/oper"
	"github.com/cuihairu/ferry/server/internal/pathhealth"
	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Effect is one cloud state mapped onto Ferry Node management dimensions
// (F-0391). Empty fields mean "leave unchanged": applying an Effect never
// touches a dimension the mapping did not name.
type Effect struct {
	CloudState  string
	Maintenance *bool
	PoolState   string // pool.StateActive / StateStandby / StateSuspended; "" = unchanged
	Enabled     *bool
	Note        *string
	Reason      string
}

func boolPtr(v bool) *bool    { return &v }
func strPtr(v string) *string { return &v }

// Map is the single source of truth for cloud state -> Node state (F-0391~
// F-0397). A running instance does not restore the Node here: availability
// comes only from the Agent checks in CheckNode (F-0393/F-0397).
func Map(state string) Effect {
	switch state {
	case cloudprov.StateRunning:
		return Effect{CloudState: state}
	case cloudprov.StateStarting, cloudprov.StateStopping, cloudprov.StateStopped, cloudprov.StateError:
		return Effect{
			CloudState: state, Maintenance: boolPtr(true),
			PoolState: pool.StateSuspended, Reason: "cloud instance not ready: " + state,
		}
	case cloudprov.StateRebuilding:
		return Effect{
			CloudState: state, Maintenance: boolPtr(true),
			PoolState: pool.StateSuspended,
			Note:      strPtr("cloud instance rebuilt: agent must re-register and revalidate config"),
			Reason:    "cloud instance rebuilding",
		}
	default:
		return Effect{CloudState: state}
	}
}

// MapMissing handles a resource that disappeared from the vendor listing
// (F-0396): the Node is disabled and removed from the pool, but its row,
// binding and history survive.
func MapMissing() Effect {
	return Effect{
		CloudState: "missing", Maintenance: boolPtr(true),
		PoolState: pool.StateSuspended, Enabled: boolPtr(false),
		Note: strPtr("cloud resource missing"), Reason: "cloud resource missing",
	}
}

// MapOperation derives the linkage Effect from a settled operation (F-0398):
// success is resolved by the vendor's real state, while failure / Unknown
// keeps the Node out of scheduling until a controlled refresh proves it live.
func MapOperation(op storage.CloudOperation, vendorState string) Effect {
	switch op.State {
	case oper.StateFailed, oper.StateUnknown:
		return Effect{
			CloudState: vendorState, Maintenance: boolPtr(true),
			PoolState: pool.StateSuspended, Reason: "cloud operation " + op.State,
		}
	default:
		return Map(vendorState)
	}
}

// Apply writes one guarded Effect onto a Node: only changed dimensions are
// written, so repeated applications are no-ops (F-0398).
func Apply(db *gorm.DB, node *storage.Node, eff Effect, now time.Time) (bool, error) {
	updates := map[string]any{}
	if eff.Maintenance != nil && node.Maintenance != *eff.Maintenance {
		updates["maintenance"] = *eff.Maintenance
	}
	if eff.PoolState != "" && node.PoolState != eff.PoolState {
		updates["pool_state"] = eff.PoolState
		updates["pool_reason"] = eff.Reason
		updates["pool_changed_at"] = now
	}
	if eff.Enabled != nil && node.Enabled != *eff.Enabled {
		updates["enabled"] = *eff.Enabled
	}
	if eff.Note != nil && *eff.Note != "" && node.ProvisionNote != *eff.Note {
		updates["provision_note"] = *eff.Note
	}
	if len(updates) == 0 {
		return false, nil
	}
	if err := db.Model(&storage.Node{}).Where("id = ?", node.ID).Updates(updates).Error; err != nil {
		return false, err
	}
	// Keep the in-memory snapshot in step with the row so back-to-back Apply
	// calls within one request remain idempotent (F-0398).
	for k, v := range updates {
		switch k {
		case "maintenance":
			node.Maintenance = v.(bool)
		case "pool_state":
			node.PoolState = v.(string)
		case "pool_reason":
			node.PoolReason = v.(string)
		case "pool_changed_at":
			t, _ := v.(time.Time)
			node.PoolChangedAt = &t
		case "enabled":
			node.Enabled = v.(bool)
		case "provision_note":
			node.ProvisionNote = v.(string)
		}
	}
	return true, nil
}

// secretStore is the minimal credential seam the Service needs.
type secretStore interface {
	Decrypt(cipherText string) (string, error)
}

// Service queries the real vendor state before changing a Node (controlled
// refresh, F-0398) and writes an audit row for each linkage decision.
type Service struct {
	db         *gorm.DB
	secrets    secretStore
	adapterFor func(storage.Provider, string) (cloudprov.Adapter, error)
	window     time.Duration
}

// New builds a Service. A nil adapter factory uses the registered cloudprov
// factory; the heartbeat window defaults to three minutes.
func New(db *gorm.DB, secrets secretStore, adapterFor func(storage.Provider, string) (cloudprov.Adapter, error)) *Service {
	if adapterFor == nil {
		adapterFor = func(p storage.Provider, plain string) (cloudprov.Adapter, error) {
			return cloudprov.NewAdapter(cloudprov.Account{
				ProviderID: p.ID, Name: p.Name, Type: p.Type, AccessKey: plain, Region: p.Region,
			})
		}
	}
	return &Service{db: db, secrets: secrets, adapterFor: adapterFor, window: 3 * time.Minute}
}

// SetWindow overrides the Agent heartbeat freshness window (tests and site
// tuning); non-positive values are ignored.
func (s *Service) SetWindow(d time.Duration) {
	if d > 0 {
		s.window = d
	}
}

// ErrNoNode means the resource is not bound to a Ferry Node.
var ErrNoNode = errors.New("cloud resource is not bound to a node")

// RefreshAndApply is the controlled refresh for one resource (F-0398): query
// the vendor's real state, map it, apply it to the bound Node and record the
// decision. A vendor outage is returned without changing Node state.
func (s *Service) RefreshAndApply(ctx context.Context, providerID, resourceID uint, now time.Time) (Effect, bool, error) {
	var prov storage.Provider
	if err := s.db.First(&prov, providerID).Error; err != nil {
		return Effect{}, false, err
	}
	var res storage.CloudResource
	if err := s.db.First(&res, resourceID).Error; err != nil {
		return Effect{}, false, err
	}
	if res.ProviderID != prov.ID {
		return Effect{}, false, errors.New("resource belongs to another provider")
	}
	adapter, err := s.adapter(prov)
	if err != nil {
		return Effect{}, false, err
	}
	state, err := adapter.RefreshStatus(ctx, res.ExternalID)
	eff := Map(state)
	if err != nil {
		if !isNotFound(err) {
			return Effect{}, false, err
		}
		eff = MapMissing()
		if err := s.markResourceMissing(&res, now); err != nil {
			return eff, false, err
		}
	} else if err := s.db.Model(&storage.CloudResource{}).Where("id = ?", res.ID).
		Updates(map[string]any{"state": state, "last_seen_at": now, "last_sync_at": now}).Error; err != nil {
		return eff, false, err
	}
	changed, err := s.apply(&res, eff, now, "cloudlink: refresh status")
	return eff, changed, err
}

// ApplyOperation links a settled cloud operation to its Node (F-0398): a
// successful op is resolved by querying the vendor, failures and Unknown are
// applied conservatively without trusting the vendor call.
func (s *Service) ApplyOperation(ctx context.Context, operationID uint, now time.Time) (Effect, bool, error) {
	var op storage.CloudOperation
	if err := s.db.First(&op, operationID).Error; err != nil {
		return Effect{}, false, err
	}
	if !oper.IsTerminal(op.State) {
		return Effect{}, false, nil
	}
	eff := MapOperation(op, "")
	if op.State == oper.StateSucceeded {
		var prov storage.Provider
		if err := s.db.First(&prov, op.ProviderID).Error; err != nil {
			return eff, false, err
		}
		var res storage.CloudResource
		if err := s.db.First(&res, op.ResourceID).Error; err != nil {
			return eff, false, err
		}
		adapter, err := s.adapter(prov)
		if err != nil {
			return eff, false, err
		}
		state, qerr := adapter.RefreshStatus(ctx, res.ExternalID)
		if qerr != nil && !isNotFound(qerr) {
			// Query failed: keep the Node out of scheduling, never assume the
			// operation made it healthy (F-0397/F-0398).
			if changed, aerr := s.applyResource(&res, eff, now, "cloudlink: operation query failed"); aerr != nil {
				return eff, changed, aerr
			} else {
				return eff, changed, qerr
			}
		}
		if qerr != nil {
			eff = MapMissing()
			if err := s.markResourceMissing(&res, now); err != nil {
				return eff, false, err
			}
		} else {
			eff = Map(state)
			if err := s.db.Model(&storage.CloudResource{}).Where("id = ?", res.ID).
				Updates(map[string]any{"state": state, "last_seen_at": now, "last_sync_at": now}).Error; err != nil {
				return eff, false, err
			}
		}
		changed, err := s.applyResourceByID(op.ResourceID, eff, now, "cloudlink: operation succeeded")
		return eff, changed, err
	}
	changed, err := s.applyResourceByID(op.ResourceID, eff, now, "cloudlink: operation "+op.State)
	return eff, changed, err
}

func (s *Service) adapter(prov storage.Provider) (cloudprov.Adapter, error) {
	plain, err := s.secrets.Decrypt(prov.AccessKey)
	if err != nil {
		return nil, fmt.Errorf("credential unreadable: %w", err)
	}
	return s.adapterFor(prov, plain)
}

func (s *Service) apply(res *storage.CloudResource, eff Effect, now time.Time, reason string) (bool, error) {
	if res.NodeID == nil {
		return false, ErrNoNode
	}
	return s.applyResource(res, eff, now, reason)
}

func (s *Service) applyResourceByID(resourceID uint, eff Effect, now time.Time, reason string) (bool, error) {
	var res storage.CloudResource
	if err := s.db.First(&res, resourceID).Error; err != nil {
		return false, err
	}
	if res.NodeID == nil {
		return false, ErrNoNode
	}
	return s.applyResource(&res, eff, now, reason)
}

func (s *Service) applyResource(res *storage.CloudResource, eff Effect, now time.Time, reason string) (bool, error) {
	if res.NodeID == nil {
		return false, ErrNoNode
	}
	var node storage.Node
	if err := s.db.First(&node, *res.NodeID).Error; err != nil {
		return false, err
	}
	changed, err := Apply(s.db, &node, eff, now)
	if err != nil || !changed {
		return changed, err
	}
	// Missing resources stay flagged across linkage sweeps (F-0396); the
	// cloudsync marker is the same soft marker used by the sync engine.
	if eff.CloudState == "missing" {
		updates := map[string]any{"missing": true}
		if res.MissingSince == nil {
			updates["missing_since"] = now
		}
		if err := s.db.Model(&storage.CloudResource{}).Where("id = ?", res.ID).Updates(updates).Error; err != nil {
			return changed, err
		}
	}
	row := storage.AuditLog{
		Actor: "cloudlink", ActorKind: "system", Action: "cloud_node_link",
		Target: strconv.FormatUint(uint64(node.ID), 10),
		Body: fmt.Sprintf("resource=%d provider=%d cloud_state=%q maintenance=%v pool_state=%s enabled=%v reason=%q",
			res.ID, res.ProviderID, eff.CloudState, node.Maintenance, node.PoolState, node.Enabled, reason),
		Success: true, CreatedAt: now,
	}
	return true, s.db.Create(&row).Error
}

// markResourceMissing flags the CloudResource itself so the soft marker
// survives later linkage sweeps (F-0396); the cloudsync engine clears it once
// the resource reappears in the vendor listing.
func (s *Service) markResourceMissing(res *storage.CloudResource, now time.Time) error {
	updates := map[string]any{"missing": true}
	if res.MissingSince == nil {
		updates["missing_since"] = now
	}
	if err := s.db.Model(&storage.CloudResource{}).Where("id = ?", res.ID).Updates(updates).Error; err != nil {
		return err
	}
	res.Missing = true
	if res.MissingSince == nil {
		t := now
		res.MissingSince = &t
	}
	return nil
}

func isNotFound(err error) bool {
	var ce *cloudprov.Error
	return errors.As(err, &ce) && ce.Type == cloudprov.ErrNotFound
}

// Health is the post-boot Node verdict (F-0393/F-0394/F-0397): a fresh Agent
// heartbeat, online status, a validated relay config and no active crash
// alert. Absolutely no cloud state participates in the verdict.
type Health struct {
	AgentOnline    bool
	HeartbeatFresh bool
	ConfigValid    bool
	CrashAlert     bool
	Healthy        bool
}

// CheckNode reads the Agent and config signals for one Node (F-0393/F-0394).
func (s *Service) CheckNode(nodeID uint, now time.Time) (Health, error) {
	var node storage.Node
	if err := s.db.First(&node, nodeID).Error; err != nil {
		return Health{}, err
	}
	h := Health{AgentOnline: node.Status == "online"}
	if node.LastSeen != nil && !node.LastSeen.After(now) && now.Sub(*node.LastSeen) <= s.window {
		h.HeartbeatFresh = true
	}
	var cfg storage.NodeConfig
	if err := s.db.Where("node_id = ? AND proc = ?", nodeID, "relay").
		Order("id DESC").First(&cfg).Error; err == nil {
		h.ConfigValid = cfg.Status == "applied" && cfg.Validated && !cfg.Reverted
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return h, err
	}
	var crashes int64
	if err := s.db.Model(&storage.Alert{}).
		Where("node_id = ? AND kind = ? AND state = ?", nodeID, "proc_crash", "active").
		Count(&crashes).Error; err != nil {
		return h, err
	}
	h.CrashAlert = crashes > 0
	h.Healthy = h.AgentOnline && h.HeartbeatFresh && h.ConfigValid && !h.CrashAlert
	return h, nil
}

// Restore lifts a Node back into the pool after a boot (F-0393/F-0402) only
// when the Agent checks pass; otherwise it returns the failed health verdict
// and leaves the Node unschedulable.
func (s *Service) Restore(nodeID uint, now time.Time) (Health, error) {
	h, err := s.CheckNode(nodeID, now)
	if err != nil || !h.Healthy {
		return h, err
	}
	var node storage.Node
	if err := s.db.First(&node, nodeID).Error; err != nil {
		return h, err
	}
	changed, err := Apply(s.db, &node, Effect{
		Maintenance: boolPtr(false), PoolState: pool.StateActive,
		Note: strPtr(""), Reason: "cloud boot: agent healthy",
	}, now)
	if err != nil {
		return h, err
	}
	if changed {
		if err := s.auditNode(nodeID, "cloud_node_restore",
			fmt.Sprintf("agent healthy heartbeat=%v config=%v", h.HeartbeatFresh, h.ConfigValid), now); err != nil {
			return h, err
		}
	}
	return h, nil
}

// AfterBoot checks a Node after a successful boot (F-0402) and emits one
// deduplicated alert when the Agent did not come back (F-0403). A healthy
// verdict restores scheduling; a failed one leaves the Node out of the pool.
func (s *Service) AfterBoot(nodeID uint, now time.Time) (Health, error) {
	h, err := s.Restore(nodeID, now)
	if err != nil || h.Healthy {
		return h, err
	}
	_, err = herald.Emit(s.db, herald.EmitInput{
		Kind: herald.KindCloudBootFailed, Severity: herald.SeverityWarning,
		Title: fmt.Sprintf("节点 %d 云主机开机后未恢复", nodeID),
		Body: fmt.Sprintf("agent_online=%v heartbeat_fresh=%v config_valid=%v crash_alert=%v",
			h.AgentOnline, h.HeartbeatFresh, h.ConfigValid, h.CrashAlert),
		Target:     herald.TargetAdmin,
		DedupKey:   fmt.Sprintf("node:%d:cloud_boot_failed", nodeID),
		Meta:       map[string]any{"node_id": nodeID, "health": h},
		OccurredAt: now,
	})
	return h, err
}

// Preflight is the pre-shutdown risk check (F-0400): recent user traffic on
// the Node makes a power-off risky, and the caller must warn the operator.
type Preflight struct {
	NodeID      uint
	Risk        bool
	RecentConns int
	Message     string
}

// CheckShutdownRisk counts recent node traffic (F-0400). A Node with user
// traffic must be drained through a maintenance window before shutdown.
func (s *Service) CheckShutdownRisk(nodeID uint, now time.Time, window time.Duration) (Preflight, error) {
	if window <= 0 {
		window = 10 * time.Minute
	}
	out := Preflight{NodeID: nodeID}
	// Conn counters are optional in heartbeat traffic rows, so a recent
	// byte-bearing row is already evidence of user traffic (F-0400).
	var rows []storage.NodeTrafficLog
	if err := s.db.Where("node_id = ? AND recorded_at >= ? AND (rx_bytes > 0 OR tx_bytes > 0)",
		nodeID, now.Add(-window)).Find(&rows).Error; err != nil {
		return out, err
	}
	for _, row := range rows {
		if row.Conns > 0 {
			out.RecentConns += row.Conns
		} else {
			out.RecentConns++
		}
	}
	out.Risk = len(rows) > 0
	if out.Risk {
		out.Message = "node has recent user traffic; drain before shutdown"
	}
	return out, nil
}

// Drain starts a maintenance window (F-0401): the Node stays online and
// probed, but leaves subscriptions and allocation immediately (standby pool
// state) before a shutdown operation is submitted.
func (s *Service) Drain(nodeID uint, reason string, now time.Time) error {
	var node storage.Node
	if err := s.db.First(&node, nodeID).Error; err != nil {
		return err
	}
	if reason == "" {
		reason = "cloud maintenance window"
	}
	changed, err := Apply(s.db, &node, Effect{
		Maintenance: boolPtr(true), PoolState: pool.StateStandby,
		Note: strPtr(reason), Reason: reason,
	}, now)
	if err != nil || !changed {
		return err
	}
	return s.auditNode(nodeID, "cloud_node_drain", reason, now)
}

// PathHealth reports the scheduling health of every Path touching the Node
// (F-0399): the same pathhealth verdict the subscription and dash surfaces
// use, so cloud linkage and path scheduling never disagree.
func (s *Service) PathHealth(nodeID uint, now time.Time) (map[uint]string, error) {
	var paths []storage.Path
	if err := s.db.Where("entry_node_id = ? OR landing_node_id = ? OR relay_node_id = ?",
		nodeID, nodeID, nodeID).Find(&paths).Error; err != nil {
		return nil, err
	}
	return pathhealth.Evaluate(s.db, paths, now), nil
}

func (s *Service) auditNode(nodeID uint, action, body string, now time.Time) error {
	row := storage.AuditLog{
		Actor: "cloudlink", ActorKind: "system", Action: action,
		Target: strconv.FormatUint(uint64(nodeID), 10), Body: body,
		Success: true, CreatedAt: now,
	}
	return s.db.Create(&row).Error
}

// Loop periodically refreshes every bound cloud resource through the vendor
// query (F-0398); failures on one resource do not block the rest.
func Loop(ctx context.Context, db *gorm.DB, secrets secretStore, interval time.Duration, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	svc := New(db, secrets, nil)
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			var rows []storage.CloudResource
			if err := db.Where("node_id IS NOT NULL").Find(&rows).Error; err != nil {
				logger.Printf("cloudlink sweep: %v", err)
				continue
			}
			for _, res := range rows {
				if _, _, err := svc.RefreshAndApply(ctx, res.ProviderID, res.ID, time.Now()); err != nil {
					logger.Printf("cloudlink resource=%d: %v", res.ID, err)
				}
			}
		}
	}
}
