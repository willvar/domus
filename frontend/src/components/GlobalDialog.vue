<script setup>
import { ref, watch, nextTick } from 'vue'
import { useDialogState } from '../composables/useNativeDialog'
import { useI18n } from '../composables/useI18n'
import BModal from './breeze/BModal.vue'
import BCard from './breeze/BCard.vue'
import BButton from './breeze/BButton.vue'
import BInput from './breeze/BInput.vue'
import BCheckbox from './breeze/BCheckbox.vue'

const { t } = useI18n()
const dialogState = useDialogState()

const visible = ref(false)
const inputValue = ref('')
const inputRef = ref(null)
const applyToAll = ref(false)

watch(dialogState, (state) => {
  if (state) {
    visible.value = true
    inputValue.value = ''
    applyToAll.value = false
    if (state.type === 'prompt') {
      nextTick(() => inputRef.value?.focus())
    }
  }
})

function handleConfirm() {
  const state = dialogState.value
  if (!state) return
  visible.value = false
  if (state.type === 'prompt') {
    state.resolve(inputValue.value || null)
  } else {
    state.resolve(true)
  }
  dialogState.value = null
}

function handleCancel() {
  const state = dialogState.value
  if (!state) return
  visible.value = false
  if (state.type === 'prompt') {
    state.resolve(null)
  } else if (state.type === 'alert') {
    state.resolve(true)
  } else {
    state.resolve(false)
  }
  dialogState.value = null
}

function handleKeydown(e) {
  if (e.key === 'Enter') {
    handleConfirm()
  }
}

function handleDuplicate(action) {
  const state = dialogState.value
  if (!state) return
  visible.value = false
  state.resolve({ action, applyToAll: applyToAll.value })
  dialogState.value = null
}

function linkify(text) {
  return text.replace(/(https?:\/\/[^\s)]+)/g, '<a href="$1" target="_blank" rel="noopener" style="color:var(--breeze-accent)">$1</a>')
}

function formatSize(bytes) {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let value = bytes
  let unit = 0
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024
    unit++
  }
  return `${value.toFixed(1)} ${units[unit]}`
}
</script>

<template>
  <BModal
    v-if="dialogState?.type !== 'duplicate'"
    :show="visible"
    preset="dialog"
    :title="dialogState?.title ?? ''"
    :positive-text="t('dialog.ok')"
    :negative-text="dialogState?.type === 'alert' ? undefined : t('dialog.cancel')"
    @positive-click="handleConfirm"
    @negative-click="handleCancel"
    @close="handleCancel"
    @mask-click="handleCancel"
  >
    <div v-if="dialogState?.content" class="dialog-content" v-html="linkify(dialogState.content)" />
    <BInput
      v-if="dialogState?.type === 'prompt'"
      ref="inputRef"
      :value="inputValue"
      :placeholder="dialogState?.placeholder ?? ''"
      @update:value="v => inputValue = v"
      @keydown="handleKeydown"
    />
  </BModal>

  <BModal
    v-else
    :show="visible"
    @close="handleCancel"
    @mask-click="handleCancel"
  >
    <BCard
      style="width: 520px"
      :title="dialogState?.title ?? ''"
      :bordered="false"
      size="small"
    >
      <div class="duplicate-summary">
        {{ t('upload.duplicate_summary', { name: dialogState?.incomingName ?? '' }) }}
      </div>

      <div class="duplicate-grid">
        <div class="duplicate-col">
          <div class="duplicate-label">{{ t('upload.duplicate_existing') }}</div>
          <div class="duplicate-name">{{ dialogState?.existingName }}</div>
          <div class="duplicate-meta">
            {{ dialogState?.existingIsDir ? t('upload.duplicate_folder') : formatSize(dialogState?.existingSize) }}
          </div>
        </div>

        <div class="duplicate-col">
          <div class="duplicate-label">{{ t('upload.duplicate_incoming') }}</div>
          <div class="duplicate-name">{{ dialogState?.incomingName }}</div>
          <div class="duplicate-meta">{{ formatSize(dialogState?.incomingSize) }}</div>
        </div>
      </div>

      <div class="duplicate-options">
        <BCheckbox v-model:checked="applyToAll">
          {{ t('upload.duplicate_apply_all') }}
        </BCheckbox>
      </div>

      <template #footer>
        <div class="duplicate-footer">
          <div style="display:flex;justify-content:flex-end;gap:8px">
            <BButton @click="handleCancel">{{ t('upload.duplicate_cancel') }}</BButton>
            <BButton @click="handleDuplicate('skip')">{{ t('upload.duplicate_skip') }}</BButton>
            <BButton @click="handleDuplicate('rename')">{{ t('upload.duplicate_rename') }}</BButton>
            <BButton type="primary" @click="handleDuplicate('replace')">{{ t('upload.duplicate_replace') }}</BButton>
          </div>
        </div>
      </template>
    </BCard>
  </BModal>
</template>

<style scoped>
.dialog-content {
  white-space: pre-line;
  line-height: 1.5;
  color: var(--breeze-text);
}

.duplicate-summary {
  line-height: 1.5;
  margin-bottom: 16px;
  color: var(--breeze-text);
}

.duplicate-grid {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 12px;
}

.duplicate-col {
  padding: 12px;
  border: 1px solid var(--breeze-border);
  border-radius: 6px;
  background: var(--breeze-bg-alt);
}

.duplicate-label {
  font-size: 12px;
  color: var(--breeze-text-secondary);
  margin-bottom: 6px;
}

.duplicate-name {
  font-size: 14px;
  word-break: break-all;
  margin-bottom: 4px;
}

.duplicate-meta {
  font-size: 12px;
  color: var(--breeze-text-secondary);
}

.duplicate-options {
  margin-top: 16px;
}

.duplicate-footer {
  display: flex;
  justify-content: flex-end;
}
</style>
