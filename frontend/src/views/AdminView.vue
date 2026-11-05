<script setup lang="ts">
import { ref, computed, onMounted, h } from 'vue'
import {
  NAlert,
  NAvatar,
  NButton,
  NDataTable,
  NDropdown,
  NForm,
  NFormItem,
  NInput,
  NModal,
  NSelect,
  NTag,
} from 'naive-ui'
import type { DataTableColumns, DropdownDividerOption, DropdownOption } from 'naive-ui'
import { useRouter } from 'vue-router'
import { useI18n } from '../composables/useI18n'
import { useAppMessage } from '../ui/feedback'
import { showPrompt, showConfirm } from '../composables/useNativeDialog'
import api from '../composables/useApi'
import dayjs from 'dayjs'
import { IconArrowLeft, IconClose, IconDotsHorizontal, IconPlus } from '../barrels/icons'

const router = useRouter()
const { t, te } = useI18n()
const message = useAppMessage()
const props = withDefaults(defineProps<{ embedded?: boolean }>(), { embedded: false })
const emit = defineEmits<{ close: []; 'menu-toggle': [open: boolean] }>()
const openUserMenus = new Set<string>()

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

const otpProtectedCount = computed(() => users.value.filter(user => user.totp_enabled).length)
const emailLinkedCount = computed(() => users.value.filter(user => Boolean(user.email)).length)

function closeAdmin(): void {
  if (props.embedded) {
    emit('close')
    return
  }
  void router.push('/')
}

function updateUserMenu(show: boolean, id: string): void {
  if (show) openUserMenus.add(id)
  else openUserMenus.delete(id)
  emit('menu-toggle', openUserMenus.size > 0)
}

function userActions(row: any): Array<DropdownOption | DropdownDividerOption> {
  const options: Array<DropdownOption | DropdownDividerOption> = [
    { label: t('admin.reset_pwd'), key: 'password' },
  ]
  if (row.totp_enabled) options.push({ label: t('admin.reset_otp'), key: 'otp' })
  if (row.email) options.push({ label: t('admin.reset_email'), key: 'email' })
  options.push(
    { type: 'divider', key: 'danger' },
    { label: t('admin.delete'), key: 'delete', props: { style: { color: 'var(--domus-danger)' } } },
  )
  return options
}

function handleUserAction(key: string | number, row: any): void {
  switch (key) {
    case 'password': void resetPassword(row); break
    case 'otp': void resetOTP(row); break
    case 'email': void resetEmail(row); break
    case 'delete': void deleteUser(row); break
  }
}

