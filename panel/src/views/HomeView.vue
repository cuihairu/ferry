<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ApiError, get, getContact, getNotifyPrefs, getSavings, getPanelDevices, getPanelAccessLogs, getPanelPlanHistory, resetPanelToken, unbindPanelDevice, updateContact, updateNotifyPrefs } from '../api'
import type { AccessLogRow, ContactInfo, Me, PlanHistoryRow, SavingsSummary, SubDeviceRow } from '../api'
import { auth, setUser, setToken } from '../auth'
import { formatDate, formatBytes, quotaText } from '../utils/format'

// Overview: usage progress, quota and expiry, subscription link copy, notification preferences (NT-2);
// "saved for you" is the SAVE-8 summary of this month's direct/interception traffic converted to cost;
// the contact-info guidance is TOUCH-1 (at least one of TG/email required, not dismissable while unbound).
const loading = ref(false)
const error = ref('')
const copied = ref('')

onMounted(async () => {
  if (!auth.token) return
  if (!auth.user) {
    loading.value = true
    try {
      setUser(await get<Me>('/api/panel/me'))
    } catch (e) {
      error.value = e instanceof ApiError ? e.message : '网络异常，请稍后再试'
      loading.value = false
      return
    } finally {
      loading.value = false
    }
  }
  loadPrefs()
  loadSavings()
  loadContact()
  loadDevices()
  loadPlanHistory()
  loadAccessLogs()
})

// ---- Contact binding (TOUCH-1): at least one of TG/email; the guidance cannot be dismissed while unbound ----
const contact = ref<ContactInfo | null>(null)
const contactEmail = ref('')
const contactChat = ref('')
const contactSaving = ref(false)
const contactError = ref('')
const contactReady = computed(() => !!contact.value && (contact.value.email !== '' || contact.value.tg_chat_id !== ''))

async function loadContact() {
  try {
    contact.value = await getContact()
    contactEmail.value = contact.value.email
    contactChat.value = contact.value.tg_chat_id
  } catch {
    /* a failed read does not show the guidance (treated as bound, so the main flow is undisturbed) */
    contact.value = null
  }
}

async function saveContact() {
  contactSaving.value = true
  contactError.value = ''
  try {
    contact.value = await updateContact({
      email: contactEmail.value.trim(),
      tg_chat_id: contactChat.value.trim(),
    })
    contactEmail.value = contact.value.email
    contactChat.value = contact.value.tg_chat_id
  } catch (e) {
    contactError.value = e instanceof ApiError ? e.message : '网络异常，请稍后再试'
  } finally {
    contactSaving.value = false
  }
}

// Unsubscribing from routine emails only changes the preference (TOUCH-1): the already-bound items are included to avoid being bounced back by "at least one required".
async function saveRoutine() {
  if (!contact.value) return
  try {
    await updateContact({
      email: contact.value.email,
      tg_chat_id: contact.value.tg_chat_id,
      routine_emails: contact.value.routine_emails,
    })
  } catch {
    /* a failure stays silent: on the next visit the server values are shown again */
  }
}

// "Saved for you" (SAVE-8): a failed read silently hides the card without disturbing the main flow.
const savings = ref<SavingsSummary | null>(null)
async function loadSavings() {
  try {
    savings.value = await getSavings()
  } catch {
    /* stay hidden */
  }
}

const me = computed(() => auth.user)
const unlimited = computed(() => (me.value?.quota_bytes ?? 0) === 0)
const pct = computed(() => {
      const m = me.value
      if (!m || m.quota_bytes === 0) return 0
      return Math.min(100, Math.round((m.used_bytes / m.quota_bytes) * 100))
    })
const remaining = computed(() => {
      const m = me.value
      if (!m) return '—'
      if (m.quota_bytes === 0) return '不限'
      return formatBytes(Math.max(0, m.quota_bytes - m.used_bytes))
    })

function subLink(target: 'v2ray' | 'clash' | 'singbox'): string {
  const base = `${window.location.origin}/sub/${auth.token}`
  return `${base}?target=${target}`
}

async function copy(text: string, key: string) {
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    // When the clipboard is unavailable, fall back to copy-from-selection: build a temporary input and use execCommand.
    const el = document.createElement('input')
    el.value = text
    document.body.appendChild(el)
    el.select()
    document.execCommand('copy')
    el.remove()
  }
  copied.value = key
  setTimeout(() => (copied.value = ''), 1600)
}

