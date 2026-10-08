<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  get, post, patch, del,
  listDistributors, createDistributor, updateDistributor, distributorLedger,
  distributorPayout, distributorAdjust,
  type CardBatch, type CardCode, type Distributor, type DistributorLedger,
} from '../api'
import { GB, formatBytes, formatDate } from '../utils/format'

// PAY-6：卡密批次管理 + 卡密明细（复制/禁用）+ CSV 导出。
// DS-2：代理管理卡（建号/停用/佣金比例）、结算打款 payout、账目流水。

const batches = ref<CardBatch[]>([])
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    batches.value = await get<CardBatch[]>('/api/card-batches')
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    loading.value = false
  }
}

// ---- 代理（DS-2 管理面）----
const distributors = ref<Distributor[]>([])

async function loadDistributors() {
  try {
    const res = await listDistributors()
    distributors.value = res.distributors
  } catch (e) {
    ElMessage.error(String(e))
  }
}

onMounted(() => {
  load()
  loadDistributors()
})

// ---- 代理建号 / 编辑（DS-2）----
const distVisible = ref(false)
const distSaving = ref(false)
const distForm = reactive({ username: '', password: '', discountPercent: 0, note: '' })

function openDistCreate() {
  Object.assign(distForm, { username: '', password: '', discountPercent: 0, note: '' })
  distVisible.value = true
}

async function saveDist() {
  const username = distForm.username.trim()
  if (!username) {
    ElMessage.warning('请输入代理用户名')
    return
  }
  if (!distForm.password) {
    ElMessage.warning('请输入初始密码')
    return
  }
  distSaving.value = true
  try {
    await createDistributor({
      username,
      password: distForm.password,
      discount_percent: distForm.discountPercent,
      note: distForm.note.trim(),
    })
    distVisible.value = false
    ElMessage.success('已建代理号')
    await loadDistributors()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    distSaving.value = false
  }
}

const editVisible = ref(false)
const editSaving = ref(false)
const editForm = reactive({ id: 0, username: '', password: '', discountPercent: 0, note: '', enabled: true })

function openDistEdit(d: Distributor) {
  Object.assign(editForm, {
    id: d.id, username: d.username, password: '',
    discountPercent: d.discount_percent, note: d.note, enabled: d.enabled,
  })
  editVisible.value = true
}

async function saveDistEdit() {
  editSaving.value = true
  try {
    const body: Record<string, unknown> = {
      discount_percent: editForm.discountPercent,
      note: editForm.note.trim(),
      enabled: editForm.enabled,
    }
    // 密码留空=不重置（部分更新口径）。
    if (editForm.password) body.password = editForm.password
    await updateDistributor(editForm.id, body)
    editVisible.value = false
    ElMessage.success('已保存')
    await loadDistributors()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    editSaving.value = false
  }
}

