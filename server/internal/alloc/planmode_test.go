package alloc

import (
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestPolicyForEntry covers the per-entry mode resolution (F-1047): a
// plan-bound entry takes its plan's override, other plans and unbound nodes
// fall back to the system tier.
func TestPolicyForEntry(t *testing.T) {
	base := DefaultPolicySetting()
	base.Out = PolicyCostFirst
	base.SchedPlanModes = map[string]string{"7": PolicyPriority, "9": PolicyQuality}

	vip := storage.Node{Name: "vip", PlanID: 7}
	if got := policyForEntry(base, vip); got != PolicyPriority {
		t.Fatalf("plan 7 mode = %s", got)
	}
	media := storage.Node{Name: "media", PlanID: 9}
	if got := policyForEntry(base, media); got != PolicyQuality {
		t.Fatalf("plan 9 mode = %s", got)
	}
	other := storage.Node{Name: "other", PlanID: 8}
	if got := policyForEntry(base, other); got != PolicyCostFirst {
		t.Fatalf("unconfigured plan mode = %s", got)
	}
	free := storage.Node{Name: "free"}
	if got := policyForEntry(base, free); got != PolicyCostFirst {
		t.Fatalf("unbound node mode = %s", got)
	}
}

// TestValidateSettingPlanModes covers the sched_plan_modes checks (F-1047):
// keys must be positive plan ids, values must be valid policy modes.
func TestValidateSettingPlanModes(t *testing.T) {
	ok := DefaultPolicySetting()
	ok.SchedPlanModes = map[string]string{"7": PolicyPriority}
	if _, err := ValidateSetting(ok); err != nil {
		t.Fatalf("valid map rejected: %v", err)
	}
	for name, modes := range map[string]map[string]string{
		"non-numeric key": {"vip": PolicyPriority},
		"zero key":        {"0": PolicyPriority},
		"invalid mode":    {"7": "greedy"},
		"empty mode":      {"7": ""},
	} {
		in := DefaultPolicySetting()
		in.SchedPlanModes = modes
		if _, err := ValidateSetting(in); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

// TestSweepPlanMode covers the sweep applying per-plan modes (F-1047): with
// the system tier on cost_first, a plan-bound entry still decides by its
// plan's priority mode while the unbound entry keeps the system tier. The
// two landings share one priority tier so the F-1029 tier partition cannot
// mask the mode difference: cost_first picks by price, priority by name.
func TestSweepPlanMode(t *testing.T) {
	db := newTestDB(t)
	seedNode(t, db, "pm-std", "entry", "out", nil)
	seedNode(t, db, "pm-vip", "entry", "out", func(n *storage.Node) {
		n.PlanID = 7
	})
	// "pm-z" is the cheap one; "pm-a" is pricey but sorts first by name
	pricey := seedNode(t, db, "pm-a", "landing", "out", func(n *storage.Node) {
		n.BillingType, n.TrafficPriceCents = "按流量", 90
	})
	cheap := seedNode(t, db, "pm-z", "landing", "out", func(n *storage.Node) {
		n.BillingType, n.TrafficPriceCents = "按流量", 5
	})

	setting := DefaultPolicySetting() // out = cost_first
	setting.SchedPlanModes = map[string]string{"7": PolicyPriority}
	if err := SavePolicy(db, setting); err != nil {
		t.Fatal(err)
	}
	events, err := Sweep(db)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	byEntry := map[string]string{}
	for _, ev := range events {
		byEntry[ev.EntryName] = ev.ToName
	}
	// the unbound entry follows cost_first (cheap); the vip entry follows its
	// plan's priority mode (name order puts the pricey pm-a first)
	if byEntry["pm-std"] != cheap.Name || byEntry["pm-vip"] != pricey.Name {
		t.Fatalf("byEntry = %v (cheap=%s pricey=%s)", byEntry, cheap.Name, pricey.Name)
	}
}
