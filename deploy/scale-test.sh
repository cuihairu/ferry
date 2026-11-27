#!/usr/bin/env bash
# Scale / soak smoke (F-2061/F-2062/F-2063/F-2064/F-2065/F-2067/F-2073):
# drive the real server with many concurrent protocol speakers. The kernel
# (xray) stays the FERRY-004 external concern; the clients here are thin real
# protocol speakers importing packages/agentproto + gorilla/websocket, exactly
# like agent/internal/link -- nothing is invented.
# Legs: enroll N=50 nodes concurrently; dial 50 long-lived agent connections
# and handshake (F-2061); three heartbeat rounds over all 50 with ack-latency
# stats (F-2062); a probe-report flood of exactly 1000 rows with per-batch
# acks, all of which must land (F-2063); a subscription request load of 400
# hits at concurrency 20, all 200 (F-2064); a traffic flood of 200 frames from
# 50 agents whose byte sum must land exactly on the recon ledger, no
# double-count (F-2065); config batch push of probe tasks to all 50 online
# agents, every one delivered (F-2067); and a multi-agent fault: 25 of the 50
# connections drop at once (agents fall offline, server stays healthy), then
# the whole pool drops (F-2073). Latency percentiles and server RSS are
# printed as STATS lines for the scale report (F-2076). Data lands only in a
# temp dir; failures print log tails and exit 1. CI: test.yml deploy-scripts.
set -euo pipefail
cd "$(dirname "$0")/.."

PORT=${SCALE_PORT:-$(( (RANDOM % 200) + 19400 ))}
N_AGENTS=${SCALE_AGENTS:-50}
TMP=$(mktemp -d)
SERVER_PID=""
cleanup() {
  [ -n "$SERVER_PID" ] && kill "$SERVER_PID" 2>/dev/null || true
  [ "${FERRY_E2E_KEEP:-}" = 1 ] || rm -rf "$TMP"
}
trap cleanup EXIT
log() { echo "[scale] $*"; }
expect() {
  if [ "$2" = "$3" ]; then log "ok - $1"
  else echo "FAIL - $1：want [$2] got [$3]" >&2; dump; exit 1; fi
}
dump() {
  for f in server.log client.log; do
    if [ -f "$TMP/$f" ]; then
      echo "--- $f 尾部 ---" >&2
      tail -n 40 "$TMP/$f" >&2 || true
    fi
  done
}
json_get() { # FIELD FILE
  python3 -c 'import json,sys
try: print(json.load(open(sys.argv[2]))[sys.argv[1]])
except Exception: pass' "$1" "$2" 2>/dev/null || true
}

log "构建 ferry-server / 负载客户端 ..."
(cd server && go build -o "$TMP/ferry-server" ./cmd/ferry)
mkdir -p "$TMP/scale-src"
cat > "$TMP/scale-src/go.mod" <<EOF
module scaleclient

go 1.24

require (
	github.com/cuihairu/ferry/packages/agentproto v0.0.0
	github.com/gorilla/websocket v1.5.3
)

replace github.com/cuihairu/ferry/packages/agentproto => $(pwd)/packages/agentproto
EOF
cat > "$TMP/scale-src/main.go" <<'EOF'
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/gorilla/websocket"
)

type nodeRef struct {
	Idx   int    `json:"idx"`
	ID    int64  `json:"id"`
	Token string `json:"token"`
}

type stats struct {
	mu    sync.Mutex
	names []string
	durs  map[string][]time.Duration
}

func newStats() *stats { return &stats{durs: map[string][]time.Duration{}} }

func (s *stats) add(name string, d time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.durs[name]; !ok {
		s.names = append(s.names, name)
	}
	s.durs[name] = append(s.durs[name], d)
}

func (s *stats) print() {
	s.mu.Lock()
	defer s.mu.Unlock()
	ms := func(d time.Duration) float64 { return float64(d.Microseconds()) / 1000.0 }
	for _, name := range s.names {
		ds := append([]time.Duration(nil), s.durs[name]...)
		sort.Slice(ds, func(i, j int) bool { return ds[i] < ds[j] })
		pick := func(p float64) float64 {
			if len(ds) == 0 {
				return 0
			}
			i := int(p * float64(len(ds)-1))
			return ms(ds[i])
		}
		fmt.Printf("STAT %s n=%d p50_ms=%.1f p95_ms=%.1f p99_ms=%.1f max_ms=%.1f\n",
			name, len(ds), pick(0.50), pick(0.95), pick(0.99), ms(ds[len(ds)-1]))
	}
}

