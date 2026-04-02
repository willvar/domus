<script setup>
import { useOperationsStore } from '../../../stores/operations'
import { useI18n } from '../../../composables/useI18n'

const ops = useOperationsStore()
const { t } = useI18n()

function progressPercent(op) {
  if (!op.total) return 0
  return Math.floor((op.done / op.total) * 100)
}

function progressClass(op) {
  if (op.status === 'failed') return 'progress-error'
  if (op.status === 'completed') return 'progress-success'
  return 'progress-info'
}
</script>

<template>
  <Transition name="slide-up">
    <div
      v-if="ops.showPanel && ops.operations.length > 0"
      class="op-panel"
    >
      <div class="op-header">{{ t('ops.title') }}</div>
      <div class="op-scroll">
        <div v-for="op in ops.operations" :key="op.id" class="op-item">
          <div class="op-desc truncate">{{ op.description }}</div>
          <div class="progress-bar">
            <div class="progress-fill" :class="progressClass(op)" :style="{ width: progressPercent(op) + '%' }" />
          </div>
          <div class="op-detail">
            <span v-if="op.total">{{ op.done }}/{{ op.total }}</span>
            <span v-if="op.error" class="op-error">{{ op.error }}</span>
          </div>
        </div>
      </div>
    </div>
  </Transition>
</template>

<style lang="scss" scoped>
.op-panel {
  position: fixed;
  bottom: 36px;
  left: 16px;
  width: 320px;
  z-index: 200;
  background: var(--breeze-surface, #292c30);
  border: 1px solid var(--breeze-border, #3b4045);
  border-radius: 8px;
  box-shadow: 0 -4px 24px rgba(0, 0, 0, 0.12);
  overflow: hidden;
}

.op-header {
  padding: 10px 12px;
  font-size: 13px;
  font-weight: 600;
  border-bottom: 1px solid var(--breeze-border, #3b4045);
}

.op-scroll { padding: 8px 12px; }

.op-item {
  padding: 6px 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.op-desc { font-size: var(--font-size-sm); }

.op-detail {
  font-size: var(--font-size-xs);
  color: var(--breeze-text-secondary);
}

.op-error { color: var(--breeze-danger); }

// Progress bar
.progress-bar {
  height: 4px;
  background: var(--breeze-border, #3b4045);
  border-radius: 2px;
  overflow: hidden;
}

.progress-fill {
  height: 100%;
  border-radius: 2px;
  transition: width 0.3s ease;
}

.progress-info { background: var(--breeze-accent, #3daee9); }
.progress-success { background: var(--breeze-success, #27ae60); }
.progress-error { background: var(--breeze-danger, #da4453); }

.slide-up-enter-active, .slide-up-leave-active {
  transition: all 0.3s ease;
}

.slide-up-enter-from, .slide-up-leave-to {
  transform: translateY(20px);
  opacity: 0;
}
</style>