const columns = computed<DataTableColumns<any>>(() => [
  {
    title: t('admin.username'),
    key: 'username',
    width: 220,
    render: (row: any) => h('div', { class: 'user-cell' }, [
      h(NAvatar, { round: true, size: 30, class: 'user-avatar' }, () => String(row.username || '?').slice(0, 1).toUpperCase()),
      h('div', {}, [
        h('strong', { title: row.username }, row.username),
        h('small', { title: String(row.id) }, `#${row.id}`),
      ]),
    ]),
  },
  {
    title: t('admin.role'),
    key: 'role',
    width: 80,
    render: (row: any) => h(NTag, { size: 'small', round: true, type: row.role === 'root' ? 'info' : 'default' }, () => row.role),
  },
  {
    title: t('admin.email'),
    key: 'email',
    width: 220,
    ellipsis: { tooltip: true },
    render: (row: any) => row.email || '—',
  },
  {
    title: t('admin.otp'),
    key: 'totp_enabled',
    width: 96,
    render: (row: any) => h(NTag, {
      size: 'small',
      round: true,
      bordered: false,
      type: row.totp_enabled ? 'success' : 'default',
    }, () => row.totp_enabled ? t('admin.otp_enabled') : '—'),
  },
  {
    title: t('admin.created'),
    key: 'created_at',
    width: 160,
    render: (row: any) => row.created_at ? dayjs(row.created_at).format('YYYY-MM-DD HH:mm') : '---',
  },
  {
    title: t('admin.actions'),
    key: 'actions',
    width: 140,
    fixed: 'right',
    render(row: any) {
      return h('div', { class: 'action-buttons' }, [
        h(NButton, { size: 'small', quaternary: true, onClick: () => openEdit(row) }, () => t('admin.edit')),
        h(NDropdown, {
          trigger: 'click',
          options: userActions(row),
          onUpdateShow: (show: boolean) => updateUserMenu(show, row.id),
          menuProps: () => ({ class: 'user-actions-menu', role: 'menu' }),
          nodeProps: () => ({ role: 'menuitem' }),
          onSelect: (key: string | number) => handleUserAction(key, row),
        }, {
          default: () => h(NButton, {
            size: 'small', quaternary: true, circle: true,
            'aria-label': t('admin.more_actions', { name: row.username }),
          }, { icon: () => h(IconDotsHorizontal) }),
        }),
      ])
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
    const { data } = await api.get('/audit/user/')
    users.value = Array.isArray(data) ? data : []
  } catch (e: any) {
    message.error(te(e, 'admin.load_failed'))
  } finally {
    loading.value = false
  }
}

async function createUser() {
  try {
    await api.post('/audit/user/', newUser.value)
    message.success(t('admin.user_created'))
    showCreate.value = false
    newUser.value = { username: '', password: '', role: 'user' }
    await loadUsers()
  } catch (e: any) {
    message.error(te(e, 'admin.create_failed'))
  }
}

async function saveEdit() {
  if (!editingUser.value) return
  try {
    await api.put('/audit/user/' + editingUser.value.id, {
      role: editingUser.value.role,
    })
    message.success(t('admin.user_updated'))
    showEdit.value = false
    editingUser.value = null
    await loadUsers()
  } catch (e: any) {
    message.error(te(e, 'admin.update_failed'))
  }
}

async function resetPassword(user: any) {
  const password = await showPrompt(t('admin.new_password', { name: user.username }))
  if (!password) return
  try {
    await api.put('/audit/user/' + user.id, { password })
    message.success(t('admin.pwd_updated'))
  } catch (e: any) {
    message.error(te(e, 'admin.password_reset_failed'))
  }
}

async function deleteUser(user: any) {
  if (!await showConfirm(t('admin.confirm_delete', { name: user.username }))) return
  try {
    await api.delete('/audit/user/' + user.id)
    message.success(t('admin.user_deleted'))
    await loadUsers()
  } catch (e: any) {
    message.error(te(e, 'admin.delete_failed'))
  }
}

async function resetOTP(user: any) {
  if (!await showConfirm(t('admin.confirm_reset_otp', { name: user.username }))) return
  try {
    await api.delete('/audit/user/' + user.id + '/otp')
    message.success(t('admin.otp_reset'))
    await loadUsers()
  } catch (e: any) {
    message.error(te(e, 'admin.otp_reset_failed'))
  }
}

async function resetEmail(user: any) {
  if (!await showConfirm(t('admin.confirm_reset_email', { name: user.username }))) return
  try {
    await api.delete('/audit/user/' + user.id + '/email')
    message.success(t('admin.email_reset'))
    await loadUsers()
  } catch (e: any) {
    message.error(te(e, 'admin.email_reset_failed'))
  }
}

// ── CORS probe ──────────────────────────────────────────────────────────────
const corsOk = ref(true) // assume ok until proven otherwise

async function checkOSSCors() {
  try {
    const res = await api.get<{ put_url: string; delete_url: string }>('/admin/oss/cors-check')
    const { put_url, delete_url } = res.data
    const putRes = await fetch(put_url, { method: 'PUT', body: new Blob(['1']) })
    if (!putRes.ok) throw new Error('oss_cors_probe_failed')
    // Clean up probe object
    fetch(delete_url, { method: 'DELETE' }).then(res => {
      if (!res.ok) console.warn('OSS CORS cleanup failed:', res.status)
    }).catch(() => {})
    corsOk.value = true
  } catch {
    corsOk.value = false
  }
}

onMounted(() => {
  loadUsers()
  checkOSSCors()
})
</script>

<template>
  <div class="admin-view" :class="{ 'admin-view--embedded': props.embedded }">
    <div class="admin-panel">
      <header class="admin-toolbar">
        <NButton v-if="!props.embedded" class="admin-back" quaternary circle :aria-label="t('admin.back_to_files')" @click="closeAdmin">
          <template #icon><IconArrowLeft /></template>
        </NButton>
        <div class="admin-heading">
          <h1>{{ t('admin.title') }}</h1>
          <p>{{ t('admin.subtitle') }}</p>
        </div>
        <div class="admin-toolbar__actions">
          <NButton type="primary" @click="showCreate = true">
            <template #icon><IconPlus /></template>
            {{ t('admin.add_user') }}
          </NButton>
          <NButton v-if="props.embedded" quaternary circle :aria-label="t('admin.close')" @click="closeAdmin">
            <template #icon><IconClose /></template>
          </NButton>
        </div>
      </header>

      <NAlert v-if="!corsOk" class="cors-warning" type="error" :title="t('admin.cors_warning_title')">
        {{ t('admin.cors_warning_body') }}
      </NAlert>

      <dl class="admin-stats">
        <div><dt>{{ t('admin.total_users') }}</dt><dd>{{ users.length }}</dd></div>
        <div><dt>{{ t('admin.otp_protected') }}</dt><dd>{{ otpProtectedCount }}</dd></div>
        <div><dt>{{ t('admin.email_linked') }}</dt><dd>{{ emailLinkedCount }}</dd></div>
      </dl>

      <section class="users-table" :aria-label="t('admin.accounts')">
        <NDataTable
          :columns="columns"
          :data="users"
          :loading="loading"
          :row-key="(row) => row.id"
          size="small"
          :bordered="false"
          :scroll-x="916"
          :max-height="props.embedded ? 'min(560px, calc(100dvh - 280px))' : '70dvh'"
        />
      </section>
    </div>

    <!-- Create User Dialog -->
    <NModal
      v-model:show="showCreate"
      preset="card"
      class="admin-dialog"
      :title="t('admin.create_user')"
      :aria-label="t('admin.create_user')"
      :content-style="{ minHeight: '0', overflowY: 'auto' }"
      :bordered="false"
    >
      <NForm @submit.prevent="createUser">
        <NFormItem :label="t('admin.username')">
          <NInput :value="newUser.username" @update:value="v => newUser.username = v" />
        </NFormItem>
        <NFormItem :label="t('admin.password')">
          <NInput :value="newUser.password" type="password" show-password-on="click" @update:value="v => newUser.password = v" />
        </NFormItem>
        <NFormItem :label="t('admin.role')">
          <NSelect v-model:value="newUser.role" :options="roleOptions" />
        </NFormItem>
        <NButton type="primary" block attr-type="submit">{{ t('admin.create') }}</NButton>
      </NForm>
    </NModal>

    <!-- Edit User Dialog -->
    <NModal
      v-model:show="showEdit"
      preset="card"
      class="admin-dialog"
      :title="t('admin.edit_user')"
      :aria-label="t('admin.edit_user')"
      :content-style="{ minHeight: '0', overflowY: 'auto' }"
      :bordered="false"
    >
      <NForm v-if="editingUser" @submit.prevent="saveEdit">
        <NFormItem :label="t('admin.username')">
          <NInput :value="editingUser.username" disabled />
        </NFormItem>
        <NFormItem :label="t('admin.role')">
          <NSelect v-model:value="editingUser.role" :options="roleOptions" />
        </NFormItem>
        <NButton type="primary" block attr-type="submit">{{ t('admin.save') }}</NButton>
      </NForm>
    </NModal>
  </div>
</template>

<style lang="scss" scoped>
.admin-view {
  min-height: 100dvh;
  padding: 24px;
  color: var(--domus-ink);
  background: var(--domus-canvas);
}

.admin-panel {
  max-width: 1180px;
  min-width: 0;
  margin: 0 auto;
}

.admin-view--embedded {
  min-height: 0;
  min-width: 0;
  padding: 20px;
  background: var(--domus-surface);
}

.admin-view--embedded .admin-panel { margin: 0; }
.admin-view--embedded .admin-toolbar { position: sticky; top: 0; z-index: 5; background: var(--domus-surface); }

.admin-toolbar {
  display: flex;
  align-items: center;
  flex-shrink: 0;
  gap: 16px;
  margin-bottom: 16px;
}

.admin-heading { min-width: 0; flex: 1; }
.admin-heading h1 { margin: 0; font-size: 22px; font-weight: 730; }
.admin-heading p { margin: 6px 0 0; color: var(--domus-muted); font-size: 12px; }
.admin-toolbar__actions { display: flex; align-items: center; flex-shrink: 0; gap: 8px; }

:deep(.action-buttons) {
  display: flex;
  align-items: center;
  gap: 6px;
  white-space: nowrap;
}

:deep(.user-cell) { display: flex; min-width: 0; align-items: center; gap: 10px; }
:deep(.user-avatar) { flex-shrink: 0; }
:deep(.user-cell > div) { min-width: 0; flex: 1; }
:deep(.user-cell strong),
:deep(.user-cell small) { display: block; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
:deep(.user-cell strong) { font-size: 13px; font-weight: 680; }
:deep(.user-cell small) { margin-top: 2px; color: var(--domus-muted); font-size: 10px; }

.cors-warning { flex-shrink: 0; margin-bottom: 16px; }

.admin-stats { display: flex; flex-shrink: 0; flex-wrap: wrap; gap: 10px 24px; margin: 0 0 16px; padding: 10px 14px; border-radius: 10px; background: var(--domus-surface-2); }
.admin-stats > div { display: flex; align-items: baseline; gap: 8px; }
.admin-stats dt { color: var(--domus-muted); font-size: 12px; }
.admin-stats dd { margin: 0; font-size: 16px; font-weight: 700; }
.users-table { min-width: 0; min-height: 0; overflow: hidden; border: 1px solid var(--domus-line); border-radius: 12px; }

.admin-dialog {
  width: min(480px, calc(100vw - 32px));
  max-height: calc(100dvh - 32px);
}

@media (max-width: 700px) {
  .admin-view { padding: 16px; }
  .admin-toolbar { flex-wrap: wrap; gap: 12px; }
  .admin-heading h1 { font-size: 20px; }
  .admin-toolbar__actions { margin-left: auto; }
  .admin-stats { gap: 8px 16px; }
}
</style>
