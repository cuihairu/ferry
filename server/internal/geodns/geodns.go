// Package geodns 实现智能 DNS 分地域（E-27，入口与负载均衡设计 §C.8）：
// ferry 不自建权威 DNS、不做解析来源地域判定（判定在 GeoDNS 提供商线路
// 库）——本包只做区域代表入口选取（与 E-26 orderByProbe 同源口径）与
// {区域slug}.{前置域名} A 记录的事件驱动对账同步（幂等 Upsert、全挂撤
// 记录，挂池摘挂钩子位不建轮询），凭证复用 DNSProvider 通道（R24 加密面）。
// 同步子域不回写 entry_domains 人工清单：分地域是增益不是依赖，拿不到
// 就近域名时订阅列表内自选优兜底。
package geodns

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/cuihairu/ferry/server/internal/dns"
	"github.com/cuihairu/ferry/server/internal/herald"
	"github.com/cuihairu/ferry/server/internal/pool"
	"github.com/cuihairu/ferry/server/internal/ringlog"
	"github.com/cuihairu/ferry/server/internal/secret"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// rttWindow 与订阅侧 E-19 口径一致（sub.go entryRttMs）：窗口内可达
// 隧道探测的平均 RTT 参与代表入口选取。
const rttWindow = 30 * time.Minute

// Syncer 是一次分地域对账的执行体；New 可注入假通道（测试）。
type Syncer struct {
	DB    *gorm.DB
	Store *secret.Store
	New   func(kind, token string) (dns.Provider, error)
}

func (s *Syncer) factory() func(kind, token string) (dns.Provider, error) {
	if s.New != nil {
		return s.New
	}
	return dns.New
}

// RegionReps 每区域选代表入口（§C.8.2）：nodes(entry/both)+池内 active+
// 非 provisioning（sick 已由摘挂出局），30min 可达隧道 RTT 聚合，区域归组
// 组内 RTT 升序、无数据殿后、同值稳定序（id 升序）——与 E-26 orderByProbe
// 同源口径但只取各组首位。区域键为 nodes.region 原值；无入口的区域不出
// 现在结果里（全挂撤记录由 Sync 的 Delete 分支处理）。
func (s *Syncer) RegionReps(now time.Time) (map[string]storage.Node, error) {
	var nodes []storage.Node
	if err := s.DB.Where("enabled = ? AND role IN (?, ?) AND pool_state = ? AND status != ?",
		true, "entry", "both", pool.StateActive, "provisioning").
		Order("id").Find(&nodes).Error; err != nil {
		return nil, err
	}
	rtt := s.entryRttMs(now)
	groups := map[string][]storage.Node{}
	for _, n := range nodes {
		key := n.Region
		if key == "" {
			key = "未知"
		}
		groups[key] = append(groups[key], n)
	}
	reps := make(map[string]storage.Node, len(groups))
	for region, ns := range groups {
		sort.SliceStable(ns, func(i, j int) bool {
			ri, rj := rtt[ns[i].ID], rtt[ns[j].ID]
			ti, tj := tierOf(ri), tierOf(rj)
			if ti != tj {
				return ti < tj
			}
			if ti == 0 && ri != rj { // 有数据档按 RTT 升序
				return ri < rj
			}
			return ns[i].ID < ns[j].ID // 同档稳定：id 升序
		})
		reps[region] = ns[0]
	}
	return reps, nil
}

// tierOf 0=有探测数据（已知 RTT），1=无数据殿后（E-26 probeTier 同口径）。
func tierOf(rtt int) int {
	if rtt > 0 {
		return 0
	}
	return 1
}

// entryRttMs 聚合窗口内可达隧道探测的平均 RTT（与 sub.go 同款查询）。
func (s *Syncer) entryRttMs(now time.Time) map[uint]int {
	out := map[uint]int{}
	var rows []struct {
		NodeID uint
		Avg    float64
	}
	if err := s.DB.Model(&storage.ProbeReport{}).
		Select("node_id, AVG(rtt_ms) AS avg").
		Where("target_kind = ? AND reachable = ? AND probed_at > ?", "tunnel", true, now.Add(-rttWindow)).
		Group("node_id").Scan(&rows).Error; err != nil {
		return out
	}
	for _, r := range rows {
		out[r.NodeID] = int(r.Avg + 0.5)
	}
	return out
}

// Slug 区域词表归一（§C.8.3）：小写、空白与下划线转连字符，只保留
// [a-z0-9-]；归一后为空（如纯中文区域名）不产记录——区域词表建议
// ASCII slug（与 entry_domains.region 人工清单同源）。
func Slug(region string) string {
	s := strings.ToLower(strings.TrimSpace(region))
	s = strings.ReplaceAll(s, "_", "-")
	s = strings.Join(strings.Fields(s), "-")
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	return strings.Trim(b.String(), "-")
}

