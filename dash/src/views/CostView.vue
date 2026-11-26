<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import {
  get, put, getEvening, getQuotaLink, putQuotaLink, getSaveStats, getBillRecords,
  spendByScope, spendTrend, getPathCosts,
  checkCostRef, probeCostRef, saveCostRefTable, ApiError,
  listPriceWatches, createPriceWatch, updatePriceWatch, deletePriceWatch,
  type EveningReport, type GroupCost, type NodeCost,
  type QuotaLinkData, type QuotaLinkSetting, type QuotaActionRow,
  type SaveStatsReport, type RefCheckReport, type RefProbe, type PriceWatch,
  type CostRecordRow, type SpendRow, type SpendTrendBucket, type PathCostRow,
} from '../api'
import { formatBytes, formatDate } from '../utils/format'

// E-23: cost dashboard — node traffic cost (only metered billing has marginal cost), region/ISP summaries,
// monthly projection (converted from the calendar month to date) and the high-cost alert threshold; nodes past
// the threshold get individual alerts in the alert list.
// E-29: the evening-peak return-trip report shares the page — 24-hour return-quality segments plus an evening-peak
// (19-23) vs off-peak comparison.
// SAVE-6: quota linkage — past a traffic/cost threshold the user's subscription is downgraded automatically
// (only low-cost-tier entries); the setting and downgrade records live on this page.
// SAVE-7: the traffic savings report shares the page — direct/interception aggregated daily with converted cost (same criterion).
// E-31: the cost reference library shares the page — public price-table import + hand-entered vs list-price deviation hints (hints only, never price changes).

interface CostReport {
  nodes: NodeCost[]
  regions: GroupCost[]
  isps: GroupCost[]
  summary: GroupCost
  threshold_cents: number
}

const loading = ref(false)
const rep = ref<CostReport | null>(null)
const evening = ref<EveningReport | null>(null)
const thresholdYuan = ref(50)
const saving = ref(false)

// Quota linkage (SAVE-6)
const link = ref<QuotaLinkData | null>(null)
const linkSetting = ref<QuotaLinkSetting>({ enabled: false, traffic_percent: 90, cost_cents: 0, max_price_cents: 0 })
const linkCostYuan = ref(0)
const linkPriceYuan = ref(0)
const linkSaving = ref(false)

// Savings report (SAVE-7)
const savings = ref<SaveStatsReport | null>(null)

