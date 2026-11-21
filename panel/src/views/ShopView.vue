<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  ApiError,
  createPanelOrder,
  getPanelChannels,
  getPanelOrderStatus,
  getPanelProducts,
} from '../api'
import type { CreateOrderResult, OrderStatus, PanelChannel, PanelProduct } from '../api'
import { formatBytes } from '../utils/format'

// 购买页（SHOP-1）：套餐卡片 + 渠道选择（按注册态置灰）+ 优惠码，
// 下单后跳收银台并轮询订单状态，paid 即展示权益到账。
const loading = ref(false)
const error = ref('')
const products = ref<PanelProduct[]>([])
const channels = ref<PanelChannel[]>([])

const channelNames: Record<string, string> = {
  epusdt: 'USDT（epusdt）',
  wechat: '微信支付',
  alipay: '支付宝',
}

async function load() {
  loading.value = true
  try {
    const [ps, cs] = await Promise.all([getPanelProducts(), getPanelChannels()])
    products.value = ps
    channels.value = cs
    // 缺省选中第一个可用渠道
    const first = cs.find((c) => c.enabled)
    if (first) selectedChannel.value = first.name
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : '网络异常，请稍后再试'
  } finally {
    loading.value = false
  }
}
onMounted(load)

// ---- 选购态 ----
const selectedProduct = ref<number>(0)
const selectedChannel = ref('')
const couponCode = ref('')
const placing = ref(false)
const placeError = ref('')

const chosen = computed(() => products.value.find((p) => p.id === selectedProduct.value) ?? null)

function grantText(p: PanelProduct): string {
  return p.grant_type === 'add_quota' ? `流量 +${formatBytes(p.grant_value)}` : `时长 +${p.grant_value} 天`
}

function amountText(cents: number): string {
  return `¥${(cents / 100).toFixed(2)}`
}

// ---- 下单与支付轮询 ----
const order = ref<CreateOrderResult | null>(null)
const orderStatus = ref<OrderStatus | null>(null)
let pollTimer: ReturnType<typeof setInterval> | null = null

async function placeOrder() {
  if (!chosen.value || !selectedChannel.value || placing.value) return
  placing.value = true
  placeError.value = ''
  try {
    order.value = await createPanelOrder({
      batch_id: chosen.value.id,
      provider: selectedChannel.value,
      coupon_code: couponCode.value.trim() || undefined,
    })
    orderStatus.value = null
    startPolling()
  } catch (e) {
    placeError.value = e instanceof ApiError ? e.message : '网络异常，请稍后再试'
  } finally {
    placing.value = false
  }
}

function startPolling() {
  stopPolling()
  pollTimer = setInterval(async () => {
    if (!order.value) return stopPolling()
    try {
      orderStatus.value = await getPanelOrderStatus(order.value.order_no)
      if (orderStatus.value.status !== 'pending') stopPolling()
    } catch {
      /* 轮询失败不打断：下个周期再试 */
    }
  }, 3000)
}

function stopPolling() {
  if (pollTimer) clearInterval(pollTimer)
  pollTimer = null
}

onBeforeUnmount(stopPolling)

function goPay() {
  if (order.value?.pay_url) window.open(order.value.pay_url, '_blank', 'noopener')
}

// 轮询期间手动检查一次（收银台回来后点它立即刷新）。
async function checkNow() {
  if (!order.value) return
  try {
    orderStatus.value = await getPanelOrderStatus(order.value.order_no)
    if (orderStatus.value.status !== 'pending') stopPolling()
  } catch {
    /* 保持现状 */
  }
}

function resetOrder() {
  stopPolling()
  order.value = null
  orderStatus.value = null
  couponCode.value = ''
}
</script>

