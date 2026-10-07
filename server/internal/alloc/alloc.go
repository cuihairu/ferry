// Package alloc 落地自动分配（E-21）：加权最小连接——按可配策略从落地池
// 为每个入口选落地，留痕到 landing_assignments（strategy != manual）。
// 手动分配优先：有生效中 manual 行（入口级或所在区域级）的入口不参与自动。
// 信号：连接数取节点流量上报最新采样（A-20），健康取窗口内最新探测结论，
// 容量=实测校准带宽>套餐带宽，成本只计「按流量」节点的边际流量单价。
package alloc

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/cuihairu/ferry/packages/agentproto"
	"github.com/cuihairu/ferry/server/internal/notify"
	"github.com/cuihairu/ferry/server/internal/storage"
	"gorm.io/gorm"
)

// 分配策略取值（与 LandingAssignment.Strategy 对齐，manual 不参与自动）。
const (
	PolicyLeastConn = "least_conn"
	PolicyCostFirst = "cost_first"
	PolicyPerfFirst = "perf_first"
	PolicyBalanced  = "balanced"
	PolicyManual    = "manual"
)

// settingKey 是策略持久化键（panel KV）；值 JSON：每方向一档。
const settingKey = "alloc_policy"

// 判定口径默认值。
const (
	// DefaultPolicy 缺省策略：均衡。
	DefaultPolicy = PolicyBalanced
	// ProbeFresh 探测结论参与健康判定的时限，超龄视为无结论（按健康参与）。
	ProbeFresh = 30 * time.Minute
	// LoadFresh 连接数采样参与负载判定的时限，超龄视为无采样（按 0 连接）。
	LoadFresh = 10 * time.Minute
)

// ValidPolicy 策略取值是否合法。
func ValidPolicy(p string) bool {
	switch p {
	case PolicyLeastConn, PolicyCostFirst, PolicyPerfFirst, PolicyBalanced:
		return true
	}
	return false
}

// PolicySetting 每方向一档：出海与回国两池策略侧重不同（设计稿 §C.5.2）；
// 低峰窗口（小时，[Start,End) 左闭右开）内做再平衡——换线只需按策略全序
// 更优；峰时保守换线，防留痕抖动（E-22）。
type PolicySetting struct {
	Out string `json:"out"`
	In  string `json:"in"`
	// RebalanceStart/RebalanceEnd 低峰窗口小时（0-23），默认 1-7。
	RebalanceStart int `json:"rebalance_start"`
	RebalanceEnd   int `json:"rebalance_end"`
}

// 低峰窗口默认值。
const (
	DefaultRebalanceStart = 1
	DefaultRebalanceEnd   = 7
)

// DefaultPolicySetting 两方向都取缺省档。
func DefaultPolicySetting() PolicySetting {
	return PolicySetting{
		Out: DefaultPolicy, In: DefaultPolicy,
		RebalanceStart: DefaultRebalanceStart, RebalanceEnd: DefaultRebalanceEnd,
	}
}

// LoadPolicy 读取策略设置；未设置回缺省。
func LoadPolicy(db *gorm.DB) (PolicySetting, error) {
	setting := DefaultPolicySetting()
	raw, ok, err := storage.GetSetting(db, settingKey)
	if err != nil || !ok {
		return setting, err
	}
	if err := json.Unmarshal([]byte(raw), &setting); err != nil {
		return DefaultPolicySetting(), nil // 脏数据按缺省走，不阻断分配
	}
	setting.normalize()
	return setting, nil
}

// normalize 补齐非法字段为缺省档。
func (s *PolicySetting) normalize() {
	if !ValidPolicy(s.Out) {
		s.Out = DefaultPolicy
	}
	if !ValidPolicy(s.In) {
		s.In = DefaultPolicy
	}
	if !validWindow(s.RebalanceStart, s.RebalanceEnd) {
		s.RebalanceStart, s.RebalanceEnd = DefaultRebalanceStart, DefaultRebalanceEnd
	}
}

// validWindow 低峰窗口口径：0-23 且 start < end（左闭右开，不支持跨日）。
func validWindow(start, end int) bool {
	return start >= 0 && start <= 23 && end >= 0 && end <= 23 && start < end
}

