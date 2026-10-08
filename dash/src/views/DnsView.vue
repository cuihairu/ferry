<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  createCertTask, createDNSFront, createDNSProvider, deleteCertTask, deleteDNSFront,
  deleteDNSProvider, geoSyncDns, getCertTasks, getDNSFronts, getDNSProviders, issueCertTask,
  updateCertTask, updateDNSFront, updateDNSProvider,
  type CertTask, type DNSFront, type DNSProvider,
} from '../api'

// 域名前置（BR-2）与证书编排（BR-4）：DNS 商凭证、前置记录、证书任务。
// 凭证加密落库（R24 口径）：表单不回显明文，留空表示保留原值；
// 记录常态指向 primary_ip，被封恢复 L1 切备用 IP 轮换、探测恢复回切——
// 域名不换、IP 随换。备用 IP 每行一个，提交时转 JSON 数组。
// 证书任务面板管编排与到期（30 天自动续），签发执行 acme.sh 工具位。

const providers = ref<DNSProvider[]>([])
const fronts = ref<DNSFront[]>([])
const certTasks = ref<CertTask[]>([])
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    ;[providers.value, fronts.value, certTasks.value] = await Promise.all([
      getDNSProviders(), getDNSFronts(), getCertTasks(),
    ])
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

// ---- DNS 商凭证 ----
const provDialog = ref(false)
const provEditing = ref<DNSProvider | null>(null)
const provForm = ref({ name: '', type: 'cloudflare', api_key: '' })

