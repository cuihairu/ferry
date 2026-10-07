<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { ElMessage } from 'element-plus'
import { get, postRaw, type Node, type NodeConfigRow, type RoutingConfig } from '../api'
import { formatDate } from '../utils/format'

// SAVE-1：分流规则库——版本史（node_configs 快照）、数据文件推送（config.push
// 通道 kind=rulelib:<name>，agent 落资产目录）、配置模板合成分流段的渲染预览。

const props = defineProps<{ node: Node | null }>()
const visible = defineModel<boolean>({ default: false })

const proc = ref('xray')
const rows = ref<NodeConfigRow[]>([])
const rendered = ref<RoutingConfig | null>(null)
const rendering = ref(false)
const pushing = ref(false)
const picked = ref<File | null>(null)
const targetName = ref<'geoip.dat' | 'geosite.dat'>('geoip.dat')

const history = computed(() => rows.value.filter((r) => r.proc === proc.value))

watch(visible, (open) => {
  if (open) {
    rendered.value = null
    picked.value = null
    load()
  }
})

async function load() {
  if (!props.node) return
  try {
    rows.value = await get<NodeConfigRow[]>(`/api/nodes/${props.node.id}/rulelib`)
  } catch (e) {
    ElMessage.error(String(e))
  }
}

async function renderPreview() {
  if (!props.node) return
  rendering.value = true
  try {
    rendered.value = await get<RoutingConfig>(`/api/nodes/${props.node.id}/routing-config`)
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    rendering.value = false
  }
}

function onFileChange(ev: Event) {
  const files = (ev.target as HTMLInputElement).files
  picked.value = files && files.length > 0 ? files[0] : null
}

async function push() {
  if (!props.node || !picked.value) {
    ElMessage.warning('请先选择规则库文件')
    return
  }
  pushing.value = true
  try {
    await postRaw(`/api/nodes/${props.node.id}/rulelib?proc=${encodeURIComponent(proc.value)}&name=${encodeURIComponent(targetName.value)}`, picked.value)
    ElMessage.success('已推送（agent 校验落盘后 reload）')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    pushing.value = false
    picked.value = null
  }
}

const STATUS_TEXT: Record<string, string> = { pending: '待确认', applied: '已生效', failed: '失败' }

function short(sha: string): string {
  return sha.slice(0, 8)
}
</script>

<template>
  <el-dialog v-model="visible" :title="`分流规则库 · ${node?.name ?? ''}`" width="680px">
    <el-form label-width="80px">
      <el-form-item label="目标进程">
        <el-input v-model="proc" style="width: 200px" placeholder="agent 侧进程名" />
      </el-form-item>
    </el-form>

    <h4>版本史</h4>
    <el-table :data="history" size="small" max-height="220" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column label="文件" min-width="120">
        <template #default="{ row }">{{ row.kind.replace('rulelib:', '') }}</template>
      </el-table-column>
      <el-table-column label="版本" width="110">
        <template #default="{ row }"><code>{{ short(row.version) }}</code></template>
      </el-table-column>
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag size="small" :type="row.status === 'applied' ? 'success' : row.status === 'failed' ? 'danger' : 'info'" disable-transitions>
            {{ STATUS_TEXT[row.status] ?? row.status }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="时间" width="150">
        <template #default="{ row }">{{ formatDate(row.created_at) }}</template>
      </el-table-column>
      <el-table-column label="说明" min-width="140">
        <template #default="{ row }"><span class="err">{{ row.error }}</span></template>
      </el-table-column>
    </el-table>

    <h4>推送更新</h4>
    <div class="push-row">
      <el-select v-model="targetName" style="width: 150px">
        <el-option value="geoip.dat" label="geoip.dat（IP 库）" />
        <el-option value="geosite.dat" label="geosite.dat（域名库）" />
      </el-select>
      <input type="file" class="file-pick" @change="onFileChange" />
      <el-button type="primary" :loading="pushing" :disabled="!picked" @click="push">推送</el-button>
    </div>
    <p class="hint">
      数据文件经 config.push 通道下发，agent 校验 sha256 后原子落盘到资产目录（XRAY_LOCATION_ASSET 需与 asset_dir 对齐）并触发 reload；每次推送在配置快照留版本。
    </p>

    <h4>配置模板合成分流段</h4>
    <el-button size="small" :loading="rendering" @click="renderPreview">渲染预览</el-button>
    <template v-if="rendered">
      <p class="hint">sha256 <code>{{ short(rendered.sha256) }}</code>，内置清单：{{ rendered.sets.map((s) => s.name).join('、') }}；下发走「配置推送」。</p>
      <pre class="preview">{{ rendered.config }}</pre>
    </template>
  </el-dialog>
</template>

<style scoped>
h4 {
  margin: 14px 0 8px;
}
.push-row {
  display: flex;
  gap: 12px;
  align-items: center;
}
.file-pick {
  color: var(--ferry-text-dim);
}
.hint {
  margin: 8px 0;
  font-size: 12px;
  color: var(--ferry-text-dim);
  line-height: 1.7;
}
.preview {
  max-height: 240px;
  overflow: auto;
  margin: 8px 0 0;
  padding: 10px;
  font-size: 12px;
  line-height: 1.6;
  background: var(--ferry-bg-panel);
  border-radius: 6px;
  white-space: pre-wrap;
  word-break: break-all;
}
.err {
  color: var(--ferry-danger, #f56c6c);
  font-size: 12px;
}
</style>
