package provision

import (
	"fmt"
	"log"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// alertJobFailed lands one provision_failed warning for a job finishing
// failed (F-1829); both tracing paths (Apply's inline write and FinishJob)
// report through here, so a failed job alerts exactly once. Best effort: a
// delivery failure never fails the provisioning round.
func alertJobFailed(db *gorm.DB, jobID uint, logTail string) {
	var job storage.ProvisionJob
	if err := db.Select("id, template_name, instance_name, action").
		First(&job, jobID).Error; err != nil {
		return
	}
	name := job.InstanceName
	if name == "" {
		name = job.TemplateName
	}
	if len(logTail) > 400 {
		logTail = logTail[len(logTail)-400:]
	}
	if _, err := herald.Emit(db, herald.EmitInput{
		Kind: herald.KindProvisionFailed, Severity: herald.SeverityWarning,
		Title:    fmt.Sprintf("置备失败：%s（%s）", name, job.Action),
		Body:     "terraform 输出尾部：" + logTail,
		Target:   herald.TargetAdmin,
		DedupKey: fmt.Sprintf("provision_job:%d:failed", jobID),
		Meta: map[string]any{
			"job_id": jobID, "template": job.TemplateName, "action": job.Action,
		},
	}); err != nil {
		log.Printf("herald emit provision_failed: job=%d %v", jobID, err)
	}
}
