package oper

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cuihairu/ferry/server/internal/cloudprov"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// newClient builds the vendor adapter for one task through the shared client
// policy (timeout/rate-limit/retry, F-0316).
func (s *Service) newClient(prov storage.Provider) (cloudprov.Adapter, error) {
	plain, err := s.secret.Decrypt(prov.AccessKey)
	if err != nil {
		return nil, fmt.Errorf("credential unreadable: %w", err)
	}
	a, err := s.adapterFor(prov, plain)
	if err != nil {
		return nil, fmt.Errorf("adapter init: %w", err)
	}
	return cloudprov.NewClient(a, cloudprov.Defaults), nil
}

// runOne executes a single pending task: pending→running, vendor call, then a
// terminal or Unknown settlement (F-0372/F-0376).
func (s *Service) runOne(ctx context.Context, row storage.CloudOperation, now time.Time) {
	if err := s.transition(ctx, row.ID, StatePending, StateRunning, "executing"); err != nil {
		return
	}
	var prov storage.Provider
	if err := s.db.First(&prov, row.ProviderID).Error; err != nil {
		s.settle(ctx, row.ID, StateFailed, "provider unavailable: "+err.Error(), "", now)
		return
	}
	client, err := s.newClient(prov)
	if err != nil {
		s.settle(ctx, row.ID, StateFailed, err.Error(), "", now)
		return
	}
	var res storage.CloudResource
	if err := s.db.First(&res, row.ResourceID).Error; err != nil {
		s.settle(ctx, row.ID, StateFailed, "resource unavailable: "+err.Error(), "", now)
		return
	}
	runErr := execute(ctx, client, row, res.ExternalID)
	if runErr == nil {
		// Record the vendor-side result state (best effort; the success of the
		// operation itself is already established).
		result := ""
		if state, qerr := client.RefreshStatus(ctx, res.ExternalID); qerr == nil && cloudprov.ValidState(state) {
			result = state
		}
		s.settleWithResult(ctx, row.ID, StateSucceeded, "", "", result, now)
		return
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		s.settle(ctx, row.ID, StateUnknown, "provider call interrupted: "+ctxErr.Error(), "", now)
		return
	}
	pe := cloudprov.AsError(runErr)
	if pe.Type == cloudprov.ErrUnavailable {
		s.settle(ctx, row.ID, StateUnknown, pe.Error(), pe.ProviderCode, now)
		return
	}
	s.settle(ctx, row.ID, StateFailed, pe.Error(), pe.ProviderCode, now)
}

// execute dispatches one adapter op (F-0363~F-0367).
func execute(ctx context.Context, client cloudprov.Adapter, row storage.CloudOperation, externalID string) error {
	switch row.Op {
	case cloudprov.OpPowerOn:
		return client.PowerOn(ctx, externalID)
	case cloudprov.OpPowerOff:
		return client.PowerOff(ctx, externalID)
	case cloudprov.OpReboot:
		return client.Reboot(ctx, externalID)
	case cloudprov.OpForceStop:
		return client.ForceStop(ctx, externalID)
	case cloudprov.OpRebuild:
		var p map[string]any
		if err := json.Unmarshal([]byte(row.Params), &p); err != nil {
			p = map[string]any{}
		}
		image, _ := p["image"].(string)
		return client.Rebuild(ctx, externalID, image)
	}
	return fmt.Errorf("unsupported operation %s", row.Op)
}

// reconcileOne settles an Unknown row by querying the vendor state (F-0373):
// the vendor's own answer decides; the operation is never blindly re-run.
func (s *Service) reconcileOne(ctx context.Context, row storage.CloudOperation, now time.Time) error {
	var prov storage.Provider
	if err := s.db.First(&prov, row.ProviderID).Error; err != nil {
		return s.log(ctx, row.ID, StateUnknown, "provider unavailable: "+err.Error())
	}
	client, err := s.newClient(prov)
	if err != nil {
		return s.log(ctx, row.ID, StateUnknown, err.Error())
	}
	var res storage.CloudResource
	if err := s.db.First(&res, row.ResourceID).Error; err != nil {
		return s.log(ctx, row.ID, StateUnknown, "resource gone: "+err.Error())
	}
	state, err := client.RefreshStatus(ctx, res.ExternalID)
	if err != nil {
		return s.log(ctx, row.ID, StateUnknown, "reconcile query failed: "+err.Error())
	}
	if !cloudprov.ValidState(state) {
		return s.log(ctx, row.ID, StateUnknown, "reconcile query returned invalid state: "+state)
	}
	switch row.Op {
	case cloudprov.OpPowerOn, cloudprov.OpReboot:
		if state == cloudprov.StateRunning || state == cloudprov.StateStarting {
			return s.settleWithResult(ctx, row.ID, StateSucceeded, "", "", state, now)
		}
	case cloudprov.OpPowerOff, cloudprov.OpForceStop:
		if state == cloudprov.StateStopped || state == cloudprov.StateStopping {
			return s.settleWithResult(ctx, row.ID, StateSucceeded, "", "", state, now)
		}
	case cloudprov.OpRebuild:
		if state == cloudprov.StateRebuilding || state == cloudprov.StateStopped {
			return s.settleWithResult(ctx, row.ID, StateSucceeded, "", "", state, now)
		}
	}
	return s.log(ctx, row.ID, StateUnknown, "resource state "+state+" does not match the operation")
}

// settleWithResult writes a terminal state together with the vendor result
// (F-0375/F-0384); settle is the shorthand for rows without a result state.
func (s *Service) settleWithResult(ctx context.Context, id uint, state, message, providerCode, result string, now time.Time) error {
	updates := map[string]any{"state": state, "error": message, "provider_code": providerCode}
	if result != "" {
		updates["result"] = result
	}
	if terminal[state] {
		updates["finished_at"] = now
	}
	res := s.db.Model(&storage.CloudOperation{}).
		Where("id = ? AND state NOT IN ?", id, []string{StateSucceeded, StateFailed, StateCancelled}).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}
	return s.log(ctx, id, state, message)
}

// transition is a guarded state move; an already-settled row is untouched.
func (s *Service) transition(ctx context.Context, id uint, from, to, message string) error {
	res := s.db.Model(&storage.CloudOperation{}).
		Where("id = ? AND state = ?", id, from).
		Updates(map[string]any{"state": to})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}
	return s.log(ctx, id, to, message)
}

// settle writes the terminal state and result/failure fields in one guarded
// update (F-0376).
func (s *Service) settle(ctx context.Context, id uint, state, message, providerCode string, now time.Time) error {
	updates := map[string]any{"state": state, "error": message, "provider_code": providerCode}
	if terminal[state] {
		updates["finished_at"] = now
	}
	res := s.db.Model(&storage.CloudOperation{}).
		Where("id = ? AND state NOT IN ?", id, []string{StateSucceeded, StateFailed, StateCancelled}).
		Updates(updates)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}
	return s.log(ctx, id, state, message)
}

// log appends one step-log entry (F-0375/F-0384).
func (s *Service) log(ctx context.Context, id uint, state, message string) error {
	if err := s.db.Create(&storage.CloudOperationLog{
		OperationID: id, State: state, Message: truncate(message, 512), CreatedAt: time.Now(),
	}).Error; err != nil {
		return err
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
