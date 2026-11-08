package upgrade

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// TestDoDownloadBodyError 下载中途断流（声明长度与实际不符）：报
// download body 错误，原二进制不动、无标记。
func TestDoDownloadBodyError(t *testing.T) {
	self, _ := writeFakeBin(t, t.TempDir(), "agent", "#!/bin/sh\n")
	calls := stubExec(t)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		_, _ = w.Write([]byte("#!/bin/sh")) // 只给一部分即断开
	}))
	defer srv.Close()

	err := Do(context.Background(), self, agentproto.Upgrade{Version: "v2", URL: srv.URL}, nil)
	if err == nil || !strings.Contains(err.Error(), "download body") {
		t.Fatalf("err = %v; want download body error", err)
	}
	if len(*calls) != 0 {
		t.Fatalf("no exec on failure, calls=%v", *calls)
	}
	if Pending(self) {
		t.Fatal("no pending marker expected on failure")
	}
	got, err := os.ReadFile(self)
	if err != nil || string(got) != "#!/bin/sh\n" {
		t.Fatalf("self binary must stay intact: %q (%v)", got, err)
	}
}
