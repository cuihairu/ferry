<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import {
  getDistMe, getDistBatches, getDistBatchCodes, getDistCustomers, getDistOrders, getDistLedger,
  type DistMe, type DistCustomer, type DistOrderRow,
  type CardBatch, type CardCode, type DistributorLedger,
} from '../api'
import { formatBytes, formatDate } from '../utils/format'

// 代理工作台（DS-3）：只读视图——账目概览 / 批次与卡密 / 客户 / 兑换订单 /
// 结算流水；结算动作在管理员面。与优惠码不叠加取优在兑换链路（优惠体系未
// 开工，现无叠加点）。

const me = ref<DistMe | null>(null)
const tab = ref('overview')

async function loadMe() {
  try {
    me.value = await getDistMe()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

function yuan(cents?: number): string {
  return ((cents ?? 0) / 100).toFixed(2)
}

// ---- 批次与卡密 ----
const batches = ref<CardBatch[]>([])
const codesVisible = ref(false)
const codes = ref<CardCode[]>([])
const activeBatch = ref<CardBatch | null>(null)

async function loadBatches() {
  try {
    batches.value = await getDistBatches()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

async function openCodes(b: CardBatch) {
  activeBatch.value = b
  codesVisible.value = true
  try {
    codes.value = await getDistBatchCodes(b.id)
  } catch (e) {
    ElMessage.error(String(e))
  }
}

async function copyCode(code: string) {
  await navigator.clipboard.writeText(code)
  ElMessage.success('已复制')
}

function grantText(b: CardBatch): string {
  return b.grant_type === 'add_quota' ? `+${formatBytes(b.grant_value)}` : `+${b.grant_value} 天`
}

function batchState(b: CardBatch): { text: string; type: 'success' | 'info' | 'danger' } {
  if (b.expired_at && new Date(b.expired_at).getTime() < Date.now()) return { text: '已过期', type: 'danger' }
  if (b.remaining === 0) return { text: '已兑换完', type: 'info' }
  return { text: '可用', type: 'success' }
}

const codeState: Record<CardCode['status'], { text: string; type: 'success' | 'info' | 'danger' }> = {
  unused: { text: '未使用', type: 'info' },
  used: { text: '已使用', type: 'success' },
  disabled: { text: '已禁用', type: 'danger' },
}

// ---- 客户 ----
const customers = ref<DistCustomer[]>([])

async function loadCustomers() {
  try {
    const res = await getDistCustomers()
    customers.value = res.customers
  } catch (e) {
    ElMessage.error(String(e))
  }
}

// ---- 兑换订单 ----
const orders = ref<DistOrderRow[]>([])

async function loadOrders() {
  try {
    const res = await getDistOrders()
    orders.value = res.orders
  } catch (e) {
    ElMessage.error(String(e))
  }
}

// ---- 结算流水 ----
const ledger = ref<DistributorLedger[]>([])
const balance = ref(0)

async function loadLedger() {
  try {
    const res = await getDistLedger()
    ledger.value = res.ledger
    balance.value = res.balance_cents
  } catch (e) {
    ElMessage.error(String(e))
  }
}

/** ledAmount 金额按余额影响方向展示：sale/commission 加、payout 减、adjust 带符号。 */
function ledAmount(r: DistributorLedger): string {
  const v = r.kind === 'payout' ? -r.amount_cents : r.amount_cents
  return `${v < 0 ? '-' : '+'}¥${(Math.abs(v) / 100).toFixed(2)}`
}

const ledgerKind: Record<DistributorLedger['kind'], { text: string; type: 'success' | 'info' | 'warning' | 'danger' }> = {
  sale: { text: '售卡', type: 'info' },
  commission: { text: '佣金', type: 'success' },
  payout: { text: '结算', type: 'warning' },
  adjust: { text: '调整', type: 'danger' },
}

onMounted(() => {
  loadMe()
  loadBatches()
  loadCustomers()
  loadOrders()
  loadLedger()
})
</script>

<template>
  <div class="page">
    <h2>代理工作台</h2>
    <p class="page-desc">
      <template v-if="me">
        {{ me.username }} · 佣金比例 {{ me.discount_percent }}% ·
        未结算 <b class="balance-hot">¥{{ yuan(me.balance_cents) }}</b>（佣金 ¥{{ yuan(me.commission_cents) }} − 已结算 ¥{{ yuan(me.payout_cents) }}）
      </template>
      <template v-else>加载中…</template>
    </p>

    <div class="stats" v-if="me">
      <div class="stat">
        <div class="stat-value">¥{{ yuan(me.sale_cents) }}</div>
        <div class="stat-label">售卡收入</div>
      </div>
      <div class="stat">
        <div class="stat-value">¥{{ yuan(me.commission_cents) }}</div>
        <div class="stat-label">佣金</div>
      </div>
      <div class="stat">
        <div class="stat-value">¥{{ yuan(me.payout_cents) }}</div>
        <div class="stat-label">已结算</div>
      </div>
      <div class="stat">
        <div class="stat-value balance-hot">¥{{ yuan(me.balance_cents) }}</div>
        <div class="stat-label">未结算</div>
      </div>
    </div>

    <el-tabs v-model="tab">
      <el-tab-pane label="批次与卡密" name="batches">
        <el-table :data="batches" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
          <el-table-column prop="name" label="名称" min-width="150" />
          <el-table-column label="权益" width="120">
            <template #default="{ row }">{{ grantText(row) }}</template>
          </el-table-column>
          <el-table-column label="面价" width="100">
            <template #default="{ row }">¥{{ yuan(row.price_cents) }}</template>
          </el-table-column>
          <el-table-column label="剩余 / 总数" width="110">
            <template #default="{ row }">{{ row.remaining }} / {{ row.total }}</template>
          </el-table-column>
          <el-table-column label="状态" width="100">
            <template #default="{ row }">
              <el-tag :type="batchState(row).type" size="small" effect="dark">{{ batchState(row).text }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="有效期至" width="150">
            <template #default="{ row }">{{ formatDate(row.expired_at) }}</template>
          </el-table-column>
          <el-table-column label="操作" width="100" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" @click="openCodes(row)">卡密</el-button>
            </template>
          </el-table-column>
        </el-table>
        <el-empty v-if="batches.length === 0" description="暂无归属批次——联系管理员建批次" :image-size="60" />
      </el-tab-pane>

      <el-tab-pane label="客户" name="customers">
        <el-table :data="customers" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
          <el-table-column prop="username" label="用户名" min-width="120" />
          <el-table-column prop="redeemed_cnt" label="兑换张数" width="90" />
          <el-table-column label="配额" width="110">
            <template #default="{ row }">{{ formatBytes(row.quota_bytes) }}</template>
          </el-table-column>
          <el-table-column label="已用" width="110">
            <template #default="{ row }">{{ formatBytes(row.used_bytes) }}</template>
          </el-table-column>
          <el-table-column label="状态" width="100">
            <template #default="{ row }">
              <el-tag :type="row.active ? 'success' : 'danger'" size="small" effect="dark">
                {{ row.over_quota ? '超限' : row.active ? '活跃' : '失效' }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column label="到期" width="150">
            <template #default="{ row }">{{ formatDate(row.expires_at) }}</template>
          </el-table-column>
          <el-table-column label="最近兑换" width="150">
            <template #default="{ row }">{{ formatDate(row.redeemed_last) }}</template>
          </el-table-column>
        </el-table>
        <el-empty v-if="customers.length === 0" description="暂无客户兑换记录" :image-size="60" />
      </el-tab-pane>

      <el-tab-pane label="兑换订单" name="orders">
        <el-table :data="orders" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
          <el-table-column prop="order_no" label="订单号" min-width="170" />
          <el-table-column prop="username" label="客户" width="110" />
          <el-table-column prop="product" label="商品" min-width="130" show-overflow-tooltip />
          <el-table-column prop="status" label="状态" width="90">
            <template #default>
              <el-tag type="success" size="small" effect="dark">已支付</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="时间" width="150">
            <template #default="{ row }">{{ formatDate(row.paid_at ?? row.created_at) }}</template>
          </el-table-column>
        </el-table>
        <el-empty v-if="orders.length === 0" description="暂无兑换订单" :image-size="60" />
      </el-tab-pane>

      <el-tab-pane label="结算流水" name="ledger">
        <div class="led-head">
          未结算余额 <b :class="{ 'balance-hot': balance > 0 }">¥{{ yuan(balance) }}</b>
          <span class="led-hint">结算打款由管理员操作；余额 = Σ佣金 + Σ调整 − Σ结算</span>
        </div>
        <el-table :data="ledger" size="small" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
          <el-table-column prop="id" label="#" width="60" />
          <el-table-column label="类型" width="80">
            <template #default="{ row }">
              <el-tag :type="ledgerKind[row.kind as DistributorLedger['kind']].type" size="small" effect="dark">
                {{ ledgerKind[row.kind as DistributorLedger['kind']].text }}
              </el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="order_no" label="关联订单" min-width="170">
            <template #default="{ row }">
              <span class="mono">{{ row.order_no || '—' }}</span>
            </template>
          </el-table-column>
          <el-table-column label="金额" width="110">
            <template #default="{ row }">{{ ledAmount(row) }}</template>
          </el-table-column>
          <el-table-column prop="note" label="备注" min-width="120" show-overflow-tooltip />
          <el-table-column label="时间" width="150">
            <template #default="{ row }">{{ formatDate(row.created_at) }}</template>
          </el-table-column>
        </el-table>
        <el-empty v-if="ledger.length === 0" description="暂无账目流水" :image-size="60" />
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="codesVisible" :title="activeBatch ? `卡密：${activeBatch.name}` : '卡密'" width="680px">
      <el-table :data="codes" max-height="420">
        <el-table-column prop="code" label="卡密" min-width="180">
          <template #default="{ row }">
            <span class="mono">{{ row.code }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="codeState[row.status as CardCode['status']].type" size="small" effect="dark">
              {{ codeState[row.status as CardCode['status']].text }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="90">
          <template #default="{ row }">
            <el-button link type="primary" @click="copyCode(row.code)">复制</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-dialog>
  </div>
</template>

<style scoped>
.stats {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 12px;
  margin-bottom: 16px;
}
.stat {
  padding: 14px 16px;
  background: var(--ferry-bg-panel);
  border: 1px solid var(--ferry-border);
  border-radius: 10px;
}
.stat-value {
  font-size: 20px;
  font-weight: 600;
}
.stat-label {
  margin-top: 4px;
  color: var(--ferry-text-dim);
  font-size: 12px;
}
.balance-hot {
  color: var(--el-color-warning);
  font-weight: 600;
}
.led-head {
  display: flex;
  align-items: baseline;
  gap: 8px;
  margin-bottom: 10px;
}
.led-hint {
  color: var(--ferry-text-muted);
  font-size: 12px;
}
.mono {
  font-family: 'SFMono-Regular', 'JetBrains Mono', Menlo, Consolas, monospace;
  letter-spacing: 0.02em;
}
</style>
