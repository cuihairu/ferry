<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { get, post, put, del, getAlloc, putAllocPolicy, explainAlloc, type Node, type LandingAssignment, type TransportRow, type AllocPolicy, type AllocExplainDecision } from '../api'
import { formatDate } from '../utils/format'

// E-20: manual landing assignment — assign landings and weights for an entry node or a whole region.
// Assignments are persisted as records and releasing never deletes a row; the activation path that pushes
// the landing list via config.push plugs in together with the agent relay's configuration support (E-5),
// so the assignment records are authoritative for now.
// E-17: the region transport verdict panel — the switch advice of "use whichever is alive".

const rows = ref<LandingAssignment[]>([])
const nodes = ref<Node[]>([])
const txRows = ref<TransportRow[]>([])
const allocPolicy = ref<AllocPolicy>({ out: 'balanced', in: 'balanced', rebalance_start: 1, rebalance_end: 7, w_load: 0.5, w_cost: 0.3, w_premium: 0.2 })
const policySaving = ref(false)
const loading = ref(false)
const scope = ref<'active' | 'all'>('active')

// E-21: weighted-least-connections auto assignment — each of the outbound/return pools picks its tier.
const POLICY_OPTIONS = [
  { value: '', label: '按方向缺省' },
  { value: 'least_conn', label: '最小连接' },
  { value: 'cost_first', label: '成本优先' },
  { value: 'perf_first', label: '性能优先' },
  { value: 'balanced', label: '均衡' },
]

