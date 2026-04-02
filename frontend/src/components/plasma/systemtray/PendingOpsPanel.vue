<script setup>
import { usePendingOpsStore } from '../../../stores/pendingOps'
import { useI18n } from '../../../composables/useI18n'
import { showConfirm } from '../../../composables/useNativeDialog'
import dayjs from 'dayjs'
import TrayPopup from './TrayPopup.vue'
import IconPencil from '~icons/mdi/pencil-outline'
import IconFolder from '~icons/mdi/folder-outline'
import IconContentSave from '~icons/mdi/content-save-outline'
import IconDelete from '~icons/mdi/delete-outline'
import IconRestore from '~icons/mdi/restore'
import IconContentCopy from '~icons/mdi/content-copy'
import IconPackage from '~icons/mdi/package-variant-closed'
import IconTimer from '~icons/mdi/timer-sand'

const pendingOps = usePendingOpsStore()
const { t } = useI18n()

function formatTime(ts) {
  return dayjs(ts).format('MM-DD HH:mm')
}

const typeIcons = {
  rename: IconPencil,
  mkdir: IconFolder,
  saveViewer: IconContentSave,
  deleteTrash: IconDelete,
  restore: IconRestore,
  copy: IconContentCopy,
  move: IconPackage,
  delete: IconDelete,
  emptyTrash: IconDelete,
}
function typeIcon(type) {
  return typeIcons[type] || IconTimer
}

async function handleDiscardAll() {
  if (!await showConfirm(t('pending.confirm_discard_all'))) return
  pendingOps.discardAll()
}
</script>

<template>
  <TrayPopup :show="pendingOps.showPanel" :title="t('pending.title')" @update:show="v => pendingOps.showPanel = v">
    <template #actions>
      <button v-if="pendingOps.hasPending" class="tray-btn" @click="handleDiscardAll">
        {{ t('pending.discard_all') }}
      </button>
    </template>

    <div v-if="!pendingOps.hasPending" class="pending-empty-wrap">
      <div class="empty-state">{{ t('pending.empty') }}</div>
    </div>

    <div v-else class="pending-scroll">
      <div v-for="op in pendingOps.ops" :key="op.id" class="pending-item">
        <div class="pending-item-header">
          <component :is="typeIcon(op.type)" class="pending-icon" width="14" height="14" />
          <span class="pending-desc truncate">{{ op.description }}</span>
        </div>
        <div class="pending-item-meta">
          <span class="pending-time">{{ formatTime(op.createdAt) }}</span>
          <span v-if="op.lastError" class="pending-error truncate">{{ op.lastError }}</span>
        </div>
        <div class="pending-item-actions">
          <button class="tray-btn tray-btn--accent" :disabled="op._retrying" @click="pendingOps.retry(op.id)">
            {{ t('pending.retry') }}
          </button>
          <button class="tray-btn" @click="pendingOps.discard(op.id)">
            {{ t('pending.discard') }}
          </button>
        </div>
      </div>
    </div>
  </TrayPopup>
</template>

<style lang="scss" scoped>
.pending-empty-wrap { padding: 24px 12px; }
.empty-state { text-align: center; color: var(--breeze-text-disabled, #505962); font-size: 13px; }
.pending-scroll { overflow-y: auto; max-height: 360px; padding: 4px 0; }

.pending-item {
  padding: 8px 12px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  border-bottom: 1px solid #31363b;

  &:last-child { border-bottom: none; }
}

.pending-item-header { display: flex; align-items: center; gap: 6px; }
.pending-icon { font-size: 14px; flex-shrink: 0; }
.pending-desc { font-size: var(--font-size-sm); max-width: 280px; }
.pending-item-meta { display: flex; align-items: center; gap: 8px; font-size: var(--font-size-xs); color: var(--breeze-text-secondary); }
.pending-error { color: var(--breeze-danger); max-width: 200px; }
.pending-time { flex-shrink: 0; }
.pending-item-actions { display: flex; gap: 4px; margin-top: 2px; }

.tray-btn {
  padding: 2px 8px;
  border: none;
  border-radius: 3px;
  background: none;
  color: var(--breeze-text-secondary, #a1a9b1);
  font-size: 12px;
  cursor: default;

  &:hover { background: $hover-white-medium; color: var(--breeze-text, #fcfcfc); }
  &:disabled { opacity: 0.5; }
  &--accent { color: var(--breeze-accent, #3daee9); }
}
</style>
