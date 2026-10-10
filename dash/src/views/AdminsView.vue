<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  getAdmins, createAdmin, updateAdmin, demoteAdmin,
  type AdminRow,
} from '../api'

// 多级管理员/子管理员配额（P2-2）：super 管理子管理员（operator）名册——
// 建号（密码+建用户配额）、改密/启停/提额、降级（摘身份不删行）。
// super 账号本体不可经此面改动（防自锁）。

const loading = ref(false)
const rows = ref<AdminRow[]>([])

async function load() {
  loading.value = true
  try {
    rows.value = await getAdmins()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    loading.value = false
  }
}
onMounted(load)

const ROLE_TAG: Record<string, { text: string; type: 'danger' | 'warning' }> = {
  super: { text: '超级', type: 'danger' },
  operator: { text: '子管理员', type: 'warning' },
}

// 新建子管理员
const creating = ref(false)
const showCreate = ref(false)
const form = ref({ username: '', password: '', maxUsers: 0 })

async function submitCreate() {
  if (!form.value.username.trim()) {
    ElMessage.warning('用户名不能为空')
    return
  }
  if (form.value.password.length < 8) {
    ElMessage.warning('密码至少 8 位')
    return
  }
  creating.value = true
  try {
    await createAdmin(form.value.username.trim(), form.value.password, form.value.maxUsers)
    ElMessage.success('子管理员已创建')
    showCreate.value = false
    form.value = { username: '', password: '', maxUsers: 0 }
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    creating.value = false
  }
}

// 编辑：密码留空不改；配额 0=不限
const editing = ref(false)
const showEdit = ref(false)
const editRow = ref<AdminRow | null>(null)
const editForm = ref({ password: '', enabled: true, maxUsers: 0 })

function openEdit(row: AdminRow) {
  editRow.value = row
  editForm.value = { password: '', enabled: row.enabled, maxUsers: row.max_users }
  showEdit.value = true
}

async function submitEdit() {
  if (!editRow.value) return
  if (editForm.value.password && editForm.value.password.length < 8) {
    ElMessage.warning('密码至少 8 位')
    return
  }
  editing.value = true
  try {
    await updateAdmin(editRow.value.id, {
      ...(editForm.value.password ? { password: editForm.value.password } : {}),
      enabled: editForm.value.enabled,
      max_users: editForm.value.maxUsers,
    })
    ElMessage.success('已保存')
    showEdit.value = false
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  } finally {
    editing.value = false
  }
}

async function demote(row: AdminRow) {
  try {
    await ElMessageBox.confirm(
      `降级子管理员「${row.username}」？摘管理员身份（保留用户行，名下数据不动）。`,
      '降级',
      { confirmButtonText: '降级', cancelButtonText: '取消', type: 'warning' },
    )
  } catch {
    return
  }
  try {
    await demoteAdmin(row.id)
    ElMessage.success('已降级')
    await load()
  } catch (e) {
    ElMessage.error(String(e))
  }
}
</script>

<template>
  <div class="page">
    <h2>管理员</h2>
    <p class="page-desc">
      两级管理员（对齐 Marzban is_sudo）：超级管理员（bootstrap 建号）管理本子管理员名册；子管理员为管理员用户，受建用户配额约束（0=不限）。
    </p>
    <div class="toolbar">
      <el-button type="primary" @click="showCreate = true">新建子管理员</el-button>
      <el-button @click="load" :loading="loading">刷新</el-button>
    </div>
    <el-table :data="rows" v-loading="loading" :header-cell-style="{ background: 'var(--ferry-bg-panel)' }">
      <el-table-column prop="username" label="用户名" min-width="140" />
      <el-table-column label="层级" width="110">
        <template #default="{ row }">
          <el-tag :type="ROLE_TAG[row.role]?.type ?? 'info'" size="small" effect="dark">
            {{ ROLE_TAG[row.role]?.text ?? row.role }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="建用户配额" width="120">
        <template #default="{ row }">
          <span v-if="row.max_users > 0">{{ row.max_users }}</span>
          <span v-else class="muted">不限</span>
        </template>
      </el-table-column>
      <el-table-column label="2FA" width="80">
        <template #default="{ row }">
          <el-tag :type="row.totp_enabled ? 'success' : 'info'" size="small" effect="plain">
            {{ row.totp_enabled ? '已绑' : '未绑' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag :type="row.enabled ? 'success' : 'danger'" size="small" effect="plain">
            {{ row.enabled ? '启用' : '停用' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="created_by" label="创建者" width="120">
        <template #default="{ row }">
          <span v-if="row.created_by">{{ row.created_by }}</span>
          <span v-else class="muted">—</span>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="150">
        <template #default="{ row }">
          <el-button size="small" plain @click="openEdit(row)">编辑</el-button>
          <el-button
            v-if="row.role !== 'super'"
            size="small"
            type="danger"
            plain
            @click="demote(row)"
          >降级</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="showCreate" title="新建子管理员" width="420px">
      <el-form label-width="110px">
        <el-form-item label="用户名">
          <el-input v-model="form.username" placeholder="1-64 字符" />
        </el-form-item>
        <el-form-item label="密码">
          <el-input v-model="form.password" type="password" show-password placeholder="至少 8 位" />
        </el-form-item>
        <el-form-item label="建用户配额">
          <el-input-number v-model="form.maxUsers" :min="0" />
          <span class="hint">0=不限</span>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showCreate = false">取消</el-button>
        <el-button type="primary" :loading="creating" @click="submitCreate">创建</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="showEdit" title="编辑子管理员" width="420px">
      <el-form label-width="110px">
        <el-form-item label="用户名">
          <span class="static">{{ editRow?.username }}</span>
        </el-form-item>
        <el-form-item label="新密码">
          <el-input v-model="editForm.password" type="password" show-password placeholder="留空不改" />
        </el-form-item>
        <el-form-item label="建用户配额">
          <el-input-number v-model="editForm.maxUsers" :min="0" />
          <span class="hint">0=不限</span>
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="editForm.enabled" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="showEdit = false">取消</el-button>
        <el-button type="primary" :loading="editing" @click="submitEdit">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<style scoped>
.hint {
  margin-left: 10px;
  color: var(--ferry-text-muted, #909399);
  font-size: 12px;
}
.muted {
  color: var(--ferry-text-muted, #909399);
}
.static {
  font-weight: 600;
}
</style>
