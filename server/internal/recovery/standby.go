package recovery

import (
	"context"
	"errors"

	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// StandbyAction 是 L2 预备机补位（BR-3）：从预备清单取一台在线备用入口
// 升入池，订阅换线即时生效（分钟级）；清单空则失败推进 L3。池空常态
// 保底开服由 replenish 承接，不在恢复流水线内重复开新机。
type StandbyAction struct {
	DB *gorm.DB
}

// Name 是动作留痕名（Recovery.Action）。
func (a *StandbyAction) Name() string { return "standby_promote" }

// Run 升一台预备机入池；成功后探测恢复（原节点复位）由编排收尾。
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
