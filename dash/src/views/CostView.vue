<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import {
  get, put, getEvening, getQuotaLink, putQuotaLink, getSaveStats,
  checkCostRef, probeCostRef, saveCostRefTable, ApiError,
  listPriceWatches, createPriceWatch, updatePriceWatch, deletePriceWatch,
  type EveningReport, type GroupCost, type NodeCost,
  type QuotaLinkData, type QuotaLinkSetting, type QuotaActionRow,
  type SaveStatsReport, type RefCheckReport, type RefProbe, type PriceWatch,
} from '../api'
import { formatBytes } from '../utils/format'

// E-23：成本看板——节点流量花费（仅按流量计费有边际成本）、区域/运营商
// 汇总、月度预估（自然月至今折算）与高成本告警阈值；超阈值节点在告警列表单发。
// E-29：晚高峰回程报表同页——24 小时回程质量分段 + 晚高峰（19–23 时）与平峰对比。
// SAVE-6：配额联动——用户流量/费用超阈值自动订阅降档（只出低成本档入口），
// 配置与降档留痕同页。
// SAVE-7：流量节省报表同页——分流直连/广告拦截按日汇总与折算费用（同口径）。
// E-31：成本参考库同页——公开价格表导入 + 手录价 vs 牌价偏差提示（只提示不改价）。

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

// 配额联动（SAVE-6）
const link = ref<QuotaLinkData | null>(null)
const linkSetting = ref<QuotaLinkSetting>({ enabled: false, traffic_percent: 90, cost_cents: 0, max_price_cents: 0 })
const linkCostYuan = ref(0)
const linkPriceYuan = ref(0)
const linkSaving = ref(false)

// 节省报表（SAVE-7）
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

// 晚高峰回程报表视图：无样本显示 —，避免把 0 当成测过。
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
// 有回程样本才渲染报表，避免全空表。
const hasEveningData = (): boolean => !!evening.value && evening.value.peak.samples + evening.value.offpeak.samples > 0
// 小时行含样本才入表，空钟点不占行。
function eveningRows() {
  return (evening.value?.hours ?? []).filter((h) => h.samples > 0)
}

// ---- 成本参考库（E-31）----

// 批量对账：机房+月固定成本齐全的节点逐个比对牌价；未导入价格表（400）
// 静默保持空态，由空态文案指引导入，首访不弹错。
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

// 价格表导入：JSON 数组文本原样交给后端校验（脏表拒绝），空串清空停用。
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

// 单点试查：自由键查牌价，可选带手录价比偏差。
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

// ---- 价格关注（E-32）：条件 CRUD；扫描与告警在服务端小时级跑 ----

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
</script>

<template>
  <div class="page">
    <h2>成本</h2>
    <p class="page-desc">
      月度成本核算：流量花费只计按流量计费节点，月固定成本进汇总不影响分配；月度预估按自然月至今折算。
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
</style>