// SavePolicy 校验并持久化策略设置。
func SavePolicy(db *gorm.DB, setting PolicySetting) error {
	if !ValidPolicy(setting.Out) || !ValidPolicy(setting.In) {
		return fmt.Errorf("policy must be %s/%s/%s/%s", PolicyLeastConn, PolicyCostFirst, PolicyPerfFirst, PolicyBalanced)
	}
	if !validWindow(setting.RebalanceStart, setting.RebalanceEnd) {
		return fmt.Errorf("rebalance window must be 0-23 with start < end")
	}
	raw, err := json.Marshal(setting)
	if err != nil {
		return err
	}
	return storage.SetSetting(db, settingKey, string(raw))
}

// InOffPeak 判定时刻是否在低峰再平衡窗口内。
func InOffPeak(now time.Time, s PolicySetting) bool {
	s.normalize()
	h := now.Hour()
	return h >= s.RebalanceStart && h < s.RebalanceEnd
}

// Candidate 是一个落地候选的打分输入。
type Candidate struct {
	Node    storage.Node
	Conns   int  // 最新连接数（无采样=0）
	Healthy bool // 窗口内最新探测结论非 sick（无结论按健康参与）
}

// CapacityMbps 转发容量（Mbps）：实测校准 > 套餐下行 > 套餐上行 > 100 兜底。
// 带宽作容量权重——3M 小带宽按容量少分新连接，不把小水管灌爆。
func (c Candidate) CapacityMbps() int {
	switch {
	case c.Node.SpeedMeasuredMbps > 0:
		return c.Node.SpeedMeasuredMbps
	case c.Node.BwDownMbps > 0:
		return c.Node.BwDownMbps
	case c.Node.BwUpMbps > 0:
		return c.Node.BwUpMbps
	}
	return 100
}

// CostPerGB 边际流量成本（分/GB）：仅「按流量计费」参与成本判定；
// 包月/固定带宽的月固定成本是沉没成本，不影响分配（设计稿 §C.5.2 计费口径）。
func (c Candidate) CostPerGB() int64 {
	if c.Node.BillingType != "按流量" {
		return 0
	}
	return c.Node.TrafficPriceCents
}

// Pick 按策略在候选中选最优，返回下标；无可分配（全病或空）返回 false。
// 口径：
//   - least_conn：负载比（连接/容量M）最低，平手取低成本；
//   - cost_first：边际成本最低，平手取低负载比；
//   - perf_first：回国线优质线档（cn2_gia/cu_vip/cmi/iplc）优先，再按负载比；
//   - balanced：负载/成本归一加权 0.5+0.3，优质线回国线减 0.2（仅 direction=in）。
//
// 同分取名字字典序，保证结果确定可测。
func Pick(policy, direction string, cands []Candidate) (int, bool) {
	healthy := make([]int, 0, len(cands))
	for i, c := range cands {
		if c.Healthy {
			healthy = append(healthy, i)
		}
	}
	if len(healthy) == 0 {
		return 0, false
	}
	if len(healthy) == 1 {
		return healthy[0], true
	}
	less := lessFor(policy, direction, cands)
	best := healthy[0]
	for _, i := range healthy[1:] {
		if less(i, best) {
			best = i
		}
	}
	return best, true
}

// 载荷指标：负载比（连接/容量M）——3M 小带宽按容量少分。
func loadRatio(c Candidate) float64 {
	return float64(c.Conns) / float64(c.CapacityMbps())
}

// costPer 边际流量成本（分/GB）。
func costPer(c Candidate) float64 {
	return float64(c.CostPerGB())
}

// premiumOf 线路档只作用于回国线（设计稿 §C.5.2）；出海线不分线档。
func premiumOf(c Candidate, direction string) bool {
	return direction == agentproto.DirectionIn && isPremiumLine(c.Node.LineType)
}

