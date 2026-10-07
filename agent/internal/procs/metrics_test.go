//go:build unix

package procs

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cuihairu/ferry/agent/internal/config"
	"github.com/cuihairu/ferry/packages/agentproto"
)

// metricsResp 是测试指标端点的可切换应答（状态码 + 正文）。
type metricsResp struct {
	code int
	body string
}

// TestMetricsParser 覆盖指标解析：JSON 数字对象、文本 key-value
// 行（空行/单词行/非数字值跳过）、空与垃圾输入得空映射。
func TestMetricsParser(t *testing.T) {
	p := newMetricsParser()

	// JSON 数字对象
	m, err := p.Parse(strings.NewReader(`{"hits":123,"misses":45}`))
	if err != nil || m["hits"] != 123 || m["misses"] != 45 {
		t.Fatalf("json parse = %v, %v", m, err)
	}

	// 文本 key-value 行：空行与单词行跳过、非数字值跳过
	m, err = p.Parse(strings.NewReader("hits 123\n\nbadline\nmisses notanum\nproxies 7\n"))
	if err != nil || m["hits"] != 123 || m["proxies"] != 7 {
		t.Fatalf("text parse = %v, %v", m, err)
	}
	if _, ok := m["misses"]; ok {
		t.Fatalf("non-numeric value must be skipped: %v", m)
	}

	// 空与垃圾输入：空映射不报错
	for _, in := range []string{"", "   \n", "not json at all"} {
		m, err = p.Parse(strings.NewReader(in))
		if err != nil || len(m) != 0 {
			t.Fatalf("parse %q = %v, %v", in, m, err)
		}
	}
}

// TestCollectProcMetrics 覆盖指标采集协程：MetricsURL 轮询结果
// 挂状态快照随心跳上报（文本行与 JSON 两路），端点故障时
// 快照清空不残留旧值。
func TestCollectProcMetrics(t *testing.T) {
	var cur atomic.Value
	cur.Store(metricsResp{code: http.StatusOK, body: "hits 12\nmisses 3\n"})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		r := cur.Load().(metricsResp)
		if r.code != http.StatusOK {
			w.WriteHeader(r.code)
			return
		}
		fmt.Fprint(w, r.body)
	}))
	defer srv.Close()

	m, _ := testManager(t, []config.ProcSpec{{
		Name:       "cache",
		Kind:       "nginx",
		Exec:       "sh",
		Args:       []string{"-c", "sleep 60"},
		MetricsURL: srv.URL,
	}})
	m.metricsInterval = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	waitFor(t, m, "cache", agentproto.ProcRunning)

	// 文本行指标随状态快照上报
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range m.Statuses() {
			if s.Name == "cache" && s.Metrics != nil &&
				s.Metrics["hits"] == 12 && s.Metrics["misses"] == 3 {
				goto collected
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("text metrics never collected")
collected:

	// 换成 JSON 对象同样可采
	cur.Store(metricsResp{code: http.StatusOK, body: `{"hits": 99, "misses": 1}`})
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range m.Statuses() {
			if s.Name == "cache" && s.Metrics != nil && s.Metrics["hits"] == 99 {
				goto jsonCollected
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("json metrics never collected")
jsonCollected:

	// 端点 500：快照清空，不残留旧值
	cur.Store(metricsResp{code: http.StatusInternalServerError})
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		for _, s := range m.Statuses() {
			if s.Name == "cache" && s.Metrics == nil {
				goto cleared
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("metrics not cleared on endpoint failure")
cleared:

	// 先停进程再结束：等待 goroutine 的退出日志落在
	// 测试窗口内，避免测试结束后写 t.Output() 触发 panic。
	if err := m.Control("cache", agentproto.ProcActionStop); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitFor(t, m, "cache", agentproto.ProcStopped)
}

// TestCollectProcMetricsNoURL 覆盖未配置 MetricsURL 的进程：
// 不起采集，状态快照无指标。
func TestCollectProcMetricsNoURL(t *testing.T) {
	m, _ := testManager(t, []config.ProcSpec{{
		Name: "plain", Kind: "xray", Exec: "sh", Args: []string{"-c", "sleep 60"},
	}})
	m.metricsInterval = 20 * time.Millisecond
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	m.Start(ctx)
	waitFor(t, m, "plain", agentproto.ProcRunning)
	time.Sleep(200 * time.Millisecond)
	for _, s := range m.Statuses() {
		if s.Name == "plain" && s.Metrics != nil {
			t.Fatalf("metrics must stay nil without MetricsURL: %v", s.Metrics)
		}
	}
	// 先停进程再结束：等待 goroutine 的退出日志落在
	// 测试窗口内，避免测试结束后写 t.Output() 触发 panic。
	if err := m.Control("plain", agentproto.ProcActionStop); err != nil {
		t.Fatalf("stop: %v", err)
	}
	waitFor(t, m, "plain", agentproto.ProcStopped)
}