type agentConn struct {
	ref    nodeRef
	conn   *websocket.Conn
	reply  chan agentproto.Envelope
	closed chan struct{}
	once   sync.Once
}

// rpc sends one envelope and waits for the reply of the wanted type; replies
// are routed through the per-connection reader started by dial.
func (a *agentConn) rpc(id, typ string, payload any, want string) (agentproto.Envelope, time.Duration, error) {
	env, err := agentproto.NewEnvelope(id, typ, payload)
	if err != nil {
		return env, 0, err
	}
	raw, err := env.Marshal()
	if err != nil {
		return env, 0, err
	}
	start := time.Now()
	if err := a.conn.WriteMessage(websocket.TextMessage, raw); err != nil {
		return env, 0, err
	}
	_ = a.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
	for {
		select {
		case <-a.closed:
			return env, 0, fmt.Errorf("connection closed")
		case rep := <-a.reply:
			if rep.Type != want {
				continue
			}
			return rep, time.Since(start), nil
		case <-time.After(10 * time.Second):
			return env, 0, fmt.Errorf("timeout waiting for %s", want)
		}
	}
}

// reader drains unsolicited panel messages (e.g. probe_task dispatch) and
// replies to them, mirroring agent/internal/link's pump; everything else is
// forwarded to the rpc reply channel.
func (a *agentConn) reader() {
	defer a.once.Do(func() { close(a.closed) })
	a.conn.SetReadLimit(1 << 20)
	for {
		_, r, err := a.conn.ReadMessage()
		if err != nil {
			a.conn.Close()
			return
		}
		var env agentproto.Envelope
		if err := json.Unmarshal(r, &env); err != nil {
			continue
		}
		if env.Type == agentproto.MsgProbeTask {
			a.ackProbeTask(env)
			continue
		}
		a.reply <- env
	}
}

func (a *agentConn) ackProbeTask(env agentproto.Envelope) {
	var pt agentproto.ProbeTask
	if err := env.Decode(&pt); err != nil {
		return
	}
	reply, _ := agentproto.NewEnvelope(env.ID, agentproto.MsgProbeTaskAck, agentproto.ProbeTaskAck{
		Version: pt.Version, OK: true, Applied: len(pt.Specs),
	})
	if raw, err := reply.Marshal(); err == nil {
		a.conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
		a.conn.WriteMessage(websocket.TextMessage, raw)
	}
}

func dial(url, token string, ref nodeRef) (*agentConn, error) {
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		return nil, err
	}
	a := &agentConn{
		ref: ref, conn: conn,
		reply: make(chan agentproto.Envelope, 16),
		closed: make(chan struct{}),
	}
	go a.reader()
	meta := agentproto.NodeMeta{Role: "entry", Direction: "in", BwUpMbps: 10, BwDownMbps: 10}
	_, _, err = a.rpc(fmt.Sprintf("hello-%d", ref.Idx), agentproto.MsgHello, agentproto.Hello{
		Token: token, AgentID: fmt.Sprintf("scale-%d", ref.Idx), Version: "scale-e2e",
		StartedAt: time.Now(), Meta: meta,
	}, agentproto.MsgHelloAck)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return a, nil
}