// ---- Notification preferences (NT-2): expiry reminders / traffic warning and threshold, in-app channel ----
const prefExpiry = ref(true)
const prefTraffic = ref(true)
const prefPercent = ref(80)
const prefSaving = ref(false)
const prefSaved = ref(false)
const prefError = ref('')

const percentOptions = [50, 60, 70, 80, 90, 95]

async function loadPrefs() {
  try {
    const p = await getNotifyPrefs()
    prefExpiry.value = p.notify_expiry
    prefTraffic.value = p.notify_traffic
    prefPercent.value = p.traffic_warn_percent
  } catch {
    /* a failed preference read does not disturb the main flow; keep the default display */
  }
}

async function savePrefs() {
  prefSaving.value = true
  prefError.value = ''
  try {
    await updateNotifyPrefs({
      notify_expiry: prefExpiry.value,
      notify_traffic: prefTraffic.value,
      traffic_warn_percent: prefPercent.value,
    })
    prefSaved.value = true
    setTimeout(() => (prefSaved.value = false), 1600)
  } catch (e) {
    prefError.value = e instanceof ApiError ? e.message : '网络异常，请稍后再试'
  } finally {
    prefSaving.value = false
  }
}

// ---- Devices (F-1505/F-1506): hwid rows are the registered devices, unbind revokes one;
// ip rows are just the 24h-window observations and expire on their own. A failed read
// hides the card without disturbing the main flow, same stance as savings. ----
const devices = ref<SubDeviceRow[]>([])
const unbinding = ref(0)
const hwDevices = computed(() => devices.value.filter((d) => d.kind === 'hwid'))
const ipCount = computed(() => devices.value.filter((d) => d.kind === 'ip').length)

async function loadDevices() {
  try {
    devices.value = await getPanelDevices()
  } catch {
    devices.value = []
  }
}

async function unbind(d: SubDeviceRow) {
  if (unbinding.value !== 0) return
  if (!window.confirm(`解绑设备 ${d.key.slice(0, 12)}？解绑后该设备下次拉订阅需重新计入设备数。`)) return
  unbinding.value = d.id
  try {
    await unbindPanelDevice(d.id)
    await loadDevices()
  } catch (e) {
    window.alert(e instanceof ApiError ? e.message : '解绑失败，请稍后再试')
  } finally {
    unbinding.value = 0
  }
}

// ---- Subscription token reset (F-1508): the token IS the credential, so this is the
// panel's "change password" — two-step confirm, then the session swaps to the new token. ----
const resetArmed = ref(false)
const resetBusy = ref(false)
const resetError = ref('')
const freshToken = ref('')

async function doResetToken() {
  if (resetBusy.value) return
  if (!resetArmed.value) {
    resetArmed.value = true
    return
  }
  resetBusy.value = true
  resetError.value = ''
  try {
    const out = await resetPanelToken()
    setToken(out.sub_token)
    freshToken.value = out.sub_token
    setUser(await get<Me>('/api/panel/me'))
    resetArmed.value = false
  } catch (e) {
    resetError.value = e instanceof ApiError ? e.message : '网络异常，请稍后再试'
  } finally {
    resetBusy.value = false
  }
}

// ---- Plan history (F-1514) and subscription access records (F-1516): both fail soft. ----
const planHistory = ref<PlanHistoryRow[]>([])
async function loadPlanHistory() {
  try {
    planHistory.value = await getPanelPlanHistory()
  } catch {
    planHistory.value = []
  }
}

const accessLogs = ref<AccessLogRow[]>([])
async function loadAccessLogs() {
  try {
    accessLogs.value = await getPanelAccessLogs()
  } catch {
    accessLogs.value = []
  }
}
</script>

