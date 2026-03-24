<script setup>
import { ref, computed, watch, onMounted, h } from 'vue'
import { NDataTable, NButton, NForm, NFormItem, NInput, NSelect, NCheckbox, NSpace, NModal, useMessage } from 'naive-ui'
import { useRouter } from 'vue-router'
import api from '../composables/useApi'
import { useI18n } from '../composables/useI18n'
import { showPrompt, showConfirm } from '../composables/useNativeDialog'
import { PERM_READ, PERM_UPLOAD, PERM_EDIT, PERM_DELETE } from '../stores/auth'
import dayjs from 'dayjs'

const router = useRouter()
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
  { label: 'Admin', value: 'admin' },
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
        h(NButton, { size: 'tiny', onClick: () => openEdit(row) }, () => t('admin.edit')),
        h(NButton, { size: 'tiny', onClick: () => resetPassword(row) }, () => t('admin.reset_pwd')),
        h(NButton, { size: 'tiny', type: 'error', onClick: () => deleteUser(row) }, () => t('admin.delete')),
      ]
      if (row.totp_enabled) {
        buttons.push(h(NButton, { size: 'tiny', type: 'warning', onClick: () => resetOTP(row) }, () => t('admin.reset_otp')))
      }
      if (row.email) {
        buttons.push(h(NButton, { size: 'tiny', type: 'warning', onClick: () => resetEmail(row) }, () => t('admin.reset_email')))
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
    const res = await api.get('/audit/user')
    users.value = Array.isArray(res.data) ? res.data : []
  } catch {
    message.error('Failed to load users')
  } finally {
    loading.value = false
  }
}

async function createUser() {
  try {
    await api.post('/audit/user', newUser.value)
    message.success(t('admin.user_created'))
    showCreate.value = false
    newUser.value = { username: '', password: '', role: 'user', permissions: 15 }
    await loadUsers()
  } catch (e) {
    message.error(te(e))
  }
}

async function saveEdit() {
  if (!editingUser.value) return
  try {
    await api.put(`/audit/user/${editingUser.value.id}`, {
      role: editingUser.value.role,
      permissions: editingUser.value.permissions,
    })
    message.success(t('admin.user_updated'))
    showEdit.value = false
    editingUser.value = null
    await loadUsers()
  } catch (e) {
    message.error(te(e))
  }
}

async function resetPassword(user) {
  const password = await showPrompt(t('admin.new_password', { name: user.username }))
  if (!password) return
  try {
    await api.put(`/audit/user/${user.id}`, { password })
    message.success(t('admin.pwd_updated'))
  } catch {
    message.error('Failed to reset password')
  }
}

async function deleteUser(user) {
  if (!await showConfirm(t('admin.confirm_delete', { name: user.username }))) return
  try {
    await api.delete(`/audit/user/${user.id}`)
    message.success(t('admin.user_deleted'))
    await loadUsers()
  } catch (e) {
    message.error(te(e))
  }
}

async function resetOTP(user) {
  if (!await showConfirm(t('admin.confirm_reset_otp', { name: user.username }))) return
  try {
    await api.delete(`/audit/user/${user.id}/otp`)
    message.success(t('admin.otp_reset'))
    await loadUsers()
  } catch {
    message.error('Failed to reset OTP')
  }
}

