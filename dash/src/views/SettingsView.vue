<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import {
  get,
  put,
  type UserTemplate,
  type EntryDomain,
  listEntryDomains,
  createEntryDomain,
  updateEntryDomain,
  deleteEntryDomain,
  getOutage,
  putOutage,
} from '../api'
import { GB } from '../utils/format'

// P1-5：默认用户模板——新建用户可套用的默认配额/时长/重置周期。
// TOUCH-7：断联容灾——断联态开关与入口域名维护（订阅注释的备用信息数据源）。

const cycleOptions = [
  { value: 'none', label: '不限' },
  { value: 'day', label: '每日' },
  { value: 'week', label: '每周' },
  { value: 'month', label: '每月' },
]
const form = reactive({ quotaGb: 0, expireDays: 0, resetCycle: 'none' })
const saving = ref(false)

async function load() {
  try {
    const t = await get<UserTemplate>('/api/user-template')
    form.quotaGb = t.quota_bytes / GB
    form.expireDays = t.expire_days
    form.resetCycle = t.reset_cycle || 'none'
  } catch (e) {
    ElMessage.error(String(e))
  }
}
onMounted(load)

async function save() {
  saving.value = true
  try {
    await put('/api/user-template', {
      quota_bytes: Math.round(form.quotaGb * GB),
      expire_days: form.expireDays,
      reset_cycle: form.resetCycle,
    })
    ElMessage.success('已保存')
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    saving.value = false
  }
}

// ---- 断联容灾（TOUCH-7）----

const outage = ref(false)
const domains = ref<EntryDomain[]>([])
const domainsLoading = ref(false)
const newDomain = reactive({ domain: '', role: 'backup', region: '' })
const adding = ref(false)

async function loadDisaster() {
  try {
    outage.value = (await getOutage()).enabled
    domainsLoading.value = true
    domains.value = await listEntryDomains()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    domainsLoading.value = false
  }
}
onMounted(loadDisaster)

async function toggleOutage(v: boolean) {
  try {
    outage.value = (await putOutage(v)).enabled
    ElMessage.success(v ? '已标记断联态，订阅注释将带警告行' : '已恢复常态')
  } catch (e) {
    outage.value = !v
    ElMessage.error(String(e))
  }
}

async function addDomain() {
  if (!newDomain.domain.trim()) {
    ElMessage.warning('请填写域名')
    return
  }
  adding.value = true
  try {
    await createEntryDomain({ domain: newDomain.domain.trim(), role: newDomain.role, region: newDomain.region.trim() || undefined })
    newDomain.domain = ''
    newDomain.role = 'backup'
    newDomain.region = ''
    domains.value = await listEntryDomains()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    adding.value = false
  }
}

async function toggleDomain(row: EntryDomain) {
  try {
    await updateEntryDomain(row.id, { enabled: row.enabled })
  } catch (e) {
    row.enabled = !row.enabled
    ElMessage.error(String(e))
  }
}

async function removeDomain(row: EntryDomain) {
  try {
    await deleteEntryDomain(row.id)
    domains.value = await listEntryDomains()
  } catch (e) {
    ElMessage.error(String(e))
  }
}
</script>

<template>
  <div class="page">
    <h2>设置</h2>
    <p class="page-desc">面板运营配置。</p>

    <h3>默认用户模板</h3>
    <p class="page-desc">新建用户时可一键套用的默认配额与时长。</p>
    <el-form label-width="110px" style="max-width: 420px">
      <el-form-item label="默认配额 (GiB)">
        <el-input-number v-model="form.quotaGb" :min="0" :step="10" />
        <span class="form-hint">0 表示不限</span>
      </el-form-item>
      <el-form-item label="默认时长 (天)">
        <el-input-number v-model="form.expireDays" :min="0" :step="30" />
        <span class="form-hint">0 表示不限期</span>
      </el-form-item>
      <el-form-item label="重置周期">
        <el-select v-model="form.resetCycle" style="width: 160px">
          <el-option v-for="o in cycleOptions" :key="o.value" :label="o.label" :value="o.value" />
        </el-select>
      </el-form-item>
      <el-form-item>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </el-form-item>
    </el-form>

    <h3>断联容灾</h3>
    <p class="page-desc">
      面板域名被封或不可达时的逃生通道：订阅文本常附备用公告地址与下方启用的域名清单（客户端缓存里自带）。
      断联态为人工标记——面板自身域名无探测面，确认不可达时打开，恢复后关闭。
    </p>
    <div style="margin-bottom: 12px">
      <el-switch :model-value="outage" @change="toggleOutage" active-text="断联态" />
      <span class="form-hint">打开后订阅注释追加断联警告；推新入口走公告扇出（站内信 + Herald 分发）</span>
    </div>
    <el-table :data="domains" v-loading="domainsLoading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }" style="max-width: 760px">
      <el-table-column prop="domain" label="域名" min-width="200" />
      <el-table-column label="角色" width="100">
        <template #default="{ row }">
          <el-tag :type="row.role === 'primary' ? 'primary' : 'info'" size="small" effect="plain">
            {{ row.role === 'primary' ? '主入口' : '备用' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="region" label="区域" width="110">
        <template #default="{ row }">{{ row.region || '全区域' }}</template>
      </el-table-column>
      <el-table-column label="启用" width="90">
        <template #default="{ row }">
          <el-switch v-model="row.enabled" size="small" @change="toggleDomain(row)" />
        </template>
      </el-table-column>
      <el-table-column label="" width="80">
        <template #default="{ row }">
          <el-button link type="danger" size="small" @click="removeDomain(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
    <div style="margin-top: 12px; display: flex; gap: 8px; align-items: center">
      <el-input v-model="newDomain.domain" placeholder="入口域名（自动剥离协议前缀）" style="width: 260px" @keyup.enter="addDomain" />
      <el-select v-model="newDomain.role" style="width: 110px">
        <el-option label="主入口" value="primary" />
        <el-option label="备用" value="backup" />
      </el-select>
      <el-input v-model="newDomain.region" placeholder="区域（空=全区域）" style="width: 160px" @keyup.enter="addDomain" />
      <el-button type="primary" :loading="adding" @click="addDomain">添加</el-button>
    </div>
  </div>
</template>

<style scoped>
h3 {
  margin: 24px 0 4px;
}
.form-hint {
  margin-left: 10px;
  color: var(--ferry-text-muted);
  font-size: 12px;
}
</style>
