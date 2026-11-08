package traffic

import (
	"context"
	"errors"
	"io"
	"log"
	"testing"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
)

// TestDispatchUnknownProc 分发器对未注册进程报错；已注册进程正常委托。
func TestDispatchUnknownProc(t *testing.T) {
	d := dispatchCollector{"xray": fakeCollector{}}
	if _, _, _, err := d.Collect("ghost"); err == nil {
		t.Fatal("unknown proc should error")
	}
	rx, tx, conns, err := d.Collect("xray")
	if err != nil || rx != 100 || tx != 200 || conns != 3 {
		t.Fatalf("delegate = %d/%d/%d, err %v; want 100/200/3 nil", rx, tx, conns, err)
	}
}

// TestNewNilLogger logger 缺省：不 panic 且用默认 logger（只验证构造）。
func TestNewNilLogger(t *testing.T) {
	r := New(nil, time.Second, nil, nil)
	if r == nil || r.log == nil {
		t.Fatal("New must tolerate nil logger")
	}
}

// TestRunZeroIntervalReturns interval<=0 关闭周期采集：Run 立即返回。
func TestRunZeroIntervalReturns(t *testing.T) {
	r := New(nil, 0, fakeCollector{}, log.New(io.Discard, "", 0))
	done := make(chan struct{})
	go func() {
		r.Run(context.Background()) // ctx 永不取消，靠 interval<=0 自行退出
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run with interval<=0 must return immediately")
	}
}

// TestReportSendFailure 上报发出失败：记日志不 panic，数据留在采集侧。
func TestReportSendFailure(t *testing.T) {
	r := New(specs(), 10*time.Millisecond, fakeCollector{}, log.New(io.Discard, "", 0))
	r.SetSend(func(agentproto.Envelope) error { return errors.New("link down") })
	r.report(r.collectAll()) // 不应 panic；同类错误不重复打日志由 collectErr 管
}
