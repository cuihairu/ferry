<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { get, put, type GroupCost, type NodeCost } from '../api'
import { formatBytes } from '../utils/format'

// E-23：成本看板——节点流量花费（仅按流量计费有边际成本）、区域/运营商
// 汇总、月度预估（自然月至今折算）与高成本告警阈值；超阈值节点在告警列表单发。

interface CostReport {
  nodes: NodeCost[]
  regions: GroupCost[]
  isps: GroupCost[]
  summary: GroupCost
  threshold_cents: number
}

const loading = ref(false)
const rep = ref<CostReport | null>(null)
const thresholdYuan = ref(50)
const saving = ref(false)

async function load() {
  loading.value = true
  try {
    rep.value = await get<CostReport>('/api/cost')
    thresholdYuan.value = Math.round((rep.value?.threshold_cents ?? 0) / 100)
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
</style>
