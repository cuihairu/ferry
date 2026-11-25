package upgrade

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// TestDoWithProgress covers the F-0712 progress reports: the happy path fires
// downloading/applying/applied and the failure path fires downloading/failed with the reason.
func TestDoWithProgress(t *testing.T) {
	self, _ := writeFakeBin(t, t.TempDir(), "agent", "#!/bin/sh\n")
	stubExec(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("#!/bin/sh\necho new\n"))
	}))
	defer srv.Close()

	var got []agentproto.UpgradeStatus
	err := DoWithProgress(context.Background(), self,
		agentproto.Upgrade{Version: "v2", URL: srv.URL}, nil,
		func(st agentproto.UpgradeStatus) { got = append(got, st) })
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected 3 progress frames, got %+v", got)
	}
	want := []string{"downloading", "applying", "applied"}
	for i := range want {
		if got[i].State != want[i] || got[i].Version != "v2" {
			t.Fatalf("frame %d = %+v, want state %s", i, got[i], want[i])
		}
	}
}

func TestDoWithProgressFailed(t *testing.T) {
	self, _ := writeFakeBin(t, t.TempDir(), "agent", "#!/bin/sh\n")
	stubExec(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	var got []agentproto.UpgradeStatus
	err := DoWithProgress(context.Background(), self,
		agentproto.Upgrade{Version: "v2", URL: srv.URL}, nil,
		func(st agentproto.UpgradeStatus) { got = append(got, st) })
	if err == nil {
		t.Fatal("404 should fail")
	}
	if len(got) != 2 || got[0].State != agentproto.UpgradeStateDownloading || got[1].State != agentproto.UpgradeStateFailed {
		t.Fatalf("frames = %+v, want downloading then failed", got)
	}
	if got[1].Error == "" {
		t.Fatal("failed frame must carry the reason")
	}
}
