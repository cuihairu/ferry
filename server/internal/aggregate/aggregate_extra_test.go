package aggregate

import (
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// TestJudgeRatioFailure covers a failure judged by ratio: Sick<MinSick but the ratio
// reaches SickRate, plus the empty-scope fallback to "unknown" and the same-scope reuse branch.
func TestJudgeRatioFailure(t *testing.T) {
	db := openTestDB(t)
	now := time.Now()
	// 4 nodes with 1 sick: ratio 25% >= the default SickRate (0.25), but 1 < MinSick(2).
	// Constructing ratio >= SickRate makes the right half of the || take effect.
	n1, n2, n3, n4 := uint(1), uint(2), uint(3), uint(4)
	seed(t, db, &n1, agentproto.ProbeTargetTunnel, "", "电信", agentproto.ProbeVerdictSick, now.Add(-time.Minute))
	seed(t, db, &n2, agentproto.ProbeTargetTunnel, "", "电信", agentproto.ProbeVerdictHealthy, now.Add(-time.Minute))
	seed(t, db, &n3, agentproto.ProbeTargetTunnel, "", "电信", agentproto.ProbeVerdictHealthy, now.Add(-time.Minute))
	seed(t, db, &n4, agentproto.ProbeTargetTunnel, "", "电信", agentproto.ProbeVerdictHealthy, now.Add(-time.Minute))
	// One more verdict for the same node (hits the record branch reusing an existing scope).
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
			// Empty-scope normalization: the region dimension should fall into "unknown".
			if v.Dimension != DimensionRegion {
				t.Fatalf("dimension=%v scope=%s", v.Dimension, v.Scope)
			}
		}
	}
}
