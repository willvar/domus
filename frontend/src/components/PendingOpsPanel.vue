<script setup>
import { NButton, NEmpty } from 'naive-ui'
import { usePendingOpsStore } from '../stores/pendingOps'
import { useI18n } from '../composables/useI18n'
import { showConfirm } from '../composables/useNativeDialog'
import dayjs from 'dayjs'

const pendingOps = usePendingOpsStore()
const { t } = useI18n()

function formatTime(ts) {
  return dayjs(ts).format('MM-DD HH:mm')
}

function typeIcon(type) {
  const icons = {
    rename: '✏️',
    mkdir: '📁',
    saveViewer: '💾',
    deleteTrash: '🗑️',
    restore: '♻️',
    copy: '📋',
    move: '📦',
    delete: '🗑️',
    emptyTrash: '🗑️',
  }
  return icons[type] || '⏳'
}

async function handleDiscardAll() {
  if (!await showConfirm(t('pending.confirm_discard_all'))) return
  pendingOps.discardAll()
}
</script>

<template>
  <Transition name="slide-up">
    <div v-if="pendingOps.showPanel" class="pending-panel">
      <div class="pending-header">
        <span class="pending-title">{{ t('pending.title') }}</span>
        <div class="pending-actions">
          <NButton v-if="pendingOps.hasPending" size="tiny" quaternary @click="handleDiscardAll">
            {{ t('pending.discard_all') }}
          </NButton>
          <NButton size="tiny" quaternary @click="pendingOps.showPanel = false">
            ✕
          </NButton>
        </div>
      </div>

      <div v-if="!pendingOps.hasPending" class="pending-empty-wrap">
        <NEmpty :description="t('pending.empty')" size="small" />
      </div>

      <div v-else class="pending-scroll">
        <div v-for="op in pendingOps.ops" :key="op.id" class="pending-item">
          <div class="pending-item-header">
            <span class="pending-icon">{{ typeIcon(op.type) }}</span>
            <span class="pending-desc truncate">{{ op.description }}</span>
          </div>
          <div class="pending-item-meta">
            <span class="pending-time">{{ formatTime(op.createdAt) }}</span>
            <span v-if="op.lastError" class="pending-error truncate">{{ op.lastError }}</span>
          </div>
          <div class="pending-item-actions">
            <NButton
              size="tiny"
              quaternary
              type="info"
              :loading="op._retrying"
              @click="pendingOps.retry(op.id)"
            >
              {{ t('pending.retry') }}
            </NButton>
            <NButton size="tiny" quaternary @click="pendingOps.discard(op.id)">
              {{ t('pending.discard') }}
            </NButton>
          </div>
        </div>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
.pending-panel {
  position: fixed;
  bottom: 68px;
  right: 8px;
  width: 360px;
  max-height: 420px;
  z-index: 1000;
  background: #2a2e32;
  border: 1px solid #3b4045;
  border-radius: 8px;
  box-shadow: 0 -4px 24px rgba(0, 0, 0, 0.2);
  display: flex;
  flex-direction: column;
}

.pending-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 10px 12px;
  border-bottom: 1px solid #3b4045;
}

.pending-title {
  font-weight: 600;
  font-size: var(--font-size-sm);
}

.pending-actions {
  display: flex;
  gap: 4px;
}

.pending-empty-wrap {
  padding: 24px 12px;
}

.pending-scroll {
  overflow-y: auto;
  max-height: 360px;
  padding: 4px 0;
}

.pending-item {
  padding: 8px 12px;
  display: flex;
  flex-direction: column;
  gap: 4px;
  border-bottom: 1px solid #31363b;
}
.pending-item:last-child {
  border-bottom: none;
}

.pending-item-header {
  display: flex;
  align-items: center;
  gap: 6px;
}

.pending-icon {
  font-size: 14px;
  flex-shrink: 0;
}

.pending-desc {
  font-size: var(--font-size-sm);
  max-width: 280px;
}

.pending-item-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: var(--font-size-xs);
  color: var(--breeze-text-secondary);
}

.pending-error {
  color: var(--breeze-danger);
  max-width: 200px;
}

.pending-time {
  flex-shrink: 0;
}

.pending-item-actions {
  display: flex;
  gap: 4px;
  margin-top: 2px;
}

.slide-up-enter-active, .slide-up-leave-active {
  transition: all 0.3s ease;
}
.slide-up-enter-from, .slide-up-leave-to {
  transform: translateY(20px);
  opacity: 0;
}

@media (max-width: 767px) {
  .pending-panel { left: 8px; right: 8px; width: auto; bottom: 72px; }
}
</style>
