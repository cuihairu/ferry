// Package oper is the cloud operation task system (F-0361~F-0384): a unified
// task table with a six-state machine, idempotency keys, pre-operation checks
// (current resource state, admin permission and confirmation for high-risk
// ops), an async executor that never blocks on the vendor, and Unknown-state
// reconciliation by querying the vendor instead of blindly retrying. Batch
// operations fan out into per-resource rows under one BatchID and record
// every result (F-0377~F-0383).
package oper

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// Task states (F-0362). Unknown is the post-timeout state: the vendor may or
// may not have applied the operation, so the reconciler queries it (F-0373).
const (
	StatePending   = "pending"
	StateRunning   = "running"
	StateSucceeded = "succeeded"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
	StateUnknown   = "unknown"
)

var terminal = map[string]bool{StateSucceeded: true, StateFailed: true, StateCancelled: true}

// HighRiskOps require a second confirmation at the request boundary (F-0370).
var HighRiskOps = map[string]bool{
	cloudprov.OpPowerOff: true, cloudprov.OpForceStop: true, cloudprov.OpRebuild: true,
}

// ErrConfirmationRequired and ErrNotFound are the two validation sentinels
// handlers branch on for 409/404 responses (F-0370/F-0368).
var (
	ErrConfirmationRequired = errors.New("high-risk operation requires confirm=true")
	ErrNotFound             = errors.New("resource not found")
)

// IsTerminal reports whether the state machine has ended.
func IsTerminal(state string) bool { return terminal[state] }

// Cancelable reports whether a state may be cancelled (F-0374): vendor
// capability and non-terminal state both matter.
func Cancelable(state string, allowCancel bool) bool {
	return !terminal[state] && allowCancel
}

// OpSpec describes one resource in a batch request (F-0377~F-0382).
type OpSpec struct {
	ResourceID uint           `json:"resource_id"`
	Params     map[string]any `json:"params,omitempty"`
}

// Request is one API submission; specs of length one are a single operation,
// more than one is a batch (F-0377).
type Request struct {
	ProviderID uint     `json:"provider_id"`
	Op         string   `json:"op"`
	Specs      []OpSpec `json:"specs"`
	// Confirmed is the second confirmation for high-risk ops (F-0370).
	Confirmed bool `json:"confirmed,omitempty"`
	// IdempotencyKey lets a retried submission reuse the original rows (F-0371);
	// derived from the request when the caller omits it.
	IdempotencyKey string `json:"idempotency_key,omitempty"`
}

// Service is the task system (F-0361). The optional adapterFor seam builds an
// Adapter for an account; production uses the registered cloudprov factory.
type Service struct {
	db          *gorm.DB
	secret      *secret.Store
	adapterFor  func(storage.Provider, string) (cloudprov.Adapter, error)
	concurrency int
}

// New builds a Service with the batch concurrency cap (F-0381).
func New(db *gorm.DB, sec *secret.Store) *Service {
	s := &Service{db: db, secret: sec, concurrency: 4}
	s.adapterFor = func(p storage.Provider, plain string) (cloudprov.Adapter, error) {
		return cloudprov.NewAdapter(cloudprov.Account{
			ProviderID: p.ID, Name: p.Name, Type: p.Type, AccessKey: plain, Region: p.Region,
		})
	}
	return s
}

// deriveIdem hashes the request into a stable key (F-0371).
func deriveIdem(prefix string, req Request) string {
	b, _ := json.Marshal([]any{prefix, req.ProviderID, req.Op, req.Specs})
	sum := sha256.Sum256(b)
	return prefix + "-" + hex.EncodeToString(sum[:12])
}

