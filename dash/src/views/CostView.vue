<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { get, put, getEvening, type EveningReport, type GroupCost, type NodeCost } from '../api'
import { formatBytes } from '../utils/format'

// E-23：成本看板——节点流量花费（仅按流量计费有边际成本）、区域/运营商
// 汇总、月度预估（自然月至今折算）与高成本告警阈值；超阈值节点在告警列表单发。
// E-29：晚高峰回程报表同页——24 小时回程质量分段 + 晚高峰（19–23 时）与平峰对比。

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

async function load() {
  loading.value = true
  try {
    const [r, ev] = await Promise.all([get<CostReport>('/api/cost'), getEvening(7)])
    rep.value = r
    evening.value = ev
    thresholdYuan.value = Math.round((r?.threshold_cents ?? 0) / 100)
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

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

function yuan(cents: number): string {
  return '¥' + (cents / 100).toFixed(2)
}

function gb(bytes: number): string {
  return formatBytes(bytes)
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
</style>
