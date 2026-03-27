<script setup>
import { ref, computed, watch, onMounted, h } from 'vue'
import { useRouter } from 'vue-router'
import { useWebSocket } from '../composables/useWebSocket'
import { useI18n } from '../composables/useI18n'
import { useMessage } from '../composables/useMessage'
import { showPrompt, showConfirm } from '../composables/useNativeDialog'
import { PERM_READ, PERM_UPLOAD, PERM_EDIT, PERM_DELETE } from '../stores/auth'
import dayjs from 'dayjs'
import BButton from '../components/breeze/BButton.vue'
import BModal from '../components/breeze/BModal.vue'
import BForm from '../components/breeze/BForm.vue'
import BFormItem from '../components/breeze/BFormItem.vue'
import BInput from '../components/breeze/BInput.vue'
import BSelect from '../components/breeze/BSelect.vue'
import BCheckbox from '../components/breeze/BCheckbox.vue'
import BDataTable from '../components/breeze/BDataTable.vue'

const router = useRouter()
const ws = useWebSocket()
const { t, te } = useI18n()
const message = useMessage()

const users = ref([])
const loading = ref(false)
const showCreate = ref(false)
const showEdit = ref(false)
const editingUser = ref(null)

const defaultPerms = { admin: 15, user: 15 }
const newUser = ref({ username: '', password: '', role: 'user', permissions: 15 })

const roleOptions = [
  { label: 'Root', value: 'root' },
  { label: 'User', value: 'user' },
]

watch(() => newUser.value.role, (role) => {
  newUser.value.permissions = defaultPerms[role] ?? 15
})

watch(() => editingUser.value?.role, (role, oldRole) => {
  if (role && oldRole && role !== oldRole && editingUser.value) {
    editingUser.value.permissions = defaultPerms[role] ?? 1
  }
})

function permLabels(perms) {
  const parts = []
  if (perms & PERM_READ) parts.push('R')
  if (perms & PERM_UPLOAD) parts.push('U')
  if (perms & PERM_EDIT) parts.push('E')
  if (perms & PERM_DELETE) parts.push('D')
  return parts.join('+')
}

const columns = computed(() => [
  { title: t('admin.username'), key: 'username', minWidth: 100 },
  { title: t('admin.role'), key: 'role', width: 80 },
  {
    title: t('admin.permissions'),
    key: 'permissions',
    width: 100,
    render: (row) => permLabels(row.permissions),
  },
  {
    title: t('admin.email'),
    key: 'email',
    width: 160,
    render: (row) => row.email || '---',
  },
  {
    title: t('admin.otp'),
    key: 'totp_enabled',
    width: 80,
    render: (row) => row.totp_enabled ? t('admin.otp_enabled') : '---',
  },
  {
    title: t('admin.created'),
    key: 'created_at',
    width: 150,
    render: (row) => row.created_at ? dayjs(row.created_at).format('YYYY-MM-DD HH:mm') : '---',
  },
  {
    title: t('admin.actions'),
    key: 'actions',
    width: 280,
    render(row) {
      const buttons = [
        h(BButton, { size: 'tiny', onClick: () => openEdit(row) }, () => t('admin.edit')),
        h(BButton, { size: 'tiny', onClick: () => resetPassword(row) }, () => t('admin.reset_pwd')),
        h(BButton, { size: 'tiny', type: 'error', onClick: () => deleteUser(row) }, () => t('admin.delete')),
      ]
      if (row.totp_enabled) {
        buttons.push(h(BButton, { size: 'tiny', type: 'warning', onClick: () => resetOTP(row) }, () => t('admin.reset_otp')))
      }
      if (row.email) {
        buttons.push(h(BButton, { size: 'tiny', type: 'warning', onClick: () => resetEmail(row) }, () => t('admin.reset_email')))
      }
      return h('div', { class: 'action-buttons' }, buttons)
    },
  },
])