// Create validates the request, resolves resources, pre-checks state (F-0368)
// and capabilities, requires confirmation for high-risk ops (F-0370), and
// persists one idempotent task row per resource (F-0371/F-0380). A duplicate
// idempotency key returns the existing rows, not a second execution.
func (s *Service) Create(ctx context.Context, req Request, actor, actorKind string, now time.Time) (uint, []storage.CloudOperation, error) {
	if req.ProviderID == 0 {
		return 0, nil, errors.New("provider_id is required")
	}
	if !validOp(req.Op) {
		return 0, nil, fmt.Errorf("unsupported operation %q", req.Op)
	}
	if len(req.Specs) == 0 {
		return 0, nil, errors.New("specs is required")
	}
	if HighRiskOps[req.Op] && !req.Confirmed {
		return 0, nil, ErrConfirmationRequired
	}
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = deriveIdem("idem", req)
	}
	// Idempotent replay: same key returns the original rows (F-0371).
	var existing []storage.CloudOperation
	if err := s.db.Where("idempotency_key LIKE ?", req.IdempotencyKey+"%").
		Order("id ASC").Find(&existing).Error; err != nil {
		return 0, nil, err
	}
	if len(existing) > 0 {
		return existing[0].ID, existing, nil
	}
	var prov storage.Provider
	if err := s.db.First(&prov, req.ProviderID).Error; err != nil {
		return 0, nil, ErrNotFound
	}
	if !prov.Enabled {
		return 0, nil, errors.New("provider disabled")
	}
	plain, err := s.secret.Decrypt(prov.AccessKey)
	if err != nil {
		return 0, nil, fmt.Errorf("credential unreadable: %w", err)
	}
	adapter, err := s.adapterFor(prov, plain)
	if err != nil {
		return 0, nil, fmt.Errorf("adapter init: %w", err)
	}
	caps, err := adapter.Capabilities(ctx)
	if err != nil {
		return 0, nil, fmt.Errorf("capability probe: %w", err)
	}
	allowed, allowCancel := capabilityFor(req.Op, caps)
	if !allowed {
		return 0, nil, fmt.Errorf("%s is not supported by this provider", req.Op)
	}
	batchID := ""
	if len(req.Specs) > 1 {
		batchID = "batch-" + req.IdempotencyKey
	}
	rows := make([]storage.CloudOperation, 0, len(req.Specs))
	for _, spec := range req.Specs {
		var res storage.CloudResource
		if err := s.db.First(&res, spec.ResourceID).Error; err != nil {
			return 0, nil, fmt.Errorf("%w: %d", ErrNotFound, spec.ResourceID)
		}
		if res.ProviderID != req.ProviderID {
			return 0, nil, fmt.Errorf("resource %d belongs to another provider", res.ID)
		}
		if res.Missing {
			return 0, nil, fmt.Errorf("resource %d is gone from the provider", res.ID)
		}
		if !stateReady(req.Op, res.State) {
			return 0, nil, fmt.Errorf("resource %d is not in a state for %s (%s)", res.ID, req.Op, res.State)
		}
		params, _ := json.Marshal(spec.Params)
		rows = append(rows, storage.CloudOperation{
			ResourceID: res.ID, BatchID: batchID, ProviderID: req.ProviderID,
			Op: req.Op, Params: string(params), State: StatePending,
			IdempotencyKey: fmt.Sprintf("%s:%d", req.IdempotencyKey, res.ID),
			Actor:          actor, ActorKind: actorKind,
			Confirmed: req.Confirmed, AllowCancel: allowCancel,
		})
	}
	if err := s.db.Transaction(func(tx *gorm.DB) error {
		for i := range rows {
			if err := tx.Create(&rows[i]).Error; err != nil {
				return err
			}
			if err := tx.Create(&storage.CloudOperationLog{
				OperationID: rows[i].ID, State: StatePending, Message: "created", CreatedAt: now,
			}).Error; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		return 0, nil, err
	}
	return rows[0].ID, rows, nil
}

func validOp(op string) bool {
	switch op {
	case cloudprov.OpPowerOn, cloudprov.OpPowerOff, cloudprov.OpReboot,
		cloudprov.OpForceStop, cloudprov.OpRebuild:
		return true
	}
	return false
}

// capabilityFor maps the operation to its declaration flag (F-0363~F-0367)
// and derives the cancellability hint recorded on the task (F-0374).
func capabilityFor(op string, caps cloudprov.Capabilities) (bool, bool) {
	switch op {
	case cloudprov.OpPowerOn, cloudprov.OpPowerOff, cloudprov.OpReboot:
		return caps.Power, caps.Power
	case cloudprov.OpForceStop:
		return caps.ForceStop && caps.Power, caps.ForceStop
	case cloudprov.OpRebuild:
		return caps.Rebuild, caps.Rebuild
	}
	return false, false
}

// stateReady is the pre-operation state check (F-0368).
func stateReady(op, state string) bool {
	switch op {
	case cloudprov.OpPowerOn:
		return state == cloudprov.StateStopped || state == cloudprov.StateError ||
			state == cloudprov.StateRebuilding
	case cloudprov.OpPowerOff, cloudprov.OpForceStop:
		return state == cloudprov.StateRunning || state == cloudprov.StateStarting ||
			state == cloudprov.StateRebuilding
	case cloudprov.OpReboot:
		return state == cloudprov.StateRunning || state == cloudprov.StateStarting
	case cloudprov.OpRebuild:
		return state == cloudprov.StateStopped || state == cloudprov.StateError
	}
	return false
}

// Run executes pending tasks through the shared client policy (F-0363~F-0367)
// with the batch concurrency cap (F-0381). A timeout lands in Unknown (F-0372),
// typed failures carry their provider code into the failure reason (F-0376).
func (s *Service) Run(ctx context.Context, now time.Time) error {
	var rows []storage.CloudOperation
	if err := s.db.Where("state = ?", StatePending).Order("id ASC").Find(&rows).Error; err != nil {
		return err
	}
	sem := make(chan struct{}, s.concurrency)
	var wg sync.WaitGroup
	for _, row := range rows {
		select {
		case <-ctx.Done():
			wg.Wait()
			return ctx.Err()
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(r storage.CloudOperation) {
			defer wg.Done()
			defer func() { <-sem }()
			s.runOne(ctx, r, now)
		}(row)
	}
	wg.Wait()
	return nil
}

// Reconcile resolves Unknown rows (F-0373) by querying the vendor; no
// operation is re-executed.
func (s *Service) Reconcile(ctx context.Context, now time.Time) error {
	var rows []storage.CloudOperation
	if err := s.db.Where("state = ?", StateUnknown).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if err := s.reconcileOne(ctx, row, now); err != nil {
			return err
		}
	}
	return nil
}

// Cancel marks a task cancelled when the vendor declared cancellability
// (F-0374); vendor calls already in flight are not interrupted mid-flight.
func (s *Service) Cancel(ctx context.Context, id uint, now time.Time) error {
	var row storage.CloudOperation
	if err := s.db.First(&row, id).Error; err != nil {
		return ErrNotFound
	}
	if !Cancelable(row.State, row.AllowCancel) {
		return fmt.Errorf("operation %d is not cancellable (%s)", id, row.State)
	}
	return s.settle(ctx, id, StateCancelled, "cancelled by operator", "", now)
}

// Get returns the task with its step log (F-0375).
func (s *Service) Get(ctx context.Context, id uint) (storage.CloudOperation, []storage.CloudOperationLog, error) {
	var row storage.CloudOperation
	if err := s.db.First(&row, id).Error; err != nil {
		return row, nil, ErrNotFound
	}
	var logs []storage.CloudOperationLog
	if err := s.db.Where("operation_id = ?", id).Order("id ASC").Find(&logs).Error; err != nil {
		return row, nil, err
	}
	return row, logs, nil
}

// List returns operations, optionally filtered by batch, resource or state
// (F-0375/F-0380).
func (s *Service) List(ctx context.Context, batchID string, resourceID uint, state string) ([]storage.CloudOperation, error) {
	q := s.db.Order("id DESC")
	if batchID != "" {
		q = q.Where("batch_id = ?", batchID)
	}
	if resourceID != 0 {
		q = q.Where("resource_id = ?", resourceID)
	}
	if state != "" {
		q = q.Where("state = ?", state)
	}
	var rows []storage.CloudOperation
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}