async function load() {
  loading.value = true
  try {
    const [r, ev, ql, sv] = await Promise.all([get<CostReport>('/api/cost'), getEvening(7), getQuotaLink(), getSaveStats(30)])
    rep.value = r
    evening.value = ev
    thresholdYuan.value = Math.round((r?.threshold_cents ?? 0) / 100)
    savings.value = sv
    link.value = ql
    linkSetting.value = { ...ql.setting }
    linkCostYuan.value = ql.setting.cost_cents / 100
    linkPriceYuan.value = ql.setting.max_price_cents / 100
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)
onMounted(() => loadRefCheck())
onMounted(loadWatches)
onMounted(() => loadBills())
onMounted(() => loadSpend())
onMounted(() => loadTrend())
onMounted(() => loadPaths())

async function saveThreshold() {
  saving.value = true
  try {
    await put('/api/cost/threshold', { cents: Math.round(thresholdYuan.value * 100) })
    ElMessage.success('阈值已保存')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    saving.value = false
  }
}

async function saveLink() {
  linkSaving.value = true
  try {
    const payload: QuotaLinkSetting = {
      ...linkSetting.value,
      cost_cents: Math.round(linkCostYuan.value * 100),
      max_price_cents: Math.round(linkPriceYuan.value * 100),
    }
    const out = await putQuotaLink(payload)
    ElMessage.success('联动配置已保存')
    linkSetting.value = { ...out }
    linkCostYuan.value = out.cost_cents / 100
    linkPriceYuan.value = out.max_price_cents / 100
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    linkSaving.value = false
  }
}

function yuan(cents: number): string {
  return '¥' + (cents / 100).toFixed(2)
}

function gb(bytes: number): string {
  return formatBytes(bytes)
}

function linkTrigger(row: QuotaActionRow): string {
  return row.trigger === 'traffic' ? '流量档' : '费用档'
}

function linkState(row: QuotaActionRow): string {
  return row.released_at ? '已恢复' : '降档中'
}

// Evening-peak return-trip report view: show — when there are no samples, so 0 is never mistaken for a measurement.
function ms(v: number): string {
  return v > 0 ? v.toFixed(0) + ' ms' : '—'
}
function pct(v: number): string {
  return v < 0 ? '—' : v.toFixed(0) + '%'
}
function hourLabel(h: number): string {
  const p = (n: number) => String(n).padStart(2, '0')
  return `${p(h)}:00–${p((h + 1) % 24)}:00`
}
function isPeak(h: number): boolean {
  return h >= 19 && h < 23
}
function peakClass({ row }: { row: { hour: number } }): string {
  return isPeak(row.hour) ? 'peak-row' : ''
}
// Render the report only when return-trip samples exist, avoiding an all-empty table.
const hasEveningData = (): boolean => !!evening.value && evening.value.peak.samples + evening.value.offpeak.samples > 0
// Only hour rows with samples enter the table; empty hours take no row.
function eveningRows() {
  return (evening.value?.hours ?? []).filter((h) => h.samples > 0)
}

// ---- Cost reference library (E-31) ----

// Batch reconciliation: nodes with both datacenter and monthly fixed cost filled in are compared against list
// prices one by one; when no price table has been imported (400) the empty state stays silently and the empty-state
// copy guides the import — no error on a first visit.
const refCheck = ref<RefCheckReport | null>(null)
const refCheckLoading = ref(false)

async function loadRefCheck(quiet = true) {
  refCheckLoading.value = true
  try {
    refCheck.value = await checkCostRef()
  } catch (e) {
    if (!quiet || !(e instanceof ApiError && e.status === 400)) ElMessage.error(String(e))
  } finally {
    refCheckLoading.value = false
  }
}

// Price-table import: the JSON array text is handed to the backend for validation as-is (a dirty table is rejected); an empty string clears and disables it.
const refTable = ref('')
const refSaving = ref(false)

async function saveRefTable() {
  refSaving.value = true
  try {
    const r = await saveCostRefTable(refTable.value)
    ElMessage.success(r.cleared ? '已清空价格表' : `已导入 ${r.rows ?? 0} 行牌价`)
    refTable.value = ''
    await loadRefCheck()
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  } finally {
    refSaving.value = false
  }
}

// Single-point probe: look up a list price by a free-form key, optionally compare the deviation against the hand-entered price.
const probeForm = reactive({ provider: '', region: '', spec: '', manual: '', currency: '' })
const probeResult = ref<RefProbe | null>(null)
const probeLoading = ref(false)

async function runProbe() {
  if (!probeForm.provider.trim() || !probeForm.spec.trim()) {
    ElMessage.warning('商家与配置档必填')
    return
  }
  probeLoading.value = true
  try {
    probeResult.value = await probeCostRef({
      provider: probeForm.provider.trim(),
      region: probeForm.region.trim(),
      spec: probeForm.spec.trim(),
      manual_cents: probeForm.manual.trim() ? Number(probeForm.manual) : '',
      currency: probeForm.currency.trim(),
    })
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  } finally {
    probeLoading.value = false
  }
}

function refPct(pct: number): string {
  return (pct > 0 ? '+' : '') + pct + '%'
}

// ---- Price watch (E-32): condition CRUD; scanning and alerting run server-side hourly ----

const watches = ref<PriceWatch[]>([])
const watchesLoading = ref(false)
const newWatch = reactive({ provider: '', region: '', spec: '', target: 0 })
const addingWatch = ref(false)

async function loadWatches() {
  watchesLoading.value = true
  try {
    watches.value = await listPriceWatches()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    watchesLoading.value = false
  }
}

async function addWatch() {
  if (!newWatch.provider.trim() || !newWatch.spec.trim()) {
    ElMessage.warning('商家与配置档必填')
    return
  }
  addingWatch.value = true
  try {
    await createPriceWatch({
      provider: newWatch.provider.trim(),
      region: newWatch.region.trim(),
      spec: newWatch.spec.trim(),
      target_price: Math.round(newWatch.target * 100) || undefined,
    })
    newWatch.provider = ''
    newWatch.region = ''
    newWatch.spec = ''
    newWatch.target = 0
    watches.value = await listPriceWatches()
    ElMessage.success('已添加关注，扫描器下轮开始抓快照')
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  } finally {
    addingWatch.value = false
  }
}

async function toggleWatch(row: PriceWatch) {
  try {
    await updatePriceWatch(row.id, { enabled: row.enabled })
  } catch (e) {
    row.enabled = !row.enabled
    ElMessage.error(String(e))
  }
}

async function removeWatch(row: PriceWatch) {
  try {
    await deletePriceWatch(row.id)
    watches.value = await listPriceWatches()
  } catch (e) {
    ElMessage.error(String(e))
  }
}
// §4.7 F-0622: actual vendor bills for the picked month. The records here are
// the imported vendor truth (marked 实际), kept visually apart from the
// estimates above; sums never merge currencies (F-0594).
const billMonth = ref(new Date())
const bills = ref<CostRecordRow[]>([])
const billsLoading = ref(false)

async function loadBills() {
  billsLoading.value = true
  try {
    const d = billMonth.value
    const from = new Date(d.getFullYear(), d.getMonth(), 1).toISOString()
    const to = new Date(d.getFullYear(), d.getMonth() + 1, 1).toISOString()
    const data = await getBillRecords({ from, to })
    bills.value = data.records
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    billsLoading.value = false
  }
}

const billTotals = computed(() => {
  const byCur = new Map<string, number>()
  for (const r of bills.value) byCur.set(r.currency, (byCur.get(r.currency) ?? 0) + r.amount_cents)
  return [...byCur.entries()].sort(([a], [b]) => a.localeCompare(b))
})

function centsText(cents: number, currency: string): string {
  return `${(cents / 100).toFixed(2)} ${currency}`
}

function billLabel(row: CostRecordRow): string {
  return row.revision ? `${row.bill_id}（修订 ${row.revision}）` : row.bill_id
}

// One month selector drives the whole actual region: bills, diff, rankings,
// trend and path estimates all follow the picked month.
function loadBillsSection() {
  loadBills()
  loadSpend()
  loadTrend()
  loadPaths()
}

// §4.7 F-0624: estimate vs actual diff. The panel projection is a single ¥
// number (node-price projection), vendor bills are per currency, so the diff
// only compares the CNY bucket honestly; other currencies are listed as-is.
const isCurrentBillMonth = computed(() => {
  const d = billMonth.value
  const now = new Date()
  return d.getFullYear() === now.getFullYear() && d.getMonth() === now.getMonth()
})

const diffRows = computed(() => {
  if (!rep.value) return []
  if (!isCurrentBillMonth.value) {
    return [{ label: '差异（估算−实际）', amount: '—', note: '账单月份非本月，估算为月至今口径，不可比' }]
  }
  const rows: { label: string; amount: string; note: string }[] = [
    { label: '估算（月度预估）', amount: yuan(rep.value.summary.projected_cents), note: '节点单价折算的月度预估（¥，面板估算口径）' },
  ]
  for (const [cur, cents] of billTotals.value) {
    rows.push({ label: `实际（${cur}）`, amount: centsText(cents, cur), note: '厂商账单导入合计（实际）' })
  }
  const cny = billTotals.value.find(([cur]) => cur === 'CNY')
  if (cny) {
    rows.push({
      label: '差异（估算−实际）',
      amount: yuan(rep.value.summary.projected_cents - cny[1]),
      note: '仅 CNY 币种账单参与比较；正数表示估算高于实际',
    })
  } else {
    rows.push({ label: '差异（估算−实际）', amount: '—', note: '本月无 CNY 币种账单，无法比较' })
  }
  return rows
})

// §4.7 F-0625/26/28: actual-spend rankings by scope, over the picked bill
// month. Rows stay per currency (F-0594); the dash sorts by actual descending.
const spendScopes = [
  { value: 'provider', label: 'Provider' },
  { value: 'account', label: '账号' },
  { value: 'region', label: '区域' },
  { value: 'resource', label: '资源' },
  { value: 'node', label: '节点' },
  { value: 'path', label: '路径' },
]
const spendScope = ref('provider')
const spendRows = ref<SpendRow[]>([])
const spendLoading = ref(false)

async function loadSpend() {
  spendLoading.value = true
  try {
    const d = billMonth.value
    const from = new Date(d.getFullYear(), d.getMonth(), 1).toISOString()
    const to = new Date(d.getFullYear(), d.getMonth() + 1, 1).toISOString()
    const data = await spendByScope(spendScope.value, from, to)
    spendRows.value = [...data.rows].sort((a, b) => b.actual_cents - a.actual_cents)
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    spendLoading.value = false
  }
}

// §4.7 F-0630: actual-spend trend buckets for the picked bill month.
const trendBuckets = [
  { value: 'day', label: '按天' },
  { value: 'month', label: '按月' },
  { value: 'cycle', label: '按账期' },
]
const trendBucket = ref('day')
const trendRows = ref<SpendTrendBucket[]>([])
const trendLoading = ref(false)

async function loadTrend() {
  trendLoading.value = true
  try {
    const d = billMonth.value
    const from = new Date(d.getFullYear(), d.getMonth(), 1).toISOString()
    const to = new Date(d.getFullYear(), d.getMonth() + 1, 1).toISOString()
    const data = await spendTrend(trendBucket.value, from, to)
    trendRows.value = data.rows
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    trendLoading.value = false
  }
}

// §4.7 F-0629: explainable per-path cost estimates. Unknown/stale prices drop
// the precise flag server-side; the badge surfaces it so a number is never
// read as exact when a leg is unpriced.
const pathRows = ref<PathCostRow[]>([])
const pathLoading = ref(false)

async function loadPaths() {
  pathLoading.value = true
  try {
    const d = billMonth.value
    const from = new Date(d.getFullYear(), d.getMonth(), 1).toISOString()
    const data = await getPathCosts(from)
    pathRows.value = data.rows
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    pathLoading.value = false
  }
}

function directionText(d: string): string {
  return d === 'in' ? '入站' : d === 'out' ? '出站' : d
}

function legText(leg: PathCostRow['explain'][number], currency: string): string {
  const state = leg.unknown ? '未知价' : leg.fresh ? '' : '价格已过期'
  const price = leg.unit === 'fixed'
    ? `${(leg.monthly_cents / 100).toFixed(2)} ${currency}/月`
    : `${(leg.cents_per_gb / 100).toFixed(3)} ${currency}/GB`
  return `${leg.name}（${leg.role}${leg.billing_type ? '·' + leg.billing_type : ''}）${price}${state ? '　' + state : ''}`
}
</script>

<template>
  <div class="page">
    <h2>成本</h2>
    <p class="page-desc">
      月度成本核算：流量花费只计按流量计费节点，月固定成本进汇总不影响分配；月度预估按自然月至今折算。
      页面金额分三种口径：<strong>实际</strong>＝厂商账单导入值（账单同步页）、<strong>估算</strong>＝面板按单价折算的预估值、<strong>分摊</strong>＝按分摊规则把账单拆到目标的值；不同口径不混用，跨币种也永不合并。
    </p>

    <div v-if="rep" class="stats">
      <div class="stat">
        <div class="stat-value">{{ yuan(rep.summary.traffic_cents) }}</div>
        <div class="stat-label">月至今流量花费</div>
      </div>
      <div class="stat">
        <div class="stat-value">{{ yuan(rep.summary.fixed_cents) }}</div>
        <div class="stat-label">月固定成本</div>
      </div>
      <div class="stat">
        <div class="stat-value">{{ yuan(rep.summary.projected_cents) }}</div>
        <div class="stat-label">月度预估</div>
      </div>
      <div class="stat">
        <div class="stat-value">{{ rep.summary.nodes }}</div>
        <div class="stat-label">节点数</div>
      </div>
    </div>

    <div class="toolbar">
      <span class="threshold">高成本告警阈值 ¥
        <el-input-number v-model="thresholdYuan" :min="0" :precision="0" :controls="false" style="width: 90px" />
        /月
      </span>
      <el-button type="primary" :loading="saving" @click="saveThreshold">保存阈值</el-button>
      <el-button @click="load" :loading="loading">刷新</el-button>
    </div>

    <el-table v-if="rep" :data="rep.nodes" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column prop="name" label="节点" min-width="120" />
      <el-table-column prop="region" label="区域" width="90" />
      <el-table-column prop="isp" label="运营商" width="100" />
      <el-table-column prop="billing_type" label="计费方式" width="90" />
      <el-table-column label="月流量" width="110">
        <template #default="{ row }">{{ gb(row.month_rx_bytes + row.month_tx_bytes) }}</template>
      </el-table-column>
      <el-table-column label="流量花费" width="110">
        <template #default="{ row }">{{ row.traffic_cost_cents > 0 ? yuan(row.traffic_cost_cents) : '—' }}</template>
      </el-table-column>
      <el-table-column label="固定成本" width="110">
        <template #default="{ row }">{{ row.fixed_cost_cents > 0 ? yuan(row.fixed_cost_cents) : '—' }}</template>
      </el-table-column>
      <el-table-column label="月度预估" width="110">
        <template #default="{ row }">
          <span :class="{ over: rep && row.billing_type === '按流量' && row.traffic_cost_cents > rep.threshold_cents }">
            {{ yuan(row.projected_cents) }}
          </span>
        </template>
      </el-table-column>
    </el-table>

    <div v-if="rep" class="groups">
      <div class="group">
        <h3 class="section">区域汇总</h3>
        <el-table :data="rep.regions" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
          <el-table-column prop="key" label="区域" min-width="90" />
          <el-table-column prop="nodes" label="节点" width="70" />
          <el-table-column label="流量花费" width="110">
            <template #default="{ row }">{{ yuan(row.traffic_cents) }}</template>
          </el-table-column>
          <el-table-column label="固定成本" width="110">
            <template #default="{ row }">{{ yuan(row.fixed_cents) }}</template>
          </el-table-column>
          <el-table-column label="月度预估" width="110">
            <template #default="{ row }">{{ yuan(row.projected_cents) }}</template>
          </el-table-column>
        </el-table>
      </div>
      <div class="group">
        <h3 class="section">运营商汇总</h3>
        <el-table :data="rep.isps" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
          <el-table-column prop="key" label="运营商" min-width="90" />
          <el-table-column prop="nodes" label="节点" width="70" />
          <el-table-column label="流量花费" width="110">
            <template #default="{ row }">{{ yuan(row.traffic_cents) }}</template>
          </el-table-column>
          <el-table-column label="固定成本" width="110">
            <template #default="{ row }">{{ yuan(row.fixed_cents) }}</template>
          </el-table-column>
          <el-table-column label="月度预估" width="110">
            <template #default="{ row }">{{ yuan(row.projected_cents) }}</template>
          </el-table-column>
        </el-table>
      </div>
    </div>

    <h3 class="section">本月实际账单（实际）</h3>
    <p class="page-desc">
      云厂商导入的真实账单记录（标记「实际」，与上方估算值口径不同）；金额按账单原币种展示，不跨币种合并。
    </p>
    <div class="toolbar">
      <el-date-picker v-model="billMonth" type="month" :clearable="false" placeholder="账单月份" style="width: 130px" @change="loadBillsSection" />
      <el-button @click="loadBills" :loading="billsLoading">刷新账单</el-button>
      <span v-if="billTotals.length" class="threshold">
        本月实际合计：
        <strong v-for="(t, i) in billTotals" :key="t[0]">{{ i > 0 ? '　·　' : '' }}{{ centsText(t[1], t[0]) }}</strong>
      </span>
      <span class="threshold">{{ bills.length }} 笔</span>
    </div>
    <el-table :data="bills" v-loading="billsLoading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column label="账单号" min-width="150">
        <template #default="{ row }">{{ billLabel(row) }}</template>
      </el-table-column>
      <el-table-column prop="source" label="来源" width="90" />
      <el-table-column prop="account" label="账号" min-width="120" />
      <el-table-column prop="region" label="区域" width="90" />
      <el-table-column label="金额（实际）" width="130">
        <template #default="{ row }">{{ centsText(row.amount_cents, row.currency) }}</template>
      </el-table-column>
      <el-table-column label="税额" width="120">
        <template #default="{ row }">{{ row.tax_cents > 0 ? centsText(row.tax_cents, row.currency) : '—' }}</template>
      </el-table-column>
      <el-table-column label="发生时间" width="140">
        <template #default="{ row }">{{ formatDate(row.incurred_at) }}</template>
      </el-table-column>
    </el-table>

    <div v-if="rep" class="groups" style="margin-top: 12px">
      <div class="group">
        <h3 class="section">估算与实际差异</h3>
        <el-table :data="diffRows" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
          <el-table-column prop="label" label="口径" width="170" />
          <el-table-column prop="amount" label="金额" min-width="130" />
          <el-table-column prop="note" label="说明" min-width="260" />
        </el-table>
      </div>
    </div>

    <h3 class="section">实际成本排名</h3>
    <div class="toolbar">
      <el-radio-group v-model="spendScope" @change="loadSpend">
        <el-radio-button v-for="s in spendScopes" :key="s.value" :value="s.value">{{ s.label }}</el-radio-button>
      </el-radio-group>
      <span class="threshold">随上方账单月份联动（实际账单口径）</span>
    </div>
    <el-table :data="spendRows" v-loading="spendLoading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column label="名称" min-width="140">
        <template #default="{ row }">{{ row.name || row.key || '—' }}</template>
      </el-table-column>
      <el-table-column prop="currency" label="币种" width="80" />
      <el-table-column label="实际" width="120">
        <template #default="{ row }">{{ row.actual_cents > 0 ? centsText(row.actual_cents, row.currency) : '—' }}</template>
      </el-table-column>
      <el-table-column label="分摊" width="120">
        <template #default="{ row }">{{ row.allocated_cents > 0 ? centsText(row.allocated_cents, row.currency) : '—' }}</template>
      </el-table-column>
      <el-table-column label="估算" width="120">
        <template #default="{ row }">{{ row.estimate_cents > 0 ? centsText(row.estimate_cents, row.currency) : '—' }}</template>
      </el-table-column>
      <el-table-column label="笔数" width="80" prop="count" />
    </el-table>

    <h3 class="section">实际成本趋势</h3>
    <div class="toolbar">
      <el-radio-group v-model="trendBucket" @change="loadTrend">
        <el-radio-button v-for="b in trendBuckets" :key="b.value" :value="b.value">{{ b.label }}</el-radio-button>
      </el-radio-group>
      <span class="threshold">同一账单月份内的实际花费分桶</span>
    </div>
    <el-table :data="trendRows" v-loading="trendLoading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column prop="bucket" label="分桶" min-width="140" />
      <el-table-column prop="currency" label="币种" width="80" />
      <el-table-column label="实际" width="130">
        <template #default="{ row }">{{ centsText(row.actual_cents, row.currency) }}</template>
      </el-table-column>
      <el-table-column label="笔数" width="80" prop="count" />
    </el-table>

    <h3 class="section">Path 成本估算（估算）</h3>
    <p class="page-desc">
      按路径逐腿解释的估算成本（标记「估算」）；任一腿价格未知或过期时「精确」为否——数字仅供比较，不代表精确账单。
    </p>
    <el-table :data="pathRows" v-loading="pathLoading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column type="expand">
        <template #default="{ row }">
          <div class="path-legs">
            <div v-for="(leg, i) in row.explain" :key="i">{{ legText(leg, row.currency) }}</div>
          </div>
        </template>
      </el-table-column>
      <el-table-column prop="name" label="路径" min-width="130" />
      <el-table-column label="方向" width="80">
        <template #default="{ row }">{{ directionText(row.direction) }}</template>
      </el-table-column>
      <el-table-column prop="currency" label="币种" width="80" />
      <el-table-column label="估算合计" width="130">
        <template #default="{ row }">{{ centsText(row.total_cents, row.currency) }}</template>
      </el-table-column>
      <el-table-column label="边际/GB" width="110">
        <template #default="{ row }">{{ row.marginal_per_gb_cents > 0 ? centsText(row.marginal_per_gb_cents, row.currency) : '—' }}</template>
      </el-table-column>
      <el-table-column label="精确" width="90">
        <template #default="{ row }">
          <el-tag :type="row.precise ? 'success' : 'warning'" size="small">{{ row.precise ? '精确' : '含未知价' }}</el-tag>
        </template>
      </el-table-column>
    </el-table>

    <h3 class="section">流量节省（近 30 天）</h3>
    <p class="page-desc">
      分流直连与广告拦截省下的流量及折算费用（仅按流量计费节点折算，与上方成本同口径）；缓存命中字节随缓存层指标接入后汇入。
    </p>
    <div v-if="savings" class="stats">
      <div class="stat">
        <div class="stat-value">{{ formatBytes(savings.total.direct_bytes) }}</div>
        <div class="stat-label">直连分流</div>
      </div>
      <div class="stat">
        <div class="stat-value">{{ formatBytes(savings.total.blocked_bytes) }}</div>
        <div class="stat-label">广告拦截</div>
      </div>
      <div class="stat">
        <div class="stat-value">{{ yuan(savings.total.cost_cents) }}</div>
        <div class="stat-label">折算节省费用</div>
      </div>
    </div>
    <el-table
      v-if="savings && savings.rows.length"
      :data="savings.rows"
      size="small"
      :header-cell-style="{ background: 'var(--ferry-bg-panel)' }"
    >
      <el-table-column label="日期" width="120">
        <template #default="{ row }">{{ row.day }}</template>
      </el-table-column>
      <el-table-column label="节点" min-width="140">
        <template #default="{ row }">{{ row.name }}</template>
      </el-table-column>
      <el-table-column label="直连分流" width="120">
        <template #default="{ row }">{{ formatBytes(row.direct_bytes) }}</template>
      </el-table-column>
      <el-table-column label="广告拦截" width="120">
        <template #default="{ row }">{{ formatBytes(row.blocked_bytes) }}</template>
      </el-table-column>
      <el-table-column label="折算费用" width="110">
        <template #default="{ row }">{{ yuan(row.cost_cents) }}</template>
      </el-table-column>
    </el-table>
    <p v-else-if="savings" class="page-desc">暂无节省数据——节点 agent 上报分流计数后按日汇总。</p>

    <h3 class="section">配额联动</h3>
    <p class="page-desc">
      用户流量/费用超阈值后订阅自动降档——只出低成本档入口（包月或单价不超档线的节点），新连接落到低成本落地，用量回落后自动恢复；动作留痕在下方。
    </p>
    <div class="toolbar link-bar">
      <span class="threshold">
        <el-switch v-model="linkSetting.enabled" active-text="启用联动" />
      </span>
      <span class="threshold">流量档：用量达配额
        <el-input-number v-model="linkSetting.traffic_percent" :min="0" :max="100" :controls="false" style="width: 60px" />
        % 触发（0=关）
      </span>
      <span class="threshold">费用档：折算费用 ¥
        <el-input-number v-model="linkCostYuan" :min="0" :precision="0" :controls="false" style="width: 80px" />
        触发（0=关）
      </span>
      <span class="threshold">低成本档线 ¥
        <el-input-number v-model="linkPriceYuan" :min="0" :precision="0" :controls="false" style="width: 70px" />
        /GB（0=只有包月）
      </span>
      <el-button type="primary" :loading="linkSaving" @click="saveLink">保存联动</el-button>
    </div>
    <el-table
      v-if="link && link.rows.length"
      :data="link.rows"
      :header-cell-style="{ background: 'var(--ferry-bg-panel)' }"
    >
      <el-table-column label="用户" width="120">
        <template #default="{ row }">{{ link.names[String(row.user_id)] || '#' + row.user_id }}</template>
      </el-table-column>
      <el-table-column label="触发" width="90">
        <template #default="{ row }">
          <el-tag :type="row.trigger === 'traffic' ? 'warning' : 'danger'" size="small" effect="plain">
            {{ linkTrigger(row) }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="触发口径" min-width="200">
        <template #default="{ row }">{{ row.reason }}</template>
      </el-table-column>
      <el-table-column label="降档档线" width="110">
        <template #default="{ row }">{{ yuan(row.max_price_cents) }}/GB</template>
      </el-table-column>
      <el-table-column label="状态" width="100">
        <template #default="{ row }">
          <el-tag :type="row.released_at ? 'info' : 'success'" size="small" effect="plain">{{ linkState(row) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="触发时间" width="170">
        <template #default="{ row }">{{ new Date(row.created_at).toLocaleString() }}</template>
      </el-table-column>
      <el-table-column label="恢复时间" width="170">
        <template #default="{ row }">{{ row.released_at ? new Date(row.released_at).toLocaleString() : '—' }}</template>
      </el-table-column>
    </el-table>
    <el-empty v-else-if="link" description="暂无降档记录（联动启用后超阈值用户在此留痕）" :image-size="60" />

    <h3 class="section">成本参考库</h3>
    <p class="page-desc">
      手录成本是唯一权威：导入公开价格表（商家/区域/配置档 → 月付牌价）后按节点比对，
      偏差超容差提示人工复核——参考价只提示，不会自动改价。
    </p>
    <div class="toolbar ref-import">
      <el-input
        v-model="refTable" type="textarea" :rows="4"
        placeholder='价格表 JSON 数组：[{"provider":"vultr","region":"tokyo","spec":"100M-500G","monthly_cents":600,"currency":"CNY","url":"https://…}]（留空提交=清空停用）'
        class="ref-textarea"
      />
      <el-button type="primary" :loading="refSaving" @click="saveRefTable">导入价格表</el-button>
    </div>
    <div class="toolbar">
      <el-button :loading="refCheckLoading" @click="loadRefCheck(false)">对账手录价（批量）</el-button>
      <span v-if="refCheck" class="threshold">
        容差 ±{{ refCheck.tolerance_pct }}% · 参查 {{ refCheck.checked }} 台 · 命中 {{ refCheck.hits }} ·
        币种不符 {{ refCheck.mismatches }}
      </span>
    </div>
    <el-table
      v-if="refCheck && refCheck.items.length"
      :data="refCheck.items"
      :header-cell-style="{ background: 'var(--ferry-bg-panel)' }"
    >
      <el-table-column label="节点" min-width="110">
        <template #default="{ row }">{{ row.node_name }}</template>
      </el-table-column>
      <el-table-column label="牌价键" min-width="200">
        <template #default="{ row }">{{ row.query.provider }} / {{ row.query.region || '—' }} / {{ row.query.spec || '—' }}</template>
      </el-table-column>
      <el-table-column label="牌价" width="110">
        <template #default="{ row }">{{ row.quote.monthly_cents / 100 }} {{ row.quote.currency }}</template>
      </el-table-column>
      <el-table-column label="手录" width="110">
        <template #default="{ row }">{{ row.manual_cents / 100 }} {{ row.currency }}</template>
      </el-table-column>
      <el-table-column label="偏差" width="110">
        <template #default="{ row }">
          <el-tag :type="row.deviation.off ? 'danger' : 'success'" size="small" effect="plain">
            {{ refPct(row.deviation.pct) }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="价格页" width="90">
        <template #default="{ row }">
          <a v-if="row.quote.url" :href="row.quote.url" target="_blank" rel="noopener">核对</a>
          <span v-else>—</span>
        </template>
      </el-table-column>
    </el-table>
    <p v-else-if="refCheck" class="page-desc">偏差都在容差内，或暂无可比牌价（先导入价格表并补齐节点机房与月固定成本）。</p>
    <p v-else class="page-desc">尚未导入价格表——在上方导入后即可对账。</p>

    <div class="toolbar">
      <el-input v-model="probeForm.provider" placeholder="商家" style="width: 130px" @keyup.enter="runProbe" />
      <el-input v-model="probeForm.region" placeholder="区域（可空）" style="width: 120px" @keyup.enter="runProbe" />
      <el-input v-model="probeForm.spec" placeholder="配置档（如 100M-500G）" style="width: 170px" @keyup.enter="runProbe" />
      <el-input v-model="probeForm.manual" placeholder="手录价（分/月，可选）" style="width: 150px" @keyup.enter="runProbe" />
      <el-input v-model="probeForm.currency" placeholder="币种（有手录价必填）" style="width: 150px" @keyup.enter="runProbe" />
      <el-button :loading="probeLoading" @click="runProbe">试查牌价</el-button>
    </div>
    <p v-if="probeResult" class="page-desc probe-box">
      <template v-if="probeResult.hit">
        牌价 {{ (probeResult.quote?.monthly_cents ?? 0) / 100 }} {{ probeResult.quote?.currency }}
        <template v-if="probeResult.quote?.traffic_price">（流量 {{ probeResult.quote.traffic_price / 100 }} {{ probeResult.quote.currency }}/GB）</template>
        ——
        <template v-if="probeResult.deviation">
          手录价{{ probeResult.deviation.pct > 0 ? '高于' : '低于' }}牌价 {{ Math.abs(probeResult.deviation.pct) }}%，{{ probeResult.deviation.off ? '超容差，建议人工复核' : '在容差内' }}。
        </template>
        <template v-else>{{ probeResult.reason }}。</template>
      </template>
      <template v-else>牌价表无此键（{{ probeResult.query.provider }} / {{ probeResult.query.region || '—' }} / {{ probeResult.query.spec || '—' }}）。</template>
    </p>

    <h3 class="section">价格关注</h3>
    <p class="page-desc">
      关注一个牌价键（与参考价表同词表）：扫描器小时级抓快照，命中降价或现价到位经告警通道（Herald，
      未配置时站内事件）提示——「到位」只在从高于目标价跨到目标价内时发一次。
    </p>
    <div class="toolbar">
      <el-input v-model="newWatch.provider" placeholder="商家" style="width: 130px" @keyup.enter="addWatch" />
      <el-input v-model="newWatch.region" placeholder="区域（可空）" style="width: 120px" @keyup.enter="addWatch" />
      <el-input v-model="newWatch.spec" placeholder="配置档（如 100m-500g）" style="width: 170px" @keyup.enter="addWatch" />
      <el-input-number v-model="newWatch.target" :min="0" :precision="0" :controls="false" placeholder="目标价 元/月（0=只盯降价）" style="width: 210px" />
      <el-button type="primary" :loading="addingWatch" @click="addWatch">添加关注</el-button>
    </div>
    <el-table
      v-if="watches.length" :data="watches" v-loading="watchesLoading"
      :header-cell-style="{ background: 'var(--ferry-bg-panel)' }"
    >
      <el-table-column label="关注键" min-width="200">
        <template #default="{ row }">{{ row.provider }} / {{ row.region || '—' }} / {{ row.spec || '—' }}</template>
      </el-table-column>
      <el-table-column label="目标价" width="110">
        <template #default="{ row }">{{ row.target_price > 0 ? row.target_price / 100 : '—' }}</template>
      </el-table-column>
      <el-table-column label="最新牌价" width="110">
        <template #default="{ row }">{{ row.latest_cents != null ? row.latest_cents / 100 : '—' }}</template>
      </el-table-column>
      <el-table-column label="动态" min-width="150">
        <template #default="{ row }">
          <template v-if="row.snapshot_done">
            <el-tag v-if="row.change_pct != null && row.change_pct < 0" type="success" size="small" effect="plain">降 {{ -row.change_pct }}%</el-tag>
            <el-tag v-else-if="row.change_pct != null && row.change_pct > 0" type="info" size="small" effect="plain">涨 {{ row.change_pct }}%</el-tag>
            <el-tag v-if="row.at_target" type="warning" size="small" effect="plain">到位</el-tag>
            <span v-if="row.change_pct == null && !row.at_target">无变化</span>
          </template>
          <span v-else>待首扫</span>
        </template>
      </el-table-column>
      <el-table-column label="快照时间" width="160">
        <template #default="{ row }">{{ row.captured_at || '—' }}</template>
      </el-table-column>
      <el-table-column label="启用" width="80">
        <template #default="{ row }">
          <el-switch v-model="row.enabled" size="small" @change="toggleWatch(row)" />
        </template>
      </el-table-column>
      <el-table-column label="" width="80">
        <template #default="{ row }">
          <el-button link type="danger" size="small" @click="removeWatch(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
    <p v-else class="page-desc">暂无关注条件——添加后扫描器开始抓快照并盯变化。</p>

    <template v-if="evening">
      <h3 class="section">晚高峰回程报表（近 {{ evening.days }} 天）</h3>
      <p class="page-desc">
        回程探测结论按小时分段：延迟只计可达样本，可用率即可达比例；晚高峰 19:00–23:00 单独汇总与平峰对比。
      </p>
      <div v-if="hasEveningData()" class="evening">
        <div class="compare">
          <div class="cmp peak">
            <div class="cmp-title">晚高峰 19–23 时</div>
            <div class="cmp-row"><span>平均延迟</span><b>{{ ms(evening.peak.avg_rtt_ms) }}</b></div>
            <div class="cmp-row"><span>平均丢包</span><b>{{ pct(evening.peak.avg_loss_pct) }}</b></div>
            <div class="cmp-row"><span>可用率</span><b>{{ pct(evening.peak.availability_pct) }}</b></div>
            <div class="cmp-row"><span>样本</span><b>{{ evening.peak.samples }}</b></div>
          </div>
          <div class="cmp">
            <div class="cmp-title">平峰（其余时段）</div>
            <div class="cmp-row"><span>平均延迟</span><b>{{ ms(evening.offpeak.avg_rtt_ms) }}</b></div>
            <div class="cmp-row"><span>平均丢包</span><b>{{ pct(evening.offpeak.avg_loss_pct) }}</b></div>
            <div class="cmp-row"><span>可用率</span><b>{{ pct(evening.offpeak.availability_pct) }}</b></div>
            <div class="cmp-row"><span>样本</span><b>{{ evening.offpeak.samples }}</b></div>
          </div>
        </div>
        <el-table
          :data="eveningRows()"
          :row-class-name="peakClass"
          :header-cell-style="{ background: 'var(--ferry-bg-panel)' }"
        >
          <el-table-column label="时段" min-width="110">
            <template #default="{ row }">{{ hourLabel(row.hour) }}</template>
          </el-table-column>
          <el-table-column prop="samples" label="样本" width="80" />
          <el-table-column label="平均延迟" width="110">
            <template #default="{ row }">{{ ms(row.avg_rtt_ms) }}</template>
          </el-table-column>
          <el-table-column label="平均丢包" width="110">
            <template #default="{ row }">{{ pct(row.avg_loss_pct) }}</template>
          </el-table-column>
          <el-table-column label="可用率" width="110">
            <template #default="{ row }">{{ pct(row.availability_pct) }}</template>
          </el-table-column>
          <el-table-column label="时段标注" width="100">
            <template #default="{ row }">
              <el-tag v-if="isPeak(row.hour)" type="warning" size="small" effect="plain">晚高峰</el-tag>
            </template>
          </el-table-column>
        </el-table>
      </div>
      <el-empty v-else description="暂无回程探测样本（回程探测接入后自动生成）" :image-size="60" />
    </template>
  </div>
</template>

<style scoped>
.stats {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 12px;
  margin-bottom: 20px;
}
.stat {
  background: var(--ferry-bg-panel);
  border: 1px solid var(--ferry-border);
  border-radius: 8px;
  padding: 14px 16px;
}
.stat-value {
  font-size: 20px;
  font-weight: 650;
}
.stat-label {
  margin-top: 2px;
  font-size: 12px;
  color: var(--ferry-text-muted);
}
.toolbar {
  display: flex;
  gap: 12px;
  align-items: center;
  margin-bottom: 14px;
}
.threshold {
  font-size: 13px;
  color: var(--ferry-text-muted);
  display: inline-flex;
  gap: 6px;
  align-items: center;
}
.over {
  color: var(--ferry-danger);
}
.groups {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 24px;
}
.section {
  margin: 20px 0 10px;
  font-size: 15px;
  font-weight: 650;
}
.evening {
  margin-top: 12px;
}
.compare {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
  margin-bottom: 14px;
}
.cmp {
  background: var(--ferry-bg-panel);
  border: 1px solid var(--ferry-border);
  border-radius: 8px;
  padding: 14px 16px;
}
.cmp.peak {
  border-color: var(--ferry-warn);
}
.cmp-title {
  font-size: 13px;
  font-weight: 650;
  margin-bottom: 8px;
}
.cmp-row {
  display: flex;
  justify-content: space-between;
  font-size: 13px;
  color: var(--ferry-text-muted);
  padding: 3px 0;
}
.cmp-row b {
  color: var(--ferry-text);
  font-variant-numeric: tabular-nums;
}
:deep(.el-table .peak-row) {
  background: var(--ferry-bg-hover);
}
.ref-import {
  align-items: flex-start;
}
.ref-textarea {
  max-width: 720px;
  font-family: monospace;
}
.probe-box {
  max-width: 860px;
  padding: 8px 12px;
  border: 1px solid var(--ferry-border);
  border-radius: 8px;
  background: var(--ferry-bg-panel);
}
.path-legs {
  padding: 4px 16px;
  line-height: 1.9;
  color: var(--ferry-text-secondary, #606266);
}
</style>
