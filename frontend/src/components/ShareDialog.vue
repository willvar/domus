<template>
  <Modal
    :show="show"
    preset="dialog"
    :title="t('share.title')"
    @close="close"
    @mask-click="close"
  >
    <div class="share-form">
      <div class="share-field">
        <label>{{ t('share.target_user') }}</label>
        <Input v-model:value="form.targetUsername" :placeholder="t('share.username_placeholder')" />
      </div>

      <div class="share-row">
        <div class="share-field">
          <label>{{ t('share.permission') }}</label>
          <Select v-model:value="form.permission" :options="permOptions" />
        </div>
        <div class="share-field">
          <label>{{ t('share.expires') }}</label>
          <Select v-model:value="form.expiresIn" :options="expiryOptions" />
        </div>
      </div>

      <div v-if="errorMsg" class="share-error">{{ errorMsg }}</div>

      <div class="share-submit">
        <Button type="primary" block :loading="creating" :disabled="!form.targetUsername.trim()" @click="createShare">
          {{ t('share.create') }}
        </Button>
      </div>
    </div>

    <div class="share-existing">
      <div class="share-existing__header">{{ t('share.current') }}</div>
      <div v-if="loadingShares" class="share-empty">Loading...</div>
      <div v-else-if="shares.length === 0" class="share-empty">{{ t('share.empty') }}</div>
      <div v-else class="share-list">
        <div v-for="share in shares" :key="share.id" class="share-card">
          <div class="share-card__top">
            <div class="share-card__user">{{ share.target_username || share.target_user_id || '—' }}</div>
            <Button size="tiny" type="error" @click="revokeShare(share)">
              {{ t('share.stop_share') }}
            </Button>
          </div>
          <div class="share-card__meta">
            <div class="share-meta-item">
              <span class="share-meta-label">{{ t('share.permission') }}</span>
              <span class="share-meta-value">{{ share.permission === 'write' ? t('share.perm_write') : t('share.perm_read') }}</span>
            </div>
            <div class="share-meta-item">
              <span class="share-meta-label">{{ t('share.expires_at') }}</span>
              <span>{{ share.expires_at ? dayjs(share.expires_at).format('YYYY-MM-DD HH:mm') : t('share.never') }}</span>
            </div>
            <div class="share-meta-item">
              <span class="share-meta-label">{{ t('share.created_at') }}</span>
              <span>{{ share.created_at ? dayjs(share.created_at).format('YYYY-MM-DD HH:mm') : '—' }}</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </Modal>
</template>

<script setup lang="ts">
import { reactive, ref, watch } from 'vue'
import dayjs from 'dayjs'
import { Button, Input, Modal, Select } from '../barrels/breeze'
import api from '../composables/useApi'
import { useI18n } from '../composables/useI18n'
import { useMessage } from '../composables/useMessage'
import { showConfirm } from '../composables/useNativeDialog'
import type { Share } from '../types'

const props = defineProps({
  show: { type: Boolean, default: false },
  filePath: { type: String, default: '' },
})

const emit = defineEmits(['update:show', 'close'])

const { t } = useI18n()
const msg = useMessage()

const form = reactive({
  permission: 'read',
  targetUsername: '',
  expiresIn: 0,
})

const creating = ref(false)
const loadingShares = ref(false)
const errorMsg = ref('')
const shares = ref<Share[]>([])

const permOptions = [
  { label: t('share.perm_read'), value: 'read' },
  { label: t('share.perm_write'), value: 'write' },
]

const expiryOptions = [
  { label: t('share.never_expire'), value: 0 },
  { label: '1 ' + t('share.hour'), value: 3600 },
  { label: '24 ' + t('share.hours'), value: 86400 },
  { label: '7 ' + t('share.days'), value: 604800 },
  { label: '30 ' + t('share.days'), value: 2592000 },
]

watch(() => [props.show, props.filePath], async ([show, path]) => {
  if (!show || !path) return
  await loadShares()
}, { immediate: true })

async function loadShares() {
  if (!props.filePath) return
  loadingShares.value = true
  try {
    const { data } = await api.get<Share[]>('/file/shares', { params: { path: props.filePath } })
    shares.value = Array.isArray(data) ? data : []
  } catch {
    shares.value = []
    msg.error(t('share.load_failed'))
  } finally {
    loadingShares.value = false
  }
}

async function createShare() {
  const targetUsername = form.targetUsername.trim()
  if (creating.value || !targetUsername) return
  creating.value = true
  errorMsg.value = ''

  try {
    const payload: Record<string, unknown> = {
      path: props.filePath,
      target_username: targetUsername,
      permission: form.permission,
    }
    if (form.expiresIn > 0) {
      payload.expires_in = form.expiresIn
    }

    await api.post('/file/share', payload)
    msg.success(t('share.success'))
    form.targetUsername = ''
    form.permission = 'read'
    form.expiresIn = 0
    await loadShares()
  } catch (e: any) {
    errorMsg.value = e.response?.data?.error || 'Share failed'
  } finally {
    creating.value = false
  }
}

async function revokeShare(share: Share) {
  if (!await showConfirm(t('share.confirm_stop'))) return
  try {
    await api.delete('/file/share/' + share.id)
    msg.success(t('share.stop_success'))
    await loadShares()
  } catch {
    msg.error(t('share.stop_failed'))
  }
}

function close() {
  errorMsg.value = ''
  emit('close')
  emit('update:show', false)
}
</script>

<style lang="scss" scoped>
.share-form {
  padding-bottom: 16px;
  border-bottom: 1px solid var(--breeze-border);
}

.share-row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;

  @include mobile {
    grid-template-columns: 1fr;
  }
}

.share-field {
  margin-bottom: 12px;

  label {
    display: block;
    font-size: 13px;
    margin-bottom: 4px;
    color: var(--text-secondary, #666);
  }
}

.share-submit {
  margin-top: 8px;
}

.share-existing {
  margin-top: 16px;

  &__header {
    font-size: 13px;
    font-weight: 600;
    margin-bottom: 8px;
    color: var(--breeze-text-secondary);
  }
}

.share-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.share-card {
  border: 1px solid var(--breeze-border);
  border-radius: 6px;
  padding: 12px;
  background: var(--breeze-surface-raised);

  &__top {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 12px;
    margin-bottom: 10px;

    @include mobile {
      flex-direction: column;
      align-items: stretch;
    }
  }

  &__user {
    font-size: 14px;
    font-weight: 600;
    color: var(--breeze-text);
    word-break: break-word;
  }

  &__meta {
    display: grid;
    grid-template-columns: repeat(3, minmax(0, 1fr));
    gap: 10px;

    @include mobile {
      grid-template-columns: 1fr;
    }
  }
}

.share-meta-item {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
  font-size: 13px;
  color: var(--breeze-text);
}

.share-meta-label {
  color: var(--breeze-text-secondary);
  font-size: 12px;
}

.share-meta-value {
  color: var(--breeze-text);
  font-weight: 500;
}

.share-empty {
  padding: 12px;
  border: 1px dashed var(--breeze-border);
  border-radius: 6px;
  color: var(--breeze-text-secondary);
  font-size: 13px;
  text-align: center;
}

.share-error {
  margin-top: 8px;
  color: #c00;
  font-size: 13px;
}
</style>
