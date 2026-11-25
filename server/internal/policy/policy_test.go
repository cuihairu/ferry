package policy

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// openTestDB opens an isolated migrated database.
func openTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := storage.Open(storage.DriverSQLite, filepath.Join(t.TempDir(), "policy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			sqlDB.Close()
		}
	})
	if err := storage.AutoMigrate(db); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestResolveMergePrecedence(t *testing.T) {
	db := openTestDB(t)
	seed := func(scope string, subject uint, c storage.PolicyConstraints) {
		if err := db.Create(&storage.Policy{
			Scope: scope, SubjectID: subject, Enabled: true,
			Constraints: storage.MustPolicyConstraints(c), Version: 1,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	// System: regions + min health. Plan: regions override + cost cap.
	// User: min health override.
	seed(storage.PolicyScopeSystem, 0, storage.PolicyConstraints{
		Regions: []string{"HK"}, MinHealth: "degraded",
	})
	seed(storage.PolicyScopePlan, 7, storage.PolicyConstraints{
		Regions:              []string{"JP"},
		MaxTrafficPriceCents: 30,
	})
	seed(storage.PolicyScopeUser, 42, storage.PolicyConstraints{
		MinHealth: "healthy",
	})

	// User 42 on plan 7: user health wins, plan regions/cost win, system
	// min health is overridden by the user's.
	eff, rows, err := Resolve(db, 42, 7)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("contributing rows = %d", len(rows))
	}
	if eff.MinHealth != "healthy" {
		t.Fatalf("min_health = %q", eff.MinHealth)
	}
	if len(eff.Regions) != 1 || eff.Regions[0] != "JP" {
		t.Fatalf("regions = %v", eff.Regions)
	}
	if eff.MaxTrafficPriceCents != 30 {
		t.Fatalf("cost cap = %d", eff.MaxTrafficPriceCents)
	}

	// A different user on the same plan: system min health, plan regions.
	eff, _, err = Resolve(db, 99, 7)
	if err != nil {
		t.Fatal(err)
	}
	if eff.MinHealth != "degraded" || eff.Regions[0] != "JP" {
		t.Fatalf("other user merge = %+v", eff)
	}

	// A subject with no user/plan policy of its own still gets the system tier.
	eff, rows, err = Resolve(db, 1000, 1000)
	if err != nil || len(rows) != 1 || eff.MinHealth != "degraded" || len(eff.Regions) != 1 || eff.Regions[0] != "HK" {
		t.Fatalf("system-only resolve = %+v rows=%d err=%v", eff, len(rows), err)
	}

	// Disabled rows do not participate.
	var sys storage.Policy
	if err := db.Where("scope = ?", storage.PolicyScopeSystem).First(&sys).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&storage.Policy{}).Where("id = ?", sys.ID).Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	eff, _, err = Resolve(db, 99, 7)
	if err != nil {
		t.Fatal(err)
	}
	if eff.MinHealth != "" {
		t.Fatalf("disabled system policy still resolved: %+v", eff)
	}
}

// TestApplyConstraints covers F-0288~F-0293 filtering and the drop reasons
// (the explain half of F-0296).
func TestApplyConstraints(t *testing.T) {
	mk := func(name, region, isp, line, status, pool string, bw int, price, monthly int64) storage.Node {
		return storage.Node{Name: name, Region: region, ISP: isp, LineType: line,
			Status: status, PoolState: pool, BwDownMbps: bw,
			TrafficPriceCents: price, MonthlyCostCents: monthly}
	}
	nodes := []storage.Node{
		mk("ok", "HK", "HKT", "cn2_gia", "online", "active", 100, 10, 0),
		mk("region-miss", "US", "Cogent", "163", "online", "active", 100, 10, 0),
		mk("isp-miss", "HK", "PCCW", "cn2_gia", "online", "active", 100, 10, 0),
		mk("line-miss", "HK", "HKT", "iplc", "online", "active", 100, 10, 0),
		mk("offline", "HK", "HKT", "cn2_gia", "offline", "active", 100, 10, 0),
		mk("suspended", "HK", "HKT", "cn2_gia", "online", "suspended", 100, 10, 0),
		mk("thin", "HK", "HKT", "cn2_gia", "online", "active", 10, 10, 0),
		mk("pricey", "HK", "HKT", "cn2_gia", "online", "active", 100, 99, 0),
		mk("fixed-pricey", "HK", "HKT", "cn2_gia", "online", "active", 100, 0, 500),
	}
	eff := storage.PolicyConstraints{
		Regions:              []string{"HK"},
		ISPs:                 []string{"HKT"},
		LineTypes:            []string{"cn2_gia"},
		MinHealth:            "healthy",
		MinCapacityMbps:      50,
		MaxTrafficPriceCents: 50,
		MaxMonthlyCostCents:  300,
	}
	kept, dropped := ApplyConstraints(nodes, eff)
	if len(kept) != 1 || kept[0].Name != "ok" {
		t.Fatalf("kept = %+v", kept)
	}
	reasons := map[string]string{}
	for _, d := range dropped {
		reasons[d.NodeName] = d.Reason
	}
	want := map[string]string{
		"region-miss":  "region not allowed",
		"isp-miss":     "isp not allowed",
		"line-miss":    "line type not allowed",
		"offline":      "below min health",
		"suspended":    "below min health",
		"thin":         "below min capacity",
		"pricey":       "above traffic price cap",
		"fixed-pricey": "above monthly cost cap",
	}
	for name, reason := range want {
		if reasons[name] != reason {
			t.Fatalf("%s reason = %q want %q", name, reasons[name], reason)
		}
	}

	// No constraints: nothing drops.
	kept, dropped = ApplyConstraints(nodes, storage.PolicyConstraints{})
	if len(kept) != len(nodes) || len(dropped) != 0 {
		t.Fatalf("empty constraints dropped something: %d/%d", len(kept), len(dropped))
	}

	// min_health=degraded still serves offline entries (only suspension drops).
	kept, _ = ApplyConstraints(nodes, storage.PolicyConstraints{MinHealth: "degraded"})
	for _, k := range kept {
		if k.Name == "offline" {
			return
		}
	}
	t.Fatal("degraded tier dropped the offline entry")
}

// TestCapOutput covers F-0286: compact keeps the probe-sorted best ten.
func TestCapOutput(t *testing.T) {
	nodes := make([]storage.Node, 15)
	rtt := map[uint]int{}
	for i := range nodes {
		nodes[i] = storage.Node{}
		nodes[i].ID = uint(i + 1)
		nodes[i].Name = "n"
		nodes[i].CreatedAt = time.Now()
		rtt[uint(i+1)] = (i + 1) * 10 // node 1 has the best rtt, node 15 the worst
	}
	// full/empty: no-op.
	if got := CapOutput(nodes, rtt, storage.PolicyConstraints{}); len(got) != 15 {
		t.Fatalf("full output capped: %d", len(got))
	}
	eff := storage.PolicyConstraints{Output: "compact"}
	got := CapOutput(nodes, rtt, eff)
	if len(got) != CompactOutputLimit {
		t.Fatalf("compact output = %d", len(got))
	}
	// The kept set is exactly the ten lowest rtts (ids 1..10).
	for _, n := range got {
		if n.ID > 10 {
			t.Fatalf("compact kept node %d", n.ID)
		}
	}
}

// TestFreezeFailover covers F-0294's flag semantics.
func TestFreezeFailover(t *testing.T) {
	if FreezeFailover(storage.PolicyConstraints{}) {
		t.Fatal("empty failover must not freeze")
	}
	if FreezeFailover(storage.PolicyConstraints{Failover: "auto"}) {
		t.Fatal("auto failover must not freeze")
	}
	if !FreezeFailover(storage.PolicyConstraints{Failover: "manual"}) {
		t.Fatal("manual failover must freeze")
	}
}
