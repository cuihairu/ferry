<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  get, getPaths, getPathDetail, getPathHealth,
  getPathGroups, getPathGroup, createPathGroup, updatePathGroup, deletePathGroup,
  addPathGroupMember, updatePathGroupMember, removePathGroupMember,
  type Node, type Path, type PathGroup, type PathGroupHealth, type PathGroupMemberRow,
} from '../api'
import { formatDate } from '../utils/format'

// F-1427/F-1428/F-1429: the path plane view — path list with filters, a per-path
// detail dialog (fields + derived health) and path group management (ordered
// candidate sets with a default). Display/management only: path creation lives
// in the preview/draft/auto-provision flow on the server side.

const PROBE_IMPORTANCE: Record<string, string> = { critical: '关键', normal: '常规', low: '低' }
const DIR_TEXT: Record<string, string> = { out: '出海', in: '回国' }

// ---- Paths (F-1427) ----
const paths = ref<Path[]>([])
const nodes = ref<Node[]>([])
const loading = ref(false)
const dirFilter = ref<'all' | 'out' | 'in'>('all')
const regionFilter = ref('all')
const enabledFilter = ref<'all' | 'on' | 'off'>('all')

const regions = computed(() => [...new Set(paths.value.map((p) => p.region).filter(Boolean))])

const filtered = computed(() =>
  paths.value.filter(
    (p) =>
      (dirFilter.value === 'all' || p.direction === dirFilter.value) &&
      (regionFilter.value === 'all' || p.region === regionFilter.value) &&
      (enabledFilter.value === 'all' || (enabledFilter.value === 'on') === p.enabled),
  ),
)

function nodeName(id: number): string {
  return nodes.value.find((n) => n.id === id)?.name ?? `#${id}`
}