function openEdit(user) {
  editingUser.value = {
    id: user.id,
    username: user.username,
    role: user.role,
    permissions: user.permissions,
  }
  showEdit.value = true
}

async function loadUsers() {
  loading.value = true
  try {
    const data = await ws.request('admin.listUsers')
    users.value = Array.isArray(data) ? data : []
  } catch {
    message.error('Failed to load users')
  } finally {
    loading.value = false
  }
}

async function createUser() {
  try {
    await ws.request('admin.createUser', newUser.value)
    message.success(t('admin.user_created'))
    showCreate.value = false
    newUser.value = { username: '', password: '', role: 'user', permissions: 15 }
    await loadUsers()
  } catch (e) {
    message.error(e.error || 'Failed')
  }
}

async function saveEdit() {
  if (!editingUser.value) return
  try {
    await ws.request('admin.updateUser', {
      id: editingUser.value.id,
      role: editingUser.value.role,
      permissions: editingUser.value.permissions,
    })
    message.success(t('admin.user_updated'))
    showEdit.value = false
    editingUser.value = null
    await loadUsers()
  } catch (e) {
    message.error(e.error || 'Failed')
  }
}

async function resetPassword(user) {
  const password = await showPrompt(t('admin.new_password', { name: user.username }))
  if (!password) return
  try {
    await ws.request('admin.updateUser', { id: user.id, password })
    message.success(t('admin.pwd_updated'))
  } catch {
    message.error('Failed to reset password')
  }
}

async function deleteUser(user) {
  if (!await showConfirm(t('admin.confirm_delete', { name: user.username }))) return
  try {
    await ws.request('admin.deleteUser', { id: user.id })
    message.success(t('admin.user_deleted'))
    await loadUsers()
  } catch (e) {
    message.error(e.error || 'Failed')
  }
}

async function resetOTP(user) {
  if (!await showConfirm(t('admin.confirm_reset_otp', { name: user.username }))) return
  try {
    await ws.request('admin.resetUserOTP', { id: user.id })
    message.success(t('admin.otp_reset'))
    await loadUsers()
  } catch {
    message.error('Failed to reset OTP')
  }
}

async function resetEmail(user) {
  if (!await showConfirm(t('admin.confirm_reset_email', { name: user.username }))) return
  try {
    await ws.request('admin.resetUserEmail', { id: user.id })
    message.success(t('admin.email_reset'))
    await loadUsers()
  } catch {
    message.error('Failed to reset email')
  }
}

onMounted(loadUsers)
</script>

