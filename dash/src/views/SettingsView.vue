<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { get, put, type UserTemplate } from '../api'
import { GB } from '../utils/format'

// P1-5：默认用户模板——新建用户可套用的默认配额/时长/重置周期。

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
