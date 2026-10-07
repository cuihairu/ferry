<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ApiError, get } from '../api'
import type { OrderRow } from '../api'
import { formatBytes, formatDate } from '../utils/format'

// 订单中心（OD-1）：兑换与购买记录列表，发放记录按单归并。
const loading = ref(false)
const error = ref('')
const orders = ref<OrderRow[]>([])

onMounted(async () => {
  loading.value = true
  try {
    orders.value = await get<OrderRow[]>('/api/panel/orders')
  } catch (e) {
    error.value = e instanceof ApiError ? e.message : '网络异常，请稍后再试'
  } finally {
    loading.value = false
  }
})

const statusText: Record<string, string> = {
  paid: '已完成',
  pending: '待付',
  failed: '失败',
  expired: '已过期',
  refunded: '已退款',
}

function grantText(g: { grant_type: string; grant_value: number }): string {
  return g.grant_type === 'add_quota'
    ? `流量 +${formatBytes(g.grant_value)}`
    : `时长 +${g.grant_value} 天`
}

function amountText(cents: number): string {
  return cents === 0 ? '—' : `¥${(cents / 100).toFixed(2)}`
}
</script>

<template>
  <div class="page">
    <p v-if="loading" class="muted">加载中…</p>
    <p v-else-if="error" class="error-text">{{ error }}</p>
    <p v-else-if="orders.length === 0" class="muted">暂无订单，兑换卡密后这里会显示记录。</p>
    <section v-for="o in orders" :key="o.order_no" class="card">
      <div class="order-head">
        <code class="mono">{{ o.order_no }}</code>
        <span class="tag" :class="o.status === 'paid' ? 'tag-ok' : o.status === 'refunded' ? 'tag-danger' : 'tag-dim'">
          {{ statusText[o.status] ?? o.status }}
        </span>
      </div>
      <dl class="kv">
        <div class="kv-row"><dt>渠道</dt><dd>{{ o.provider }}</dd></div>
        <div v-if="o.product" class="kv-row"><dt>商品</dt><dd>{{ o.product }}</dd></div>
        <div class="kv-row"><dt>金额</dt><dd>{{ amountText(o.amount_cents) }}</dd></div>
        <div v-for="(g, i) in o.grants" :key="i" class="kv-row">
          <dt>权益{{ o.grants.length > 1 ? i + 1 : '' }}</dt><dd>{{ grantText(g) }}</dd>
        </div>
        <div class="kv-row"><dt>下单</dt><dd>{{ formatDate(o.created_at) }}</dd></div>
        <div v-if="o.paid_at" class="kv-row"><dt>支付</dt><dd>{{ formatDate(o.paid_at) }}</dd></div>
        <div v-if="o.refund_at" class="kv-row"><dt>退款</dt><dd>{{ formatDate(o.refund_at) }}</dd></div>
      </dl>
    </section>
  </div>
</template>
