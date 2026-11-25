package recovery

import (
	"context"
	"errors"

	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// StandbyAction is the L2 standby backfill (BR-3): take one online standby
// entry from the standby list and promote it into the pool; the subscription
// line switch takes effect immediately (minutes). An empty list fails and advances to L3. Baseline provisioning while the pool is empty is handled by replenish, so this action does not spin up a new machine inside the recovery pipeline.
type StandbyAction struct {
	DB *gorm.DB
}

// Name is the action trace name (Recovery.Action).
func (a *StandbyAction) Name() string { return "standby_promote" }

// Run promotes one standby node into the pool; afterwards probe recovery (the original node resetting) is finished by the orchestration.
func (a *StandbyAction) Run(ctx context.Context, node storage.Node) error {
	if a.DB == nil {
		return errors.New("recovery: standby action not wired")
	}
	pick, err := pool.StandbyEntry(a.DB)
	if err != nil {
		return err
	}
	if pick.ID == 0 {
		return errors.New("recovery: no standby node available (mark one with /api/pool/:id/standby)")
	}
	return pool.Promote(a.DB, pick.ID, "恢复补位（"+node.Name+" 被封，预备机顶上）")
}