<template>
  <div class="page">
    <p v-if="loading" class="muted">加载中…</p>
    <p v-else-if="error" class="error-text">{{ error }}</p>
    <p v-else-if="products.length === 0" class="muted">暂无可售套餐。</p>
    <template v-else>
      <!-- 支付进行中/完成面板：盖在选购区之上，paid 或过期后可返回继续买 -->
      <section v-if="order" class="card">
        <h2 class="card-title">订单支付</h2>
        <dl class="kv">
          <div class="kv-row"><dt>订单号</dt><dd><code class="mono">{{ order.order_no }}</code></dd></div>
          <div class="kv-row"><dt>商品</dt><dd>{{ order.product }}</dd></div>
          <div class="kv-row"><dt>应付</dt><dd>{{ amountText(order.amount_cents) }}</dd></div>
          <div class="kv-row">
            <dt>状态</dt>
            <dd>
              <span v-if="!orderStatus || orderStatus.status === 'pending'" class="tag tag-warn">待支付</span>
              <span v-else-if="orderStatus.status === 'paid'" class="tag tag-ok">已完成</span>
              <span v-else class="tag tag-danger">{{ orderStatus.status }}</span>
            </dd>
          </div>
          <template v-if="orderStatus && orderStatus.status === 'paid'">
            <div v-for="(g, i) in orderStatus.grants" :key="i" class="kv-row">
              <dt>权益{{ orderStatus.grants.length > 1 ? i + 1 : '' }}</dt>
              <dd>{{ g.grant_type === 'add_quota' ? `流量 +${formatBytes(g.grant_value)}` : `时长 +${g.grant_value} 天` }}</dd>
            </div>
          </template>
        </dl>
        <p v-if="!orderStatus || orderStatus.status === 'pending'" class="muted">
          支付完成后本页自动刷新；也可在收银台付款后回来点「我已支付」。
        </p>
        <p v-else-if="orderStatus.status === 'paid'" class="muted">权益已到账，可在概览页查看用量变化。</p>
        <div class="shop-actions">
          <button v-if="order.pay_url && (!orderStatus || orderStatus.status === 'pending')" class="btn btn-primary" @click="goPay">去支付</button>
          <button v-if="!orderStatus || orderStatus.status === 'pending'" class="btn btn-ghost" @click="checkNow">我已支付</button>
          <button class="btn btn-ghost" @click="resetOrder">{{ orderStatus && orderStatus.status !== 'pending' ? '继续购买' : '取消订单' }}</button>
        </div>
      </section>

      <template v-else>
        <section class="card">
          <h2 class="card-title">选择套餐</h2>
          <div
            v-for="p in products"
            :key="p.id"
            class="product-row"
            :class="{ selected: selectedProduct === p.id }"
            @click="selectedProduct = p.id"
          >
            <label class="pref-check product-pick">
              <input v-model="selectedProduct" type="radio" :value="p.id" name="product" />
              <span class="product-name">{{ p.name }}</span>
            </label>
            <dl class="kv product-kv">
              <div class="kv-row"><dt>权益</dt><dd>{{ grantText(p) }}</dd></div>
              <div class="kv-row"><dt>剩余</dt><dd>{{ p.remaining }} 份</dd></div>
              <div class="kv-row"><dt>价格</dt><dd class="product-price">{{ amountText(p.price_cents) }}</dd></div>
            </dl>
          </div>
        </section>

        <section class="card">
          <h2 class="card-title">支付方式</h2>
          <div class="channel-row">
            <template v-for="ch in channels" :key="ch.name">
              <label v-if="ch.enabled" class="channel-item" :class="{ active: selectedChannel === ch.name }">
                <input v-model="selectedChannel" type="radio" :value="ch.name" name="channel" />
                {{ channelNames[ch.name] ?? ch.name }}
              </label>
              <span v-else class="channel-item channel-off" :title="'未启用'">
                {{ channelNames[ch.name] ?? ch.name }}（未启用）
              </span>
            </template>
          </div>
          <div class="pref-row">
            <input v-model="couponCode" class="pref-input coupon-input" placeholder="优惠码（选填）" />
          </div>
          <div class="shop-actions">
            <button class="btn btn-primary" :disabled="!selectedProduct || !selectedChannel || placing" @click="placeOrder">
              {{ placing ? '下单中…' : chosen ? `立即购买 · ${amountText(chosen.price_cents)}` : '立即购买' }}
            </button>
            <span v-if="placeError" class="error-text">{{ placeError }}</span>
          </div>
        </section>
      </template>
    </template>
  </div>
</template>

<style scoped>
.product-row {
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 12px 14px;
  border: 1px solid var(--ferry-border);
  border-radius: 8px;
  margin-bottom: 10px;
  cursor: pointer;
}
.product-row:hover { border-color: var(--ferry-accent); }
.product-row.selected { border-color: var(--ferry-accent); }
.product-pick { display: flex; align-items: center; gap: 8px; }
.product-name { font-weight: 600; white-space: nowrap; }
.product-kv { flex: 1; margin: 0; }
.channel-row { display: flex; flex-wrap: wrap; gap: 10px; margin-bottom: 14px; }
.channel-item {
  display: inline-flex; align-items: center; gap: 8px;
  padding: 8px 14px;
  border: 1px solid var(--ferry-border);
  border-radius: 8px;
  cursor: pointer;
}
.channel-item.active { border-color: var(--ferry-accent); }
.channel-off { opacity: 0.45; cursor: not-allowed; }
.coupon-input { max-width: 260px; }
.shop-actions { display: flex; align-items: center; gap: 12px; margin-top: 8px; }
</style>
