<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  createDNSFront, createDNSProvider, deleteDNSFront, deleteDNSProvider,
  getDNSFronts, getDNSProviders, updateDNSFront, updateDNSProvider,
  type DNSFront, type DNSProvider,
} from '../api'

// 域名前置（BR-2）：DNS 商凭证与前置记录。
// 凭证加密落库（R24 口径）：表单不回显明文，留空表示保留原值；
// 记录常态指向 primary_ip，被封恢复 L1 切备用 IP 轮换、探测恢复回切——
// 域名不换、IP 随换。备用 IP 每行一个，提交时转 JSON 数组。

const providers = ref<DNSProvider[]>([])
const fronts = ref<DNSFront[]>([])
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    ;[providers.value, fronts.value] = await Promise.all([getDNSProviders(), getDNSFronts()])
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
      <el-button type="primary" size="small" :disabled="providers.length === 0" @click="newFront">
        新建记录
      </el-button>
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
