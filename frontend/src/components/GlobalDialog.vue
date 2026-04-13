<script setup lang="ts">
import { ref, watch, nextTick } from 'vue'
import { useDialogState } from '../composables/useNativeDialog'
import { useI18n } from '../composables/useI18n'
import { Modal, Card, Button, Input, Checkbox } from '../barrels/breeze'
import { IconAlertCircle } from '../barrels/icons'

const { t } = useI18n()
const dialogState = useDialogState()

const visible = ref(false)
const inputValue = ref('')
const inputRef = ref<any>(null)
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

function handleKeydown(e: KeyboardEvent) {
  if (e.key === 'Enter') {
    handleConfirm()
  }
}

function handleDuplicate(action: string) {
  const state = dialogState.value
  if (!state) return
  visible.value = false
  state.resolve({ action, applyToAll: applyToAll.value })
  dialogState.value = null
}

function linkify(text: string) {
  return text.replace(/(https?:\/\/[^\s)]+)/g, '<a href="$1" target="_blank" rel="noopener" style="color:var(--breeze-accent)">$1</a>')
}

function formatSize(bytes: number | undefined) {
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
  <Modal
    v-if="dialogState?.type !== 'duplicate'"
    :show="visible"
    :z-index="6000"
    preset="dialog"
    :title="dialogState?.title ?? ''"
    :positive-text="dialogState?.positiveText || t('dialog.ok')"
    :positive-type="dialogState?.positiveType || 'primary'"
    :negative-text="dialogState?.type === 'alert' ? undefined : t('dialog.cancel')"
    @positive-click="handleConfirm"
    @negative-click="handleCancel"
    @close="handleCancel"
    @mask-click="handleCancel"
  >
    <div v-if="dialogState?.icon || dialogState?.content" class="dialog-body-row">
      <IconAlertCircle v-if="dialogState?.icon === 'warning'" class="dialog-icon--warning" width="36" height="36" />
      <div v-if="dialogState?.content" class="dialog-content" v-html="linkify(dialogState.content)" />
    </div>
    <Input
      v-if="dialogState?.type === 'prompt'"
      ref="inputRef"
      :value="inputValue"
      :placeholder="dialogState?.placeholder ?? ''"
      @update:value="v => inputValue = v"
      @keydown="handleKeydown"
    />
  </Modal>

  <Modal
    v-else
    :show="visible"
    :z-index="6000"
    @close="handleCancel"
    @mask-click="handleCancel"
  >
    <Card
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
        <Checkbox v-model:checked="applyToAll">
          {{ t('upload.duplicate_apply_all') }}
        </Checkbox>
      </div>

      <template #footer>
        <div class="duplicate-footer">
          <div style="display:flex;justify-content:flex-end;gap:8px">
            <Button @click="handleCancel">{{ t('upload.duplicate_cancel') }}</Button>
            <Button @click="handleDuplicate('skip')">{{ t('upload.duplicate_skip') }}</Button>
            <Button @click="handleDuplicate('rename')">{{ t('upload.duplicate_rename') }}</Button>
            <Button type="primary" @click="handleDuplicate('replace')">{{ t('upload.duplicate_replace') }}</Button>
          </div>
        </div>
      </template>
    </Card>
  </Modal>
</template>

<style lang="scss" scoped>
.dialog-body-row {
  display: flex;
  align-items: flex-start;
  gap: 12px;
}

.dialog-icon--warning {
  flex-shrink: 0;
  color: #f0ad4e;
}

.dialog-content {
  white-space: pre-line;
  line-height: 1.5;
  color: var(--breeze-text);
  padding-top: 6px;
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
