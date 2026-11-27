package oper

import (
	"fmt"
	"log"

	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// alertOpFailed lands one cloud_op_failed warning per operation settling
// failed (F-1829): the executor's failure closure is the single settlement
// point, so every failed op row alerts exactly once (the row id is the dedup
// family). Best effort: an emission failure never fails the run.
func alertOpFailed(db *gorm.DB, row storage.CloudOperation, message, providerCode string) {
	body := message
	if providerCode != "" {
		body += "（厂商错误码 " + providerCode + "）"
	}
	if _, err := herald.Emit(db, herald.EmitInput{
		Kind: herald.KindCloudOpFailed, Severity: herald.SeverityWarning,
		Title:    fmt.Sprintf("云操作 %s 失败（资源 #%d）", row.Op, row.ResourceID),
		Body:     body,
		Target:   herald.TargetAdmin,
		DedupKey: fmt.Sprintf("cloudop:%d:failed", row.ID),
		Meta: map[string]any{
			"op_id": row.ID, "op": row.Op, "resource_id": row.ResourceID,
			"provider_id": row.ProviderID, "provider_code": providerCode,
		},
	}); err != nil {
		log.Printf("herald emit cloud_op_failed: op=%d %v", row.ID, err)
	}
}
