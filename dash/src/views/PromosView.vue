<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  getCoupons, createCoupon, updateCoupon, deleteCoupon,
  type CouponRow, type CouponInput, type CardBatch,
} from '../api'
import { get } from '../api'
import { formatDate } from '../utils/format'

// 促销管理（PROMO-2，运营设计 §5）：优惠码 CRUD 与核销进度。
// 取优口径=一单一优惠源不叠加；快照随订单落库，删除码不影响历史对账。

const rows = ref<CouponRow[]>([])
const batches = ref<CardBatch[]>([])
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    rows.value = await getCoupons()
    batches.value = await get<CardBatch[]>('/api/card-batches')
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    loading.value = false
  }
}

const batchName = (id: number): string => batches.value.find((b) => b.id === id)?.name ?? `#${id}`

function scopeText(scope: string): string {
  if (scope === 'all') return '全部商品'
  const id = Number(scope.slice('batch:'.length))
  return `仅批次：${batchName(id)}`
}

function valueText(row: CouponRow): string {
  return row.kind === 'cut' ? `减 ¥${(row.value / 100).toFixed(2)}` : `减 ${row.value / 100}%`
}

function windowText(row: CouponRow): string {
  const s = row.starts_at ? formatDate(row.starts_at) : '即刻'
  const e = row.ends_at ? formatDate(row.ends_at) : '长期'
  return `${s} ~ ${e}`
}

function stateText(row: CouponRow): { text: string; type: 'success' | 'warning' | 'info' | 'danger' } {
  const now = Date.now()
  if (row.starts_at && new Date(row.starts_at).getTime() > now) return { text: '未开始', type: 'info' }
  if (row.ends_at && new Date(row.ends_at).getTime() <= now) return { text: '已过期', type: 'danger' }
  if (row.total > 0 && row.used >= row.total) return { text: '已领完', type: 'warning' }
  return { text: '进行中', type: 'success' }
}

// ---- 新建/编辑（同一表单）----
const dialogVisible = ref(false)
const saving = ref(false)
const editing = ref<CouponRow | null>(null)
const form = reactive({
  code: '',
  kind: 'cut' as CouponRow['kind'],
  valueYuan: 5, // cut 录元，口径分
  pctOff: 10, // pct 录百分比，口径基点
  scopeAll: true,
  batchId: 0,
  minAmountYuan: 0,
  window: null as [Date, Date] | null,
  total: 0,
  perUser: 1,
})

function openCreate() {
  editing.value = null
  Object.assign(form, { code: '', kind: 'cut', valueYuan: 5, pctOff: 10, scopeAll: true, batchId: 0, minAmountYuan: 0, window: null, total: 0, perUser: 1 })
  dialogVisible.value = true
}

function openEdit(row: CouponRow) {
  editing.value = row
  const scopeAll = row.scope === 'all'
  Object.assign(form, {
    code: row.code,
    kind: row.kind,
    valueYuan: row.kind === 'cut' ? row.value / 100 : 5,
    pctOff: row.kind === 'pct' ? row.value / 100 : 10,
    scopeAll,
    batchId: scopeAll ? 0 : Number(row.scope.slice('batch:'.length)),
    minAmountYuan: row.min_amount / 100,
    window: row.starts_at || row.ends_at ? [row.starts_at ? new Date(row.starts_at) : new Date(), row.ends_at ? new Date(row.ends_at) : new Date()] : null,
    total: row.total,
    perUser: row.per_user,
  })
  dialogVisible.value = true
}

async function save() {
  if (!form.scopeAll && !form.batchId) {
    ElMessage.warning('请选择适用批次')
    return
  }
  const input: CouponInput = {
    code: form.code.trim() || undefined,
    kind: form.kind,
    value: form.kind === 'cut' ? Math.round(form.valueYuan * 100) : Math.round(form.pctOff * 100),
    scope: form.scopeAll ? 'all' : `batch:${form.batchId}`,
    min_amount: Math.round(form.minAmountYuan * 100),
    total: form.total,
    per_user: form.perUser,
  }
  if (form.window) {
    input.starts_at = form.window[0]?.toISOString() ?? null
    input.ends_at = form.window[1]?.toISOString() ?? null
  }
  saving.value = true
  try {
    if (editing.value) {
      await updateCoupon(editing.value.id, input)
      ElMessage.success('已更新')
    } else {
      const row = await createCoupon(input)
      ElMessage.success(`已创建优惠码 ${row.code}`)
    }
    dialogVisible.value = false
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    saving.value = false
  }
}

