package aggregate

import (
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// TestJudgeRatioFailure 覆盖 Sick<MinSick 但占比达 SickRate 的故障判定，
// 以及空 scope 归"未知"与同 scope 聚合复用分支。
func TestJudgeRatioFailure(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()
	// 4 节点 1 sick：占比 25% ≥ 缺省 SickRate（0.25），但 1 < MinSick(2)。
	// 构造 ratio >= SickRate 使右半 || 生效。
	n1, n2, n3, n4 := uint(1), uint(2), uint(3), uint(4)
	seed(t, db, &n1, agentproto.ProbeTargetTunnel, "", "电信", agentproto.ProbeVerdictSick, now.Add(-time.Minute))
	seed(t, db, &n2, agentproto.ProbeTargetTunnel, "", "电信", agentproto.ProbeVerdictHealthy, now.Add(-time.Minute))
	seed(t, db, &n3, agentproto.ProbeTargetTunnel, "", "电信", agentproto.ProbeVerdictHealthy, now.Add(-time.Minute))
	seed(t, db, &n4, agentproto.ProbeTargetTunnel, "", "电信", agentproto.ProbeVerdictHealthy, now.Add(-time.Minute))
	// 同一节点再补一条（触发 record 已有 scope 复用分支）。
	seed(t, db, &n1, agentproto.ProbeTargetTunnel, "", "电信", agentproto.ProbeVerdictHealthy, now.Add(-30*time.Second))

	verdicts, err := Judge(db, Options{})
	if err != nil {
		t.Fatalf("judge: %v", err)
	}
	if len(verdicts) == 0 {
		t.Fatal("expected verdicts")
	}
	for _, v := range verdicts {
		if v.Scope == "未知" {
			// 空 scope 归一：region 维度应归"未知"。
			if v.Dimension != DimensionRegion {
				t.Fatalf("dimension=%v scope=%s", v.Dimension, v.Scope)
			}
		}
	}
}
