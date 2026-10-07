package traffic

import (
	"context"
	"io"
	"log"
	"testing"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/packages/agentproto"
)

// fakeCollector 一个进程有数据，另一个未实现。
type fakeCollector struct{}

func (fakeCollector) Collect(proc string) (uint64, uint64, int, error) {
	if proc == "xray" {
		return 100, 200, 3, nil
	}
	return 0, 0, 0, errNotImplemented
}

func specs() []config.ProcSpec {
	return []config.ProcSpec{
		{Name: "xray", Kind: "xray"},
		{Name: "hysteria2", Kind: "hysteria2"},
	}
}

func TestReporterCollectsAndReports(t *testing.T) {
	sent := make(chan agentproto.Envelope, 4)
	r := New(specs(), 10*time.Millisecond, fakeCollector{}, log.New(io.Discard, "", 0))
	r.SetSend(func(env agentproto.Envelope) error {
		sent <- env
		return nil
	})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx)

	select {
	case env := <-sent:
		if env.Type != agentproto.MsgTraffic {
			t.Fatalf("expected traffic report, got %s", env.Type)
		}
		var tr agentproto.TrafficReport
		if err := env.Decode(&tr); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if len(tr.Items) != 1 {
			t.Fatalf("items = %d, want 1 (未实现的进程要跳过)", len(tr.Items))
		}
		it := tr.Items[0]
		if it.Proc != "xray" || it.Rx != 100 || it.Tx != 200 || it.Conns != 3 {
			t.Fatalf("item mismatch: %+v", it)
		}
		if it.At.IsZero() {
			t.Fatal("sample time must be set")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no traffic report sent")
	}
}

func TestReporterOfflineDropsSilently(t *testing.T) {
	r := New(specs(), 10*time.Millisecond, fakeCollector{}, log.New(io.Discard, "", 0))
	// 未绑定发送口（离线）：Run 不发送、不 panic。
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		r.Run(ctx)
		close(done)
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestCollectorForUnintegratedKindsNoop(t *testing.T) {
	for _, kind := range []string{"xray", "sing-box", "hysteria2"} {
		if _, _, _, err := CollectorFor(kind).Collect("xray"); err == nil {
			t.Fatalf("kind %s: P0 must not claim real collection", kind)
		}
	}
}

// fakeUserCollector 带 per-user 明细（对齐 xray gRPC stats 的采集口径）。
type fakeUserCollector struct{ fakeCollector }

func (fakeUserCollector) CollectUsers(proc string) ([]agentproto.UserTraffic, error) {
	if proc != "xray" {
		return nil, errNotImplemented
	}
	return []agentproto.UserTraffic{
		{Email: "alice", Rx: 100, Tx: 40},
		{Email: "bob", Rx: 60, Tx: 10},
	}, nil
}

func TestReporterPerUserPath(t *testing.T) {
	// per-user 采集器存在时：节点级 = per-user 求和，明细随 items 上报。
	r := New(specs(), 10*time.Millisecond, fakeUserCollector{}, log.New(io.Discard, "", 0))
	items := r.collectAll()
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	it := items[0]
	if it.Proc != "xray" || it.Rx != 160 || it.Tx != 50 {
		t.Fatalf("item = %+v, want rx=160 tx=50 (per-user 求和)", it)
	}
	if len(it.Users) != 2 || it.Users[0].Email != "alice" {
		t.Fatalf("users = %+v", it.Users)
	}
}

func TestDispatchRoutesXrayStats(t *testing.T) {
	// 配了 stats_api 的 xray 进程装配真实采集器；其余回退 Noop。
	specs := []config.ProcSpec{
		{Name: "xray", Kind: "xray", StatsAPI: "127.0.0.1:10085"},
		{Name: "sb", Kind: "sing-box"},
	}
	d := Dispatch(specs)
	uc, ok := d.(UserCollector)
	if !ok {
		t.Fatal("dispatcher must satisfy UserCollector")
	}
	if _, err := uc.CollectUsers("xray"); err == nil {
		t.Fatal("xray api 不可达时查询应报错（真实采集器已装配）")
	}
	if _, err := uc.CollectUsers("sb"); err != errNotImplemented {
		t.Fatalf("sb collect users err = %v, want errNotImplemented", err)
	}
}

// fakeBlockCollector 在 per-user 采集之上带拦截计数（对齐 xraystats.Collector）。
type fakeBlockCollector struct{ fakeUserCollector }

func (fakeBlockCollector) CollectBlocked(proc string) (uint64, error) {
	if proc != "xray" {
		return 0, errNotImplemented
	}
	return 77, nil
}

// TestReporterAttachesBlocked 覆盖拦截增量附带（SAVE-4）：支持
// BlockCollector 的采集器在同周期把 block 出站增量挂进行上；不支持的
// 采集器行上无该字段。
func TestReporterAttachesBlocked(t *testing.T) {
	// 支持拦截采集：xray 行带 BlockedBytes
	r := New(specs(), 10*time.Millisecond, fakeBlockCollector{}, log.New(io.Discard, "", 0))
	items := r.collectAll()
	if len(items) != 1 {
		t.Fatalf("items = %d, want 1", len(items))
	}
	if items[0].Proc != "xray" || items[0].BlockedBytes != 77 {
		t.Fatalf("item = %+v, want xray blocked=77", items[0])
	}

	// 不支持拦截采集（plain fake）：字段缺省不报错
	r2 := New(specs(), 10*time.Millisecond, fakeUserCollector{}, log.New(io.Discard, "", 0))
	for _, it := range r2.collectAll() {
		if it.BlockedBytes != 0 {
			t.Fatalf("no BlockCollector must leave blocked zero: %+v", it)
		}
	}

	// 拦截查询报错按缺失处理，不影响流量行
	r3 := New(specs(), 10*time.Millisecond, fakeBlockErr{}, log.New(io.Discard, "", 0))
	items = r3.collectAll()
	if len(items) != 1 || items[0].BlockedBytes != 0 || items[0].Rx != 160 {
		t.Fatalf("blocked query error must not break traffic row: %+v", items)
	}
}

// fakeBlockErr 拦截查询恒报错的采集器。
type fakeBlockErr struct{ fakeUserCollector }

func (fakeBlockErr) CollectBlocked(string) (uint64, error) { return 0, errNotImplemented }