// lessFor 生成策略比较函数：less(a,b)=a 是否优于 b。
func lessFor(policy, direction string, cands []Candidate) func(a, b int) bool {
	load := func(i int) float64 { return loadRatio(cands[i]) }
	cost := func(i int) float64 { return costPer(cands[i]) }
	premium := func(i int) bool { return premiumOf(cands[i], direction) }
	byName := func(a, b int) bool { return cands[a].Node.Name < cands[b].Node.Name }
	switch policy {
	case PolicyCostFirst:
		return func(a, b int) bool {
			if cost(a) != cost(b) {
				return cost(a) < cost(b)
			}
			if load(a) != load(b) {
				return load(a) < load(b)
			}
			return byName(a, b)
		}
	case PolicyPerfFirst:
		return func(a, b int) bool {
			if pa, pb := premium(a), premium(b); pa != pb {
				return pa // 优质线优先
			}
			if load(a) != load(b) {
				return load(a) < load(b)
			}
			if cost(a) != cost(b) {
				return cost(a) < cost(b)
			}
			return byName(a, b)
		}
	case PolicyBalanced:
		// 归一化到 [0,1] 后加权；优质线作固定减项。
		all := make([]int, len(cands))
		for i := range cands {
			all[i] = i
		}
		loadN := normalize(cands, all, load)
		costN := normalize(cands, all, cost)
		return func(a, b int) bool {
			sa := 0.5*loadN(a) + 0.3*costN(a)
			sb := 0.5*loadN(b) + 0.3*costN(b)
			if premium(a) {
				sa -= 0.2
			}
			if premium(b) {
				sb -= 0.2
			}
			if sa != sb {
				return sa < sb
			}
			return byName(a, b)
		}
	default: // least_conn
		return func(a, b int) bool {
			if load(a) != load(b) {
				return load(a) < load(b)
			}
			if cost(a) != cost(b) {
				return cost(a) < cost(b)
			}
			return byName(a, b)
		}
	}
}

// 峰时换线阈值：主指标须显著更优（省 30% 以上）才动，防留痕抖动（E-22）。
const peakSwitchMargin = 0.7

// shouldSwitch 峰时换线判定（低峰窗口直接按策略全序换，不走此判定）。
// 现行不在健康候选内（病了/下线）时由调用方强制换线，不进此函数。
func shouldSwitch(policy, direction string, cands []Candidate, cur, win int) bool {
	switch policy {
	case PolicyPerfFirst:
		// 峰时只允许线档升级（普线→优质线）；降级留到低峰再平衡。
		return !premiumOf(cands[cur], direction) && premiumOf(cands[win], direction)
	case PolicyCostFirst:
		curCost := costPer(cands[cur])
		return curCost > 0 && costPer(cands[win]) < curCost*peakSwitchMargin
	case PolicyBalanced:
		all := make([]int, len(cands))
		for i := range cands {
			all[i] = i
		}
		loadN := normalize(cands, all, func(i int) float64 { return loadRatio(cands[i]) })
		costN := normalize(cands, all, func(i int) float64 { return costPer(cands[i]) })
		score := func(i int) float64 {
			s := 0.5*loadN(i) + 0.3*costN(i)
			if premiumOf(cands[i], direction) {
				s -= 0.2
			}
			return s
		}
		return score(win) < score(cur)-0.15 // 综合分显著更优
	default: // least_conn
		curLoad := loadRatio(cands[cur])
		return loadRatio(cands[win]) < curLoad*peakSwitchMargin
	}
}

// isPremiumLine 回国优质线档。
func isPremiumLine(lineType string) bool {
	switch lineType {
	case "cn2_gia", "cu_vip", "cmi", "iplc":
		return true
	}
	return false
}

// normalize 把指标在健康候选内 min-max 归一到 [0,1]；全同值返回常数 0.5。
func normalize(cands []Candidate, idx []int, metric func(int) float64) func(int) float64 {
	lo, hi := metric(idx[0]), metric(idx[0])
	for _, i := range idx[1:] {
		v := metric(i)
		if v < lo {
			lo = v
		}
		if v > hi {
			hi = v
		}
	}
	if hi == lo {
		return func(int) float64 { return 0.5 }
	}
	return func(i int) float64 { return (metric(i) - lo) / (hi - lo) }
}

// SwitchEvent 是一次自动换线。
type SwitchEvent struct {
	EntryID   uint
	EntryName string
	FromName  string
	ToName    string
	Direction string
	Policy    string
	Reason    string
}

// Loop 周期执行分配判定直到 ctx 取消；换线记日志并外发事件。
func Loop(ctx context.Context, db *gorm.DB, interval time.Duration, logger *log.Logger) {
	if logger == nil {
		logger = log.Default()
	}
	if interval <= 0 {
		interval = 5 * time.Minute
	}
	t := time.NewTimer(0) // 启动即跑一轮
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if err := sweepOnce(db, logger); err != nil {
			logger.Printf("alloc sweep: %v", err)
		}
		t.Reset(interval)
	}
}

