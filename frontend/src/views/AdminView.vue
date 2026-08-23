<script setup lang="ts">
import { ref, computed, onMounted, h } from 'vue'
import {
  NAlert,
  NAvatar,
  NButton,
  NCard,
  NDataTable,
  NForm,
  NFormItem,
  NInput,
  NModal,
  NSelect,
  NStatistic,
  NTag,
} from 'naive-ui'
import { useRouter } from 'vue-router'
import { useI18n } from '../composables/useI18n'
import { useAppMessage } from '../ui/feedback'
import { showPrompt, showConfirm } from '../composables/useNativeDialog'
import api from '../composables/useApi'
import dayjs from 'dayjs'
import { IconAccount, IconArrowLeft, IconCheck, IconEmailOutline, IconPlus } from '../barrels/icons'

const router = useRouter()
const { t, te } = useI18n()
const message = useAppMessage()

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

const columns = computed(() => [
  {
    title: t('admin.username'),
    key: 'username',
    minWidth: 180,
    render: (row: any) => h('div', { class: 'user-cell' }, [
      h(NAvatar, { round: true, size: 34 }, () => String(row.username || '?').slice(0, 1).toUpperCase()),
      h('div', {}, [
        h('strong', {}, row.username),
        h('small', {}, `#${row.id}`),
      ]),
    ]),
  },
  {
    title: t('admin.role'),
    key: 'role',
    width: 100,
    render: (row: any) => h(NTag, { size: 'small', round: true, type: row.role === 'root' ? 'info' : 'default' }, () => row.role),
  },
  {
    title: t('admin.email'),
    key: 'email',
    width: 160,
    render: (row: any) => row.email || '—',
  },
  {
    title: t('admin.otp'),
    key: 'totp_enabled',
    width: 80,
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
    width: 150,
    render: (row: any) => row.created_at ? dayjs(row.created_at).format('YYYY-MM-DD HH:mm') : '---',
  },
  {
    title: t('admin.actions'),
    key: 'actions',
    width: 330,
    render(row: any) {
      const buttons = [
        h(NButton, { size: 'small', quaternary: true, onClick: () => openEdit(row) }, () => t('admin.edit')),
        h(NButton, { size: 'small', quaternary: true, onClick: () => resetPassword(row) }, () => t('admin.reset_pwd')),
        h(NButton, { size: 'small', quaternary: true, type: 'error', onClick: () => deleteUser(row) }, () => t('admin.delete')),
      ]
      if (row.totp_enabled) {
        buttons.push(h(NButton, { size: 'small', quaternary: true, type: 'warning', onClick: () => resetOTP(row) }, () => t('admin.reset_otp')))
      }
      if (row.email) {
        buttons.push(h(NButton, { size: 'small', quaternary: true, type: 'warning', onClick: () => resetEmail(row) }, () => t('admin.reset_email')))
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
  <div class="admin-view">
    <header class="admin-topbar">
      <div class="admin-brand"><span>D</span><strong>DOMUS</strong></div>
      <NButton quaternary @click="router.push('/')">
        <template #icon><IconArrowLeft /></template>
        {{ t('admin.back_to_files') }}
      </NButton>
    </header>

    <main class="admin-content">
      <div class="admin-header">
        <div>
          <span>{{ t('titlebar.admin_panel') }}</span>
          <h1>{{ t('admin.title') }}</h1>
          <p>{{ t('admin.subtitle') }}</p>
        </div>
        <NButton type="primary" @click="showCreate = true">
          <template #icon><IconPlus /></template>
          {{ t('admin.add_user') }}
        </NButton>
      </div>

      <NAlert v-if="!corsOk" class="cors-warning" type="error" :title="t('admin.cors_warning_title')">
        {{ t('admin.cors_warning_body') }}
      </NAlert>

      <section class="admin-stats">
        <NCard :bordered="false">
          <NStatistic :label="t('admin.total_users')" :value="users.length">
            <template #prefix><IconAccount /></template>
          </NStatistic>
        </NCard>
        <NCard :bordered="false">
          <NStatistic :label="t('admin.otp_protected')" :value="otpProtectedCount">
            <template #prefix><IconCheck /></template>
          </NStatistic>
        </NCard>
        <NCard :bordered="false">
          <NStatistic :label="t('admin.email_linked')" :value="emailLinkedCount">
            <template #prefix><IconEmailOutline /></template>
          </NStatistic>
        </NCard>
      </section>

      <NCard class="users-card" :bordered="false">
        <template #header>
          <div class="users-card__header">
            <div><strong>{{ t('admin.accounts') }}</strong><span>{{ t('admin.accounts_hint') }}</span></div>
            <span>{{ users.length }}</span>
          </div>
        </template>
        <NDataTable
          :columns="columns"
          :data="users"
          :loading="loading"
          :row-key="(row) => row.id"
          size="small"
          :bordered="false"
          :scroll-x="1050"
        />
      </NCard>
    </main>

    <!-- Create User Dialog -->
    <NModal
      v-model:show="showCreate"
      preset="card"
      class="admin-dialog"
      :title="t('admin.create_user')"
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
  overflow: auto;
  color: #172033;
  background: #f4f6fa;
}

.admin-topbar {
  display: flex;
  height: 72px;
  align-items: center;
  justify-content: space-between;
  padding: 0 max(28px, calc((100vw - 1180px) / 2));
  border-bottom: 1px solid #e3e7ef;
  background: rgb(255 255 255 / 88%);
  backdrop-filter: blur(18px);
}

.admin-brand { display: flex; align-items: center; gap: 11px; font-size: 14px; letter-spacing: .14em; }
.admin-brand span { display: grid; width: 36px; height: 36px; place-items: center; border-radius: 11px; color: #fff; background: linear-gradient(145deg, #6574f0, #4352d0); box-shadow: 0 8px 18px rgb(79 95 231 / 22%); font-size: 15px; font-weight: 800; letter-spacing: 0; }

.admin-content {
  width: min(1180px, calc(100% - 48px));
  margin: 0 auto;
  padding: 48px 0 72px;
}

.admin-header {
  display: flex;
  align-items: end;
  justify-content: space-between;
  gap: 24px;
  margin-bottom: 28px;

  h1 {
    margin: 6px 0 0;
    font-size: 34px;
    font-weight: 730;
    letter-spacing: -.04em;
  }

  > div > span { color: #4f5fe7; font-size: 11px; font-weight: 800; letter-spacing: .12em; text-transform: uppercase; }
  p { margin: 9px 0 0; color: #657087; font-size: 14px; }
}

:deep(.action-buttons) {
  display: flex;
  gap: 2px;
  flex-wrap: wrap;
}

:deep(.user-cell) { display: flex; align-items: center; gap: 11px; }
:deep(.user-cell strong),
:deep(.user-cell small) { display: block; }
:deep(.user-cell strong) { font-size: 13px; font-weight: 680; }
:deep(.user-cell small) { margin-top: 2px; color: #657087; font-size: 10px; }

.cors-warning {
  margin-bottom: 22px;
}

.admin-stats { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 16px; margin-bottom: 18px; }
.admin-stats :deep(.n-card) { border: 1px solid #e6e9f0; box-shadow: 0 8px 26px rgb(26 36 58 / 4%); }
.admin-stats :deep(.n-statistic-value) { display: flex; align-items: center; gap: 10px; color: #172033; font-size: 28px; font-weight: 720; }
.admin-stats :deep(.n-statistic-value__prefix) { display: grid; width: 36px; height: 36px; place-items: center; border-radius: 11px; color: #4f5fe7; background: #edf0ff; }
.admin-stats :deep(.n-statistic-value__prefix svg) { width: 19px; height: 19px; }

.users-card { border: 1px solid #e4e8ef; box-shadow: 0 12px 36px rgb(26 36 58 / 5%); }
.users-card :deep(.n-card__content) { padding: 0; overflow: hidden; border-radius: 0 0 18px 18px; }
.users-card__header { display: flex; align-items: center; justify-content: space-between; }
.users-card__header strong,
.users-card__header div span { display: block; }
.users-card__header strong { font-size: 17px; }
.users-card__header div span { margin-top: 4px; color: #68748a; font-size: 12px; font-weight: 400; }
.users-card__header > span { display: grid; min-width: 32px; height: 26px; place-items: center; border-radius: 999px; color: #4f5fe7; background: #edf0ff; font-size: 12px; font-weight: 700; }

.admin-dialog {
  width: min(480px, calc(100vw - 32px));
}

@media (max-width: 700px) {
  .admin-topbar { height: 64px; padding: 0 16px; }
  .admin-brand strong { display: none; }
  .admin-content { width: min(100% - 30px, 1180px); padding: 30px 0 50px; }
  .admin-header { align-items: flex-start; flex-direction: column; gap: 18px; }
  .admin-header h1 { font-size: 29px; }
  .admin-stats { grid-template-columns: 1fr; gap: 10px; }
  .admin-stats :deep(.n-card__content) { padding: 17px 20px; }
}
</style>