function newProvider() {
  provEditing.value = null
  provForm.value = { name: '', type: 'cloudflare', api_key: '' }
  provDialog.value = true
}
function editProvider(row: DNSProvider) {
  provEditing.value = row
  provForm.value = { name: row.name, type: row.type, api_key: '' }
  provDialog.value = true
}
async function saveProvider() {
  try {
    if (provEditing.value) {
      const body: Record<string, string> = { name: provForm.value.name, type: provForm.value.type }
      if (provForm.value.api_key) body.api_key = provForm.value.api_key
      await updateDNSProvider(provEditing.value.id, body)
      ElMessage.success('凭证已更新')
    } else {
      await createDNSProvider(provForm.value)
      ElMessage.success('凭证已录入（机密已加密）')
    }
    provDialog.value = false
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}
async function removeProvider(row: DNSProvider) {
  try {
    await ElMessageBox.confirm(`删除 DNS 商「${row.name}」？机密密文一并删除。`, '删除确认', { type: 'warning' })
  } catch {
    return
  }
  try {
    await deleteDNSProvider(row.id)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

// ---- 前置记录 ----
const frontDialog = ref(false)
const frontEditing = ref<DNSFront | null>(null)
const emptyFront = () => ({ name: '', domain: '', provider_id: 0, primary_ip: '', backups: '' })
const frontForm = ref(emptyFront())

// backups 文本域每行一个 IP ↔ 表单 JSON 字符串数组。
function toLines(raw: string): string {
  try {
    const ips: string[] = JSON.parse(raw || '[]')
    return ips.join('\n')
  } catch {
    return raw
  }
}
function fromLines(raw: string): string {
  const ips = raw
    .split('\n')
    .map((s) => s.trim())
    .filter(Boolean)
  return JSON.stringify(ips)
}

function newFront() {
  frontEditing.value = null
  frontForm.value = emptyFront()
  frontDialog.value = true
}
function editFront(row: DNSFront) {
  frontEditing.value = row
  frontForm.value = {
    name: row.name, domain: row.domain, provider_id: row.provider_id,
    primary_ip: row.primary_ip, backups: toLines(row.backup_ips),
  }
  frontDialog.value = true
}
async function saveFront() {
  const body = {
    name: frontForm.value.name,
    domain: frontForm.value.domain,
    provider_id: frontForm.value.provider_id,
    primary_ip: frontForm.value.primary_ip,
    backup_ips: fromLines(frontForm.value.backups),
  }
  try {
    if (frontEditing.value) {
      await updateDNSFront(frontEditing.value.id, body)
      ElMessage.success('前置记录已更新')
    } else {
      await createDNSFront(body)
      ElMessage.success('前置记录已创建')
    }
    frontDialog.value = false
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}
async function removeFront(row: DNSFront) {
  try {
    await ElMessageBox.confirm(`删除前置记录「${row.domain}」？`, '删除确认', { type: 'warning' })
  } catch {
    return
  }
  try {
    await deleteDNSFront(row.id)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

// ---- 分地域对账（E-27）----
// 池摘挂自动触发同步，这里是补偿入口：自动同步失败或人工改库后手动对齐
// {区域slug}.{前置域名} A 记录到各区域代表入口。
const syncing = ref(false)
async function syncGeo() {
  syncing.value = true
  try {
    const out = await geoSyncDns()
    ElMessage.success(out.changed > 0 ? `对账完成：${out.changed} 条记录变更` : '对账完成：无变更')
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    syncing.value = false
  }
}

// ---- 证书任务（BR-4）----
const certDialog = ref(false)
const certEditing = ref<CertTask | null>(null)
const issuing = ref<number[]>([])
const emptyCert = () => ({ name: '', domain: '', sans: '', method: 'dns-01', provider_id: 0 })
const certForm = ref(emptyCert())

const CERT_STATE: Record<string, { text: string; type: 'success' | 'warning' | 'danger' | 'info' }> = {
  pending: { text: '待签发', type: 'info' },
  issuing: { text: '签发中', type: 'warning' },
  ok: { text: '有效', type: 'success' },
  failed: { text: '失败', type: 'danger' },
}

function newCert() {
  certEditing.value = null
  certForm.value = emptyCert()
  certDialog.value = true
}
function editCert(row: CertTask) {
  certEditing.value = row
  certForm.value = {
    name: row.name, domain: row.domain, sans: row.sans,
    method: row.method, provider_id: row.provider_id,
  }
  certDialog.value = true
}
async function saveCert() {
  try {
    if (certEditing.value) {
      await updateCertTask(certEditing.value.id, certForm.value)
      ElMessage.success('证书任务已更新（编排参数变更将自动重签）')
    } else {
      await createCertTask(certForm.value)
      ElMessage.success('证书任务已创建，将自动签发')
    }
    certDialog.value = false
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}
async function runIssue(row: CertTask) {
  issuing.value = [...issuing.value, row.id]
  try {
    await issueCertTask(row.id)
    ElMessage.success('签发已受理，稍后刷新看结果')
    setTimeout(load, 2000)
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    issuing.value = issuing.value.filter((id) => id !== row.id)
  }
}
async function removeCert(row: CertTask) {
  try {
    await ElMessageBox.confirm(`删除证书任务「${row.domain}」？`, '删除确认', { type: 'warning' })
  } catch {
    return
  }
  try {
    await deleteCertTask(row.id)
    ElMessage.success('已删除')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

function fmtDate(v: string | null): string {
  if (!v) return '—'
  return new Date(v).toLocaleString()
}
</script>

<template>
  <div class="page">
    <h2>域名前置</h2>
    <p class="page-desc">
      域名不换、IP 随换：入口证书/SNI 用常态域名，记录常态指向主入口 IP；
      节点被判封进入恢复流水线 L1 时自动切到备用 IP 轮换，探测恢复自动回切。
      凭证加密存储（主密钥 FERRY_SECRET_KEY 部署侧注入，丢失不可恢复请备份）。
    </p>

    <div class="head-row">
      <h3 class="section">DNS 商凭证</h3>
      <el-button type="primary" size="small" @click="newProvider">录入凭证</el-button>
    </div>
    <el-table :data="providers" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column prop="name" label="名称" min-width="140" />
      <el-table-column prop="type" label="类型" width="120" />
      <el-table-column label="机密" width="110">
        <template #default="{ row }">
          <el-tag v-if="row.has_api_key" type="success" size="small" effect="plain">已录入</el-tag>
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
      <h3 class="section">前置记录</h3>
      <div>
        <el-button size="small" :loading="syncing" @click="syncGeo">分地域对账</el-button>
        <el-button type="primary" size="small" :disabled="providers.length === 0" @click="newFront">
          新建记录
        </el-button>
      </div>
    </div>
    <el-table :data="fronts" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column prop="name" label="说明" min-width="110" />
      <el-table-column prop="domain" label="域名" min-width="170" />
      <el-table-column label="凭证" width="110">
        <template #default="{ row }">
          {{ providers.find((p) => p.id === row.provider_id)?.name ?? row.provider_id }}
        </template>
      </el-table-column>
      <el-table-column prop="primary_ip" label="常态 IP" width="130" />
      <el-table-column label="当前指向" min-width="130">
        <template #default="{ row }">{{ row.current_ip || row.primary_ip }}</template>
      </el-table-column>
      <el-table-column label="状态" width="100">
        <template #default="{ row }">
          <el-tag :type="row.switched ? 'warning' : 'success'" size="small" effect="plain">
            {{ row.switched ? '已切离' : '常态' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="140">
        <template #default="{ row }">
          <el-button link type="primary" size="small" @click="editFront(row)">编辑</el-button>
          <el-button link type="danger" size="small" @click="removeFront(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="provDialog" :title="provEditing ? '编辑凭证' : '录入凭证'" width="440px">
      <el-form label-width="80px">
        <el-form-item label="名称">
          <el-input v-model="provForm.name" placeholder="如 cf-main" />
        </el-form-item>
        <el-form-item label="类型">
          <el-select v-model="provForm.type" style="width: 100%">
            <el-option value="cloudflare" label="cloudflare" />
          </el-select>
        </el-form-item>
        <el-form-item label="API Token">
          <el-input
            v-model="provForm.api_key" type="password" show-password
            :placeholder="provEditing ? '留空保留原 Token' : 'API Token（加密存储，不回显）'"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="provDialog = false">取消</el-button>
        <el-button type="primary" @click="saveProvider">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="frontDialog" :title="frontEditing ? '编辑记录' : '新建记录'" width="480px">
      <el-form label-width="80px">
        <el-form-item label="说明">
          <el-input v-model="frontForm.name" placeholder="如 主入口域名" />
        </el-form-item>
        <el-form-item label="域名">
          <el-input v-model="frontForm.domain" placeholder="如 edge.example.com" />
        </el-form-item>
        <el-form-item label="凭证">
          <el-select v-model="frontForm.provider_id" style="width: 100%">
            <el-option v-for="p in providers" :key="p.id" :value="p.id" :label="p.name" />
          </el-select>
        </el-form-item>
        <el-form-item label="常态 IP">
          <el-input v-model="frontForm.primary_ip" placeholder="主入口 IP（恢复回切目标）" />
        </el-form-item>
        <el-form-item label="备用 IP">
          <el-input
            v-model="frontForm.backups" type="textarea" :rows="3"
            placeholder="每行一个，被封时按顺序轮换切指"
          />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="frontDialog = false">取消</el-button>
        <el-button type="primary" @click="saveFront">保存</el-button>
      </template>
    </el-dialog>

    <div class="head-row">
      <h3 class="section">证书任务</h3>
      <el-button type="primary" size="small" :disabled="providers.length === 0" @click="newCert">
        新建任务
      </el-button>
    </div>
    <el-table :data="certTasks" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column prop="name" label="说明" min-width="110" />
      <el-table-column prop="domain" label="域名" min-width="170" />
      <el-table-column label="方式" width="90">
        <template #default="{ row }">{{ row.method }}</template>
      </el-table-column>
      <el-table-column label="状态" width="100">
        <template #default="{ row }">
          <el-tooltip :disabled="!row.last_error" :content="row.last_error" placement="top">
            <el-tag :type="CERT_STATE[row.state]?.type ?? 'info'" size="small" effect="plain">
              {{ CERT_STATE[row.state]?.text ?? row.state }}
            </el-tag>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column label="到期" min-width="150">
        <template #default="{ row }">
          <span :class="{ dim: !row.not_after }">{{ fmtDate(row.not_after) }}</span>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="190">
        <template #default="{ row }">
          <el-button
            link type="primary" size="small"
            :disabled="row.state === 'issuing'" :loading="issuing.includes(row.id)"
            @click="runIssue(row)"
          >签发</el-button>
          <el-button link type="primary" size="small" @click="editCert(row)">编辑</el-button>
          <el-button link type="danger" size="small" @click="removeCert(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="certDialog" :title="certEditing ? '编辑任务' : '新建任务'" width="480px">
      <el-form label-width="80px">
        <el-form-item label="说明">
          <el-input v-model="certForm.name" placeholder="如 主入口证书" />
        </el-form-item>
        <el-form-item label="域名">
          <el-input v-model="certForm.domain" placeholder="如 edge.example.com" />
        </el-form-item>
        <el-form-item label="附加域名">
          <el-input v-model="certForm.sans" placeholder="逗号分隔，可空" />
        </el-form-item>
        <el-form-item label="验证方式">
          <el-select v-model="certForm.method" style="width: 100%">
            <el-option value="dns-01" label="dns-01（DNS 凭证，泛域名可用）" />
            <el-option value="http-01" label="http-01（面板 webroot）" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="certForm.method === 'dns-01'" label="DNS 凭证">
          <el-select v-model="certForm.provider_id" style="width: 100%" placeholder="选择 DNS 商凭证">
            <el-option v-for="p in providers" :key="p.id" :value="p.id" :label="p.name" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="certDialog = false">取消</el-button>
        <el-button type="primary" @click="saveCert">保存</el-button>
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
</style>
