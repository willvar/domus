<template>
  <Modal
    :show="show"
    :title="t('share.title')"
    :positive-text="creating ? '' : t('share.create')"
    :negative-text="t('common.cancel')"
    @positive-click="createShare"
    @negative-click="close"
    @close="close"
    @mask-click="close"
  >
    <!-- Target user -->
    <div class="share-field">
      <label>{{ t('share.target_user') }}</label>
      <Input v-model:value="form.targetUsername" :placeholder="t('share.username_placeholder')" />
    </div>

    <!-- Permission selector -->
    <div class="share-field">
      <label>{{ t('share.permission') }}</label>
      <Select v-model:value="form.permission" :options="permOptions" />
    </div>

    <!-- Expiration -->
    <div class="share-field">
      <label>{{ t('share.expires') }}</label>
      <Select v-model:value="form.expiresIn" :options="expiryOptions" />
    </div>

    <!-- Error -->
    <div v-if="errorMsg" class="share-error">{{ errorMsg }}</div>
  </Modal>
</template>

<script setup lang="ts">
import { ref, reactive } from 'vue'
import { Modal, Select, Input } from '../barrels/breeze'
import api from '../composables/useApi'
import { useI18n } from '../composables/useI18n'
import { useMessage } from '../composables/useMessage'

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
const errorMsg = ref('')

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

async function createShare() {
  if (creating.value || !form.targetUsername) return
  creating.value = true
  errorMsg.value = ''

  try {
    const payload: Record<string, unknown> = {
      path: props.filePath,
      target_username: form.targetUsername,
      permission: form.permission,
    }
    if (form.expiresIn > 0) {
      payload.expires_in = form.expiresIn
    }

    await api.post('/file/share', payload)
    msg.success(t('share.success'))
    close()
  } catch (e: any) {
    errorMsg.value = e.response?.data?.error || 'Share failed'
  } finally {
    creating.value = false
  }
}

function close() {
  errorMsg.value = ''
  emit('close')
  emit('update:show', false)
}
</script>

<style lang="scss" scoped>
.share-field {
  margin-bottom: 12px;

  label {
    display: block;
    font-size: 13px;
    margin-bottom: 4px;
    color: var(--text-secondary, #666);
  }
}

.share-error {
  margin-top: 8px;
  color: #c00;
  font-size: 13px;
}
</style>