<template>
  <div class="page">
    <p v-if="loading" class="muted">加载中…</p>
    <p v-else-if="error" class="error-text">{{ error }}</p>
    <template v-else-if="me">
      <div v-if="!me.active" class="alert">
        当前账号不可用（已停用 / 已到期 / 已超配额），订阅链接不会返回节点。如需继续使用请兑换卡密。
      </div>

      <section v-if="contact && (!contactReady || contact.stale)" class="card">
        <h2 class="card-title">{{ contact.stale ? '联系方式已失效，请换绑' : '完善联系方式' }}</h2>
        <p class="muted">
          {{ contact.stale
            ? '此前投递失败（邮箱退信或 TG 送达失败），通知已暂停，换绑后自动恢复。'
            : 'Telegram 或邮箱至少绑定一个，用于账号通知与断联时找回入口。' }}
        </p>
        <div class="pref-row">
          <input v-model="contactEmail" class="pref-input" placeholder="邮箱（选填）" type="email" />
        </div>
        <div class="pref-row">
          <input v-model="contactChat" class="pref-input" placeholder="Telegram chat_id（选填）" />
        </div>
        <div class="pref-bar">
          <button class="btn btn-ghost" :disabled="contactSaving" @click="saveContact">
            {{ contactSaving ? '保存中…' : '保存' }}
          </button>
          <span v-if="contactError" class="error-text">{{ contactError }}</span>
        </div>
      </section>

      <section class="card">
        <h2 class="card-title">用量</h2>
        <div class="usage-row">
          <span class="usage-num">{{ formatBytes(me.used_bytes) }}</span>
          <span class="muted">已用 / {{ quotaText(me.quota_bytes) }}</span>
        </div>
        <div v-if="!unlimited" class="meter">
          <i :class="{ warn: pct >= 70, danger: pct >= 95 }" :style="{ width: pct + '%' }"></i>
        </div>
        <dl class="kv">
          <div class="kv-row"><dt>套餐</dt><dd>{{ me.plan_name || '—' }}</dd></div>
          <div class="kv-row"><dt>剩余</dt><dd>{{ remaining }}</dd></div>
          <div class="kv-row"><dt>到期</dt><dd>{{ formatDate(me.expires_at) }}</dd></div>
          <div class="kv-row"><dt>状态</dt><dd>{{ me.active ? '正常' : '不可用' }}</dd></div>
          <div class="kv-row"><dt>设备</dt><dd>{{ me.devices }} / {{ me.device_limit > 0 ? me.device_limit : '不限' }}</dd></div>
          <div class="kv-row"><dt>IP（24h）</dt><dd>{{ me.ips }} / {{ me.ip_limit > 0 ? me.ip_limit : '不限' }}</dd></div>
        </dl>
      </section>

      <section v-if="savings" class="card">
        <h2 class="card-title">已为你省下（本月）</h2>
        <p class="muted">分流直连与广告拦截为你省下的流量，按各节点用量占比折算。</p>
        <dl class="kv">
          <div class="kv-row"><dt>直连分流</dt><dd>{{ formatBytes(savings.direct_bytes) }}</dd></div>
          <div class="kv-row"><dt>广告拦截</dt><dd>{{ formatBytes(savings.blocked_bytes) }}</dd></div>
          <div v-if="savings.cache_hit_bytes > 0" class="kv-row"><dt>缓存命中</dt><dd>{{ formatBytes(savings.cache_hit_bytes) }}</dd></div>
        </dl>
      </section>

      <section class="card">
        <h2 class="card-title">订阅链接</h2>
        <p class="muted">按客户端类型复制对应链接；多数客户端粘贴通用链接可自动识别。</p>
        <div class="link-block">
          <div class="link-label">通用（v2ray / base64）</div>
          <div class="link-row">
            <code class="link-text">{{ subLink('v2ray') }}</code>
            <button class="btn btn-ghost" @click="copy(subLink('v2ray'), 'v2ray')">
              {{ copied === 'v2ray' ? '已复制' : '复制' }}
            </button>
          </div>
        </div>
        <div class="link-block">
          <div class="link-label">clash / mihomo（YAML）</div>
          <div class="link-row">
            <code class="link-text">{{ subLink('clash') }}</code>
            <button class="btn btn-ghost" @click="copy(subLink('clash'), 'clash')">
              {{ copied === 'clash' ? '已复制' : '复制' }}
            </button>
          </div>
          <p class="muted" style="margin: 6px 0 0; font-size: 12px">
            导入：Clash Verge / Mihomo Party → 「订阅」→ 新建配置，粘贴本链接并更新；手机端 ClashMeta for Android / Stash（iOS）选「从 URL 添加」。
          </p>
        </div>
        <div class="link-block">
          <div class="link-label">sing-box / Stash（JSON）</div>
          <div class="link-row">
            <code class="link-text">{{ subLink('singbox') }}</code>
            <button class="btn btn-ghost" @click="copy(subLink('singbox'), 'singbox')">
              {{ copied === 'singbox' ? '已复制' : '复制' }}
            </button>
          </div>
          <p class="muted" style="margin: 6px 0 0; font-size: 12px">
            导入：sing-box → 「配置」→「新建」→「从 URL 导入」，粘贴本链接；SFM / Stash（iOS）在首页「+」选「订阅」粘贴链接后拉取。
          </p>
        </div>
      </section>

      <section v-if="planHistory.length" class="card">
        <h2 class="card-title">套餐变更历史</h2>
        <p class="muted">套餐应用记录（管理员操作留痕）；套餐变更即复制限额生效，历史仅留档。</p>
        <div v-for="(h, i) in planHistory" :key="i" class="list-row">
          <div class="list-main">
            <div class="list-title">{{ h.plan_name || '（套餐已删除）' }}</div>
            <div class="list-sub">{{ formatDate(h.applied_at) }} · 操作者 {{ h.by || '—' }}</div>
          </div>
        </div>
      </section>

      <section class="card">
        <h2 class="card-title">设备</h2>
        <p class="muted">订阅客户端按设备上报（hwid）；24 小时内独立 IP {{ ipCount }} 个。</p>
        <template v-if="hwDevices.length">
          <div v-for="d in hwDevices" :key="d.id" class="list-row">
            <div class="list-main">
              <div class="list-title">{{ d.key.slice(0, 8) }}…</div>
              <div class="list-sub">
                {{ d.user_agent || '未知客户端' }} · 首次 {{ formatDate(d.first_seen) }} · 最近 {{ formatDate(d.last_seen) }}
              </div>
            </div>
            <button class="btn btn-ghost" :disabled="unbinding !== 0" @click="unbind(d)">
              {{ unbinding === d.id ? '解绑中…' : '解绑' }}
            </button>
          </div>
        </template>
        <p v-else class="muted">暂无设备记录（客户端未上报设备标识）。</p>
      </section>

      <section class="card">
        <h2 class="card-title">安全设置</h2>
        <p class="muted">
          订阅令牌即身份凭证，等同于密码；重置后旧令牌立即失效，已复制的订阅链接需换用下方新令牌重新复制。
        </p>
        <div class="pref-bar">
          <button
            class="btn"
            :class="resetArmed ? 'btn-primary' : 'btn-ghost'"
            :disabled="resetBusy"
            @click="doResetToken"
          >
            {{ resetBusy ? '重置中…' : resetArmed ? '再次点击确认重置' : '重置订阅令牌' }}
          </button>
          <span v-if="resetError" class="error-text">{{ resetError }}</span>
        </div>
        <div v-if="freshToken" class="token-reveal">新令牌：{{ freshToken }}（请立即复制保存，不再完整显示）</div>
      </section>

      <section v-if="accessLogs.length" class="card">
        <h2 class="card-title">订阅访问记录（近 {{ accessLogs.length }} 条）</h2>
        <p class="muted">每次拉取订阅都会留时间/IP/响应码记录。</p>
        <div v-for="l in accessLogs" :key="l.id" class="list-row">
          <div class="list-main">
            <div class="list-title">{{ formatDate(l.created_at) }}</div>
            <div class="list-sub">{{ l.ip || '—' }} · {{ l.ua || '未知客户端' }} · 响应 {{ l.code }}</div>
          </div>
        </div>
      </section>

      <section class="card">
        <h2 class="card-title">通知偏好</h2>
        <p class="muted">站内信提醒（通知页查看）；公告与到账通知不在此列，始终送达。</p>
        <div class="pref-row">
          <label class="pref-check"><input v-model="prefExpiry" type="checkbox" /> 到期提醒（到期前 7 天起每日一条）</label>
        </div>
        <div class="pref-row">
          <label class="pref-check"><input v-model="prefTraffic" type="checkbox" /> 流量预警</label>
          <select v-model="prefPercent" class="pref-select" :disabled="!prefTraffic">
            <option v-for="p in percentOptions" :key="p" :value="p">用量达 {{ p }}% 时提醒</option>
          </select>
        </div>
        <div v-if="contact" class="pref-row">
          <label class="pref-check">
            <input v-model="contact.routine_emails" type="checkbox" :disabled="!contactReady" @change="saveRoutine" /> 域名例行邮件（月账单必收，不在此列）
          </label>
        </div>
        <div class="pref-bar">
          <button class="btn btn-ghost" :disabled="prefSaving" @click="savePrefs">
            {{ prefSaving ? '保存中…' : prefSaved ? '已保存' : '保存' }}
          </button>
          <span v-if="prefError" class="error-text">{{ prefError }}</span>
        </div>
      </section>
    </template>
  </div>
</template>
