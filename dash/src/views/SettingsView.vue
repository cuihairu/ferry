<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import QRCode from 'qrcode'
import {
  get,
  put,
  type UserTemplate,
  type EntryDomain,
  type LoginLogRow,
  listEntryDomains,
  createEntryDomain,
  updateEntryDomain,
  deleteEntryDomain,
  getOutage,
  putOutage,
  getTwoFAStatus,
  setupTwoFA,
  enableTwoFA,
  disableTwoFA,
  getLoginLogs,
} from '../api'
import { GB, formatDate } from '../utils/format'

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

// ---- 两步验证（安全设计 §1）----

// 绑定两步走 setup→enable：setup 只把密钥加密暂存并回二维码/密钥串，enable
// 校验一次 TOTP 才落库并回一次性恢复码——二维码没扫成不留半绑定态。
const twoFA = reactive({
  enabled: false,
  setup: false,
  secret: '',
  otpauthUrl: '',
  qr: '',
  code: '',
  disablePwd: '',
  confirming: false,
})
const recoveryCodes = ref<string[]>([])

async function loadTwoFA() {
  try {
    twoFA.enabled = (await getTwoFAStatus()).enabled
  } catch (e) {
    ElMessage.error(String(e))
  }
}
onMounted(loadTwoFA)

async function startBind() {
  try {
    const r = await setupTwoFA()
    twoFA.secret = r.secret
    twoFA.otpauthUrl = r.otpauth_url
    twoFA.qr = await QRCode.toDataURL(r.otpauth_url, { width: 200, margin: 1 })
    twoFA.setup = true
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  }
}

function cancelBind() {
  twoFA.setup = false
  twoFA.code = ''
}

async function confirmBind() {
  if (!twoFA.code.trim()) {
    ElMessage.warning('请输入验证器上的 6 位数字')
    return
  }
  twoFA.confirming = true
  try {
    const r = await enableTwoFA(twoFA.code.trim())
    recoveryCodes.value = r.recovery_codes
    twoFA.enabled = true
    twoFA.setup = false
    twoFA.code = ''
    ElMessage.success('两步验证已开启，下次登录需验证码')
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  } finally {
    twoFA.confirming = false
  }
}

async function doDisable() {
  if (!twoFA.disablePwd) {
    ElMessage.warning('请输入登录密码确认')
    return
  }
  try {
    await disableTwoFA(twoFA.disablePwd)
    twoFA.enabled = false
    twoFA.disablePwd = ''
    recoveryCodes.value = []
    ElMessage.success('已关闭两步验证')
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  }
}

async function copyRecoveryCodes() {
  try {
    await navigator.clipboard.writeText(recoveryCodes.value.join('\n'))
    ElMessage.success('已复制')
  } catch {
    ElMessage.warning('复制失败，请手动选择文本复制')
  }
}

// ---- 登录日志（安全设计 §1）----

const logs = reactive({ items: [] as LoginLogRow[], total: 0, page: 1, pageSize: 20, loading: false })

