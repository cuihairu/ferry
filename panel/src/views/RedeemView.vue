<script setup lang="ts">
import { ref } from 'vue'
import { ApiError, post } from '../api'
import type { RedeemResult } from '../api'
import { formatBytes, formatDate } from '../utils/format'

// 兑换：输入卡密 → 成功展示权益回执；失败统一文案（防枚举，见 PAY-5）。
const code = ref('')
const loading = ref(false)
const error = ref('')
const result = ref<RedeemResult | null>(null)

async function submit() {
  error.value = ''
  result.value = null
  if (!code.value.trim()) {
    error.value = '请输入卡密'
    return
  }
  loading.value = true
  try {
    result.value = await post<RedeemResult>('/api/panel/redeem', { code: code.value.trim() })
    code.value = ''
  } catch (e) {
    if (e instanceof ApiError && e.status === 429) {
      error.value = '尝试过于频繁，请稍后再试'
    } else if (e instanceof ApiError) {
      error.value = e.message
    } else {
      error.value = '网络异常，请稍后再试'
    }
  } finally {
    loading.value = false
  }
}

function grantText(r: RedeemResult): string {
  return r.grant_type === 'add_quota'
    ? `流量 +${formatBytes(r.grant_value)}`
    : `时长 +${r.grant_value} 天`
}
</script>

<template>
  <div class="page">
    <section class="card">
      <h2 class="card-title">兑换卡密</h2>
      <form @submit.prevent="submit">
        <label class="label" for="code">卡密</label>
        <input
          id="code"
          v-model="code"
          class="input input-mono"
          placeholder="XXXX-XXXX-XXXX"
          autocomplete="off"
        />
        <p v-if="error" class="error-text">{{ error }}</p>
        <div style="margin-top: 14px">
          <button class="btn btn-primary" type="submit" :disabled="loading">
            {{ loading ? '兑换中…' : '兑换' }}
          </button>
        </div>
      </form>
    </section>

    <section v-if="result" class="card">
      <h2 class="card-title">兑换成功</h2>
      <dl class="kv">
        <div class="kv-row"><dt>权益</dt><dd>{{ grantText(result) }}</dd></div>
        <div class="kv-row"><dt>当前配额</dt><dd>{{ formatBytes(result.quota_bytes) }}</dd></div>
        <div class="kv-row"><dt>到期</dt><dd>{{ formatDate(result.expires_at) }}</dd></div>
        <div class="kv-row"><dt>订单号</dt><dd class="mono">{{ result.order_no }}</dd></div>
      </dl>
    </section>
  </div>
</template>
