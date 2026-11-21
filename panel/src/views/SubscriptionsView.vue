<script setup lang="ts">
import { onMounted, ref } from 'vue'
import {
  ApiError,
  createSubscription,
  deleteSubscription,
  getSubscriptions,
  resetSubscriptionToken,
  updateSubscription,
} from '../api'
import type { SubConfig, SubFilter, SubscriptionRow } from '../api'

// 自建订阅（ferry-sub-filters）：用户按三维度过滤 + per-sub 配置定制，
// 生成独立 /sub/x/:token 地址。列表/创建/编辑/删除/重置令牌/复制链接。
const loading = ref(false)
const error = ref('')
const notice = ref('')
const copiedId = ref(0)
const subs = ref<SubscriptionRow[]>([])

// 表单态：id=null 为新建，否则编辑。
interface FormState {
  id: number | null
  name: string
  regions: string
  labels: string
  protocols: string[]
  remark_template: string
  sort: '' | 'region' | 'rtt' | 'name'
  max_nodes: number
  include_rtt: boolean
}
const editing = ref<FormState | null>(null)
const saving = ref(false)

const protocols = ['vless', 'vmess', 'trojan', 'shadowsocks', 'hysteria2']

onMounted(load)

async function load() {
  loading.value = true
  error.value = ''
  try {
    subs.value = await getSubscriptions()
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : '网络异常，请稍后再试'
  } finally {
    loading.value = false
  }
}

// ---- 解析/序列化 filter/config ----
function parseFilter(raw: string): SubFilter {
  try {
    return JSON.parse(raw || '{}') as SubFilter
  } catch {
    return {}
  }
}
function parseConfig(raw: string): SubConfig {
  try {
    return JSON.parse(raw || '{}') as SubConfig
  } catch {
    return {}
  }
}
function splitCsv(s: string): string[] {
  return [...new Set(s.split(',').map((x) => x.trim()).filter(Boolean))]
}

// ---- 表单 ----
function startCreate() {
  editing.value = {
    id: null,
    name: '',
    regions: '',
    labels: '',
    protocols: [],
    remark_template: '',
    sort: '',
    max_nodes: 0,
    include_rtt: true,
  }
}

function startEdit(s: SubscriptionRow) {
  const f = parseFilter(s.filter)
  const c = parseConfig(s.config)
  editing.value = {
    id: s.id,
    name: s.name,
    regions: (f.regions ?? []).join(', '),
    labels: (f.labels ?? []).join(', '),
    protocols: f.protocols ?? [],
    remark_template: c.remark_template ?? '',
    sort: (c.sort as FormState['sort']) ?? '',
    max_nodes: c.max_nodes ?? 0,
    include_rtt: c.include_rtt ?? true,
  }
}

function cancelEdit() {
  editing.value = null
}

function buildPayload(): { name: string; filter: SubFilter; config: SubConfig } {
  const e = editing.value!
  const filter: SubFilter = {}
  const regions = splitCsv(e.regions)
  const labels = splitCsv(e.labels)
  if (regions.length) filter.regions = regions
  if (labels.length) filter.labels = labels
  if (e.protocols.length) filter.protocols = e.protocols
  const config: SubConfig = {}
  if (e.remark_template.trim()) config.remark_template = e.remark_template.trim()
  if (e.sort) config.sort = e.sort
  if (e.max_nodes > 0) config.max_nodes = e.max_nodes
  if (!e.include_rtt) config.include_rtt = false
  return { name: e.name.trim(), filter, config }
}

async function save() {
  if (!editing.value) return
  if (!editing.value.name.trim()) {
    error.value = '请填写订阅名称'
    return
  }
  saving.value = true
  error.value = ''
  try {
    const p = buildPayload()
    if (editing.value.id === null) {
      await createSubscription(p)
      notice.value = '已创建'
    } else {
      await updateSubscription(editing.value.id, p)
      notice.value = '已保存'
    }
    editing.value = null
    setTimeout(() => (notice.value = ''), 1600)
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : '网络异常，请稍后再试'
  } finally {
    saving.value = false
  }
}

async function remove(s: SubscriptionRow) {
  if (!window.confirm(`删除订阅「${s.name}」？链接即刻失效。`)) return
  error.value = ''
  try {
    await deleteSubscription(s.id)
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : '网络异常，请稍后再试'
  }
}

async function resetToken(s: SubscriptionRow) {
  error.value = ''
  try {
    await resetSubscriptionToken(s.id)
    notice.value = '已重置令牌'
    setTimeout(() => (notice.value = ''), 1600)
    await load()
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : '网络异常，请稍后再试'
  }
}

// ---- 链接 ----
function subLink(s: SubscriptionRow): string {
  return `${window.location.origin}/sub/x/${s.token}`
}

async function copyLink(s: SubscriptionRow) {
  const text = subLink(s)
  try {
    await navigator.clipboard.writeText(text)
  } catch {
    const el = document.createElement('input')
    el.value = text
    document.body.appendChild(el)
    el.select()
    document.execCommand('copy')
    el.remove()
  }
  copiedId.value = s.id
  setTimeout(() => (copiedId.value = 0), 1600)
}

function openLink(s: SubscriptionRow) {
  window.open(subLink(s), '_blank')
}

