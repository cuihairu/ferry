<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  createProvider, createTemplate, deleteProvider, deleteTemplate,
  getProviders, getProvisionJobs, getTemplates, runProvision, updateProvider, updateTemplate,
  type CloudProvider, type ProvisionJob, type ProvisionTemplate,
} from '../api'

// OS-1：供给配置——云提供商凭证与机型模板。
// 凭证机密加密落库（R24 口径）：表单不回显明文，留空表示保留原值；
// 模板含机型/区域/带宽/计费/方向线路标签，OS-2 渲染 HCL、OS-4 入池补全元数据。
// OS-2：模板可触发 plan/apply（后台执行），job 留痕列表看结果与日志尾部。

const providers = ref<CloudProvider[]>([])
const templates = ref<ProvisionTemplate[]>([])
const jobs = ref<ProvisionJob[]>([])
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    ;[providers.value, templates.value, jobs.value] = await Promise.all([
      getProviders(), getTemplates(), getProvisionJobs(30),
    ])
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

// ---- 供给执行（OS-2）----
const running = ref<number[]>([])

async function runTpl(row: ProvisionTemplate, action: 'plan' | 'apply') {
  if (action === 'apply') {
    try {
      await ElMessageBox.confirm(
        `按模板「${row.name}」执行 apply？将真实创建/变更云资源。`, '供给确认', { type: 'warning' },
      )
    } catch {
      return
    }
  }
  running.value = [...running.value, row.id]
  try {
    await runProvision(row.id, action)
    ElMessage.success(action === 'apply' ? 'apply 已受理，执行中' : 'plan 已受理，执行中')
    setTimeout(load, 1500) // 稍后刷新 job 状态
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    running.value = running.value.filter((id) => id !== row.id)
  }
}

// ---- 提供商 ----
const provDialog = ref(false)
const provEditing = ref<CloudProvider | null>(null)
const provForm = ref({ name: '', type: '', access_key: '' })

