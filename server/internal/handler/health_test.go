package handler

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// healthBody is the response shape of /api/health ("Panel Availability" §5).
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

// TestHealth covers the health self-check ("Panel Availability" §5, P1): the DB-writable probe, the online-agent
// count, and the three-state backup freshness — never backed up → degraded, a new backup → ok, a backup older than
// FreshHours → degraded; a check anomaly is not silent but still returns 200 (so the liveness caller is not killed).
func TestHealth(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	// Never backed up: db_writable true but backup freshness unsatisfied → degraded
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

	// Fresh backup (scheduled) → ok
	if err := db.Create(&storage.Backup{Kind: "scheduled", Path: "/tmp/x.db", CreatedAt: time.Now()}).Error; err != nil {
		t.Fatalf("seed backup: %v", err)
	}
	out = getHealth(t, r)
	if out.Status != "ok" || !out.Checks.Backup.Fresh || out.Checks.Backup.LastKind != "scheduled" {
		t.Fatalf("fresh state = %+v, want ok/fresh", out)
	}

	// Aged-out backup (49h > FreshHours 48h) → degraded
	if err := db.Model(&storage.Backup{}).Where("1=1").Update("created_at", time.Now().Add(-49*time.Hour)).Error; err != nil {
		t.Fatalf("age backup: %v", err)
	}
	out = getHealth(t, r)
	if out.Status != "degraded" || out.Checks.Backup.Fresh {
		t.Fatalf("stale state = %+v, want degraded/unfresh", out)
	}
}