func sweepOnce(db *gorm.DB, logger *log.Logger) error {
	events, err := Sweep(db)
	if err != nil {
		return err
	}
	for _, ev := range events {
		logger.Printf("alloc switch: entry %d %s %s %s -> %s（%s）",
			ev.EntryID, ev.EntryName, ev.Direction, ev.FromName, ev.ToName, ev.Reason)
		announce(db, logger, ev)
	}
	return nil
}

// Sweep 执行一轮分配：每个可分配入口（enabled、池内 active、无生效中
// manual 分配）按其方向策略选落地；与现行 auto 行不一致时释放旧行留痕
// 并新建。峰时换线保守（主指标显著更优才动），低峰窗口按策略全序再平衡。
// 返回换线事件。全病候选时该入口本轮不动（保现行，等恢复）。
func Sweep(db *gorm.DB) ([]SwitchEvent, error) {
	return SweepAt(db, time.Now())
}

// SweepAt 是 Sweep 的时钟注入版，供测试固定时刻。
func SweepAt(db *gorm.DB, now time.Time) ([]SwitchEvent, error) {
	policy, err := LoadPolicy(db)
	if err != nil {
		return nil, err
	}
	var entries []storage.Node
	if err := db.Where("enabled = ? AND role IN (?, ?) AND pool_state = ?",
		true, "entry", "both", "active").Find(&entries).Error; err != nil {
		return nil, err
	}
	var landings []storage.Node
	if err := db.Where("enabled = ? AND role IN (?, ?)", true, "landing", "both").
		Find(&landings).Error; err != nil {
		return nil, err
	}
	conns := latestConns(db)
	sick := latestSick(db)

	var events []SwitchEvent
	offPeak := InOffPeak(now, policy)
	for _, entry := range entries {
		direction := entry.Direction
		if direction == agentproto.DirectionBoth {
			// relay 单进程单方向（单落地地址），both 入口按出海档分配。
			direction = agentproto.DirectionOut
		}
		if direction != agentproto.DirectionIn && direction != agentproto.DirectionOut {
			direction = agentproto.DirectionOut
		}
		// 手动分配优先：入口级或所在区域级有生效中 manual 行则跳过。
		manual, err := hasManualAssignment(db, entry.ID, entry.Region)
		if err != nil {
			return events, err
		}
		if manual {
			continue
		}
		// 候选收集：方向匹配且健康（最新结论非 sick）。
		cands := make([]Candidate, 0, len(landings))
		for _, l := range landings {
			if l.ID == entry.ID {
				continue
			}
			if l.Direction != direction && l.Direction != agentproto.DirectionBoth {
				continue
			}
			cands = append(cands, Candidate{Node: l, Conns: conns[l.ID], Healthy: !sick[l.ID]})
		}
		// 现行 auto 行（同入口同方向最新一条生效中）。
		current, err := currentAuto(db, entry.ID, direction)
		if err != nil {
			return events, err
		}
		idx, ok := Pick(policyFor(policy, direction), direction, cands)
		if !ok {
			continue // 全病或无候选：保现行，等恢复
		}
		winner := cands[idx].Node
		// 现行落地在健康候选内时走峰时保守判定；不在（病了/下线/无现行）
		// 则强制换线——健康问题不排队。
		curIdx := -1
		if current != nil {
			for i := range cands {
				if cands[i].Healthy && cands[i].Node.ID == current.LandingNodeID {
					curIdx = i
					break
				}
			}
		}
		if curIdx >= 0 {
			if curIdx == idx {
				continue // 现行即最优，不落行防抖动
			}
			// 峰时须主指标显著更优才动；低峰窗口按策略全序再平衡。
			if !offPeak && !shouldSwitch(policyFor(policy, direction), direction, cands, curIdx, idx) {
				continue
			}
		}
		c := cands[idx]
		reason := fmt.Sprintf("%s 负载 %d 连/%dM", policyFor(policy, direction), c.Conns, c.CapacityMbps())
		if current != nil {
			if err := db.Model(&storage.LandingAssignment{}).Where("id = ?", current.ID).
				Updates(map[string]any{"released_at": now, "release_reason": "自动换线"}).Error; err != nil {
				return events, err
			}
		}
		row := storage.LandingAssignment{
			EntryNodeID:   &entry.ID,
			LandingNodeID: winner.ID,
			Direction:     direction,
			Strategy:      policyFor(policy, direction),
			Weight:        0,
			Reason:        reason,
			AssignedAt:    now,
		}
		if err := db.Create(&row).Error; err != nil {
			return events, err
		}
		fromName := "（无）"
		if current != nil {
			fromName = nodeName(landings, current.LandingNodeID)
		}
		events = append(events, SwitchEvent{
			EntryID:   entry.ID,
			EntryName: entry.Name,
			FromName:  fromName,
			ToName:    winner.Name,
			Direction: direction,
			Policy:    policyFor(policy, direction),
			Reason:    reason,
		})
	}
	return events, nil
}

