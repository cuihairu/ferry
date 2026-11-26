package alloc

import (
	"testing"

	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestPolicyForEntryGroup covers the group layer of the per-entry mode
// resolution (F-1048): a node's business group takes its override over the
// system tier, and a plan override is more specific and wins over the group.
func TestPolicyForEntryGroup(t *testing.T) {
	base := DefaultPolicySetting()
	base.Out = PolicyCostFirst
	base.SchedGroupModes = map[string]string{"vip": PolicyPriority, "media": PolicyQuality}
	base.SchedPlanModes = map[string]string{"7": PolicyLeastConn}

	grp := storage.Node{Name: "n1", Group: "vip"}
	if got := policyForEntry(base, grp); got != PolicyPriority {
		t.Fatalf("group vip mode = %s", got)
	}
	media := storage.Node{Name: "n2", Group: "media"}
	if got := policyForEntry(base, media); got != PolicyQuality {
		t.Fatalf("group media mode = %s", got)
	}
	other := storage.Node{Name: "n3", Group: "free"}
	if got := policyForEntry(base, other); got != PolicyCostFirst {
		t.Fatalf("unconfigured group mode = %s", got)
	}
	noGroup := storage.Node{Name: "n4"}
	if got := policyForEntry(base, noGroup); got != PolicyCostFirst {
		t.Fatalf("groupless node mode = %s", got)
	}
	// the plan binding is more specific: plan 7 wins over group vip
	both := storage.Node{Name: "n5", Group: "vip", PlanID: 7}
	if got := policyForEntry(base, both); got != PolicyLeastConn {
		t.Fatalf("plan-over-group mode = %s", got)
	}
}

// TestValidateSettingGroupModes covers the sched_group_modes checks (F-1048):
// keys are free-form group tags, values must be valid policy modes.
func TestValidateSettingGroupModes(t *testing.T) {
	ok := DefaultPolicySetting()
	ok.SchedGroupModes = map[string]string{"vip": PolicyPriority}
	if _, err := ValidateSetting(ok); err != nil {
		t.Fatalf("valid map rejected: %v", err)
	}
	for name, modes := range map[string]map[string]string{
		"invalid mode": {"vip": "greedy"},
		"empty mode":   {"vip": ""},
	} {
		in := DefaultPolicySetting()
		in.SchedGroupModes = modes
		if _, err := ValidateSetting(in); err == nil {
			t.Fatalf("%s accepted", name)
		}
	}
}

// TestSweepGroupMode covers the sweep applying per-group modes (F-1048): with
// the system tier on cost_first, a grouped entry still decides by its group's
// priority mode while the ungrouped entry keeps the system tier. The two
// landings share one priority tier so the F-1029 tier partition cannot mask
// the mode difference: cost_first picks by price, priority by name.
func TestSweepGroupMode(t *testing.T) {
	db := newTestDB(t)
	seedNode(t, db, "gm-std", "entry", "out", nil)
	seedNode(t, db, "gm-vip", "entry", "out", func(n *storage.Node) {
		n.Group = "vip"
	})
	// "gm-z" is the cheap one; "gm-a" is pricey but sorts first by name
	pricey := seedNode(t, db, "gm-a", "landing", "out", func(n *storage.Node) {
		n.BillingType, n.TrafficPriceCents = "按流量", 90
	})
	cheap := seedNode(t, db, "gm-z", "landing", "out", func(n *storage.Node) {
		n.BillingType, n.TrafficPriceCents = "按流量", 5
	})

	setting := DefaultPolicySetting() // out = cost_first
	setting.SchedGroupModes = map[string]string{"vip": PolicyPriority}
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
	// the ungrouped entry follows cost_first (cheap); the vip entry follows
	// its group's priority mode (name order puts the pricey gm-a first)
	if byEntry["gm-std"] != cheap.Name || byEntry["gm-vip"] != pricey.Name {
		t.Fatalf("byEntry = %v (cheap=%s pricey=%s)", byEntry, cheap.Name, pricey.Name)
	}
}
