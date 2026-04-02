<template>
  <BModal
    :show="show"
    :title="t('share.title')"
    :positive-text="creating ? '' : t('share.create')"
    :negative-text="t('common.cancel')"
    @positive-click="createShare"
    @negative-click="close"
    @close="close"
    @mask-click="close"
  >
    <!-- Share type selector -->
    <div class="share-field">
      <label>{{ t('share.type') }}</label>
      <BSelect v-model:value="form.type" :options="typeOptions" />
    </div>

    <!-- Target user (for user shares) -->
    <div v-if="form.type === 'user'" class="share-field">
      <label>{{ t('share.target_user') }}</label>
      <BInput v-model:value="form.targetUsername" :placeholder="t('share.username_placeholder')" />
    </div>

    <!-- Permission selector -->
    <div class="share-field">
      <label>{{ t('share.permission') }}</label>
      <BSelect v-model:value="form.permission" :options="permOptions" />
    </div>

    <!-- Expiration (for link shares) -->
    <div v-if="form.type === 'link'" class="share-field">
      <label>{{ t('share.expires') }}</label>
      <BSelect v-model:value="form.expiresIn" :options="expiryOptions" />
    </div>

    <!-- Result: share link -->
    <div v-if="shareLink" class="share-result">
      <label>{{ t('share.link') }}</label>
      <div class="share-link-row">
        <input readonly :value="shareLink" class="share-link-input" @focus="$event.target.select()" />
        <BButton size="small" @click="copyLink">{{ copied ? t('share.copied') : t('share.copy') }}</BButton>
      </div>
    </div>

    <!-- Error -->
    <div v-if="errorMsg" class="share-error">{{ errorMsg }}</div>
  </BModal>
</template>

<script setup>
import { ref, reactive } from 'vue'
import BModal from './breeze/BModal.vue'
import BSelect from './breeze/BSelect.vue'
import BInput from './breeze/BInput.vue'
import BButton from './breeze/BButton.vue'
import api from '../composables/useApi'
import { useI18n } from '../composables/useI18n'

const props = defineProps({
  show: Boolean,
  filePath: String,
})

const emit = defineEmits(['update:show', 'close'])

const { t } = useI18n()

const form = reactive({
  type: 'link',
  permission: 'read',
  targetUsername: '',
  expiresIn: 86400,
})

const creating = ref(false)
const shareLink = ref('')
const copied = ref(false)
const errorMsg = ref('')

const typeOptions = [
  { label: t('share.type_link'), value: 'link' },
  { label: t('share.type_user'), value: 'user' },
]

const permOptions = [
  { label: t('share.perm_read'), value: 'read' },
  { label: t('share.perm_write'), value: 'write' },
]

const expiryOptions = [
  { label: '1 ' + t('share.hour'), value: 3600 },
  { label: '24 ' + t('share.hours'), value: 86400 },
  { label: '7 ' + t('share.days'), value: 604800 },
  { label: '30 ' + t('share.days'), value: 2592000 },
]

async function createShare() {
  if (creating.value) return
  creating.value = true
  errorMsg.value = ''
  shareLink.value = ''

  try {
    const payload = {
      path: props.filePath,
      type: form.type,
      permission: form.permission,
    }
    if (form.type === 'link') {
      payload.expires_in = form.expiresIn
    } else {
      payload.target_username = form.targetUsername
    }

    const res = await api.post('/file/share', payload)

    if (form.type === 'link' && res.data.share_id && res.data.share_key) {
      const base = window.location.origin + window.location.pathname
      shareLink.value = `${base}#/s/${res.data.share_id}#${res.data.share_key}`
    }
  } catch (e) {
    errorMsg.value = e.response?.data?.error || 'Share failed'
  } finally {
    creating.value = false
  }
}

function copyLink() {
  if (!shareLink.value) return
  navigator.clipboard.writeText(shareLink.value)
  copied.value = true
  setTimeout(() => { copied.value = false }, 2000)
}

function close() {
  shareLink.value = ''
  errorMsg.value = ''
  emit('close')
  emit('update:show', false)
}
</script>

<style scoped>
.share-field {
  margin-bottom: 12px;
}
.share-field label {
  display: block;
  font-size: 13px;
  margin-bottom: 4px;
  color: var(--text-secondary, #666);
}
.share-result {
  margin-top: 16px;
  padding-top: 12px;
  border-top: 1px solid var(--border-color, #e0e0e0);
}
.share-result label {
  display: block;
  font-size: 13px;
  margin-bottom: 4px;
  color: var(--text-secondary, #666);
}
.share-link-row {
  display: flex;
  gap: 8px;
}
.share-link-input {
  flex: 1;
  padding: 4px 8px;
  font-size: 12px;
  border: 1px solid var(--border-color, #ccc);
  border-radius: 4px;
  background: var(--bg-secondary, #f5f5f5);
}
.share-error {
  margin-top: 8px;
  color: #c00;
  font-size: 13px;
}
</style>
