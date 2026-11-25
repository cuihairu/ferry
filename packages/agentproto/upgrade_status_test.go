package agentproto

import "testing"

// TestUpgradeStatusRoundTrip pins the new F-0712 message type and payload shape.
func TestUpgradeStatusRoundTrip(t *testing.T) {
	if MsgUpgradeStatus != "agent.upgrade_status" {
		t.Fatalf("message type drifted: %q", MsgUpgradeStatus)
	}
	want := UpgradeStatus{
		UpgradeID: "upg-1", Version: "v2", State: UpgradeStateFailed, Error: "checksum mismatch",
	}
	if !ValidUpgradeState(want.State) || ValidUpgradeState("teleporting") {
		t.Fatal("state validation broken")
	}
	env, err := NewEnvelope("upg-1-failed", MsgUpgradeStatus, want)
	if err != nil {
		t.Fatalf("envelope: %v", err)
	}
	var got UpgradeStatus
	if err := env.Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got != want || env.ID != "upg-1-failed" {
		t.Fatalf("round trip = %+v, want %+v", got, want)
	}
}
