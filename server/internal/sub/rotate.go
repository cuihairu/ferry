// 订阅入口顺序（E-26）：按探测结论重排——已知延迟按 RTT 升序在
// 前、无探测数据殿后，结论相同（同 RTT 档）的条目按轮换窗口轮转。
// 客户端常取订阅首位，轮转把首位亲和度摊到同档各入口；无探测数据
// 时保持原顺序（不轮转，行为与 E-19 一致）。
// 口径：只作用 v2ray 订阅——客户端直接消费列表顺序；clash 的
// url-test 组由客户端按自测延迟选优，服务端排序无消费方，不动。
package sub

import (
	"sort"
	"time"
)

// rotateWindowSec 是同 RTT 档条目轮换周期（秒）：窗口内顺序固定，
// 跨窗口轮转一位。轮换位由时间无状态派生，订阅侧不持久化。
const rotateWindowSec = 600

// orderByProbe 返回按探测结论重排的副本：区域归组（稳定，与 v2ray
// 分组口径一致），组内已知 RTT 升序、无数据（0）殿后，同 RTT 档
// 轮转。now 注入便于测试。
func orderByProbe(entries []Entry, now time.Time) []Entry {
	out := make([]Entry, len(entries))
	copy(out, entries)
	sort.SliceStable(out, func(i, j int) bool {
		return entryRegion(&out[i]) < entryRegion(&out[j])
	})
	for start := 0; start < len(out); {
		end := start + 1
		for end < len(out) && entryRegion(&out[end]) == entryRegion(&out[start]) {
			end++
		}
		orderSegment(out[start:end], now)
		start = end
	}
	return out
}

// orderSegment 对同一区域段按探测结论排序并轮转同档（原地）。
func orderSegment(seg []Entry, now time.Time) {
	// 已知档（rtt>0）在前按 RTT 升序，未知档（rtt==0）殿后保持原序。
	sort.SliceStable(seg, func(i, j int) bool {
		return probeTier(seg[i]) < probeTier(seg[j]) ||
			(probeTier(seg[i]) == probeTier(seg[j]) && seg[i].RttMs < seg[j].RttMs)
	})
	for start := 0; start < len(seg); {
		end := start + 1
		for end < len(seg) && sameVerdict(seg[start], seg[end]) {
			end++
		}
		// 仅已知档轮转：未知档无结论可比，保持原序。
		if seg[start].RttMs > 0 {
			offset := int(now.Unix()/rotateWindowSec) % (end - start)
			rotateRun(seg, start, end, offset)
		}
		start = end
	}
}

// probeTier 探测结论档：0=有结论（已知 RTT），1=无结论。
func probeTier(e Entry) int {
	if e.RttMs > 0 {
		return 0
	}
	return 1
}

// sameVerdict 判定两条目结论是否相同（同档同 RTT）。
func sameVerdict(a, b Entry) bool {
	return a.RttMs == b.RttMs
}

// rotateRun 把段内 [start,end) 轮转 offset 位（原地）。
func rotateRun(seg []Entry, start, end, offset int) {
	n := end - start
	if n <= 1 {
		return
	}
	offset %= n
	if offset < 0 {
		offset += n
	}
	if offset == 0 {
		return
	}
	tmp := make([]Entry, n)
	copy(tmp, seg[start:end])
	for i := 0; i < n; i++ {
		seg[start+i] = tmp[(i+offset)%n]
	}
}