// policyFor 取方向对应档位。
func policyFor(p PolicySetting, direction string) string {
	if direction == agentproto.DirectionIn {
		return p.In
	}
	return p.Out
}

// hasManualAssignment 入口级或所在区域级是否有生效中 manual 分配。
func hasManualAssignment(db *gorm.DB, entryID uint, region string) (bool, error) {
	var n int64
	if err := db.Model(&storage.LandingAssignment{}).
		Where("released_at IS NULL AND strategy = ? AND (entry_node_id = ? OR (entry_node_id IS NULL AND region = ?))",
			PolicyManual, entryID, region).
		Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

// currentAuto 取入口同方向最新一条生效中 auto 行。
func currentAuto(db *gorm.DB, entryID uint, direction string) (*storage.LandingAssignment, error) {
	var row storage.LandingAssignment
	err := db.Where("released_at IS NULL AND strategy != ? AND entry_node_id = ? AND direction = ?",
		PolicyManual, entryID, direction).
		Order("id DESC").First(&row).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &row, nil
}

// latestConns 汇总各节点最新采样连接数：每进程取窗口内最新一条再按节点求和。
func latestConns(db *gorm.DB) map[uint]int {
	out := map[uint]int{}
	since := time.Now().Add(-LoadFresh)
	var logs []storage.NodeTrafficLog
	if err := db.Where("recorded_at > ?", since).
		Order("recorded_at ASC, id ASC").Find(&logs).Error; err != nil {
		return out // 查询失败按无采样处理（负载 0），不阻断分配
	}
	perProc := map[uint]map[string]int{}
	for _, l := range logs {
		if perProc[l.NodeID] == nil {
			perProc[l.NodeID] = map[string]int{}
		}
		perProc[l.NodeID][l.Proc] = l.Conns // 后写覆盖=每进程最新
	}
	for nodeID, procs := range perProc {
		total := 0
		for _, c := range procs {
			total += c
		}
		out[nodeID] = total
	}
	return out
}

// latestSick 取窗口内有最新 sick 结论的目标节点集合；不在集合内=健康或无结论
// （无结论按健康参与，冷启动才能完成首次分配）。
func latestSick(db *gorm.DB) map[uint]bool {
	out := map[uint]bool{}
	since := time.Now().Add(-ProbeFresh)
	var reports []storage.ProbeReport
	if err := db.Where("probed_at > ? AND target_kind IN ? AND target_node_id IS NOT NULL",
		since, []string{agentproto.ProbeTargetTunnel, agentproto.ProbeTargetPeer}).
		Order("probed_at ASC, id ASC").Find(&reports).Error; err != nil {
		return out
	}
	for _, r := range reports {
		out[*r.TargetNodeID] = r.Verdict == agentproto.ProbeVerdictSick // 后写覆盖=最新
	}
	return out
}

func nodeName(nodes []storage.Node, id uint) string {
	for _, n := range nodes {
		if n.ID == id {
			return n.Name
		}
	}
	return fmt.Sprintf("#%d", id)
}

// announce 自动换线外发事件（P1-10 通知通道）；失败只记日志不阻断。
func announce(db *gorm.DB, logger *log.Logger, ev SwitchEvent) {
	n := notify.FromDB(db)
	if !n.Enabled() {
		return
	}
	e := notify.Event{
		Event: "alloc.switch",
		Text:  fmt.Sprintf("落地自动换线：%s %s 由 %s 切至 %s（%s）", ev.EntryName, ev.Direction, ev.FromName, ev.ToName, ev.Reason),
		Fields: map[string]any{
			"entry_id": ev.EntryID, "direction": ev.Direction,
			"policy": ev.Policy, "reason": ev.Reason,
		},
	}
	if err := n.Send(e); err != nil {
		logger.Printf("notify webhook: %v", err)
	}
}
