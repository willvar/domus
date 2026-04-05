<script setup lang="ts">
import { ref, computed, onMounted, h } from 'vue'
import { useRouter } from 'vue-router'
import { useWebSocket } from '../composables/useWebSocket'
import { useI18n } from '../composables/useI18n'
import { useMessage } from '../composables/useMessage'
import { showPrompt, showConfirm } from '../composables/useNativeDialog'
import dayjs from 'dayjs'
import { Button, Modal, Form, FormItem, Input, Select, DataTable } from '../barrels/breeze'

const router = useRouter()
const ws = useWebSocket()
const { t } = useI18n()
const message = useMessage()

const users = ref<any[]>([])
const loading = ref(false)
const showCreate = ref(false)
const showEdit = ref(false)
const editingUser = ref<{ id: string; username: string; role: string } | null>(null)

const newUser = ref({ username: '', password: '', role: 'user' })

const roleOptions = [
  { label: 'Root', value: 'root' },
  { label: 'User', value: 'user' },
]

const columns = computed(() => [
  { title: t('admin.username'), key: 'username', minWidth: 100 },
  { title: t('admin.role'), key: 'role', width: 80 },
  {
    title: t('admin.email'),
    key: 'email',
    width: 160,
    render: (row: any) => row.email || '---',
  },
  {
    title: t('admin.otp'),
    key: 'totp_enabled',
    width: 80,
    render: (row: any) => row.totp_enabled ? t('admin.otp_enabled') : '---',
  },
  {
    title: t('admin.created'),
    key: 'created_at',
    width: 150,
    render: (row: any) => row.created_at ? dayjs(row.created_at).format('YYYY-MM-DD HH:mm') : '---',
  },
  {
    title: t('admin.actions'),
    key: 'actions',
    width: 280,
    render(row: any) {
      const buttons = [
        h(Button, { size: 'tiny', onClick: () => openEdit(row) }, () => t('admin.edit')),
        h(Button, { size: 'tiny', onClick: () => resetPassword(row) }, () => t('admin.reset_pwd')),
        h(Button, { size: 'tiny', type: 'error', onClick: () => deleteUser(row) }, () => t('admin.delete')),
      ]
      if (row.totp_enabled) {
        buttons.push(h(Button, { size: 'tiny', type: 'warning', onClick: () => resetOTP(row) }, () => t('admin.reset_otp')))
      }
      if (row.email) {
        buttons.push(h(Button, { size: 'tiny', type: 'warning', onClick: () => resetEmail(row) }, () => t('admin.reset_email')))
      }
      return h('div', { class: 'action-buttons' }, buttons)
    },
  },
])

function openEdit(user: any) {
  editingUser.value = {
    id: user.id,
    username: user.username,
    role: user.role,
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
    newUser.value = { username: '', password: '', role: 'user' }
    await loadUsers()
  } catch (e: any) {
    message.error(e.error || 'Failed')
  }
}

async function saveEdit() {
  if (!editingUser.value) return
  try {
    await ws.request('admin.updateUser', {
      id: editingUser.value.id,
      role: editingUser.value.role,
    })
    message.success(t('admin.user_updated'))
    showEdit.value = false
    editingUser.value = null
    await loadUsers()
  } catch (e: any) {
    message.error(e.error || 'Failed')
  }
}

async function resetPassword(user: any) {
  const password = await showPrompt(t('admin.new_password', { name: user.username }))
  if (!password) return
  try {
    await ws.request('admin.updateUser', { id: user.id, password })
    message.success(t('admin.pwd_updated'))
  } catch {
    message.error('Failed to reset password')
  }
}

async function deleteUser(user: any) {
  if (!await showConfirm(t('admin.confirm_delete', { name: user.username }))) return
  try {
    await ws.request('admin.deleteUser', { id: user.id })
    message.success(t('admin.user_deleted'))
    await loadUsers()
  } catch (e: any) {
    message.error(e.error || 'Failed')
  }
}

async function resetOTP(user: any) {
  if (!await showConfirm(t('admin.confirm_reset_otp', { name: user.username }))) return
  try {
    await ws.request('admin.resetUserOTP', { id: user.id })
    message.success(t('admin.otp_reset'))
    await loadUsers()
  } catch {
    message.error('Failed to reset OTP')
  }
}

async function resetEmail(user: any) {
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
        <Button type="primary" size="small" @click="showCreate = true">
          {{ t('admin.add_user') }}
        </Button>
        <Button size="small" @click="router.push('/')">
          {{ t('admin.back_to_files') }}
        </Button>
      </div>
    </div>

    <DataTable
      :columns="columns"
      :data="users"
      :loading="loading"
      :row-key="(row) => row.id"
      size="small"
    />

    <!-- Create User Dialog -->
    <Modal :show="showCreate" preset="dialog" :title="t('admin.create_user')" @close="showCreate = false" @mask-click="showCreate = false">
      <Form>
        <FormItem :label="t('admin.username')">
          <Input :value="newUser.username" placeholder="" @update:value="v => newUser.username = v" />
        </FormItem>
        <FormItem :label="t('admin.password')">
          <Input :value="newUser.password" type="password" placeholder="" @update:value="v => newUser.password = v" />
        </FormItem>
        <FormItem :label="t('admin.role')">
          <Select v-model:value="newUser.role" :options="roleOptions" />
        </FormItem>
        <Button type="primary" block @click="createUser">{{ t('admin.create') }}</Button>
      </Form>
    </Modal>

    <!-- Edit User Dialog -->
    <Modal :show="showEdit" preset="dialog" :title="t('admin.edit_user')" @close="showEdit = false" @mask-click="showEdit = false">
      <Form v-if="editingUser">
        <FormItem :label="t('admin.username')">
          <Input :value="editingUser.username" disabled />
        </FormItem>
        <FormItem :label="t('admin.role')">
          <Select v-model:value="editingUser.role" :options="roleOptions" />
        </FormItem>
        <Button type="primary" block @click="saveEdit">{{ t('admin.save') }}</Button>
      </Form>
    </Modal>
  </div>
</template>

<style lang="scss" scoped>
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

  h1 {
    font-size: 22px;
    font-weight: 600;
    margin: 0;
    color: var(--breeze-text);
  }
}

:deep(.action-buttons) {
  display: flex;
  gap: 4px;
  flex-wrap: wrap;
}
</style>