async function remove(row: CouponRow) {
  await ElMessageBox.confirm(`删除优惠码 ${row.code} 后不可再用（已核销订单的快照保留可对账），确认？`, '删除优惠码', { type: 'warning' })
  try {
    await deleteCoupon(row.id)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

const dialogTitle = computed(() => (editing.value ? `编辑优惠码 ${editing.value.code}` : '新建优惠码'))

onMounted(load)
</script>

<template>
  <div class="page">
    <h2>促销</h2>
    <p class="page-desc">优惠码（下单立减/折扣）：一单一码不叠加；已核销订单的优惠快照随单留档，删除码不影响历史对账。</p>

    <div style="margin-bottom: 12px">
      <el-button type="primary" @click="openCreate">新建优惠码</el-button>
    </div>

    <el-table :data="rows" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column prop="code" label="优惠码" width="180">
        <template #default="{ row }"><code>{{ row.code }}</code></template>
      </el-table-column>
      <el-table-column label="力度" width="120">
        <template #default="{ row }">{{ valueText(row) }}</template>
      </el-table-column>
      <el-table-column label="适用范围" min-width="150">
        <template #default="{ row }">{{ scopeText(row.scope) }}</template>
      </el-table-column>
      <el-table-column label="门槛" width="100">
        <template #default="{ row }">{{ row.min_amount > 0 ? `满 ¥${(row.min_amount / 100).toFixed(2)}` : '—' }}</template>
      </el-table-column>
      <el-table-column label="有效期" min-width="240">
        <template #default="{ row }">{{ windowText(row) }}</template>
      </el-table-column>
      <el-table-column label="已用/总量" width="110">
        <template #default="{ row }">{{ row.used }} / {{ row.total > 0 ? row.total : '∞' }}</template>
      </el-table-column>
      <el-table-column prop="per_user" label="每用户" width="80" />
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag :type="stateText(row).type" size="small">{{ stateText(row).text }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="140">
        <template #default="{ row }">
          <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button link type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialogVisible" :title="dialogTitle" width="520px">
      <el-form label-width="110px">
        <el-form-item label="优惠码">
          <el-input v-model="form.code" placeholder="留空自动生成" style="width: 300px" />
        </el-form-item>
        <el-form-item label="类型">
          <el-radio-group v-model="form.kind">
            <el-radio-button value="cut">立减（元）</el-radio-button>
            <el-radio-button value="pct">折扣（%）</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item :label="form.kind === 'cut' ? '减价金额' : '优惠比例'">
          <el-input-number v-if="form.kind === 'cut'" v-model="form.valueYuan" :min="0.01" :precision="2" :step="5" />
          <el-input-number v-else v-model="form.pctOff" :min="0.01" :max="99.99" :precision="2" :step="5" />
          <span class="form-hint" style="margin-left: 8px">{{ form.kind === 'cut' ? '按元录入，单笔实付最低 0 元' : '按百分比录入（如 25 = 减 25%）' }}</span>
        </el-form-item>
        <el-form-item label="适用范围">
          <el-radio-group v-model="form.scopeAll">
            <el-radio-button :value="true">全部商品</el-radio-button>
            <el-radio-button :value="false">指定批次</el-radio-button>
          </el-radio-group>
          <el-select v-if="!form.scopeAll" v-model="form.batchId" placeholder="选择批次" style="width: 220px; margin-left: 8px">
            <el-option v-for="b in batches" :key="b.id" :value="b.id" :label="`${b.name}（¥${(b.price_cents / 100).toFixed(2)}）`" />
          </el-select>
        </el-form-item>
        <el-form-item label="使用门槛">
          <el-input-number v-model="form.minAmountYuan" :min="0" :precision="2" :step="10" />
          <span class="form-hint" style="margin-left: 8px">订单原价满此金额可用，0=无门槛</span>
        </el-form-item>
        <el-form-item label="有效期">
          <el-date-picker v-model="form.window" type="datetimerange" start-placeholder="开始（默认即刻）" end-placeholder="结束（默认长期）" value-format="YYYY-MM-DDTHH:mm:ssZ" style="width: 340px" />
        </el-form-item>
        <el-form-item label="发放总量">
          <el-input-number v-model="form.total" :min="0" :step="100" />
          <span class="form-hint" style="margin-left: 8px">0=不限</span>
        </el-form-item>
        <el-form-item label="每用户限用">
          <el-input-number v-model="form.perUser" :min="1" :step="1" />
          <span class="form-hint" style="margin-left: 8px">按未失效订单计次（含待支付）</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>