async function resetEmail(user) {
  if (!await showConfirm(t('admin.confirm_reset_email', { name: user.username }))) return
  try {
    await api.delete(`/audit/user/${user.id}/email`)
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
      <NSpace>
        <NButton type="primary" size="small" @click="showCreate = true">
          {{ t('admin.add_user') }}
        </NButton>
        <NButton size="small" @click="router.push('/')">
          {{ t('admin.back_to_files') }}
        </NButton>
      </NSpace>
    </div>

    <NDataTable
      :columns="columns"
      :data="users"
      :loading="loading"
      :row-key="(row) => row.id"
      size="small"
    />

    <!-- Create User Dialog -->
    <NModal v-model:show="showCreate" preset="dialog" :title="t('admin.create_user')">
      <NForm>
        <NFormItem :label="t('admin.username')">
          <NInput v-model:value="newUser.username" placeholder="" />
        </NFormItem>
        <NFormItem :label="t('admin.password')">
          <NInput v-model:value="newUser.password" type="password" placeholder="" />
        </NFormItem>
        <NFormItem :label="t('admin.role')">
          <NSelect v-model:value="newUser.role" :options="roleOptions" />
        </NFormItem>
        <NFormItem :label="t('admin.permissions')">
          <NSpace>
            <NCheckbox
              :checked="(newUser.permissions & PERM_READ) !== 0"
              @update:checked="v => newUser.permissions = v ? (newUser.permissions | PERM_READ) : (newUser.permissions & ~PERM_READ)"
            >{{ t('admin.perm_read') }}</NCheckbox>
            <NCheckbox
              :checked="(newUser.permissions & PERM_UPLOAD) !== 0"
              @update:checked="v => newUser.permissions = v ? (newUser.permissions | PERM_UPLOAD) : (newUser.permissions & ~PERM_UPLOAD)"
            >{{ t('admin.perm_upload') }}</NCheckbox>
            <NCheckbox
              :checked="(newUser.permissions & PERM_EDIT) !== 0"
              @update:checked="v => newUser.permissions = v ? (newUser.permissions | PERM_EDIT) : (newUser.permissions & ~PERM_EDIT)"
            >{{ t('admin.perm_edit') }}</NCheckbox>
            <NCheckbox
              :checked="(newUser.permissions & PERM_DELETE) !== 0"
              @update:checked="v => newUser.permissions = v ? (newUser.permissions | PERM_DELETE) : (newUser.permissions & ~PERM_DELETE)"
            >{{ t('admin.perm_delete') }}</NCheckbox>
          </NSpace>
        </NFormItem>
        <NButton type="primary" block @click="createUser">{{ t('admin.create') }}</NButton>
      </NForm>
    </NModal>

    <!-- Edit User Dialog -->
    <NModal v-model:show="showEdit" preset="dialog" :title="t('admin.edit_user')">
      <NForm v-if="editingUser">
        <NFormItem :label="t('admin.username')">
          <NInput :value="editingUser.username" disabled />
        </NFormItem>
        <NFormItem :label="t('admin.role')">
          <NSelect v-model:value="editingUser.role" :options="roleOptions" />
        </NFormItem>
        <NFormItem :label="t('admin.permissions')">
          <NSpace>
            <NCheckbox
              :checked="(editingUser.permissions & PERM_READ) !== 0"
              @update:checked="v => editingUser.permissions = v ? (editingUser.permissions | PERM_READ) : (editingUser.permissions & ~PERM_READ)"
            >{{ t('admin.perm_read') }}</NCheckbox>
            <NCheckbox
              :checked="(editingUser.permissions & PERM_UPLOAD) !== 0"
              @update:checked="v => editingUser.permissions = v ? (editingUser.permissions | PERM_UPLOAD) : (editingUser.permissions & ~PERM_UPLOAD)"
            >{{ t('admin.perm_upload') }}</NCheckbox>
            <NCheckbox
              :checked="(editingUser.permissions & PERM_EDIT) !== 0"
              @update:checked="v => editingUser.permissions = v ? (editingUser.permissions | PERM_EDIT) : (editingUser.permissions & ~PERM_EDIT)"
            >{{ t('admin.perm_edit') }}</NCheckbox>
            <NCheckbox
              :checked="(editingUser.permissions & PERM_DELETE) !== 0"
              @update:checked="v => editingUser.permissions = v ? (editingUser.permissions | PERM_DELETE) : (editingUser.permissions & ~PERM_DELETE)"
            >{{ t('admin.perm_delete') }}</NCheckbox>
          </NSpace>
        </NFormItem>
        <NButton type="primary" block @click="saveEdit">{{ t('admin.save') }}</NButton>
      </NForm>
    </NModal>
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