async function load() {
  loading.value = true
  try {
    const [ps, ns] = await Promise.all([getPaths(), get<Node[]>('/api/nodes')])
    paths.value = ps.paths
    nodes.value = ns
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

// ---- Path detail (F-1428) ----
const detailVisible = ref(false)
const detailPath = ref<Path | null>(null)
const detailHealth = ref('')

async function openDetail(p: Path) {
  detailPath.value = p
  detailHealth.value = ''
  detailVisible.value = true
  try {
    const fresh = await getPathDetail(p.id)
    detailPath.value = fresh
    const h = await getPathHealth(p.id)
    detailHealth.value = h.health
  } catch (e) {
    ElMessage.error(String(e))
  }
}

// ---- Path groups (F-1429) ----
const groups = ref<PathGroup[]>([])
const groupsLoading = ref(false)

const defaultPathName = (g: PathGroup): string =>
  g.default_path_id ? paths.value.find((p) => p.id === g.default_path_id)?.name ?? `#${g.default_path_id}` : '—'

async function loadGroups() {
  groupsLoading.value = true
  try {
    const data = await getPathGroups()
    groups.value = data.path_groups
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    groupsLoading.value = false
  }
}

// ---- Group create/edit ----
const groupForm = reactive({ visible: false, saving: false, id: 0, name: '', direction: 'out', region: '', purpose: '', enabled: true, note: '' })

function openGroupCreate() {
  Object.assign(groupForm, { visible: true, saving: false, id: 0, name: '', direction: 'out', region: '', purpose: '', enabled: true, note: '' })
}

function openGroupEdit(g: PathGroup) {
  Object.assign(groupForm, { visible: true, saving: false, id: g.id, name: g.name, direction: g.direction, region: g.region, purpose: g.purpose, enabled: g.enabled, note: g.note })
}

async function saveGroup() {
  if (!groupForm.name.trim()) {
    ElMessage.error('请输入名称')
    return
  }
  groupForm.saving = true
  try {
    const input = {
      name: groupForm.name,
      direction: groupForm.direction,
      region: groupForm.region,
      purpose: groupForm.purpose,
      enabled: groupForm.enabled,
      note: groupForm.note,
    }
    if (groupForm.id) {
      await updatePathGroup(groupForm.id, input)
      ElMessage.success('已保存')
    } else {
      await createPathGroup(input)
      ElMessage.success('已创建')
    }
    groupForm.visible = false
    await loadGroups()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    groupForm.saving = false
  }
}

async function removeGroup(g: PathGroup) {
  await ElMessageBox.confirm(`删除分组「${g.name}」？成员关系一并解除，路径本身不受影响。`, '删除分组', { type: 'warning' })
  try {
    await deletePathGroup(g.id)
    ElMessage.success('已删除')
    await loadGroups()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

// ---- Group members dialog ----
const membersVisible = ref(false)
const membersBusy = ref(false)
const groupFor = ref<PathGroup | null>(null)
const members = ref<PathGroupMemberRow[]>([])
const addPathIdText = ref('')

const HEALTH_TYPE: Record<string, 'success' | 'warning' | 'info' | 'danger'> = {
  healthy: 'success', degraded: 'warning', failed: 'danger', unknown: 'info',
}

// Health read per F-0268/F-0269: worst-of over active members + availability window.
const groupHealth = reactive<{ state: string; availability: number | null }>({ state: '', availability: null })

function pathNameOf(id: number): string {
  return paths.value.find((p) => p.id === id)?.name ?? `#${id}`
}

async function openMembers(g: PathGroup) {
  groupFor.value = g
  membersVisible.value = true
  addPathIdText.value = ''
  await reloadMembers()
}

async function reloadMembers() {
  if (!groupFor.value) return
  membersBusy.value = true
  try {
    const data = await getPathGroup(groupFor.value.id)
    groupFor.value = data.path_group
    members.value = data.members
    const h: PathGroupHealth = await get(`/api/path-groups/${groupFor.value.id}/health`)
    groupHealth.state = h.health
    groupHealth.availability = h.availability
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    membersBusy.value = false
  }
}

async function addMember() {
  if (!groupFor.value) return
  const id = Number(addPathIdText.value)
  if (!id) {
    ElMessage.error('请输入路径 ID')
    return
  }
  try {
    await addPathGroupMember(groupFor.value.id, id)
    ElMessage.success('已加入')
    addPathIdText.value = ''
    await reloadMembers()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

async function toggleExclude(m: PathGroupMemberRow) {
  if (!groupFor.value) return
  try {
    await updatePathGroupMember(groupFor.value.id, m.path_id, { excluded: !m.excluded })
    await reloadMembers()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

async function changePosition(m: PathGroupMemberRow, pos: number) {
  if (!groupFor.value) return
  try {
    await updatePathGroupMember(groupFor.value.id, m.path_id, { position: pos })
    await reloadMembers()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

async function removeMember(m: PathGroupMemberRow) {
  if (!groupFor.value) return
  try {
    await removePathGroupMember(groupFor.value.id, m.path_id)
    ElMessage.success('已移出')
    await reloadMembers()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

async function setDefault(g: PathGroup) {
  if (!g.default_path_id) {
    ElMessage.info('该分组未设默认路径')
    return
  }
  try {
    await updatePathGroup(g.id, { name: g.name, direction: g.direction, region: g.region, purpose: g.purpose, enabled: g.enabled, note: g.note, default_path_id: g.default_path_id })
    ElMessage.success(`默认路径 ${pathNameOf(g.default_path_id)} 已保存`)
    await loadGroups()
  } catch (e) {
    ElMessage.error(String(e))
  }
}
</script>

<template>
  <div class="page">
    <h2>线路</h2>
    <p class="page-desc">业务路径（入口→中转→落地）与路径分组（有序候选集）。</p>

    <div class="toolbar">
      <el-select v-model="dirFilter" style="width: 110px">
        <el-option value="all" label="全部方向" />
        <el-option value="out" label="出海" />
        <el-option value="in" label="回国" />
      </el-select>
      <el-select v-model="regionFilter" style="width: 140px">
        <el-option value="all" label="全部区域" />
        <el-option v-for="r in regions" :key="r" :value="r" :label="r" />
      </el-select>
      <el-select v-model="enabledFilter" style="width: 110px">
        <el-option value="all" label="全部状态" />
        <el-option value="on" label="启用" />
        <el-option value="off" label="停用" />
      </el-select>
      <el-button :loading="loading" @click="load(); loadGroups()">刷新</el-button>
    </div>

    <!-- Path list (F-1427) -->
    <el-table :data="filtered" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column prop="id" label="ID" width="60" />
      <el-table-column prop="name" label="名称" min-width="130" />
      <el-table-column label="入口" min-width="110">
        <template #default="{ row }">{{ nodeName(row.entry_node_id) }}</template>
      </el-table-column>
      <el-table-column label="中转" min-width="110">
        <template #default="{ row }">{{ row.relay_node_id ? nodeName(row.relay_node_id) : '直连' }}</template>
      </el-table-column>
      <el-table-column label="落地" min-width="110">
        <template #default="{ row }">{{ nodeName(row.landing_node_id) }}</template>
      </el-table-column>
      <el-table-column prop="transport" label="传输" width="80" />
      <el-table-column label="方向" width="80">
        <template #default="{ row }">{{ DIR_TEXT[row.direction] ?? row.direction }}</template>
      </el-table-column>
      <el-table-column label="区域" width="90">
        <template #default="{ row }">{{ row.region || '—' }}</template>
      </el-table-column>
      <el-table-column label="探测等级" width="90">
        <template #default="{ row }">{{ PROBE_IMPORTANCE[row.probe_importance] ?? row.probe_importance }}</template>
      </el-table-column>
      <el-table-column label="启用" width="70">
        <template #default="{ row }">
          <el-tag size="small" :type="row.enabled ? 'success' : 'info'" disable-transitions>{{ row.enabled ? '是' : '否' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="version" label="版本" width="70" />
      <el-table-column label="更新时间" min-width="150">
        <template #default="{ row }">{{ formatDate(row.updated_at) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="90" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openDetail(row)">详情</el-button>
        </template>
      </el-table-column>
    </el-table>

    <!-- Path groups (F-1429) -->
    <h3 class="section">路径分组</h3>
    <div class="toolbar">
      <span class="hint">分组是有序候选路径集（position 决定回退顺序），带默认路径；排除成员保留行但不参与聚合。</span>
      <el-button type="primary" @click="openGroupCreate">新建分组</el-button>
    </div>
    <el-table :data="groups" v-loading="groupsLoading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column prop="id" label="ID" width="60" />
      <el-table-column prop="name" label="名称" min-width="130" />
      <el-table-column label="方向" width="80">
        <template #default="{ row }">{{ DIR_TEXT[row.direction] ?? row.direction }}</template>
      </el-table-column>
      <el-table-column label="区域" width="90">
        <template #default="{ row }">{{ row.region || '—' }}</template>
      </el-table-column>
      <el-table-column label="用途" width="100">
        <template #default="{ row }">{{ row.purpose || '—' }}</template>
      </el-table-column>
      <el-table-column label="默认路径" min-width="120">
        <template #default="{ row }">{{ defaultPathName(row) }}</template>
      </el-table-column>
      <el-table-column label="启用" width="70">
        <template #default="{ row }">
          <el-tag size="small" :type="row.enabled ? 'success' : 'info'" disable-transitions>{{ row.enabled ? '是' : '否' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="note" label="备注" min-width="120" />
      <el-table-column label="操作" width="180" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openMembers(row)">成员</el-button>
          <el-button link type="primary" @click="openGroupEdit(row)">编辑</el-button>
          <el-button v-if="row.default_path_id" link type="info" @click="setDefault(row)">存默认</el-button>
          <el-button link type="danger" @click="removeGroup(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>

    <!-- Path detail dialog (F-1428) -->
    <el-dialog v-model="detailVisible" :title="`线路详情 · ${detailPath?.name ?? ''}`" width="640px">
      <el-descriptions v-if="detailPath" :column="2" border size="small">
        <el-descriptions-item label="ID">{{ detailPath.id }}</el-descriptions-item>
        <el-descriptions-item label="版本">{{ detailPath.version }}</el-descriptions-item>
        <el-descriptions-item label="入口">{{ nodeName(detailPath.entry_node_id) }}</el-descriptions-item>
        <el-descriptions-item label="落地">{{ nodeName(detailPath.landing_node_id) }}</el-descriptions-item>
        <el-descriptions-item label="中转">{{ detailPath.relay_node_id ? nodeName(detailPath.relay_node_id) : '直连' }}</el-descriptions-item>
        <el-descriptions-item label="传输">{{ detailPath.transport }}</el-descriptions-item>
        <el-descriptions-item label="方向">{{ DIR_TEXT[detailPath.direction] ?? detailPath.direction }}</el-descriptions-item>
        <el-descriptions-item label="区域">{{ detailPath.region || '—' }}</el-descriptions-item>
        <el-descriptions-item label="探测等级">{{ PROBE_IMPORTANCE[detailPath.probe_importance] ?? detailPath.probe_importance }}</el-descriptions-item>
        <el-descriptions-item label="启用">{{ detailPath.enabled ? '是' : '否' }}</el-descriptions-item>
        <el-descriptions-item label="健康">
          <el-tag v-if="detailHealth" size="small" :type="HEALTH_TYPE[detailHealth] ?? 'info'" disable-transitions>{{ detailHealth }}</el-tag>
          <span v-else>读取中…</span>
        </el-descriptions-item>
        <el-descriptions-item label="更新时间">{{ formatDate(detailPath.updated_at) }}</el-descriptions-item>
        <el-descriptions-item label="备注" :span="2">{{ detailPath.note || '—' }}</el-descriptions-item>
      </el-descriptions>
      <template #footer>
        <el-button @click="detailVisible = false">关闭</el-button>
      </template>
    </el-dialog>

    <!-- Group members dialog (F-1429) -->
    <el-dialog v-model="membersVisible" :title="`分组成员 · ${groupFor?.name ?? ''}`" width="720px">
      <div class="health-line">
        <span>聚合健康：</span>
        <el-tag v-if="groupHealth.state" size="small" :type="HEALTH_TYPE[groupHealth.state] ?? 'info'" disable-transitions>{{ groupHealth.state }}</el-tag>
        <span v-else>—</span>
        <span class="hint" style="margin-left: 12px">
          可用率 {{ groupHealth.availability === null ? '—' : groupHealth.availability.toFixed(1) + '%' }}（24h 探针窗口）
        </span>
        <el-button size="small" style="margin-left: auto" :loading="membersBusy" @click="reloadMembers">刷新</el-button>
      </div>
      <el-table :data="members" v-loading="membersBusy" size="small" empty-text="暂无成员，先加入路径">
        <el-table-column label="顺序" width="110">
          <template #default="{ row }">
            <el-input-number :model-value="row.position" size="small" :step="10" @change="(v: number | undefined) => changePosition(row, v ?? 0)" />
          </template>
        </el-table-column>
        <el-table-column label="路径" min-width="140">
          <template #default="{ row }">{{ pathNameOf(row.path_id) }}</template>
        </el-table-column>
        <el-table-column label="参与聚合" width="100">
          <template #default="{ row }">
            <el-tag size="small" :type="row.excluded ? 'danger' : 'success'" disable-transitions>{{ row.excluded ? '已排除' : '参与' }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="操作" width="150">
          <template #default="{ row }">
            <el-button link type="warning" @click="toggleExclude(row)">{{ row.excluded ? '恢复' : '排除' }}</el-button>
            <el-button link type="danger" @click="removeMember(row)">移出</el-button>
          </template>
        </el-table-column>
      </el-table>
      <div class="add-member">
        <el-input v-model="addPathIdText" placeholder="路径 ID" style="width: 140px" />
        <el-button type="primary" @click="addMember">加入</el-button>
        <span class="hint">仅同方向路径可加入；默认路径必须是成员。</span>
      </div>
      <div v-if="groupFor" class="add-member">
        <span class="hint">默认路径：{{ defaultPathName(groupFor) }}（在分组行「编辑」里改 default_path_id）</span>
      </div>
      <template #footer>
        <el-button @click="membersVisible = false">关闭</el-button>
      </template>
    </el-dialog>

    <!-- Group create/edit dialog -->
    <el-dialog v-model="groupForm.visible" :title="groupForm.id ? '编辑分组' : '新建分组'" width="520px">
      <el-form label-width="90px">
        <el-form-item label="名称">
          <el-input v-model="groupForm.name" maxlength="64" placeholder="分组名称" />
        </el-form-item>
        <el-form-item label="方向">
          <el-select v-model="groupForm.direction" style="width: 100%">
            <el-option value="out" label="出海" />
            <el-option value="in" label="回国" />
          </el-select>
        </el-form-item>
        <el-form-item label="区域">
          <el-input v-model="groupForm.region" maxlength="64" placeholder="可选" />
        </el-form-item>
        <el-form-item label="用途">
          <el-input v-model="groupForm.purpose" maxlength="32" placeholder="可选，如 streaming" />
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="groupForm.enabled" />
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="groupForm.note" maxlength="255" placeholder="可选" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="groupForm.visible = false">取消</el-button>
        <el-button type="primary" :loading="groupForm.saving" @click="saveGroup">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.section {
  margin: 22px 0 10px;
  font-size: 15px;
}
.hint {
  font-size: 12px;
  color: var(--ferry-text-muted);
}
.toolbar {
  margin-bottom: 12px;
}
.health-line {
  display: flex;
  align-items: center;
  margin-bottom: 10px;
}
.add-member {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 12px;
}
</style>
