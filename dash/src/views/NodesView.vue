<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { get, post, put, del, type Node } from '../api'

// P0-16：节点表格 + 新建/编辑对话框（协议/地址/端口/配置模板）。

const PROTOCOLS = [
  { value: 'vless', label: 'VLESS' },
  { value: 'vmess', label: 'VMess' },
  { value: 'trojan', label: 'Trojan' },
  { value: 'shadowsocks', label: 'Shadowsocks' },
]

const STATUS_TYPE: Record<string, 'success' | 'danger' | 'info' | 'warning'> = {
  online: 'success',
  offline: 'info',
  unknown: 'warning',
}

const nodes = ref<Node[]>([])
const loading = ref(false)

async function load() {
  loading.value = true
  try {
    nodes.value = await get<Node[]>('/api/nodes')
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

const dialogVisible = ref(false)
const editing = ref<Node | null>(null)
const saving = ref(false)
const form = reactive({
  name: '',
  address: '',
  port: 443,
  protocol: 'vless',
  config: '{}',
  enabled: true,
})

function openCreate() {
  editing.value = null
  Object.assign(form, { name: '', address: '', port: 443, protocol: 'vless', config: '{}', enabled: true })
  dialogVisible.value = true
}

function openEdit(n: Node) {
  editing.value = n
  Object.assign(form, {
    name: n.name,
    address: n.address,
    port: n.port,
    protocol: n.protocol,
    config: n.config || '{}',
    enabled: n.enabled,
  })
  dialogVisible.value = true
}

async function save() {
  if (!form.name.trim() || !form.address.trim()) {
    ElMessage.warning('名称与地址必填')
    return
  }
  try {
    JSON.parse(form.config)
  } catch {
    ElMessage.warning('配置模板必须是合法 JSON')
    return
  }
  saving.value = true
  try {
    const body = {
      name: form.name.trim(),
      address: form.address.trim(),
      port: form.port,
      protocol: form.protocol,
      config: form.config,
      enabled: form.enabled,
    }
    if (editing.value) {
      await put(`/api/nodes/${editing.value.id}`, body)
    } else {
      await post('/api/nodes', body)
    }
    dialogVisible.value = false
    ElMessage.success('已保存')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    saving.value = false
  }
}

async function toggleEnabled(n: Node, enabled: boolean) {
  try {
    await put(`/api/nodes/${n.id}`, { enabled })
    n.enabled = enabled
  } catch (e) {
    ElMessage.error(String(e))
  }
}

async function remove(n: Node) {
  await ElMessageBox.confirm(`删除节点 ${n.name}，确认？`, '删除节点', { type: 'warning' })
  await del(`/api/nodes/${n.id}`)
  ElMessage.success('已删除')
  await load()
}
</script>

<template>
  <div class="page">
    <h2>节点</h2>
    <p class="page-desc">代理节点与协议配置。</p>

    <div class="toolbar">
      <el-button type="primary" @click="openCreate">新建节点</el-button>
    </div>

    <el-table :data="nodes" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column prop="name" label="名称" min-width="130" />
      <el-table-column label="协议" width="120">
        <template #default="{ row }">
          <el-tag size="small" disable-transitions>{{ row.protocol }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="地址" min-width="180">
        <template #default="{ row }">{{ row.address }}:{{ row.port }}</template>
      </el-table-column>
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag size="small" :type="STATUS_TYPE[row.status] ?? 'info'" disable-transitions>{{ row.status }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="region" label="区域" width="110" />
      <el-table-column prop="isp" label="运营商" width="100" />
      <el-table-column label="启用" width="80">
        <template #default="{ row }">
          <el-switch :model-value="row.enabled" @change="(v: boolean) => toggleEnabled(row, v)" />
        </template>
      </el-table-column>
      <el-table-column label="操作" width="140" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button link type="danger" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialogVisible" :title="editing ? '编辑节点' : '新建节点'" width="560px">
      <el-form label-width="90px">
        <el-form-item label="名称">
          <el-input v-model="form.name" maxlength="64" placeholder="节点名称" />
        </el-form-item>
        <el-form-item label="协议">
          <el-select v-model="form.protocol" style="width: 100%">
            <el-option v-for="p in PROTOCOLS" :key="p.value" :value="p.value" :label="p.label" />
          </el-select>
        </el-form-item>
        <el-form-item label="地址">
          <el-input v-model="form.address" maxlength="255" placeholder="域名或 IP" />
        </el-form-item>
        <el-form-item label="端口">
          <el-input-number v-model="form.port" :min="1" :max="65535" />
        </el-form-item>
        <el-form-item label="配置模板">
          <el-input
            v-model="form.config"
            type="textarea"
            :rows="6"
            class="config-input"
            placeholder='{"uuid": "...", "tls": true, "net": "ws"}'
          />
          <span class="form-hint">协议配置 JSON，按协议约定字段</span>
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="form.enabled" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.toolbar {
  margin-bottom: 16px;
}
.form-hint {
  margin-left: 10px;
  color: var(--ferry-text-muted);
  font-size: 12px;
}
.config-input :deep(textarea) {
  font-family: ui-monospace, 'SF Mono', Menlo, Consolas, monospace;
  font-size: 12px;
}
</style>
