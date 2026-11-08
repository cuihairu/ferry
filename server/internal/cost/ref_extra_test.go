package cost

import (
	"testing"

	"github.com/cuihairu/ferry/packages/costref"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestSaveRefTableEdge 覆盖 SaveRefTable 空串清空与脏表拒绝分支。
func TestSaveRefTableEdge(t *testing.T) {
	db := newTestDB(t)
	// 空串：写入空值（现有实现以 SetSetting("") 表达清空）。
	if err := SaveRefTable(db, []byte("   ")); err != nil {
		t.Fatalf("clear: %v", err)
	}
	// 脏表拒绝。
	if err := SaveRefTable(db, []byte(`{"not":"a-table"}`)); err == nil {
		t.Fatal("bad table should error")
	}
	// 好表落库。
	good := []byte(`[{"provider":"vultr","region":"tokyo","spec":"500M-2G","monthly_cents":10000,"currency":"CNY"}]`)
	if err := SaveRefTable(db, good); err != nil {
		t.Fatalf("save good: %v", err)
	}
	if v, ok, _ := storage.GetSetting(db, SettingRefTable); !ok || v == "" {
		t.Fatal("setting missing after save")
	}
	// 再清空：值为空串（表达停用）。
	if err := SaveRefTable(db, nil); err != nil {
		t.Fatalf("clear nil: %v", err)
	}
	if v, _, _ := storage.GetSetting(db, SettingRefTable); v != "" {
		t.Fatalf("clear should blank value, got %q", v)
	}
}

// TestCheckNodeLookupError 覆盖 CheckNode 在牌价查询失败时返回错误分支。
func TestCheckNodeLookupError(t *testing.T) {
	src := costref.NewStatic("pricelist", nil) // 空表 → ErrNotFound
	n := storage.Node{Datacenter: "vultr", Region: "tokyo",
		BwDownMbps: 500, MonthlyTrafficQuotaBytes: 2 * BytesPerGB,
		MonthlyCostCents: 10000, Currency: "CNY"}
	if _, _, err := CheckNode(src, n, 30); err == nil {
		t.Fatal("want error for missing quote")
	}
}
