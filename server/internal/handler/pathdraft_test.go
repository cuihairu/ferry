package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/pathhealth"
	"github.com/cuihairu/ferry/server/internal/storage"
)

// TestPathDraftLifecycle covers F-1016 (and the F-1019 terminal-state rules):
// a draft is created pending from previewed node ids, listed with a state
// filter, confirmed into exactly one Path, or rejected so a later confirm
// cannot resurrect it. Confirm revalidates the stored topology, so a node
// deleted after the draft was written (or a name that has since been taken)
// fails the transaction and leaves the draft pending.
func TestPathDraftLifecycle(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	mkNode := func(name string) uint {
		n := storage.Node{
			Name: name, Address: name + ".example.com", Port: 443, Protocol: "vless",
			Token: name, Enabled: true, Status: "online", PoolState: "active",
			Role: "landing", Direction: "out",
		}
		if err := db.Create(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n.ID
	}
	entry := mkNode("pd-entry")
	landing := mkNode("pd-landing")

	// create: pending draft with the audit fields
	rec := doJSON(t, r, "POST", "/api/paths/drafts", map[string]any{
		"name": "draft-one", "entry_node_id": entry, "landing_node_id": landing,
		"direction": "out", "reason": "from preview", "operator": "op-a",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create draft: %d %s", rec.Code, rec.Body)
	}
	var d storage.PendingPath
	if err := json.Unmarshal(rec.Body.Bytes(), &d); err != nil {
		t.Fatal(err)
	}
	if d.State != "pending" || d.CreatedBy != "op-a" || d.Reason != "from preview" {
		t.Fatalf("draft = %+v", d)
	}

	// bad topology refused at draft time (entry == landing)
	rec = doJSON(t, r, "POST", "/api/paths/drafts", map[string]any{
		"name": "draft-loop", "entry_node_id": entry, "landing_node_id": entry,
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("self-loop draft = %d %s", rec.Code, rec.Body)
	}

	// state filter listing
	rec = doJSON(t, r, "GET", "/api/paths/drafts?state=pending", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list drafts: %d %s", rec.Code, rec.Body)
	}
	var list struct {
		Drafts []storage.PendingPath `json:"drafts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Drafts) != 1 || list.Drafts[0].ID != d.ID {
		t.Fatalf("pending list = %+v", list.Drafts)
	}
	rec = doJSON(t, r, "GET", "/api/paths/drafts?state=rejected", nil)
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Drafts) != 0 {
		t.Fatalf("rejected list = %+v", list.Drafts)
	}

	// confirm: one Path row, draft marked confirmed with path_id
	rec = doJSON(t, r, "POST", "/api/paths/drafts/"+utoa(d.ID)+"/confirm", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body)
	}
	var confirmed struct {
		Draft uint         `json:"draft"`
		Path  storage.Path `json:"path"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &confirmed); err != nil {
		t.Fatal(err)
	}
	if confirmed.Draft != d.ID || confirmed.Path.EntryNodeID != entry || confirmed.Path.LandingNodeID != landing {
		t.Fatalf("confirmed = %+v", confirmed)
	}
	var pathCount int64
	db.Model(&storage.Path{}).Count(&pathCount)
	if pathCount != 1 {
		t.Fatalf("path count = %d, want 1", pathCount)
	}
	var d2 storage.PendingPath
	if err := db.First(&d2, d.ID).Error; err != nil {
		t.Fatal(err)
	}
	if d2.State != "confirmed" || d2.PathID == nil || *d2.PathID != confirmed.Path.ID {
		t.Fatalf("draft after confirm = %+v", d2)
	}

	// confirming twice is terminal
	if rec = doJSON(t, r, "POST", "/api/paths/drafts/"+utoa(d.ID)+"/confirm", nil); rec.Code != http.StatusConflict {
		t.Fatalf("re-confirm = %d %s", rec.Code, rec.Body)
	}
	db.Model(&storage.Path{}).Count(&pathCount)
	if pathCount != 1 {
		t.Fatalf("re-confirm created an extra path: %d", pathCount)
	}

	// reject: terminal, later confirm cannot resurrect
	rec = doJSON(t, r, "POST", "/api/paths/drafts", map[string]any{
		"name": "draft-two", "entry_node_id": entry, "landing_node_id": landing,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create draft-two: %d %s", rec.Code, rec.Body)
	}
	var d3 storage.PendingPath
	if err := json.Unmarshal(rec.Body.Bytes(), &d3); err != nil {
		t.Fatal(err)
	}
	if rec = doJSON(t, r, "POST", "/api/paths/drafts/"+utoa(d3.ID)+"/reject", nil); rec.Code != http.StatusOK {
		t.Fatalf("reject: %d %s", rec.Code, rec.Body)
	}
	if rec = doJSON(t, r, "POST", "/api/paths/drafts/"+utoa(d3.ID)+"/reject", nil); rec.Code != http.StatusConflict {
		t.Fatalf("re-reject = %d %s", rec.Code, rec.Body)
	}
	if rec = doJSON(t, r, "POST", "/api/paths/drafts/"+utoa(d3.ID)+"/confirm", nil); rec.Code != http.StatusConflict {
		t.Fatalf("confirm after reject = %d %s", rec.Code, rec.Body)
	}
	db.Model(&storage.Path{}).Count(&pathCount)
	if pathCount != 1 {
		t.Fatalf("rejected draft produced a path: %d", pathCount)
	}

	// a name taken after the draft was written rolls the confirm back: no new
	// path, the draft stays pending so the operator can rename it.
	rec = doJSON(t, r, "POST", "/api/paths/drafts", map[string]any{
		"name": "draft-dup", "entry_node_id": entry, "landing_node_id": landing,
	})
	var d4 storage.PendingPath
	if err := json.Unmarshal(rec.Body.Bytes(), &d4); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&storage.Path{Name: "draft-dup", EntryNodeID: entry, LandingNodeID: landing, Direction: "out"}).Error; err != nil {
		t.Fatal(err)
	}
	if rec = doJSON(t, r, "POST", "/api/paths/drafts/"+utoa(d4.ID)+"/confirm", nil); rec.Code != http.StatusConflict {
		t.Fatalf("confirm onto taken name = %d %s", rec.Code, rec.Body)
	}
	var d4b storage.PendingPath
	if err := db.First(&d4b, d4.ID).Error; err != nil {
		t.Fatal(err)
	}
	if d4b.State != "pending" {
		t.Fatalf("rolled-back confirm must keep the draft pending: %+v", d4b)
	}

	// confirm revalidates the stored pair: a node deleted after the draft was
	// written is caught, and the draft stays pending.
	rec = doJSON(t, r, "POST", "/api/paths/drafts", map[string]any{
		"name": "draft-gone", "entry_node_id": entry, "landing_node_id": landing,
	})
	var d5 storage.PendingPath
	if err := json.Unmarshal(rec.Body.Bytes(), &d5); err != nil {
		t.Fatal(err)
	}
	if err := db.Delete(&storage.Node{}, landing).Error; err != nil {
		t.Fatal(err)
	}
	if rec = doJSON(t, r, "POST", "/api/paths/drafts/"+utoa(d5.ID)+"/confirm", nil); rec.Code != http.StatusBadRequest {
		t.Fatalf("confirm with deleted landing = %d %s", rec.Code, rec.Body)
	}

	// missing draft 404
	if rec = doJSON(t, r, "POST", "/api/paths/drafts/424242/confirm", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing draft confirm = %d", rec.Code)
	}
	if rec = doJSON(t, r, "POST", "/api/paths/drafts/424242/reject", nil); rec.Code != http.StatusNotFound {
		t.Fatalf("missing draft reject = %d", rec.Code)
	}
}

// TestAutoProvisionVerify covers F-1017/F-1018/F-1019: automatic provisioning
// requires a fresh healthy tunnel probe from entry to landing; a missing,
// stale or sick verdict is a 422 whose rollback deletes the path this call
// created, leaving no partial resource behind.
func TestAutoProvisionVerify(t *testing.T) {
	r, db := newTestRouterWithDB(t)

	mkNode := func(name string) uint {
		n := storage.Node{
			Name: name, Address: name + ".example.com", Port: 443, Protocol: "vless",
			Token: name, Enabled: true, Status: "online", PoolState: "active",
			Role: "landing", Direction: "out",
		}
		if err := db.Create(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n.ID
	}
	entry := mkNode("ap-entry")
	landing := mkNode("ap-landing")

	probe := func(verdict string, reachable bool, at time.Time) {
		if err := db.Create(&storage.ProbeReport{
			NodeID: entry, TargetKind: agentproto.ProbeTargetTunnel, TargetNodeID: &landing,
			Verdict: verdict, Reachable: reachable, ProbedAt: at,
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	provision := func(name string) *httptest.ResponseRecorder {
		return doJSON(t, r, "POST", "/api/paths/provision", map[string]any{
			"name": name, "entry_node_id": entry, "landing_node_id": landing, "direction": "out",
		})
	}
	pathCount := func() int64 {
		var n int64
		if err := db.Model(&storage.Path{}).Count(&n).Error; err != nil {
			t.Fatal(err)
		}
		return n
	}

	// no probe data yet: verification cannot pass, the created path is deleted
	rec := provision("ap-1")
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("no-probe provision = %d %s", rec.Code, rec.Body)
	}
	var resp struct {
		Error            string `json:"error"`
		RolledBackPathID uint   `json:"rolled_back_path_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.RolledBackPathID == 0 || resp.Error == "" {
		t.Fatalf("rollback response = %+v", resp)
	}
	if n := pathCount(); n != 0 {
		t.Fatalf("failed provision left %d paths", n)
	}

	// a verdict outside the fresh window is stale, not evidence
	probe(agentproto.ProbeVerdictHealthy, true, time.Now().Add(-pathhealth.ProbeFresh-time.Minute))
	if rec = provision("ap-2"); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("stale-probe provision = %d %s", rec.Code, rec.Body)
	}
	if n := pathCount(); n != 0 {
		t.Fatalf("stale-probe provision left %d paths", n)
	}

	// a fresh sick verdict refuses provisioning too
	probe(agentproto.ProbeVerdictSick, false, time.Now())
	if rec = provision("ap-3"); rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("sick-probe provision = %d %s", rec.Code, rec.Body)
	}
	if n := pathCount(); n != 0 {
		t.Fatalf("sick-probe provision left %d paths", n)
	}

	// a fresh healthy verdict provisions; the latest verdict wins over older ones
	probe(agentproto.ProbeVerdictHealthy, true, time.Now())
	rec = provision("ap-4")
	if rec.Code != http.StatusCreated {
		t.Fatalf("healthy provision = %d %s", rec.Code, rec.Body)
	}
	var ok struct {
		Path     storage.Path `json:"path"`
		Verified bool         `json:"verified"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &ok); err != nil {
		t.Fatal(err)
	}
	if !ok.Verified || ok.Path.Name != "ap-4" || ok.Path.Note != "auto-provision" {
		t.Fatalf("provisioned = %+v", ok)
	}
	if n := pathCount(); n != 1 {
		t.Fatalf("healthy provision left %d paths, want 1", n)
	}

	// a name already taken is a conflict before any verification
	if rec = provision("ap-4"); rec.Code != http.StatusConflict {
		t.Fatalf("duplicate-name provision = %d %s", rec.Code, rec.Body)
	}

	// input validation runs before any row is written
	if rec = provision(""); rec.Code != http.StatusBadRequest {
		t.Fatalf("blank-name provision = %d", rec.Code)
	}
	if rec = doJSON(t, r, "POST", "/api/paths/provision", map[string]any{
		"name": "ap-5", "entry_node_id": entry, "landing_node_id": entry,
	}); rec.Code != http.StatusBadRequest {
		t.Fatalf("self-loop provision = %d", rec.Code)
	}
	if n := pathCount(); n != 1 {
		t.Fatalf("rejected provisions wrote rows: %d", n)
	}
}