function newProvider() {
  provEditing.value = null
  provForm.value = { name: '', type: '', access_key: '' }
  provDialog.value = true
}
function editProvider(row: CloudProvider) {
  provEditing.value = row
  provForm.value = { name: row.name, type: row.type, access_key: '' }
  provDialog.value = true
}
async function saveProvider() {
  try {
    if (provEditing.value) {
      const body: Record<string, string> = { name: provForm.value.name, type: provForm.value.type }
      if (provForm.value.access_key) body.access_key = provForm.value.access_key
      await updateProvider(provEditing.value.id, body)
      ElMessage.success('提供商已更新')
    } else {
      await createProvider(provForm.value)
      ElMessage.success('提供商已录入（机密已加密）')
    }
    provDialog.value = false
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}
async function removeProvider(row: CloudProvider) {
  try {
    await ElMessageBox.confirm(`删除提供商「${row.name}」？机密密文一并删除。`, '删除确认', { type: 'warning' })
  } catch {
    return
  }
  try {
    await deleteProvider(row.id)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

// ---- 机型模板 ----
const tplDialog = ref(false)
const tplEditing = ref<ProvisionTemplate | null>(null)
const emptyTpl = () => ({
  name: '', provider_id: 0, plan: '', region: '', bw_mbps: 0,
  billing_type: '包月', monthly_cost_cents: 0, traffic_price_cents: 0,
  direction: 'out', line_type: '163', role: 'entry', transport: 'ws-tls',
  config: '',
})
const tplForm = ref(emptyTpl())

const DIRECTION_TEXT: Record<string, string> = { out: '出海', in: '回国', both: '双向' }
const ROLE_TEXT: Record<string, string> = { entry: '入口', landing: '落地', both: '双向', relay: 'relay' }

function newTemplate() {
  tplEditing.value = null
  tplForm.value = emptyTpl()
  tplDialog.value = true
}
function editTemplate(row: ProvisionTemplate) {
  tplEditing.value = row
  tplForm.value = { ...row }
  tplDialog.value = true
}
async function saveTemplate() {
  try {
    if (tplEditing.value) {
      await updateTemplate(tplEditing.value.id, tplForm.value)
      ElMessage.success('模板已更新')
    } else {
      await createTemplate(tplForm.value)
      ElMessage.success('模板已创建')
    }
    tplDialog.value = false
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}
async function removeTemplate(row: ProvisionTemplate) {
  try {
    await ElMessageBox.confirm(`删除模板「${row.name}」？`, '删除确认', { type: 'warning' })
  } catch {
    return
  }
  try {
    await deleteTemplate(row.id)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

function yuan(cents: number): string {
  return '¥' + (cents / 100).toFixed(2)
}
</script>

<template>
  <div class="page">
    <h2>供给</h2>
    <p class="page-desc">
      云提供商凭证与机型模板：凭证加密存储（主密钥 FERRY_SECRET_KEY 部署侧注入，丢失不可恢复请备份）；
      模板供一键开服渲染供给配置，方向/线路/计费口径与节点元数据对齐。
    </p>

    <div class="head-row">
      <h3 class="section">云提供商</h3>
      <el-button type="primary" size="small" @click="newProvider">录入提供商</el-button>
    </div>
    <el-table :data="providers" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column prop="name" label="名称" min-width="140" />
      <el-table-column prop="type" label="类型" width="120" />
      <el-table-column label="机密" width="110">
        <template #default="{ row }">
          <el-tag v-if="row.has_access_key" type="success" size="small" effect="plain">已录入</el-tag>
          <span v-else class="dim">未录入</span>
        </template>
      </el-table-column>
      <el-table-column label="启用" width="90">
        <template #default="{ row }">
          <el-tag :type="row.enabled ? 'success' : 'info'" size="small" effect="plain">
            {{ row.enabled ? '启用' : '停用' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="140">
        <template #default="{ row }">
          <el-button link type="primary" size="small" @click="editProvider(row)">编辑</el-button>
          <el-button link type="danger" size="small" @click="removeProvider(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <div class="head-row">
      <h3 class="section">机型模板</h3>
      <el-button type="primary" size="small" :disabled="providers.length === 0" @click="newTemplate">
        新建模板
      </el-button>
    </div>
    <el-table :data="templates" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column prop="name" label="名称" min-width="120" />
      <el-table-column label="提供商" width="110">
        <template #default="{ row }">{{ providers.find((p) => p.id === row.provider_id)?.name ?? row.provider_id }}</template>
      </el-table-column>
      <el-table-column prop="plan" label="机型" min-width="110" />
      <el-table-column prop="region" label="区域" width="90" />
      <el-table-column label="带宽" width="90">
        <template #default="{ row }">{{ row.bw_mbps ? row.bw_mbps + ' Mbps' : '—' }}</template>
      </el-table-column>
      <el-table-column prop="billing_type" label="计费" width="80" />
      <el-table-column label="月固定" width="90">
        <template #default="{ row }">{{ row.monthly_cost_cents ? yuan(row.monthly_cost_cents) : '—' }}</template>
      </el-table-column>
      <el-table-column label="方向" width="70">
        <template #default="{ row }">{{ DIRECTION_TEXT[row.direction] ?? row.direction }}</template>
      </el-table-column>
      <el-table-column prop="line_type" label="线路" width="90" />
      <el-table-column label="角色" width="80">
        <template #default="{ row }">{{ ROLE_TEXT[row.role] ?? row.role }}</template>
      </el-table-column>
      <el-table-column label="操作" width="230">
        <template #default="{ row }">
          <el-button link type="primary" size="small" :loading="running.includes(row.id)" @click="runTpl(row, 'plan')">plan</el-button>
          <el-button link type="success" size="small" :loading="running.includes(row.id)" @click="runTpl(row, 'apply')">apply</el-button>
          <el-button link type="primary" size="small" @click="editTemplate(row)">编辑</el-button>
          <el-button link type="danger" size="small" @click="removeTemplate(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <div class="head-row">
      <h3 class="section">执行留痕</h3>
      <el-button size="small" @click="load" :loading="loading">刷新</el-button>
    </div>
    <el-table :data="jobs" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column prop="template_name" label="模板" min-width="110" />
      <el-table-column prop="action" label="动作" width="80" />
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag :type="row.status === 'ok' ? 'success' : row.status === 'failed' ? 'danger' : 'warning'" size="small" effect="plain">
            {{ row.status === 'ok' ? '成功' : row.status === 'failed' ? '失败' : '执行中' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="日志尾部" min-width="220">
        <template #default="{ row }">
          <el-tooltip v-if="row.log" :content="row.log" raw-content placement="top" :show-after="200">
            <span class="log-cell">{{ row.log.slice(-60) }}</span>
          </el-tooltip>
          <span v-else class="dim">—</span>
        </template>
      </el-table-column>
      <el-table-column prop="created_at" label="时间" width="170" />
    </el-table>

    <el-dialog v-model="provDialog" :title="provEditing ? '编辑提供商' : '录入提供商'" width="440px">
      <el-form label-width="80px">
        <el-form-item label="名称">
          <el-input v-model="provForm.name" placeholder="如 vultr-main" />
        </el-form-item>
        <el-form-item label="类型">
          <el-input v-model="provForm.type" placeholder="OpenTofu provider 名，如 vultr / hetzner" />
        </el-form-item>
        <el-form-item label="API 密钥">
          <el-input
            v-model="provForm.access_key" type="password" show-password
            :placeholder="provEditing ? '留空保留原密钥' : 'API 密钥（加密存储，不回显）'"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="provDialog = false">取消</el-button>
        <el-button type="primary" @click="saveProvider">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="tplDialog" :title="tplEditing ? '编辑模板' : '新建模板'" width="520px">
      <el-form label-width="90px">
        <el-form-item label="名称">
          <el-input v-model="tplForm.name" placeholder="如 hk-3t-500m" />
        </el-form-item>
        <el-form-item label="提供商">
          <el-select v-model="tplForm.provider_id" style="width: 100%">
            <el-option v-for="p in providers" :key="p.id" :value="p.id" :label="p.name" />
          </el-select>
        </el-form-item>
        <el-form-item label="机型 / 区域">
          <div class="row2">
            <el-input v-model="tplForm.plan" placeholder="机型 slug" />
            <el-input v-model="tplForm.region" placeholder="区域" />
          </div>
        </el-form-item>
        <el-form-item label="带宽 Mbps">
          <el-input-number v-model="tplForm.bw_mbps" :min="0" :controls="false" style="width: 120px" />
        </el-form-item>
        <el-form-item label="计费">
          <div class="row2">
            <el-select v-model="tplForm.billing_type" style="width: 110px">
              <el-option value="包月" label="包月" />
              <el-option value="按流量" label="按流量" />
            </el-select>
            <el-input-number v-model="tplForm.monthly_cost_cents" :min="0" :controls="false" placeholder="月固定(分)" style="width: 120px" />
            <el-input-number v-model="tplForm.traffic_price_cents" :min="0" :controls="false" placeholder="流量单价(分/GB)" style="width: 140px" />
          </div>
        </el-form-item>
        <el-form-item label="方向 / 线路">
          <div class="row2">
            <el-select v-model="tplForm.direction" style="width: 100px">
              <el-option value="out" label="出海" />
              <el-option value="in" label="回国" />
              <el-option value="both" label="双向" />
            </el-select>
            <el-select v-model="tplForm.line_type" style="width: 130px">
              <el-option v-for="l in ['163', 'cn2_gia', 'cu_vip', 'cmi', 'iplc']" :key="l" :value="l" :label="l" />
            </el-select>
          </div>
        </el-form-item>
        <el-form-item label="角色 / 传输">
          <div class="row2">
            <el-select v-model="tplForm.role" style="width: 110px">
              <el-option value="entry" label="入口" />
              <el-option value="landing" label="落地" />
              <el-option value="both" label="双向" />
              <el-option value="relay" label="relay" />
            </el-select>
            <el-select v-model="tplForm.transport" style="width: 130px">
              <el-option v-for="t in ['tls', 'ws-tls', 'quic', 'ssh']" :key="t" :value="t"
                :label="t === 'ssh' ? 'ssh（备选）' : t" />
            </el-select>
          </div>
        </el-form-item>
        <el-form-item label="配置模板">
          <el-input
            v-model="tplForm.config" type="textarea" :rows="4"
            placeholder='协议配置模板 JSON（可选）。开服后复制到节点行，agent 首连即自动下发，探测通过自动入池'
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="tplDialog = false">取消</el-button>
        <el-button type="primary" @click="saveTemplate">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.page-desc {
  color: var(--ferry-text-dim);
  font-size: 13px;
  margin-bottom: 16px;
}
.head-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin: 18px 0 10px;
}
.head-row .section {
  margin: 0;
}
.section {
  font-size: 15px;
  font-weight: 650;
}
.dim {
  color: var(--ferry-text-muted);
}
.row2 {
  display: flex;
  gap: 8px;
  flex: 1;
}
.log-cell {
  font-family: ui-monospace, monospace;
  font-size: 12px;
  color: var(--ferry-text-dim);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  display: inline-block;
  max-width: 240px;
  vertical-align: bottom;
}
</style>
