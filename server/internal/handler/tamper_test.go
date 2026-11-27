package handler

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/storage"
	"github.com/gin-gonic/gin"
)

// TestConfigPushTamperTrail covers F-1735's config-push tamper line: the
// server stamps the payload digest (Version = Sha256 = sha256(payload)) onto
// both the wire envelope and the trail row — the anchor the agent checks
// before applying a config — and every ack verdict (applied, invalid,
// reverted, never-delivered) lands in the trail as tamper evidence instead of
// being swallowed.
func TestConfigPushTamperTrail(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	id, token := createNodeFor(t, r, "tamper-1")
	offID, _ := createNodeFor(t, r, "tamper-off")

	srv := httptest.NewServer(r)
	defer srv.Close()
	c := fakeAgent(t, srv.URL, "tamper-1", token)

	// Scripted agent verdicts: applied -> invalid (validation rejected it) ->
	// reverted (a bad config reached the node and was rolled back).
	acks := []agentproto.ConfigAck{
		{OK: true, Validated: true},
		{OK: false, Validated: true, Error: "validation failed: bad inbound"},
		{OK: false, Reverted: true, Validated: true, Error: "invalid, rolled back"},
	}
	i := 0
	go func() {
		for {
			var env agentproto.Envelope
			if err := c.ReadJSON(&env); err != nil {
				return
			}
			if env.Type != agentproto.MsgConfigPush {
				continue
			}
			var cp agentproto.ConfigPush
			if err := env.Decode(&cp); err != nil {
				return
			}
			sum := sha256.Sum256([]byte(cp.Payload))
			digest := hex.EncodeToString(sum[:])
			if cp.Sha256 != digest || cp.Version != digest {
				t.Errorf("envelope digest mismatch: sha=%s ver=%s", cp.Sha256, cp.Version)
			}
			if i >= len(acks) {
				return
			}
			ack := acks[i]
			ack.Proc = cp.Proc
			ack.Version = cp.Version
			i++
			replyAck(t, c, env, agentproto.MsgConfigAck, ack)
		}
	}()

	payloads := []string{
		`{"inbounds":[{"port":443}]}`,
		`{"routing":"corrupted"}`,
		`{"inbounds":"x"}`,
	}
	for j, pl := range payloads {
		rec := doJSON(t, r, "POST", "/api/nodes/"+itoa64(int(id))+"/config", map[string]any{
			"proc": "xray", "kind": "xray", "payload": pl,
		})
		if rec.Code != http.StatusOK {
			t.Fatalf("push %d: %d %s", j, rec.Code, rec.Body)
		}
	}

	digest := func(s string) string {
		b := sha256.Sum256([]byte(s))
		return hex.EncodeToString(b[:])
	}
	for j, pl := range payloads {
		var row storage.NodeConfig
		if err := db.Where("node_id=? AND payload=?", id, pl).First(&row).Error; err != nil {
			t.Fatalf("trail %d: %v", j, err)
		}
		if row.Sha256 != digest(pl) || row.Version != digest(pl) {
			t.Fatalf("trail %d digest: sha=%s ver=%s", j, row.Sha256, row.Version)
		}
	}
	var okRow, badRow, revRow storage.NodeConfig
	if err := db.Where("node_id=? AND payload=?", id, payloads[0]).First(&okRow).Error; err != nil {
		t.Fatal(err)
	}
	if okRow.Status != "applied" || !okRow.Validated || okRow.Reverted || okRow.Error != "" {
		t.Fatalf("applied trail = %+v", okRow)
	}
	if err := db.Where("node_id=? AND payload=?", id, payloads[1]).First(&badRow).Error; err != nil {
		t.Fatal(err)
	}
	if badRow.Status != "failed" || !badRow.Validated || !containsStr(badRow.Error, "validation failed") {
		t.Fatalf("invalid trail = %+v", badRow)
	}
	if err := db.Where("node_id=? AND payload=?", id, payloads[2]).First(&revRow).Error; err != nil {
		t.Fatal(err)
	}
	if revRow.Status != "failed" || !revRow.Reverted {
		t.Fatalf("reverted trail = %+v", revRow)
	}

	// Never-delivered: an offline node answers 502 and still leaves a failed
	// trail row carrying the cause (tamper evidence survives failed delivery).
	rec := doJSON(t, r, "POST", "/api/nodes/"+itoa64(int(offID))+"/config", map[string]any{
		"proc": "xray", "kind": "xray", "payload": `{"inbounds":[]}`,
	})
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("offline push: %d %s", rec.Code, rec.Body)
	}
	var offRow storage.NodeConfig
	if err := db.Where("node_id=? AND status=?", offID, "failed").First(&offRow).Error; err != nil {
		t.Fatalf("offline trail: %v", err)
	}
	if offRow.Error == "" {
		t.Fatalf("offline trail must record the cause: %+v", offRow)
	}
}

// TestRoutingAdsTamperFallback covers F-1735's routing tamper line: a
// hand-tampered ads switch value must fall back to the design default (on)
// with a clean render, and the explicit-off value still wins over the default.
func TestRoutingAdsTamperFallback(t *testing.T) {
	r, db := newTestRouterWithDB(t)
	nid, _ := createNodeFor(t, r, "tamper-ads")

	if err := db.Create(&storage.Setting{Key: adsSettingKey, Value: "garbage"}).Error; err != nil {
		t.Fatal(err)
	}
	if !renderHasAdBlock(t, r, nid) {
		t.Fatal("tampered ads value must fall back to the default-on render")
	}

	if err := db.Model(&storage.Setting{}).Where("key=?", adsSettingKey).Update("value", "false").Error; err != nil {
		t.Fatal(err)
	}
	if renderHasAdBlock(t, r, nid) {
		t.Fatal("explicit off must exclude the ad-block set")
	}
}

// renderHasAdBlock renders the node routing config and reports whether the
// ad-block rule set survives the merge.
func renderHasAdBlock(t *testing.T, r *gin.Engine, nid uint) bool {
	t.Helper()
	rec := doJSON(t, r, "GET", "/api/nodes/"+itoa64(int(nid))+"/routing-config", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("render routing-config: %d %s", rec.Code, rec.Body)
	}
	var res struct {
		Sets []map[string]any `json:"sets"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	for _, s := range res.Sets {
		if s["name"] == "ad-block" {
			return true
		}
	}
	return false
}
