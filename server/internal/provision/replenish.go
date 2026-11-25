package provision

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// Replenisher is the safety net for an empty pool (BR-3): when the entry pool has no usable node at all,
// a new machine is launched automatically from the template (the same launch path as PrepareLaunch, with the
// OS-4 pipeline admitting it into the pool). It complements L3 of the recovery pipeline: L3 is "replacing one
// blocked node", this is "the last resort when the pool empties out". Re-entrancy guard: if there is an entry being
// supplied (provisioning) or an in-flight apply job it stands down, avoiding a chain of launches. Template 0 = off.
type Replenisher struct {
	Manager    *Manager
	Store      *secret.Store
	TemplateID uint
	// Launch assembles the launch parameters injected by the panel (same as recovery L3, reusing the panel address/download conventions).
	Launch func(name string) LaunchParams
}

// LaunchTimeout bounds one background replenish launch (F-0042: no unbounded background work);
// the derived context also dies with the sweep context, so shutdown cancels an in-flight launch.
// Var so tests can shorten it.
var LaunchTimeout = 15 * time.Minute

// Enabled: when no template is configured the safety net stays off.
func (r *Replenisher) Enabled() bool { return r != nil && r.TemplateID > 0 && r.Launch != nil }

// Sweep runs one round: the pool is empty and nothing is being supplied -> launch one replacement machine in the background. It reports whether it triggered.
func (r *Replenisher) Sweep(ctx context.Context, logger *log.Logger) (bool, error) {
	if !r.Enabled() {
		return false, nil
	}
	if logger == nil {
		logger = log.Default()
	}
	db := r.Manager.DB()
	var n int64
	if err := db.Model(&storage.Node{}).
		Where("enabled = ? AND role IN (?, ?) AND pool_state = ? AND status != ?",
			true, "entry", "both", pool.StateActive, "provisioning").Count(&n).Error; err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil // the pool is not empty
	}
	// Re-entrancy guard: an entry being supplied or an in-flight apply both count as in flight.
	if err := db.Model(&storage.Node{}).
		Where("enabled = ? AND role IN (?, ?) AND status = ?",
			true, "entry", "both", "provisioning").Count(&n).Error; err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}
	if err := db.Model(&storage.ProvisionJob{}).
		Where("action = ? AND status = ?", "apply", "running").Count(&n).Error; err != nil {
		return false, err
	}
	if n > 0 {
		return false, nil
	}

	var tpl storage.ProvisionTemplate
	if err := db.First(&tpl, r.TemplateID).Error; err != nil {
		return false, fmt.Errorf("replenish template %d: %w", r.TemplateID, err)
	}
	var prov storage.Provider
	if err := db.First(&prov, tpl.ProviderID).Error; err != nil {
		return false, errors.New("replenish: template provider missing")
	}
	if !prov.Enabled {
		return false, errors.New("replenish: template provider disabled")
	}
	name := fmt.Sprintf("pool-%d", time.Now().Unix())
	p := r.Launch(name)
	p.Template, p.Provider, p.Store = tpl, prov, r.Store
	p.InstanceName = name
	prep, err := r.Manager.PrepareLaunch(p)
	if err != nil {
		return false, err
	}
	logger.Printf("replenish: pool empty, launching %s from template %d", name, r.TemplateID)
	go func() {
		runCtx, cancel := context.WithTimeout(ctx, LaunchTimeout)
		defer cancel()
		status, logTail := r.Manager.Execute(runCtx, prep.Params)
		r.Manager.FinishJob(prep.Job.ID, status, logTail)
		logger.Printf("replenish: %s finished with %s", name, status)
	}()
	return true, nil
}

// ReplenishLoop runs the empty-pool safety net periodically until ctx is cancelled.
func ReplenishLoop(ctx context.Context, r *Replenisher, interval time.Duration, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if _, err := r.Sweep(ctx, logger); err != nil {
			logger.Printf("replenish sweep: %v", err)
		}
		t.Reset(interval)
	}
}