<template>
  <div class="admin-view">
    <div class="admin-header">
      <h1>{{ t('admin.title') }}</h1>
      <div style="display:flex;gap:8px">
        <BButton type="primary" size="small" @click="showCreate = true">
          {{ t('admin.add_user') }}
        </BButton>
        <BButton size="small" @click="router.push('/')">
          {{ t('admin.back_to_files') }}
        </BButton>
      </div>
    </div>

    <BDataTable
      :columns="columns"
      :data="users"
      :loading="loading"
      :row-key="(row) => row.id"
      size="small"
    />

    <!-- Create User Dialog -->
    <BModal :show="showCreate" preset="dialog" :title="t('admin.create_user')" @close="showCreate = false" @mask-click="showCreate = false">
      <BForm>
        <BFormItem :label="t('admin.username')">
          <BInput :value="newUser.username" placeholder="" @update:value="v => newUser.username = v" />
        </BFormItem>
        <BFormItem :label="t('admin.password')">
          <BInput :value="newUser.password" type="password" placeholder="" @update:value="v => newUser.password = v" />
        </BFormItem>
        <BFormItem :label="t('admin.role')">
          <BSelect v-model:value="newUser.role" :options="roleOptions" />
        </BFormItem>
        <BFormItem :label="t('admin.permissions')">
          <div style="display:flex;flex-wrap:wrap;gap:8px">
            <BCheckbox
              :checked="(newUser.permissions & PERM_READ) !== 0"
              @update:checked="v => newUser.permissions = v ? (newUser.permissions | PERM_READ) : (newUser.permissions & ~PERM_READ)"
            >{{ t('admin.perm_read') }}</BCheckbox>
            <BCheckbox
              :checked="(newUser.permissions & PERM_UPLOAD) !== 0"
              @update:checked="v => newUser.permissions = v ? (newUser.permissions | PERM_UPLOAD) : (newUser.permissions & ~PERM_UPLOAD)"
            >{{ t('admin.perm_upload') }}</BCheckbox>
            <BCheckbox
              :checked="(newUser.permissions & PERM_EDIT) !== 0"
              @update:checked="v => newUser.permissions = v ? (newUser.permissions | PERM_EDIT) : (newUser.permissions & ~PERM_EDIT)"
            >{{ t('admin.perm_edit') }}</BCheckbox>
            <BCheckbox
              :checked="(newUser.permissions & PERM_DELETE) !== 0"
              @update:checked="v => newUser.permissions = v ? (newUser.permissions | PERM_DELETE) : (newUser.permissions & ~PERM_DELETE)"
            >{{ t('admin.perm_delete') }}</BCheckbox>
          </div>
        </BFormItem>
        <BButton type="primary" block @click="createUser">{{ t('admin.create') }}</BButton>
      </BForm>
    </BModal>

    <!-- Edit User Dialog -->
    <BModal :show="showEdit" preset="dialog" :title="t('admin.edit_user')" @close="showEdit = false" @mask-click="showEdit = false">
      <BForm v-if="editingUser">
        <BFormItem :label="t('admin.username')">
          <BInput :value="editingUser.username" disabled />
        </BFormItem>
        <BFormItem :label="t('admin.role')">
          <BSelect v-model:value="editingUser.role" :options="roleOptions" />
        </BFormItem>
        <BFormItem :label="t('admin.permissions')">
          <div style="display:flex;flex-wrap:wrap;gap:8px">
            <BCheckbox
              :checked="(editingUser.permissions & PERM_READ) !== 0"
              @update:checked="v => editingUser.permissions = v ? (editingUser.permissions | PERM_READ) : (editingUser.permissions & ~PERM_READ)"
            >{{ t('admin.perm_read') }}</BCheckbox>
            <BCheckbox
              :checked="(editingUser.permissions & PERM_UPLOAD) !== 0"
              @update:checked="v => editingUser.permissions = v ? (editingUser.permissions | PERM_UPLOAD) : (editingUser.permissions & ~PERM_UPLOAD)"
            >{{ t('admin.perm_upload') }}</BCheckbox>
            <BCheckbox
              :checked="(editingUser.permissions & PERM_EDIT) !== 0"
              @update:checked="v => editingUser.permissions = v ? (editingUser.permissions | PERM_EDIT) : (editingUser.permissions & ~PERM_EDIT)"
            >{{ t('admin.perm_edit') }}</BCheckbox>
            <BCheckbox
              :checked="(editingUser.permissions & PERM_DELETE) !== 0"
              @update:checked="v => editingUser.permissions = v ? (editingUser.permissions | PERM_DELETE) : (editingUser.permissions & ~PERM_DELETE)"
            >{{ t('admin.perm_delete') }}</BCheckbox>
          </div>
        </BFormItem>
        <BButton type="primary" block @click="saveEdit">{{ t('admin.save') }}</BButton>
      </BForm>
    </BModal>
  </div>
</template>

<style scoped>
.admin-view {
  max-width: 1000px;
  margin: 0 auto;
  padding: 32px 24px;
  min-height: 100vh;
  background: var(--breeze-bg);
}
.admin-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 20px;
}
.admin-header h1 {
  font-size: 22px;
  font-weight: 600;
  margin: 0;
  color: var(--breeze-text);
}
:deep(.action-buttons) {
  display: flex;
  gap: 4px;
  flex-wrap: wrap;
}
</style>