async function loadLogs() {
  logs.loading = true
  try {
    const r = await getLoginLogs(logs.page, logs.pageSize)
    logs.items = r.items
    logs.total = r.total
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    logs.loading = false
  }
}
onMounted(loadLogs)
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

    <h3>两步验证</h3>
    <p class="page-desc">
      登录二次校验（安全设计 §1）：绑定后登录需验证器 6 位验证码，恢复码一次性备用；
      未绑定者登录行为不变。绑定需面板已配置主密钥 FERRY_SECRET_KEY。
    </p>

    <!-- 绑定中：二维码 + 密钥串 + 校验码确认 -->
    <div v-if="twoFA.setup" class="twofa-setup">
      <div class="twofa-cols">
        <img v-if="twoFA.qr" :src="twoFA.qr" alt="TOTP 二维码" class="twofa-qr" />
        <div class="twofa-secret">
          <p class="twofa-tip">用验证器 App（如 Google Authenticator、1Password）扫码，或手动录入密钥：</p>
          <el-text class="secret-str" tag="code">{{ twoFA.secret }}</el-text>
          <div class="twofa-row">
            <el-input
              v-model="twoFA.code" placeholder="输入 6 位验证码确认" style="width: 200px"
              maxlength="6" @keyup.enter="confirmBind"
            />
            <el-button type="primary" :loading="twoFA.confirming" @click="confirmBind">确认开启</el-button>
            <el-button @click="cancelBind">取消</el-button>
          </div>
        </div>
      </div>
    </div>

    <!-- 已绑定：关闭入口 -->
    <div v-else-if="twoFA.enabled" class="twofa-row">
      <el-tag type="success" effect="plain">已开启</el-tag>
      <el-input
        v-model="twoFA.disablePwd" type="password" show-password placeholder="登录密码确认"
        style="width: 220px"
      />
      <el-button type="danger" plain @click="doDisable">关闭两步验证</el-button>
    </div>

    <!-- 未绑定 -->
    <div v-else>
      <el-button type="primary" @click="startBind">绑定两步验证</el-button>
    </div>

    <!-- 恢复码一次性展示（明文只此一次，落库只存哈希） -->
    <el-alert v-if="recoveryCodes.length" type="warning" :closable="false" class="recovery">
      <template #title>
        恢复码只显示这一次，请立即保存——每个只能用一次（登录时输在验证码框即可），用一个销一个。
      </template>
      <div class="codes">
        <code v-for="c in recoveryCodes" :key="c" class="code-chip">{{ c }}</code>
      </div>
      <el-button size="small" @click="copyRecoveryCodes">复制全部</el-button>
    </el-alert>

    <h3>登录日志</h3>
    <p class="page-desc">
      每次登录尝试留痕（时间/IP/UA/结果）；连续失败达阈值与异网段成功登录会经告警通道（Herald，
      未配置时站内事件）提示。
    </p>
    <el-table
      :data="logs.items" v-loading="logs.loading"
      :header-cell-style="{ background: 'var(--ferry-bg-panel)' }" style="max-width: 980px"
    >
      <el-table-column label="时间" width="150">
        <template #default="{ row }">{{ formatDate(row.created_at) }}</template>
      </el-table-column>
      <el-table-column prop="username" label="账号" width="110" />
      <el-table-column prop="ip" label="IP" width="140" />
      <el-table-column prop="ua" label="User-Agent" min-width="220" show-overflow-tooltip />
      <el-table-column label="结果" width="80">
        <template #default="{ row }">
          <el-tag :type="row.ok ? 'success' : 'danger'" size="small" effect="plain">
            {{ row.ok ? '成功' : '失败' }}
          </el-tag>
        </template>
      </el-table-column>
    </el-table>
    <el-pagination
      v-model:current-page="logs.page" :page-size="logs.pageSize" :total="logs.total"
      layout="total, prev, pager, next" style="margin-top: 12px" @current-change="loadLogs"
    />
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
.twofa-cols {
  display: flex;
  gap: 20px;
  align-items: flex-start;
}
.twofa-qr {
  width: 200px;
  height: 200px;
  border-radius: 8px;
  background: #fff;
  padding: 6px;
}
.twofa-tip {
  margin: 0 0 6px;
  color: var(--ferry-text-dim);
  font-size: 13px;
}
.secret-str {
  display: inline-block;
  margin-bottom: 12px;
  padding: 4px 8px;
  background: var(--ferry-bg-hover);
  border-radius: 6px;
  font-family: monospace;
  user-select: all;
}
.twofa-row {
  display: flex;
  gap: 10px;
  align-items: center;
  margin-top: 10px;
}
.recovery {
  max-width: 640px;
  margin-top: 14px;
}
.codes {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  margin: 8px 0 10px;
}
.code-chip {
  padding: 2px 8px;
  background: var(--ferry-bg-hover);
  border-radius: 6px;
  font-family: monospace;
  user-select: all;
}
</style>
