package billing

import (
	"fmt"
	"log"
	"time"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// alertSyncFailed lands one billing_sync_failed warning per source per day
// (F-1830): a failed pull is an ops fact -- the cost freshness gauge (F-1814)
// goes stale silently otherwise. Both finish closures (API pull and file
// import) report through here; dedup is per source per day, Herald merges the
// repeats. Best effort: a delivery failure never fails the run.
func alertSyncFailed(db *gorm.DB, run *storage.BillImport, now time.Time) {
	dedup := fmt.Sprintf("billing_sync:%d:failed:%s", run.CostProviderID, now.Format("20060102"))
	var cnt int64
	if err := db.Model(&storage.Event{}).
		Where("kind = ? AND dedup_key = ?", herald.KindBillingSyncFailed, dedup).
		Count(&cnt).Error; err != nil || cnt > 0 {
		return // same source already alerted today; Herald merges any leftovers
	}
	var src storage.CostProvider
	name := fmt.Sprintf("#%d", run.CostProviderID)
	if err := db.Select("id, name").First(&src, run.CostProviderID).Error; err == nil && src.Name != "" {
		name = src.Name
	}
	body := run.Error
	if body == "" {
		body = "同步流程失败（详见导入记录）"
	}
	if _, err := herald.Emit(db, herald.EmitInput{
		Kind: herald.KindBillingSyncFailed, Severity: herald.SeverityWarning,
		Title:    fmt.Sprintf("成本源 %s 同步失败", name),
		Body:     fmt.Sprintf("来源 %s，触发 %s：%s", run.Source, run.Trigger, body),
		Target:   herald.TargetAdmin,
		DedupKey: dedup,
		Meta: map[string]any{
			"source_id": run.CostProviderID, "source": run.Source,
			"trigger": run.Trigger, "run_id": run.ID,
		},
	}); err != nil {
		log.Printf("herald emit billing_sync_failed: source=%d %v", run.CostProviderID, err)
	}
}