async function toggleDist(d: Distributor) {
  const action = d.enabled ? '停用' : '启用'
  await ElMessageBox.confirm(
    d.enabled ? `停用「${d.username}」即禁登录，账目保留，确认？` : `启用「${d.username}」，确认？`,
    `${action}代理`, { type: 'warning' },
  )
  try {
    await updateDistributor(d.id, { enabled: !d.enabled })
    ElMessage.success(`已${action}`)
    await loadDistributors()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

// ---- 代理流水 / 结算打款 / 人工调（DS-2）----
const ledVisible = ref(false)
const ledLoading = ref(false)
const ledDistributor = ref<Distributor | null>(null)
const ledRows = ref<DistributorLedger[]>([])
const ledBalance = ref(0)
const opAmountYuan = ref(0)
const opNote = ref('')
const opBusy = ref(false)

async function openLedger(d: Distributor) {
  ledDistributor.value = d
  opAmountYuan.value = 0
  opNote.value = ''
  ledVisible.value = true
  await loadLedger()
}

async function loadLedger() {
  if (!ledDistributor.value) return
  ledLoading.value = true
  try {
    const res = await distributorLedger(ledDistributor.value.id)
    ledRows.value = res.ledger
    ledBalance.value = res.balance_cents
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    ledLoading.value = false
  }
}

async function doPayout() {
  if (!ledDistributor.value) return
  const cents = Math.round(opAmountYuan.value * 100)
  if (cents <= 0) {
    ElMessage.warning('打款金额需大于 0')
    return
  }
  opBusy.value = true
  try {
    await distributorPayout(ledDistributor.value.id, { amount_cents: cents, note: opNote.value.trim() })
    ElMessage.success('已落 payout 结算行')
    await loadLedger()
    await loadDistributors()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    opBusy.value = false
  }
}

async function doAdjust() {
  if (!ledDistributor.value) return
  const cents = Math.round(opAmountYuan.value * 100)
  if (cents === 0) {
    ElMessage.warning('调整金额不能为 0')
    return
  }
  opBusy.value = true
  try {
    await distributorAdjust(ledDistributor.value.id, { amount_cents: cents, note: opNote.value.trim() })
    ElMessage.success('已落 adjust 调整行')
    await loadLedger()
    await loadDistributors()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    opBusy.value = false
  }
}

/** yuan 分转元两位小数展示。 */
function yuan(cents?: number): string {
  return ((cents ?? 0) / 100).toFixed(2)
}

/** ledAmount 流水金额按余额影响方向展示：sale/commission 加、payout 减、adjust 带符号。 */
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

/** distName 批次归属代理名（0=面板自营）。 */
function distName(id: number): string {
  if (!id) return ''
  return distributors.value.find((d) => d.id === id)?.username ?? `#${id}`
}

// ---- 建批次 ----
const createVisible = ref(false)
const saving = ref(false)
const form = reactive({
  name: '',
  grantType: 'add_quota' as CardBatch['grant_type'],
  grantValue: 10,
  priceYuan: 0,
  total: 10,
  expiredAt: null as Date | null,
  distributorId: 0, // 归属代理：0=面板自营（DS-1），建时定不可自改
})

function openCreate() {
  Object.assign(form, { name: '', grantType: 'add_quota', grantValue: 10, priceYuan: 0, total: 10, expiredAt: null, distributorId: 0 })
  createVisible.value = true
}

async function save() {
  const name = form.name.trim()
  if (!name) {
    ElMessage.warning('请输入批次名称')
    return
  }
  if (form.grantValue <= 0) {
    ElMessage.warning('权益值需大于 0')
    return
  }
  saving.value = true
  try {
    const body: Record<string, unknown> = {
      name,
      grant_type: form.grantType,
      // 加配额按 GiB 录入，后端口径为字节；售价按元录入，口径为分。
      grant_value: form.grantType === 'add_quota' ? Math.round(form.grantValue * GB) : form.grantValue,
      price_cents: Math.round(form.priceYuan * 100),
      total: form.total,
      distributor_id: form.distributorId,
    }
    if (form.expiredAt) body.expired_at = form.expiredAt.toISOString()
    const res = await post<{ batch: CardBatch; codes: string[] }>('/api/card-batches', body)
    createVisible.value = false
    ElMessage.success(`已生成 ${res.codes.length} 张卡密`)
    await load()
    await openCodes(res.batch)
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    saving.value = false
  }
}

// ---- 卡密明细 ----
const codesVisible = ref(false)
const codes = ref<CardCode[]>([])
const activeBatch = ref<CardBatch | null>(null)

async function openCodes(b: CardBatch) {
  activeBatch.value = b
  codesVisible.value = true
  try {
    codes.value = await get<CardCode[]>(`/api/card-batches/${b.id}/codes`)
  } catch (e) {
    ElMessage.error(String(e))
  }
}

async function copyCode(code: string) {
  await navigator.clipboard.writeText(code)
  ElMessage.success('已复制')
}

async function disableCode(row: CardCode) {
  await ElMessageBox.confirm(`禁用卡密 ${row.code} 后不可再兑换，确认？`, '禁用卡密', { type: 'warning' })
  try {
    await patch(`/api/card-codes/${row.id}/disable`)
    ElMessage.success('已禁用')
    if (activeBatch.value) await openCodes(activeBatch.value)
  } catch (e) {
    ElMessage.error(String(e))
  }
}

// ---- 导出 / 删除 ----
async function exportBatch(b: CardBatch) {
  try {
    const res = await fetch(`/api/card-batches/${b.id}/export.csv`)
    if (!res.ok) throw new Error(`导出失败（${res.status}）`)
    const blob = await res.blob()
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `card-batch-${b.id}.csv`
    a.click()
    URL.revokeObjectURL(url)
  } catch (e) {
    ElMessage.error(String(e))
  }
}

async function remove(b: CardBatch) {
  await ElMessageBox.confirm(`删除批次「${b.name}」将连带删除其全部卡密，确认？`, '删除批次', { type: 'warning' })
  try {
    await del(`/api/card-batches/${b.id}`)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

// ---- 展示辅助 ----
function grantText(b: CardBatch): string {
  return b.grant_type === 'add_quota' ? `+${formatBytes(b.grant_value)}` : `+${b.grant_value} 天`
}

function priceText(b: CardBatch): string {
  return b.price_cents > 0 ? `¥${(b.price_cents / 100).toFixed(2)}` : '—'
}

function batchState(b: CardBatch): { text: string; type: 'success' | 'info' | 'danger' | 'warning' } {
  if (b.expired_at && new Date(b.expired_at).getTime() < Date.now()) return { text: '已过期', type: 'danger' }
  if (b.remaining === 0) return { text: '已售罄', type: 'info' }
  return { text: '可用', type: 'success' }
}

const codeState: Record<CardCode['status'], { text: string; type: 'success' | 'info' | 'danger' }> = {
  unused: { text: '未使用', type: 'info' },
  used: { text: '已使用', type: 'success' },
  disabled: { text: '已禁用', type: 'danger' },
}
</script>

<template>
  <div class="page">
    <h2>卡密</h2>
    <p class="page-desc">批次生成、导出与回收；兑换记为 provider=card 的订单。</p>

    <div class="toolbar">
      <el-button type="primary" @click="openCreate">新建批次</el-button>
    </div>

    <el-table :data="batches" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column prop="name" label="名称" min-width="150" />
      <el-table-column label="归属" width="90">
        <template #default="{ row }">
          <el-tag v-if="row.distributor_id" size="small" effect="plain">{{ distName(row.distributor_id) }}</el-tag>
          <span v-else>自营</span>
        </template>
      </el-table-column>
      <el-table-column label="权益" width="130">
        <template #default="{ row }">{{ grantText(row) }}</template>
      </el-table-column>
      <el-table-column label="在线售价" width="100">
        <template #default="{ row }">{{ priceText(row) }}</template>
      </el-table-column>
      <el-table-column label="剩余 / 总数" width="120">
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
      <el-table-column prop="created_by" label="创建人" width="100" />
      <el-table-column label="创建时间" width="150">
        <template #default="{ row }">{{ formatDate(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="220" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openCodes(row)">卡密</el-button>
          <el-button link @click="exportBatch(row)">导出</el-button>
          <el-button link type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <h3 class="section-title">代理（DS 分销）</h3>
    <p class="page-desc">代理批次按面价分润：sale 留痕、commission 入未结算余额；结算打款落 payout，线下打款面板只记账。</p>
    <div class="toolbar">
      <el-button type="primary" @click="openDistCreate">新建代理</el-button>
    </div>
    <el-table :data="distributors" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column prop="username" label="用户名" min-width="120" />
      <el-table-column label="佣金比例" width="90">
        <template #default="{ row }">{{ row.discount_percent }}%</template>
      </el-table-column>
      <el-table-column label="售卡收入" width="100">
        <template #default="{ row }">¥{{ yuan(row.sale_cents) }}</template>
      </el-table-column>
      <el-table-column label="佣金" width="100">
        <template #default="{ row }">¥{{ yuan(row.commission_cents) }}</template>
      </el-table-column>
      <el-table-column label="已结算" width="100">
        <template #default="{ row }">¥{{ yuan(row.payout_cents) }}</template>
      </el-table-column>
      <el-table-column label="未结算" width="100">
        <template #default="{ row }">
          <span :class="{ 'balance-hot': row.balance_cents > 0 }">¥{{ yuan(row.balance_cents) }}</span>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="80">
        <template #default="{ row }">
          <el-tag :type="row.enabled ? 'success' : 'danger'" size="small" effect="dark">
            {{ row.enabled ? '启用' : '停用' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="note" label="备注" min-width="110" show-overflow-tooltip />
      <el-table-column label="操作" width="220" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openLedger(row)">流水 / 结算</el-button>
          <el-button link @click="openDistEdit(row)">编辑</el-button>
          <el-button link :type="row.enabled ? 'danger' : 'success'" @click="toggleDist(row)">
            {{ row.enabled ? '停用' : '启用' }}
          </el-button>
        </template>
      </el-table-column>
    </el-table>
    <el-empty v-if="distributors.length === 0" description="暂无代理——建号后在新建批次里选归属" :image-size="60" />

    <el-dialog v-model="createVisible" title="新建批次" width="480px">
      <el-form label-width="100px">
        <el-form-item label="名称">
          <el-input v-model="form.name" maxlength="128" placeholder="如：50GB 月卡" />
        </el-form-item>
        <el-form-item label="权益类型">
          <el-radio-group v-model="form.grantType">
            <el-radio-button value="add_quota">加配额</el-radio-button>
            <el-radio-button value="extend_days">延长时长</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item :label="form.grantType === 'add_quota' ? '配额 (GiB)' : '时长 (天)'">
          <el-input-number v-model="form.grantValue" :min="1" :step="form.grantType === 'add_quota' ? 10 : 30" />
        </el-form-item>
        <el-form-item label="在线售价 (元)">
          <el-input-number v-model="form.priceYuan" :min="0" :precision="2" :step="10" />
          <div class="form-tip">0 表示仅卡密兑换，不进入门户在售商品（PAY-11）；代理批次按此面价分润</div>
        </el-form-item>
        <el-form-item label="归属代理">
          <el-select v-model="form.distributorId" style="width: 100%">
            <el-option :value="0" label="面板自营" />
            <el-option v-for="d in distributors" :key="d.id" :value="d.id" :label="`${d.username}（佣金 ${d.discount_percent}%）`" />
          </el-select>
          <div class="form-tip">归属建时定，代理不可自改；代理批次兑换即落 sale + commission 账目</div>
        </el-form-item>
        <el-form-item label="生成数量">
          <el-input-number v-model="form.total" :min="1" :max="10000" :step="10" />
        </el-form-item>
        <el-form-item label="有效期至">
          <el-date-picker v-model="form.expiredAt" type="datetime" placeholder="留空表示永久" style="width: 100%" />
          <span class="form-hint">过期后卡密不可兑换</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="createVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">生成</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="codesVisible" :title="activeBatch ? `卡密：${activeBatch.name}` : '卡密'" width="720px">
      <el-table :data="codes" max-height="420">
        <el-table-column prop="code" label="卡密" min-width="180">
          <template #default="{ row }">
            <span class="code">{{ row.code }}</span>
          </template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag :type="codeState[row.status as CardCode['status']].type" size="small" effect="dark">
              {{ codeState[row.status as CardCode['status']].text }}
            </el-tag>
          </template>
        </el-table-column>
        <el-table-column prop="fail_count" label="失败次数" width="90" />
        <el-table-column label="操作" width="150">
          <template #default="{ row }">
            <el-button link type="primary" @click="copyCode(row.code)">复制</el-button>
            <el-button v-if="row.status === 'unused'" link type="danger" @click="disableCode(row)">禁用</el-button>
          </template>
        </el-table-column>
      </el-table>
    </el-dialog>

    <el-dialog v-model="distVisible" title="新建代理" width="460px">
      <el-form label-width="100px">
        <el-form-item label="用户名">
          <el-input v-model="distForm.username" maxlength="64" placeholder="代理登录名（唯一）" />
        </el-form-item>
        <el-form-item label="初始密码">
          <el-input v-model="distForm.password" type="password" show-password placeholder="代理登录凭证" />
        </el-form-item>
        <el-form-item label="佣金比例 (%)">
          <el-input-number v-model="distForm.discountPercent" :min="0" :max="100" />
          <div class="form-tip">面价让利归代理：commission = 面价 × 比例</div>
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="distForm.note" maxlength="255" placeholder="渠道 / 联系方式（选填）" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="distVisible = false">取消</el-button>
        <el-button type="primary" :loading="distSaving" @click="saveDist">建号</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="editVisible" title="编辑代理" width="460px">
      <el-form label-width="100px">
        <el-form-item label="用户名">
          <el-input :model-value="editForm.username" disabled />
          <div class="form-tip">登录身份即账目主体，不可改名</div>
        </el-form-item>
        <el-form-item label="重置密码">
          <el-input v-model="editForm.password" type="password" show-password placeholder="留空不重置" />
        </el-form-item>
        <el-form-item label="佣金比例 (%)">
          <el-input-number v-model="editForm.discountPercent" :min="0" :max="100" />
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="editForm.enabled" />
          <span class="form-hint">停用即禁登录，账目保留</span>
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="editForm.note" maxlength="255" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="editVisible = false">取消</el-button>
        <el-button type="primary" :loading="editSaving" @click="saveDistEdit">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="ledVisible" :title="ledDistributor ? `账目：${ledDistributor.username}` : '账目'" width="760px">
      <div class="led-head">
        <span>未结算余额 <b :class="{ 'balance-hot': ledBalance > 0 }">¥{{ yuan(ledBalance) }}</b></span>
        <span class="led-hint">余额 = Σ佣金 + Σ调整 − Σ结算（sale 只留痕）</span>
      </div>
      <div class="op-row">
        <el-input-number v-model="opAmountYuan" :precision="2" :step="10" placeholder="金额" />
        <el-input v-model="opNote" maxlength="255" placeholder="备注（账期 / 单号 / 原因）" class="op-note" />
        <el-button type="warning" :disabled="opBusy" @click="doPayout">结算打款</el-button>
        <el-button :disabled="opBusy" @click="doAdjust">人工调整 ±</el-button>
      </div>
      <el-table :data="ledRows" v-loading="ledLoading" max-height="380" size="small">
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
            <span class="code">{{ row.order_no || '—' }}</span>
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
    </el-dialog>
  </div>
</template>

<style scoped>
.toolbar {
  margin-bottom: 16px;
}
.section-title {
  margin: 28px 0 4px;
  font-size: 16px;
}
.balance-hot {
  color: var(--el-color-warning);
  font-weight: 600;
}
.led-head {
  display: flex;
  align-items: baseline;
  gap: 12px;
  margin-bottom: 10px;
}
.led-hint {
  color: var(--ferry-text-muted);
  font-size: 12px;
}
.op-row {
  display: flex;
  gap: 8px;
  align-items: center;
  margin-bottom: 12px;
}
.op-note {
  flex: 1;
}
.form-hint {
  margin-left: 10px;
  color: var(--ferry-text-muted);
  font-size: 12px;
}
.form-tip {
  width: 100%;
  margin-top: 4px;
  color: var(--ferry-text-muted);
  font-size: 12px;
  line-height: 1.4;
}
.code {
  font-family: 'SFMono-Regular', 'JetBrains Mono', Menlo, Consolas, monospace;
  letter-spacing: 0.02em;
}
</style>
