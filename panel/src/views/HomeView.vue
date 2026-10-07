<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { ApiError, get, getContact, getNotifyPrefs, getSavings, updateContact, updateNotifyPrefs } from '../api'
import type { ContactInfo, Me, SavingsSummary } from '../api'
import { auth, setUser } from '../auth'
import { formatDate, formatBytes, quotaText } from '../utils/format'

// 概览：用量进度、配额与到期、订阅链接复制、通知偏好（NT-2）；
// 「已为你省下」为 SAVE-8 本月分流直连/广告拦截的占比折算汇总；
// 联系方式引导为 TOUCH-1（TG/邮箱至少一个必填，未绑定不可关闭）。
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
})

// ---- 联系方式绑定（TOUCH-1）：TG/邮箱至少一个，未绑定引导不可关闭 ----
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
    /* 读取失败不弹引导（按已绑定处理，不打扰主流程） */
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

// 例行邮件退订只改偏好（TOUCH-1）：带上已绑项避免被「至少一项」挡回。
async function saveRoutine() {
  if (!contact.value) return
  try {
    await updateContact({
      email: contact.value.email,
      tg_chat_id: contact.value.tg_chat_id,
      routine_emails: contact.value.routine_emails,
    })
  } catch {
    /* 失败静默：下次进入按服务端值回显 */
  }
}

// 已为你省下（SAVE-8）：读取失败静默隐藏卡片，不打扰主流程。
const savings = ref<SavingsSummary | null>(null)
async function loadSavings() {
  try {
    savings.value = await getSavings()
  } catch {
    /* 保持隐藏 */
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

function subLink(target: 'v2ray' | 'clash'): string {
  const base = `${window.location.origin}/sub/${auth.token}`
  return `${base}?target=${target}`
}

async function copy(text: string, key: string) {
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    // 剪贴板不可用时退回选中复制：建临时 input 走 execCommand。
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

// ---- 通知偏好（NT-2）：到期提醒 / 流量预警与阈值，站内信通道 ----
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
    /* 偏好读取失败不打扰主流程，保持默认展示 */
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
          <div class="kv-row"><dt>剩余</dt><dd>{{ remaining }}</dd></div>
          <div class="kv-row"><dt>到期</dt><dd>{{ formatDate(me.expires_at) }}</dd></div>
          <div class="kv-row"><dt>状态</dt><dd>{{ me.active ? '正常' : '不可用' }}</dd></div>
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