function filterSummary(s: SubscriptionRow): string {
  const f = parseFilter(s.filter)
  const parts: string[] = []
  if (f.regions?.length) parts.push(`区域：${f.regions.join('/')}`)
  if (f.labels?.length) parts.push(`标签：${f.labels.join('/')}`)
  if (f.protocols?.length) parts.push(`协议：${f.protocols.join('/')}`)
  return parts.length ? parts.join(' · ') : '不限（全部入口）'
}

function configSummary(s: SubscriptionRow): string {
  const c = parseConfig(s.config)
  const parts: string[] = []
  if (c.sort) parts.push(`排序：${c.sort}`)
  if (c.max_nodes && c.max_nodes > 0) parts.push(`最多 ${c.max_nodes} 节点`)
  if (c.include_rtt === false) parts.push('不带延迟')
  if (c.remark_template) parts.push('自定义备注')
  return parts.length ? parts.join(' · ') : '默认'
}

function toggleProtocol(p: string) {
  const e = editing.value!
  const i = e.protocols.indexOf(p)
  if (i >= 0) e.protocols.splice(i, 1)
  else e.protocols.push(p)
}
</script>

<template>
  <div class="page">
    <div class="row" style="justify-content: space-between; margin-bottom: 12px">
      <div>
        <h2 class="page-title">订阅</h2>
        <p class="page-desc">按过滤维度生成独立订阅地址，同一账号可建多个，互不影响主订阅。</p>
      </div>
      <button v-if="!editing" class="btn btn-primary" @click="startCreate">新建订阅</button>
    </div>

    <p v-if="notice" class="muted">{{ notice }}</p>
    <p v-if="error" class="error-text">{{ error }}</p>
    <p v-if="loading" class="muted">加载中…</p>
    <p v-else-if="!error && subs.length === 0 && !editing" class="muted">还没有自建订阅，点「新建订阅」创建。</p>

    <!-- 创建/编辑表单 -->
    <section v-if="editing" class="card">
      <h2 class="card-title">{{ editing.id === null ? '新建订阅' : '编辑订阅' }}</h2>
      <div class="pref-row">
        <label class="label" for="sub-name">名称</label>
        <input id="sub-name" v-model="editing.name" class="pref-input" placeholder="如：香港专用 / 游戏机" />
      </div>
      <div class="pref-row">
        <label class="label" for="sub-regions">区域（逗号分隔）</label>
        <input id="sub-regions" v-model="editing.regions" class="pref-input" placeholder="如：香港, 日本" />
      </div>
      <div class="pref-row">
        <label class="label" for="sub-labels">标签（逗号分隔）</label>
        <input id="sub-labels" v-model="editing.labels" class="pref-input" placeholder="如：streaming, game" />
      </div>
      <div class="pref-row">
        <label class="label">协议</label>
        <label v-for="p in protocols" :key="p" class="pref-check">
          <input type="checkbox" :checked="editing.protocols.includes(p)" @change="toggleProtocol(p)" />
          {{ p }}
        </label>
      </div>
      <div class="pref-row">
        <label class="label" for="sub-remark">备注模板</label>
        <input id="sub-remark" v-model="editing.remark_template" class="pref-input" placeholder="{region} · {name} ({rtt}ms)" />
      </div>
      <div class="pref-row">
        <label class="label" for="sub-sort">排序</label>
        <select id="sub-sort" v-model="editing.sort" class="pref-select">
          <option value="">默认（区域分组）</option>
          <option value="region">按区域</option>
          <option value="rtt">按延迟</option>
          <option value="name">按名称</option>
        </select>
        <label class="label" for="sub-max" style="margin-left: 12px">最多节点数</label>
        <input id="sub-max" v-model.number="editing.max_nodes" type="number" min="0" class="pref-input" style="width: 90px" />
        <label class="pref-check">
          <input v-model="editing.include_rtt" type="checkbox" /> 备注带延迟
        </label>
      </div>
      <div class="pref-bar">
        <button class="btn btn-primary" :disabled="saving" @click="save">
          {{ saving ? '保存中…' : editing.id === null ? '创建' : '保存' }}
        </button>
        <button class="btn btn-ghost" :disabled="saving" @click="cancelEdit">取消</button>
      </div>
    </section>

    <!-- 列表 -->
    <section v-for="s in subs" :key="s.id" class="card">
      <div class="order-head">
        <span>{{ s.name }}</span>
        <span class="tag" :class="s.enabled ? 'tag-ok' : 'tag-dim'">{{ s.enabled ? '已启用' : '已停用' }}</span>
      </div>
      <dl class="kv">
        <div class="kv-row"><dt>地址</dt><dd class="mono">{{ subLink(s) }}</dd></div>
        <div class="kv-row"><dt>过滤</dt><dd>{{ filterSummary(s) }}</dd></div>
        <div class="kv-row"><dt>配置</dt><dd>{{ configSummary(s) }}</dd></div>
      </dl>
      <div class="pref-bar">
        <button class="btn btn-ghost" @click="copyLink(s)">{{ copiedId === s.id ? '已复制' : '复制链接' }}</button>
        <button class="btn btn-ghost" @click="openLink(s)">打开</button>
        <button class="btn btn-ghost" @click="startEdit(s)">编辑</button>
        <button class="btn btn-ghost" @click="resetToken(s)">重置令牌</button>
        <button class="btn btn-link" @click="remove(s)">删除</button>
      </div>
    </section>
  </div>
</template>