async function load() {
  loading.value = true
  try {
    const [ls, ns, ts, alloc] = await Promise.all([
      get<LandingAssignment[]>(`/api/landings?scope=${scope.value}`),
      get<Node[]>('/api/nodes'),
      get<TransportRow[]>('/api/transport-status'),
      getAlloc(),
    ])
    rows.value = ls
    nodes.value = ns
    txRows.value = ts
    allocPolicy.value = alloc.policy
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

async function savePolicy() {
  policySaving.value = true
  try {
    await putAllocPolicy(allocPolicy.value)
    ElMessage.success('分配策略已保存')
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    policySaving.value = false
  }
}

const STRATEGY_TEXT: Record<string, string> = {
  manual: '手动',
  least_conn: '最小连接',
  cost_first: '成本优先',
  perf_first: '性能优先',
  balanced: '均衡',
}

function txText(r: TransportRow): string {
  return r.transports.map((t) => `${t.transport} ${t.alive}/${t.total}`).join(' · ')
}

const nodeById = computed(() => new Map(nodes.value.map((n) => [n.id, n])))

function nodeName(id: number | null): string {
  if (!id) return '—'
  return nodeById.value.get(id)?.name ?? `#${id}`
}

const entries = computed(() => nodes.value.filter((n) => n.role === 'entry' || n.role === 'both'))
const landings = computed(() => nodes.value.filter((n) => n.role === 'landing' || n.role === 'both'))
const regions = computed(() => [...new Set(entries.value.map((n) => n.region).filter(Boolean))])

// ---- Create ----
const dialogVisible = ref(false)
const saving = ref(false)
const form = reactive({
  scopeMode: 'entry' as 'entry' | 'region',
  entryNodeId: null as number | null,
  region: '',
  landingNodeId: null as number | null,
  direction: 'out' as 'out' | 'in',
  weight: 0,
  reason: '',
})

function openCreate() {
  Object.assign(form, {
    scopeMode: 'entry', entryNodeId: null, region: '',
    landingNodeId: null, direction: 'out', weight: 0, reason: '',
  })
  dialogVisible.value = true
}

async function save() {
  if (form.scopeMode === 'entry' && !form.entryNodeId) {
    ElMessage.warning('请选择入口节点')
    return
  }
  if (form.scopeMode === 'region' && !form.region.trim()) {
    ElMessage.warning('请填写区域')
    return
  }
  if (!form.landingNodeId) {
    ElMessage.warning('请选择落地节点')
    return
  }
  saving.value = true
  try {
    const body: Record<string, unknown> = {
      landing_node_id: form.landingNodeId,
      direction: form.direction,
      weight: form.weight,
      reason: form.reason.trim() || '手动分配',
    }
    if (form.scopeMode === 'entry') body.entry_node_id = form.entryNodeId
    else body.region = form.region.trim()
    await post('/api/landings', body)
    dialogVisible.value = false
    ElMessage.success('已分配')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    saving.value = false
  }
}

async function tweakWeight(row: LandingAssignment) {
  const { value } = await ElMessageBox.prompt(
    `调整 ${nodeName(row.entry_node_id) !== '—' ? nodeName(row.entry_node_id) : row.region} → ${nodeName(row.landing_node_id)} 的权重`,
    '调整权重',
    { inputValue: String(row.weight), inputPattern: /^\d+$/, inputErrorMessage: '请输入非负整数' },
  ).catch(() => ({ value: null }))
  if (value === null) return
  try {
    await put(`/api/landings/${row.id}`, { weight: Number(value) })
    ElMessage.success('已调整')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

async function release(row: LandingAssignment) {
  await ElMessageBox.confirm(
    `释放该分配后落地恢复自动兜底，确认？`,
    '释放分配',
    { type: 'warning' },
  )
  try {
    await del(`/api/landings/${row.id}`)
    ElMessage.success('已释放')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}

const DIR_TEXT: Record<string, string> = { out: '出海', in: '回国' }

// Scheduling explanation (F-1431): the live per-entry decision view (F-1023/F-1034)
// read from GET /api/alloc/explain — ranked candidates with eligibility reasons,
// pre-score exclusions, winner and action. Read-only; nothing is applied here.
const explainVisible = ref(false)
const explainBusy = ref(false)
const explainRoundAt = ref('')
const explainDecisions = ref<AllocExplainDecision[]>([])
const explainManual = ref<number[]>([])

const EXPLAIN_ACTION: Record<string, string> = {
  keep: '保持', switch: '切换', 'keep-current': '保持', force_switch: '强制切换',
}

async function openExplain() {
  explainVisible.value = true
  explainBusy.value = true
  try {
    const out = await explainAlloc()
    explainRoundAt.value = out.round_at
    explainDecisions.value = out.decisions
    explainManual.value = out.manual
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    explainBusy.value = false
  }
}
</script>

<template>
  <div class="page">
    <h2>落地分配</h2>
    <p class="page-desc">
      手动为入口节点或区域指定落地与权重；释放保留分配史。生效链路（relay 配置推送）随 agent relay 配置化接入。
    </p>

    <div class="toolbar">
      <el-radio-group v-model="scope" @change="load">
        <el-radio-button value="active">生效中</el-radio-button>
        <el-radio-button value="all">含历史</el-radio-button>
      </el-radio-group>
      <el-button type="primary" @click="openCreate">新建分配</el-button>
      <el-button @click="openExplain">调度解释</el-button>
    </div>

    <el-table :data="rows" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column label="入口 / 区域" min-width="140">
        <template #default="{ row }">
          <el-tag v-if="row.entry_node_id" size="small" effect="plain" disable-transitions>
            节点 · {{ nodeName(row.entry_node_id) }}
          </el-tag>
          <el-tag v-else size="small" type="warning" effect="plain" disable-transitions>
            区域 · {{ row.region }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="落地" min-width="130">
        <template #default="{ row }">{{ nodeName(row.landing_node_id) }}</template>
      </el-table-column>
      <el-table-column label="方向" width="80">
        <template #default="{ row }">{{ DIR_TEXT[row.direction] ?? row.direction }}</template>
      </el-table-column>
      <el-table-column prop="weight" label="权重" width="80" />
      <el-table-column label="策略" width="90">
        <template #default="{ row }">{{ STRATEGY_TEXT[row.strategy] ?? row.strategy }}</template>
      </el-table-column>
      <el-table-column prop="reason" label="缘由" min-width="110" />
      <el-table-column label="分配时间" width="150">
        <template #default="{ row }">{{ formatDate(row.assigned_at) }}</template>
      </el-table-column>
      <el-table-column label="状态" width="110">
        <template #default="{ row }">
          <el-tag v-if="!row.released_at" size="small" type="success" disable-transitions>生效中</el-tag>
          <el-tooltip v-else :content="row.release_reason || '已释放'">
            <el-tag size="small" type="info" disable-transitions>已释放</el-tag>
          </el-tooltip>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="150" fixed="right">
        <template #default="{ row }">
          <template v-if="!row.released_at">
            <el-button link type="primary" @click="tweakWeight(row)">权重</el-button>
            <el-button link type="danger" @click="release(row)">释放</el-button>
          </template>
        </template>
      </el-table-column>
    </el-table>

    <h3 class="section">自动分配策略（E-21）</h3>
    <p class="section-desc">
      方向分流（E-30）：出海与回国各自选档，缺省出海走性价比、回国优先优质线路；
      加权最小连接：健康与负载之外按容量分连接（3M 小带宽少分）、按流量计费节点参与成本判定（包月不参与）；
      峰时换线保守（主指标显著更优才动），低峰窗口内按策略全序再平衡。手动分配的入口不受影响。
    </p>
    <div class="policy-bar">
      <span class="policy-item">出海
        <el-select v-model="allocPolicy.out" style="width: 120px">
          <el-option v-for="o in POLICY_OPTIONS" :key="o.value" :value="o.value" :label="o.label" />
        </el-select>
      </span>
      <span class="policy-item">回国
        <el-select v-model="allocPolicy.in" style="width: 120px">
          <el-option v-for="o in POLICY_OPTIONS" :key="o.value" :value="o.value" :label="o.label" />
        </el-select>
      </span>
      <span class="policy-item">低峰窗口
        <el-input-number v-model="allocPolicy.rebalance_start" :min="0" :max="23" :controls="false" style="width: 48px" />
        –
        <el-input-number v-model="allocPolicy.rebalance_end" :min="0" :max="23" :controls="false" style="width: 48px" />
        时
      </span>
      <el-button type="primary" :loading="policySaving" @click="savePolicy">保存策略</el-button>
    </div>
    <div class="policy-bar">
      <span class="policy-item">均衡系数（乘法 score：负载/成本 − 优质线，仅回国线）
        <el-input-number v-model="allocPolicy.w_load" :min="0" :max="1" :step="0.1" :controls="false" style="width: 72px" />
        <el-input-number v-model="allocPolicy.w_cost" :min="0" :max="1" :step="0.1" :controls="false" style="width: 72px" />
        <el-input-number v-model="allocPolicy.w_premium" :min="0" :max="1" :step="0.1" :controls="false" style="width: 72px" />
        <span class="muted">缺省 0.5 / 0.3 / 0.2</span>
      </span>
    </div>

    <h3 class="section">区域传输判定（E-17）</h3>
    <p class="section-desc">区域内各传输形态的探测存活聚合，「哪种活着用哪种」；建议与现行不一致时给出换线提示，切换 = 改节点传输标注并重推配置。</p>
    <el-table :data="txRows" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column prop="region" label="区域" width="120" />
      <el-table-column label="各传输存活" min-width="200">
        <template #default="{ row }">{{ txText(row) }}</template>
      </el-table-column>
      <el-table-column prop="current" label="现行" width="100" />
      <el-table-column label="建议" width="100">
        <template #default="{ row }">{{ row.recommended }}</template>
      </el-table-column>
      <el-table-column label="换线" width="90">
        <template #default="{ row }">
          <el-tag v-if="row.switchneeded" type="warning" size="small" effect="dark" disable-transitions>建议换线</el-tag>
          <span v-else class="tx-ok">—</span>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialogVisible" title="新建落地分配" width="500px">
      <el-form label-width="90px">
        <el-form-item label="分配给">
          <el-radio-group v-model="form.scopeMode">
            <el-radio-button value="entry">入口节点</el-radio-button>
            <el-radio-button value="region">区域整体</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="form.scopeMode === 'entry'" label="入口">
          <el-select v-model="form.entryNodeId" style="width: 100%" placeholder="选择入口节点">
            <el-option v-for="n in entries" :key="n.id" :value="n.id" :label="`${n.name}（${n.region}）`" />
          </el-select>
        </el-form-item>
        <el-form-item v-else label="区域">
          <el-select v-model="form.region" style="width: 100%" placeholder="选择或输入区域" filterable allow-create>
            <el-option v-for="rg in regions" :key="rg" :value="rg" :label="rg" />
          </el-select>
        </el-form-item>
        <el-form-item label="落地">
          <el-select v-model="form.landingNodeId" style="width: 100%" placeholder="选择落地节点">
            <el-option v-for="n in landings" :key="n.id" :value="n.id" :label="`${n.name}（${n.region}）`" />
          </el-select>
        </el-form-item>
        <el-form-item label="方向">
          <el-radio-group v-model="form.direction">
            <el-radio-button value="out">出海</el-radio-button>
            <el-radio-button value="in">回国</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="权重">
          <el-input-number v-model="form.weight" :min="0" :max="1000" />
        </el-form-item>
        <el-form-item label="缘由">
          <el-input v-model="form.reason" maxlength="64" placeholder="留痕用，如：晚高峰切优质线" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="dialogVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">分配</el-button>
      </template>
    </el-dialog>

    <!-- Scheduling explanation dialog (F-1431): live per-entry decisions with candidates and exclusion reasons -->
    <el-dialog v-model="explainVisible" title="调度解释" width="760px">
      <div v-loading="explainBusy" class="explain-head">
        <span v-if="explainRoundAt">回合时间：{{ new Date(explainRoundAt).toLocaleString() }}</span>
        <span v-if="explainManual.length" class="hint" style="margin-left: 12px">
          手动接管入口：{{ explainManual.join('、') }}（不参与自动调度）
        </span>
      </div>
      <el-table :data="explainDecisions" size="small" empty-text="暂无可调度入口" row-key="entry_id">
        <el-table-column type="expand">
          <template #default="{ row }">
            <div class="explain-detail">
              <div class="explain-line">
                当前落地：<b>{{ row.current || '—' }}</b>
                <template v-if="row.to">· 目标：<b>{{ row.to }}</b></template>
                <template v-if="row.winner_id">· 胜选：{{ row.to || `#${row.winner_id}` }}</template>
                <template v-if="row.reason">· 原因：{{ row.reason }}</template>
              </div>
              <div class="explain-sub">候选（{{ row.candidates.length }}）</div>
              <el-table :data="row.candidates" size="small">
                <el-table-column prop="name" label="落地" min-width="120" />
                <el-table-column label="参评" width="80">
                  <template #default="{ row: c }">
                    <el-tag size="small" :type="c.eligible ? 'success' : 'danger'" disable-transitions>{{ c.eligible ? '参评' : '淘汰' }}</el-tag>
                  </template>
                </el-table-column>
                <el-table-column label="质量分" width="90">
                  <template #default="{ row: c }">{{ c.quality_score.toFixed(3) }}</template>
                </el-table-column>
                <el-table-column prop="reason" label="淘汰原因" min-width="160">
                  <template #default="{ row: c }">{{ c.reason || '—' }}</template>
                </el-table-column>
              </el-table>
              <div class="explain-sub">预先排除（{{ row.exclusions.length }}）</div>
              <el-table :data="row.exclusions" size="small" empty-text="无预先排除">
                <el-table-column prop="name" label="落地" min-width="120" />
                <el-table-column prop="reason" label="排除原因" min-width="220" />
              </el-table>
            </div>
          </template>
        </el-table-column>
        <el-table-column prop="entry_name" label="入口" min-width="120" />
        <el-table-column label="方向" width="80">
          <template #default="{ row }">{{ DIR_TEXT[row.direction] ?? row.direction }}</template>
        </el-table-column>
        <el-table-column prop="policy" label="策略" width="100" />
        <el-table-column label="动作" width="100">
          <template #default="{ row }">{{ EXPLAIN_ACTION[row.action] ?? row.action }}</template>
        </el-table-column>
        <el-table-column label="候选数" width="80">
          <template #default="{ row }">{{ row.candidates.length }}</template>
        </el-table-column>
      </el-table>
      <template #footer>
        <el-button :loading="explainBusy" @click="openExplain">刷新</el-button>
        <el-button @click="explainVisible = false">关闭</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.explain-head {
  margin-bottom: 10px;
  font-size: 13px;
}
.explain-detail {
  padding: 4px 12px;
}
.explain-line {
  margin-bottom: 8px;
  font-size: 13px;
}
.explain-sub {
  margin: 8px 0 4px;
  font-weight: 600;
  font-size: 12px;
}
.toolbar {
  margin-bottom: 16px;
  display: flex;
  gap: 12px;
  align-items: center;
}
.toolbar .el-button--primary {
  margin-left: auto;
}
.section {
  margin: 28px 0 4px;
  font-size: 15px;
  font-weight: 650;
}
.policy-bar {
  display: flex;
  gap: 20px;
  align-items: center;
  margin-bottom: 8px;
}
.policy-item {
  display: inline-flex;
  gap: 8px;
  align-items: center;
  color: var(--ferry-text-muted);
  font-size: 13px;
}
.section-desc {
  margin: 0 0 12px;
  color: var(--ferry-text-muted);
  font-size: 12px;
}
.tx-ok {
  color: var(--ferry-text-muted);
}
</style>