// Sync 对账同步（§C.8.4）：对每条启用 DNS 商名下的前置域名，把
// {slug}.{domain} A 记录对齐代表入口集——新增/变更 Upsert、消失 Delete
// （区域全挂撤记录）；已同步面落 GeoDNSRecord 作下轮 diff 基准。变化
// 留痕 admin 日志，单条失败经 herald 告警（按日 dedup）后继续其余记录。
// 未配置启用 DNS 商或前置域名时 no-op（功能未启用）。返回变更记录数。
func (s *Syncer) Sync(ctx context.Context) (int, error) {
	var provs []storage.DNSProvider
	if err := s.DB.Where("enabled = ?", true).Find(&provs).Error; err != nil {
		return 0, err
	}
	if len(provs) == 0 {
		return 0, nil
	}
	ids := make([]uint, 0, len(provs))
	for _, p := range provs {
		ids = append(ids, p.ID)
	}
	var fronts []storage.DNSFront
	if err := s.DB.Where("provider_id IN ?", ids).Find(&fronts).Error; err != nil {
		return 0, err
	}
	if len(fronts) == 0 {
		return 0, nil
	}
	if s.Store == nil || !s.Store.Enabled() {
		return 0, errors.New("geodns: secret master key not configured (set FERRY_SECRET_KEY)")
	}
	channels := map[uint]dns.Provider{}
	for _, p := range provs {
		token, err := s.Store.Decrypt(p.APIKey)
		if err != nil {
			return 0, fmt.Errorf("geodns: dns provider %s: %w", p.Name, err)
		}
		ch, err := s.factory()(p.Type, token)
		if err != nil {
			return 0, fmt.Errorf("geodns: dns provider %s: %w", p.Name, err)
		}
		channels[p.ID] = ch
	}
	reps, err := s.RegionReps(time.Now())
	if err != nil {
		return 0, err
	}
	var existing []storage.GeoDNSRecord
	if err := s.DB.Find(&existing).Error; err != nil {
		return 0, err
	}
	exByKey := make(map[string]storage.GeoDNSRecord, len(existing))
	for _, r := range existing {
		exByKey[fmt.Sprintf("%d/%s", r.FrontID, r.Slug)] = r
	}
	now := time.Now()
	changes := 0
	var firstErr error
	fail := func(format string, args ...any) {
		err := fmt.Errorf(format, args...)
		if firstErr == nil {
			firstErr = err
		}
		emitSyncFailed(s.DB, err)
	}
	for _, f := range fronts {
		ch := channels[f.ProviderID]
		want := map[string]string{}
		for region, n := range reps {
			sl := Slug(region)
			if sl == "" {
				continue // 区域词表非 ASCII slug，不产记录
			}
			want[sl] = n.Address
		}
		for sl, val := range want {
			key := fmt.Sprintf("%d/%s", f.ID, sl)
			if rec, ok := exByKey[key]; ok && rec.Value == val {
				continue // 无变化，幂等跳过
			}
			name := sl + "." + f.Domain
			if err := ch.Upsert(ctx, name, "A", val); err != nil {
				fail("geodns: upsert %s: %w", name, err)
				continue
			}
			rec := storage.GeoDNSRecord{FrontID: f.ID, Slug: sl, Name: name, Value: val, UpdatedAt: now}
			if prev, ok := exByKey[key]; ok {
				rec.ID = prev.ID
			}
			if err := s.DB.Save(&rec).Error; err != nil {
				fail("geodns: save record %s: %w", name, err)
				continue
			}
			changes++
		}
		// 撤记录：已同步面里该前置域名下不再存在代表入口的区域。
		for _, r := range existing {
			if r.FrontID != f.ID {
				continue
			}
			if _, ok := want[r.Slug]; ok {
				continue
			}
			if err := ch.Delete(ctx, r.Name, "A"); err != nil {
				fail("geodns: delete %s: %w", r.Name, err)
				continue
			}
			if err := s.DB.Delete(&storage.GeoDNSRecord{}, r.ID).Error; err != nil {
				fail("geodns: drop record %s: %w", r.Name, err)
				continue
			}
			changes++
		}
	}
	if changes > 0 {
		ringlog.Default().Add(fmt.Sprintf("geodns: 分地域对账 %d 条记录变更", changes))
	}
	return changes, firstErr
}

// emitSyncFailed 对账失败告警：本地同日至多一条（dedup 兜底，口径同
// price_alert）；未配 Herald 落本地 outbox 站内可见。
func emitSyncFailed(db *gorm.DB, cause error) {
	key := fmt.Sprintf("geodns_sync:%s", time.Now().Format("2006-01-02"))
	var n int64
	if err := db.Model(&storage.Event{}).
		Where("kind = ? AND dedup_key = ?", herald.KindGeoSyncFailed, key).Count(&n).Error; err != nil {
		log.Printf("geodns alert: %v", err)
		return
	}
	if n > 0 {
		return
	}
	if _, err := herald.Emit(db, herald.EmitInput{
		Kind:     herald.KindGeoSyncFailed,
		Severity: herald.SeverityWarning,
		Title:    "智能 DNS 分地域对账失败",
		Body:     cause.Error() + "（分地域记录未对齐，订阅自选优不受影响）",
		Target:   herald.TargetAdmin,
		DedupKey: key,
	}); err != nil {
		log.Printf("geodns alert: %v", err)
	}
}
