<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { getReconcile, refundOrder, type Grant, type ReconcileData, type ReconcileRow } from '../api'
import { formatBytes, formatDate } from '../utils/format'

// PAY-9：三账对账——订单/流水/发放按订单分组，标出缺失环节；游离记录单独列出。
// OD-2：paid 订单可标记退款（钱款退回经渠道后台操作，此处只做状态流转与留痕）。

const loading = ref(false)
const orders = ref<ReconcileRow[]>([])
const orphans = ref<ReconcileData['orphans']>([])
const summary = ref<ReconcileData['summary']>({
  paid_orders: 0, paid_cents: 0, refunded_orders: 0, refunded_cents: 0, txn_cents: 0, grants: 0,
})

async function load() {
  loading.value = true
  try {
    const data = await getReconcile()
    orders.value = data.orders
    orphans.value = data.orphans
    summary.value = data.summary
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

// 缺失环节的订单数（一眼看出账面健康度）。
const missingCount = computed(() => orders.value.filter((o) => o.missing.length > 0).length)

const stats = computed(() => [
  { label: '已支付订单', value: String(summary.value.paid_orders) },
  { label: '订单金额', value: yuan(summary.value.paid_cents) },
  { label: '退款订单', value: `${summary.value.refunded_orders} / ${yuan(summary.value.refunded_cents)}` },
  { label: '流水金额', value: yuan(summary.value.txn_cents) },
  { label: '发放笔数', value: String(summary.value.grants) },
  { label: '缺失环节', value: String(missingCount.value) },
])

/** yuan 分转元两位小数展示。 */
function yuan(cents: number): string {
  return (cents / 100).toFixed(2)
}

const statusTag: Record<string, { text: string; type: 'success' | 'info' | 'danger' | 'warning' }> = {
  pending: { text: '待支付', type: 'warning' },
  paid: { text: '已支付', type: 'success' },
  failed: { text: '失败', type: 'danger' },
  expired: { text: '已过期', type: 'info' },
  refunded: { text: '已退款', type: 'danger' },
}

const providerText: Record<string, string> = {
  card: '卡密',
  epusdt: 'USDT',
  wechat: '微信',
  alipay: '支付宝',
}

function grantText(g: Grant): string {
  return g.grant_type === 'add_quota' ? `+${formatBytes(g.grant_value)}` : `+${g.grant_value} 天`
}

const refunding = ref('')

/** onRefund 标记退款：先渠道后台退钱，此处补状态流转与留痕（note 记退款单号/原因）。 */
async function onRefund(row: ReconcileRow) {
  let note: string
  try {
    const { value } = await ElMessageBox.prompt(
      '钱款退回需在渠道后台操作，此处仅记录状态流转与留痕。可填渠道退款单号 / 原因：',
      `退款 ${row.order_no}（¥${yuan(row.amount_cents)}）`,
      {
        confirmButtonText: '标记已退款',
        cancelButtonText: '取消',
        inputPlaceholder: '退款单号 / 原因（选填）',
        inputValue: '',
      },
    )
    note = value ?? ''
  } catch {
    return
  }
  refunding.value = row.order_no
  try {
    await refundOrder(row.order_no, note)
    ElMessage.success('已标记退款')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    refunding.value = ''
  }
}
</script>

<template>
  <div class="page">
    <h2>对账</h2>
    <p class="page-desc">订单 / 流水 / 发放三账按订单分组核对，缺失环节红标；游离记录（有流水或发放却无订单）为对账硬伤。</p>

    <div class="stats">
      <div v-for="s in stats" :key="s.label" class="stat">
        <div class="stat-value" :class="{ warn: s.label === '缺失环节' && missingCount > 0 }">{{ s.value }}</div>
        <div class="stat-label">{{ s.label }}</div>
      </div>
    </div>

    <div class="toolbar">
      <el-button @click="load" :loading="loading">刷新</el-button>
    </div>

    <el-table :data="orders" v-loading="loading" row-key="order_no" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column type="expand">
        <template #default="{ row }">
          <div class="expand">
            <div v-if="row.refund_at" class="refund-line">
              <el-tag type="danger" size="small" effect="dark">已退款</el-tag>
              <span>{{ formatDate(row.refund_at) }}</span>
              <span v-if="row.refund_note" class="refund-note">{{ row.refund_note }}</span>
            </div>
            <div class="expand-col">
              <div class="expand-title">支付流水（{{ row.transactions.length }}）</div>
              <el-table :data="row.transactions" size="small">
                <el-table-column prop="external_id" label="外部单号" min-width="160" />
                <el-table-column label="金额" width="110">
                  <template #default="{ row: t }">¥{{ yuan(t.amount_cents) }}</template>
                </el-table-column>
                <el-table-column label="发生时间" width="150">
                  <template #default="{ row: t }">{{ formatDate(t.occurred_at) }}</template>
                </el-table-column>
              </el-table>
              <div v-if="row.transactions.length === 0" class="expand-empty">无流水</div>
            </div>
            <div class="expand-col">
              <div class="expand-title">权益发放（{{ row.grants.length }}）</div>
              <el-table :data="row.grants" size="small">
                <el-table-column label="内容" min-width="120">
                  <template #default="{ row: g }">{{ grantText(g) }}</template>
                </el-table-column>
                <el-table-column label="发放时间" width="150">
                  <template #default="{ row: g }">{{ formatDate(g.created_at) }}</template>
                </el-table-column>
              </el-table>
              <div v-if="row.grants.length === 0" class="expand-empty">无发放</div>
            </div>
          </div>
        </template>
      </el-table-column>
      <el-table-column prop="order_no" label="订单号" min-width="170" />
      <el-table-column prop="user_id" label="用户" width="70" />
      <el-table-column label="渠道" width="90">
        <template #default="{ row }">{{ providerText[row.provider] ?? row.provider }}</template>
      </el-table-column>
      <el-table-column prop="product" label="商品" min-width="130" show-overflow-tooltip />
      <el-table-column label="金额" width="100">
        <template #default="{ row }">¥{{ yuan(row.amount_cents) }}</template>
      </el-table-column>
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag :type="statusTag[row.status]?.type ?? 'info'" size="small" effect="dark">
            {{ statusTag[row.status]?.text ?? row.status }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="缺失环节" min-width="170">
        <template #default="{ row }">
          <template v-if="row.missing.length">
            <el-tag v-for="m in row.missing" :key="m" type="danger" size="small" effect="dark" class="missing-tag">{{ m }}</el-tag>
          </template>
          <span v-else class="clean">—</span>
        </template>
      </el-table-column>
      <el-table-column label="创建时间" width="150">
        <template #default="{ row }">{{ formatDate(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="支付时间" width="150">
        <template #default="{ row }">{{ formatDate(row.paid_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="90">
        <template #default="{ row }">
          <el-button
            v-if="row.status === 'paid'"
            size="small"
            type="danger"
            plain
            :loading="refunding === row.order_no"
            @click="onRefund(row)"
          >退款</el-button>
        </template>
      </el-table-column>
    </el-table>

    <template v-if="orphans.length">
      <h3 class="section">游离记录（{{ orphans.length }}）</h3>
      <el-table :data="orphans" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
        <el-table-column label="类型" width="100">
          <template #default="{ row }">
            <el-tag type="warning" size="small" effect="dark">
              {{ row.kind === 'transaction' ? '流水' : '发放' }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="order_no" label="订单号" min-width="170" />
        <el-table-column label="渠道" width="90">
          <template #default="{ row }">{{ row.provider ? providerText[row.provider] ?? row.provider : '—' }}</template>
        </el-table-column>
        <el-table-column prop="detail" label="明细" min-width="130" />
      </el-table>
    </template>
  </div>
</template>

<style scoped>
.stats {
  display: grid;
  grid-template-columns: repeat(6, 1fr);
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
.stat-value.warn {
  color: var(--ferry-danger);
}
.stat-label {
  margin-top: 2px;
  font-size: 12px;
  color: var(--ferry-text-muted);
}
.missing-tag {
  margin-right: 6px;
}
.clean {
  color: var(--ferry-text-muted);
}
.expand {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 24px;
  padding: 4px 12px;
}
.refund-line {
  grid-column: 1 / -1;
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: var(--ferry-text-muted);
}
.refund-note {
  color: var(--ferry-text-dim);
}
.expand-title {
  font-size: 12px;
  color: var(--ferry-text-dim);
  margin-bottom: 6px;
}
.expand-empty {
  font-size: 12px;
  color: var(--ferry-text-muted);
  padding: 6px 0;
}
.section {
  margin: 24px 0 12px;
  font-size: 15px;
  font-weight: 650;
}
</style>
