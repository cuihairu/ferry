<script setup lang="ts">
import { ref } from 'vue'
import { ElMessage } from 'element-plus'
import { post, ApiError, type RedeemResult } from './api'
import { GB, formatBytes, formatDate } from './utils/format'

// PAY-7：兑换页。P0 无会话，用户 ID 由管理端提供；
// 用户中心（会话识别）就绪后，此处改为从会话取用户，隐藏 ID 输入。

const userId = ref<number | null>(null)
const code = ref('')
const loading = ref(false)
const result = ref<RedeemResult | null>(null)

async function redeem() {
  if (!userId.value || userId.value <= 0) {
    ElMessage.warning('请输入用户 ID')
    return
  }
  const c = code.value.trim()
  if (!c) {
    ElMessage.warning('请输入卡密')
    return
  }
  loading.value = true
  result.value = null
  try {
    result.value = await post<RedeemResult>('/api/redeem', { code: c, user_id: userId.value })
    ElMessage.success('兑换成功')
    code.value = ''
  } catch (e) {
    // 429 来自 IP 限流，文案由后端给出，这里原样展示。
    const msg = e instanceof ApiError ? e.message : '网络错误，请稍后再试'
    ElMessage.error(msg)
  } finally {
    loading.value = false
  }
}
</script>

<template>
  <div class="page">
    <h1>ferry 兑换中心</h1>
    <p class="page-desc">
      输入卡密兑换流量配额或使用时长。用户 ID 由管理端提供；登录与自助查询随用户中心上线。
    </p>

    <el-form label-position="top" @submit.prevent="redeem">
      <el-form-item label="用户 ID">
        <el-input-number v-model="userId" :min="1" :step="1" placeholder="如 1" style="width: 220px" />
      </el-form-item>
      <el-form-item label="卡密">
        <el-input v-model="code" placeholder="XXXX-XXXX-XXXX" maxlength="64" clearable />
      </el-form-item>
      <el-button type="primary" :loading="loading" style="width: 100%" @click="redeem">兑换</el-button>
    </el-form>

    <div v-if="result" class="result">
      <div class="result-title">兑换成功</div>
      <dl class="result-grid">
        <dt>获得权益</dt>
        <dd>
          {{
            result.grant_type === 'add_quota'
              ? `+${formatBytes(result.grant_value)} 流量配额`
              : `+${result.grant_value} 天使用时长`
          }}
        </dd>
        <dt>当前配额</dt>
        <dd>{{ formatBytes(result.quota_bytes) }}</dd>
        <dt>到期时间</dt>
        <dd>{{ formatDate(result.expires_at) }}</dd>
        <dt>订单号</dt>
        <dd class="mono">{{ result.order_no }}</dd>
      </dl>
      <p class="result-hint">配额以 1 GiB = {{ GB }} 字节计。</p>
    </div>
  </div>
</template>

<style scoped>
.result {
  margin-top: 28px;
  padding: 18px 20px;
  border: 1px solid var(--ferry-border);
  border-radius: 8px;
  background: var(--ferry-bg-panel);
}
.result-title {
  font-weight: 650;
  color: var(--ferry-ok);
  margin-bottom: 12px;
}
.result-grid {
  display: grid;
  grid-template-columns: 88px 1fr;
  gap: 8px 16px;
  margin: 0;
}
.result-grid dt {
  color: var(--ferry-text-muted);
}
.result-grid dd {
  margin: 0;
  color: var(--ferry-text);
}
.mono {
  font-family: 'SFMono-Regular', 'JetBrains Mono', Menlo, Consolas, monospace;
  font-size: 13px;
}
.result-hint {
  margin: 14px 0 0;
  font-size: 12px;
  color: var(--ferry-text-muted);
}
</style>
