package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// healthBody 是 /api/health 的响应形状（面板可用性 §5）。
type healthBody struct {
	Status string `json:"status"`
	Checks struct {
		DBWritable   bool `json:"db_writable"`
		AgentsOnline int  `json:"agents_online"`
		Backup       struct {
			LastAt   *time.Time `json:"last_at"`
			AgeHours *float64   `json:"age_hours"`
			Fresh    bool       `json:"fresh"`
			LastKind string     `json:"last_kind,omitempty"`
		} `json:"backup"`
	} `json:"checks"`
}

func getHealth(t *testing.T, r *gin.Engine) healthBody {
	t.Helper()
	rec := doJSON(t, r, "GET", "/api/health", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("health: %d %s", rec.Code, rec.Body)
	}
	var out healthBody
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode health: %v (%s)", err, rec.Body)
	}
	return out
}

// TestHealth 覆盖健康自检（面板可用性 §5，P1）：DB 可写探针、agent 在线
// 数、备份新鲜度三态——从未备份 degraded、有新档 ok、超 FreshHours 旧档
// degraded；检查异常不静默但仍 200（探活方不误杀）。
func TestHealth(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// 从未备份：db_writable 但备份新鲜度不满足 → degraded
	out := getHealth(t, r)
	if !out.Checks.DBWritable {
		t.Fatal("db_writable should be true on live db")
	}
	if out.Checks.AgentsOnline != 0 {
		t.Fatalf("agents_online = %d, want 0", out.Checks.AgentsOnline)
	}
	if out.Status != "degraded" || out.Checks.Backup.Fresh {
		t.Fatalf("never-backed-up state = %+v, want degraded/unfresh", out)
	}

	// 新鲜档（scheduled）→ ok
	if err := db.Create(&storage.Backup{Kind: "scheduled", Path: "/tmp/x.db", CreatedAt: time.Now()}).Error; err != nil {
		t.Fatalf("seed backup: %v", err)
	}
	out = getHealth(t, r)
	if out.Status != "ok" || !out.Checks.Backup.Fresh || out.Checks.Backup.LastKind != "scheduled" {
		t.Fatalf("fresh state = %+v, want ok/fresh", out)
	}

	// 超龄档（49h > FreshHours 48h）→ degraded
	if err := db.Model(&storage.Backup{}).Where("1=1").Update("created_at", time.Now().Add(-49*time.Hour)).Error; err != nil {
		t.Fatalf("age backup: %v", err)
	}
	out = getHealth(t, r)
	if out.Status != "degraded" || out.Checks.Backup.Fresh {
		t.Fatalf("stale state = %+v, want degraded/unfresh", out)
	}
}