func agentsOnline(healthURL string) (int, error) {
	resp, err := http.Get(healthURL)
	if err != nil {
		return -1, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return -1, err
	}
	var out struct {
		Checks map[string]any `json:"checks"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return -1, err
	}
	v, _ := out.Checks["agents_online"].(float64)
	return int(v), nil
}

func main() {
	sub := flag.String("sub", "", "subcommand: enroll|run")
	flag.Parse()
	args := flag.Args()
	switch *sub {
	case "enroll":
		base, admin, out := args[0], args[1], args[2]
		n, _ := strconv.Atoi(args[3])
		doEnroll(base, admin, out, n)
	case "run":
		url, tokens, health := args[0], args[1], args[2]
		closeRange := args[3]
		base, admin := args[4], args[5]
		doRun(url, tokens, health, closeRange, base, admin)
	default:
		fmt.Println("unknown subcommand")
		os.Exit(1)
	}
}

func doEnroll(base, admin, out string, n int) {
	type enrolled struct {
		Nodes []nodeRef `json:"nodes"`
	}
	refCh := make(chan nodeRef, n)
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	client := &http.Client{Timeout: 15 * time.Second}
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			cred := map[string]string{"note": fmt.Sprintf("scale-%d", idx)}
			cb, _ := json.Marshal(cred)
			req, _ := http.NewRequest("POST", base+"/api/enrollment-tokens", bytes.NewReader(cb))
			req.Header.Set("Authorization", "Bearer "+admin)
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
			if err != nil || resp.StatusCode != 201 {
				fmt.Printf("enroll token %d: status=%v err=%v\n", idx, statusOf(resp), err)
				os.Exit(1)
			}
			var ct struct{ Token string `json:"token"` }
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			json.Unmarshal(body, &ct)
			node := map[string]any{
				"name": fmt.Sprintf("scale-%d", idx), "address": "127.0.0.1",
				"port": 21000 + idx, "protocol": "vless",
				// Subscription rendering needs a usable per-node config (vless
				// requires config.uuid), exactly like a real provisioned node.
				"config": fmt.Sprintf(`{"uuid":"scale-uuid-%d","tls":true,"net":"ws","path":"/wss"}`, idx),
			}
			nb, _ := json.Marshal(node)
			req2, _ := http.NewRequest("POST", base+"/api/enroll", bytes.NewReader(nb))
			req2.Header.Set("Authorization", "Bearer "+ct.Token)
			req2.Header.Set("Content-Type", "application/json")
			resp2, err := client.Do(req2)
			if err != nil || resp2.StatusCode != 201 {
				fmt.Printf("enroll node %d: status=%v err=%v\n", idx, statusOf(resp2), err)
				os.Exit(1)
			}
			var ne struct {
				ID    int64  `json:"id"`
				Token string `json:"token"`
			}
			body2, _ := io.ReadAll(resp2.Body)
			resp2.Body.Close()
			json.Unmarshal(body2, &ne)
			refCh <- nodeRef{Idx: idx, ID: ne.ID, Token: ne.Token}
		}(i)
	}
	wg.Wait()
	close(refCh)
	refs := make([]nodeRef, 0, n)
	for r := range refCh {
		refs = append(refs, r)
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Idx < refs[j].Idx })
	b, _ := json.Marshal(enrolled{Nodes: refs})
	if err := os.WriteFile(out, b, 0o644); err != nil {
		fmt.Println("write tokens:", err)
		os.Exit(1)
	}
	fmt.Printf("ENROLL-OK n=%d\n", len(refs))
}

func statusOf(r *http.Response) int {
	if r == nil {
		return -1
	}
	return r.StatusCode
}

// doRun dials every node, handshakes, then drives the load phases; when
// closeRange is non-empty ("0-24"), those agent indices drop right after the
// hello phase (multi-agent fault) and the remaining pool is verified online.
func doRun(url, tokens, health, closeRange, base, admin string) {
	raw, err := os.ReadFile(tokens)
	if err != nil {
		fmt.Println("read tokens:", err)
		os.Exit(1)
	}
	var refs struct{ Nodes []nodeRef }
	if err := json.Unmarshal(raw, &refs); err != nil {
		fmt.Println("parse tokens:", err)
		os.Exit(1)
	}
	st := newStats()
	var mu sync.Mutex
	conns := make(map[int]*agentConn)
	var wg sync.WaitGroup
	fail := false
	for _, ref := range refs.Nodes {
		wg.Add(1)
		go func(ref nodeRef) {
			defer wg.Done()
			a, err := dial(url, ref.Token, ref)
			if err != nil {
				fmt.Printf("dial %d: %v\n", ref.Idx, err)
				mu.Lock(); fail = true; mu.Unlock()
				return
			}
			mu.Lock(); conns[ref.Idx] = a; mu.Unlock()
		}(ref)
	}
	wg.Wait()
	if fail {
		os.Exit(1)
	}
	fmt.Printf("HELLO-OK n=%d\n", len(conns))

	// F-2061 acceptance: every long connection is registered, the health
	// endpoint reports the full pool online.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		n, err := agentsOnline(health)
		if err == nil && n == len(conns) {
			fmt.Printf("ONLINE-OK n=%d\n", n)
			break
		}
		time.Sleep(500 * time.Millisecond)
	}

	// Multi-agent fault leg (F-2073): drop the given index range at once.
	if closeRange != "" {
		var lo, hi int
		if _, err := fmt.Sscanf(closeRange, "%d-%d", &lo, &hi); err != nil {
			fmt.Println("bad close range:", err)
			os.Exit(1)
		}
		closed := 0
		for i := lo; i <= hi; i++ {
			if a, ok := conns[i]; ok {
				a.conn.Close()
				mu.Lock(); delete(conns, i); mu.Unlock()
				closed++
			}
		}
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			n, err := agentsOnline(health)
			if err == nil && n == len(conns) {
				fmt.Printf("FAULT-OK closed=%d online=%d\n", closed, n)
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		// Hold the survivors briefly so the shell observes the halved pool
		// before every connection goes away at process exit.
		time.Sleep(2 * time.Second)
		fmt.Println("FAULT-DONE")
		return
	}

	// F-2062: heartbeat rounds over every agent, ack latency recorded.
	const rounds = 3
	hbOK := int64(0)
	var hbWg sync.WaitGroup
	for _, a := range conns {
		hbWg.Add(1)
		go func(a *agentConn) {
			defer hbWg.Done()
			for r := 0; r < rounds; r++ {
				hb := agentproto.Heartbeat{
					UptimeSec: 100, Load1: 0.5, CPUUtil: 12,
					MemUsedBytes: 1 << 30, MemTotalBytes: 4 << 30,
				}
				_, d, err := a.rpc(fmt.Sprintf("hb-%d-%d", a.ref.Idx, r),
					agentproto.MsgHeartbeat, hb, agentproto.MsgHeartbeatAck)
				if err != nil {
					fmt.Printf("heartbeat %d round %d: %v\n", a.ref.Idx, r, err)
					os.Exit(1)
				}
				st.add("heartbeat", d)
				mu.Lock(); hbOK++; mu.Unlock()
			}
		}(a)
	}
	hbWg.Wait()
	fmt.Printf("HEARTBEAT-OK total=%d\n", hbOK)

	// F-2063: probe-report flood; exactly rounds*items per agent so the shell
	// can count every landed row against the expected total.
	const pbBatches, pbItems = 10, 2
	var pbWg sync.WaitGroup
	for _, a := range conns {
		pbWg.Add(1)
		go func(a *agentConn) {
			defer pbWg.Done()
			for b := 0; b < pbBatches; b++ {
				items := make([]agentproto.ProbeReport, pbItems)
				for k := range items {
					items[k] = agentproto.ProbeReport{
						TargetKind: "tunnel",
						TargetHost: fmt.Sprintf("127.0.0.1:%d", 21000+a.ref.Idx),
						Direction:  "out", Reachable: true, Verdict: "healthy",
						RttMs: 42, ProbedAt: time.Now(),
					}
				}
				_, d, err := a.rpc(fmt.Sprintf("pb-%d-%d", a.ref.Idx, b),
					agentproto.MsgProbeReport, agentproto.ProbeReportBatch{Items: items},
					agentproto.MsgProbeAck)
				if err != nil {
					fmt.Printf("probe %d batch %d: %v\n", a.ref.Idx, b, err)
					os.Exit(1)
				}
				st.add("probe", d)
			}
		}(a)
	}
	pbWg.Wait()
	fmt.Printf("PROBE-OK batches=%d items=%d\n", len(conns)*pbBatches, len(conns)*pbBatches*pbItems)

	// F-2065: traffic flood with a deterministic byte sum: every frame carries
	// user scale-user rx=1000 tx=500 -> per node 6000, across 50 nodes 300000.
	const tfFrames = 4
	var tfWg sync.WaitGroup
	for _, a := range conns {
		tfWg.Add(1)
		go func(a *agentConn) {
			defer tfWg.Done()
			for f := 0; f < tfFrames; f++ {
				frame := agentproto.TrafficReport{Items: []agentproto.ProcTraffic{{
					Proc: "k1", Rx: 1000, Tx: 500, Conns: 5, At: time.Now(),
					Users: []agentproto.UserTraffic{{Email: "scale-user", Rx: 1000, Tx: 500}},
				}}}
				_, d, err := a.rpc(fmt.Sprintf("tf-%d-%d", a.ref.Idx, f),
					agentproto.MsgTraffic, frame, agentproto.MsgTrafficAck)
				if err != nil {
					fmt.Printf("traffic %d frame %d: %v\n", a.ref.Idx, f, err)
					os.Exit(1)
				}
				st.add("traffic", d)
			}
		}(a)
	}
	tfWg.Wait()
	fmt.Printf("TRAFFIC-OK frames=%d\n", len(conns)*tfFrames)

	// F-2067: config batch push to every live connection; each PUT must be
	// acked by its agent (delivered=true).
	delivered := 0
	var cpWg sync.WaitGroup
	cpSem := make(chan struct{}, 8)
	var cpMu sync.Mutex
	spec := []map[string]any{{
		"name": "scale-probe", "target_kind": "tunnel",
		"target": "127.0.0.1:1", "interval_sec": 30, "proto": "tcp",
	}}
	for _, a := range conns {
		cpWg.Add(1)
		go func(a *agentConn) {
			defer cpWg.Done()
			cpSem <- struct{}{}
			defer func() { <-cpSem }()
			body, _ := json.Marshal(map[string]any{"specs": spec})
			req, _ := http.NewRequest("PUT",
				fmt.Sprintf("%s/api/nodes/%d/probe_tasks", base, a.ref.ID), bytes.NewReader(body))
			req.Header.Set("Authorization", "Bearer "+admin)
			req.Header.Set("Content-Type", "application/json")
			resp, err := (&http.Client{Timeout: 20 * time.Second}).Do(req)
			if err != nil || resp.StatusCode != 200 {
				fmt.Printf("cfgpush %d: status=%v err=%v\n", a.ref.Idx, statusOf(resp), err)
				return
			}
			var out struct{ Delivered bool `json:"delivered"` }
			rb, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if json.Unmarshal(rb, &out) == nil && out.Delivered {
				cpMu.Lock(); delivered++
				cpMu.Unlock()
			}
		}(a)
	}
	cpWg.Wait()
	fmt.Printf("CFGPUSH-OK delivered=%d/%d\n", delivered, len(conns))

	st.print()
	fmt.Println("RUN-DONE")
}
EOF
(cd "$TMP/scale-src" && GOPROXY=off GOSUMDB=off GOFLAGS=-mod=mod go mod tidy > "$TMP/tidy.log" 2>&1 \
  && GOPROXY=off GOSUMDB=off go build -o "$TMP/scale-client" .) \
  || { echo "FAIL - 负载客户端构建失败" >&2; cat "$TMP/tidy.log" >&2 2>/dev/null; exit 1; }

log "启动 server ..."
FERRY_ADDR="127.0.0.1:$PORT" \
FERRY_DB_DRIVER=sqlite FERRY_DB_DSN="$TMP/e2e.db" \
FERRY_ADMIN_USER=admin FERRY_ADMIN_PASSWORD=e2e-pass-0123456 \
FERRY_KEYFILE="$TMP/keys.env" \
FERRY_BACKUP_DIR="$TMP/backup" FERRY_BACKUP_CRON="@every 24h" \
FERRY_HEARTBEAT_SEC=5 \
  "$TMP/ferry-server" > "$TMP/server.log" 2>&1 &
SERVER_PID=$!
BASE="http://127.0.0.1:$PORT"
code=""
for i in $(seq 1 40); do
  code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/health" || true)
  [ "$code" = 200 ] && break
  sleep 0.5
done
expect "server 可达" 200 "$code"

code=$(curl -s -o "$TMP/login.json" -w '%{http_code}' -X POST "$BASE/admin/login" \
  -H 'Content-Type: application/json' -d '{"username":"admin","password":"e2e-pass-0123456"}')
expect "管理员登录" 200 "$code"
ADMIN_TOKEN=$(json_get token "$TMP/login.json")
[ -n "$ADMIN_TOKEN" ] || { echo "FAIL - 登录响应缺 token" >&2; dump; exit 1; }

log "创建面板用户（订阅与记账共用）..."
code=$(curl -s -o "$TMP/user.json" -w '%{http_code}' -X POST "$BASE/api/users" \
  -H "Authorization: Bearer $ADMIN_TOKEN" -H 'Content-Type: application/json' \
  -d '{"username":"scale-user","quota_bytes":107374182400,"expires_at":"2030-01-01T00:00:00Z"}')
expect "创建用户 scale-user" 201 "$code"
SUBTOKEN=$(json_get sub_token "$TMP/user.json")
[ -n "$SUBTOKEN" ] || { echo "FAIL - 用户响应缺 sub_token" >&2; dump; exit 1; }

log "F-2061: 并发注册 $N_AGENTS 节点 + 并发长连接握手 ..."
T0=$(date +%s)
"$TMP/scale-client" -sub enroll "$BASE" "$ADMIN_TOKEN" "$TMP/nodes.json" "$N_AGENTS" > "$TMP/enroll.log" 2>&1
grep -q "ENROLL-OK n=$N_AGENTS" "$TMP/enroll.log" || { echo "FAIL - 注册未完成" >&2; cat "$TMP/enroll.log" >&2; dump; exit 1; }
log "ok - $N_AGENTS 节点并发注册（$(($(date +%s) - T0))s）"

T0=$(date +%s)
"$TMP/scale-client" -sub run "ws://127.0.0.1:$PORT/agent/ws" "$TMP/nodes.json" "http://127.0.0.1:$PORT/api/health" "" \
  "$BASE" "$ADMIN_TOKEN" > "$TMP/client.log" 2>&1
grep -q "RUN-DONE" "$TMP/client.log" || { echo "FAIL - 负载运行未完成" >&2; cat "$TMP/client.log" >&2; dump; exit 1; }
log "ok - 全阶段完成（$(($(date +%s) - T0))s）"
grep -q "HELLO-OK n=$N_AGENTS" "$TMP/client.log" || { echo "FAIL - 长连接握手不全" >&2; cat "$TMP/client.log" >&2; dump; exit 1; }
log "ok - $N_AGENTS 条长连接握手成功"
grep -q "ONLINE-OK n=$N_AGENTS" "$TMP/client.log" || { echo "FAIL - 在线池不完整" >&2; cat "$TMP/client.log" >&2; dump; exit 1; }
log "ok - health 报告在线节点数=$N_AGENTS"
grep -q "HEARTBEAT-OK total=$((N_AGENTS * 3))" "$TMP/client.log" && log "ok - 心跳 3 轮全部 ack"
grep -q "CFGPUSH-OK delivered=$N_AGENTS/$N_AGENTS" "$TMP/client.log" || { echo "FAIL - 配置下发送达不全" >&2; cat "$TMP/client.log" >&2; dump; exit 1; }
log "ok - 配置批量下发 $N_AGENTS 节点全部 delivered=true"

log "F-2063: 断言 1000 条探测报告全部落库 ..."
probe_ok=""
for i in $(seq 1 40); do
  curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
    "$BASE/api/probe-reports?limit=1000" -o "$TMP/probe.json"
  probe_ok=$(python3 - "$TMP/probe.json" <<'PY' 2>/dev/null || true
import json, sys
try:
    rows = json.load(open(sys.argv[1]))
    healthy = [r for r in rows if r.get("reachable") and r.get("verdict") == "healthy"]
    print("1" if len(rows) == 1000 and len(healthy) == 1000 else "")
except Exception:
    pass
PY
)
  [ "$probe_ok" = "1" ] && break
  sleep 0.5
done
expect "探测报告落库恰好 1000 条（reachable/healthy）" "1" "$probe_ok"

log "F-2064: 订阅请求负载（400 次，并发 20）..."
# The sub endpoint rate-limits per token+IP (30/min, router.go subLimiter), so
# a burst this hot MUST see 429s -- that is the throttle working, not a fault.
# The acceptance bar: zero 5xx (every render succeeds or is throttled), the
# limiter budget served, and the endpoint stays responsive throughout.
T0=$(date +%s)
seq 1 400 | xargs -P 20 -I{} curl -s -o /dev/null -w '%{http_code}\n' "$BASE/sub/$SUBTOKEN" \
  > "$TMP/sub-codes.txt"
SUB_ELAPSED=$(($(date +%s) - T0))
subverdict=$(python3 - "$TMP/sub-codes.txt" "$SUB_ELAPSED" <<'PY' 2>/dev/null || true
import sys
codes = [l.strip() for l in open(sys.argv[1]) if l.strip()]
from collections import Counter
c = Counter(codes)
ok, throttled, bad = c.get("200", 0), c.get("429", 0), c.get("500", 0) + c.get("502", 0) + c.get("503", 0)
assert len(codes) == 400, len(codes)
assert bad == 0, ("5xx under load", c)
assert ok >= 30, ("limiter budget not served", c)
assert throttled > 0, ("throttle never fired under hot burst", c)
print(f"200={ok} 429={throttled} bad={bad} {sys.argv[2]}s")
PY
)
if [ -n "$subverdict" ]; then log "ok - 订阅负载无 5xx，限流正确（$subverdict）"
else echo "FAIL - 订阅负载判定失败" >&2; cat "$TMP/sub-codes.txt" | sort | uniq -c >&2; dump; exit 1; fi

log "F-2065: 断言流量台账逐字节（50 节点 × 4 帧 × 1500B = 300000）..."
sum=""
for i in $(seq 1 40); do
  curl -s -H "Authorization: Bearer $ADMIN_TOKEN" \
    "$BASE/api/traffic-recon?days=1" -o "$TMP/recon.json"
  sum=$(python3 - "$TMP/recon.json" <<'PY' 2>/dev/null || true
import json, sys
try:
    rows = json.load(open(sys.argv[1]))["nodes"]
    print(sum(r["user_billed"] for r in rows))
except Exception:
    pass
PY
)
  [ "$sum" = "300000" ] && break
  sleep 0.5
done
expect "user 台账合计=300000" "300000" "$sum"
meters=$(python3 - "$TMP/recon.json" <<'PY' 2>/dev/null || true
import json, sys
rows = json.load(open(sys.argv[1]))["nodes"]
billed = [r for r in rows if r["user_billed"] == 6000]
metered = [r for r in rows if r["node_metered"] == 6000]
clean = [r for r in rows if r["delta"] == 0 and r["status"] == "ok"]
assert len(billed) == 50 and len(metered) == 50 and len(clean) == 50, (len(billed), len(metered), len(clean))
print("1")
PY
)
[ "$meters" = "1" ] || { echo "FAIL - 节点台账分布不对" >&2; dump; exit 1; }
log "ok - 50 节点双台账各 6000 且 delta=0"

wait_online() { # WANT TRIES
  local want="$1" tries="$2" got=""
  for i in $(seq 1 "$tries"); do
    curl -s "$BASE/api/health" -o "$TMP/health.json" 2>/dev/null || true
    got=$(python3 -c 'import json,sys
try: print(json.load(open(sys.argv[1]))["checks"]["agents_online"])
except Exception: pass' "$TMP/health.json" 2>/dev/null || true)
    [ "$got" = "$want" ] && break
    sleep 0.5
  done
  printf '%s' "$got"
}

log "F-2073: 多 agent 故障（一半连接同时断开）..."
"$TMP/scale-client" -sub run "ws://127.0.0.1:$PORT/agent/ws" "$TMP/nodes.json" "http://127.0.0.1:$PORT/api/health" "0-$((N_AGENTS / 2 - 1))" \
  "$BASE" "$ADMIN_TOKEN" > "$TMP/fault.log" 2>&1
grep -q "FAULT-OK closed=$((N_AGENTS / 2)) online=$((N_AGENTS / 2))" "$TMP/fault.log" \
  && log "ok - 一半连接断开后 agents_online=$((N_AGENTS / 2))"
grep -q "FAULT-DONE" "$TMP/fault.log" || { echo "FAIL - 故障腿未完成" >&2; cat "$TMP/fault.log" >&2; dump; exit 1; }
expect "客户端退出后全量下线" "0" "$(wait_online 0 30)"
code=$(curl -s -o /dev/null -w '%{http_code}' "$BASE/api/health")
expect "全量下线后 server 仍健康" 200 "$code"

log "F-2076: 资源占用与环境记录 ..."
RSS=$(grep VmRSS "/proc/$SERVER_PID/status" 2>/dev/null | awk '{print $2, $3}' || echo unknown)
CPUS=$(nproc)
log "STATS server_rss=$RSS cpus=$CPUS agents=$N_AGENTS sub_400_c20=${SUB_ELAPSED}s"
grep -E '^STAT ' "$TMP/client.log"

log "PASS - F-2061~2065/2067/2073 规模负载全绿（真协议层无 mock），STATS 行供 F-2076 归档"
