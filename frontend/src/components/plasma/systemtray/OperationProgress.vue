<script setup>
import { NCard, NProgress } from 'naive-ui'
import { useOperationsStore } from '../../../stores/operations'
import { useI18n } from '../../../composables/useI18n'

const ops = useOperationsStore()
const { t } = useI18n()

function progressPercent(op) {
  if (!op.total) return 0
  return Math.floor((op.done / op.total) * 100)
}
</script>

<template>
  <Transition name="slide-up">
    <NCard
      v-if="ops.showPanel && ops.operations.length > 0"
      class="op-panel"
      :bordered="true"
      size="small"
    >
      <template #header>{{ t('ops.title') }}</template>
      <div v-for="op in ops.operations" :key="op.id" class="op-item">
        <div class="op-desc truncate">{{ op.description }}</div>
        <NProgress
          :percentage="progressPercent(op)"
          :status="op.status === 'failed' ? 'error' : op.status === 'completed' ? 'success' : 'info'"
          :height="4"
          :show-indicator="false"
        />
        <div class="op-detail">
          <span v-if="op.total">{{ op.done }}/{{ op.total }}</span>
          <span v-if="op.error" class="op-error">{{ op.error }}</span>
        </div>
      </div>
    </NCard>
  </Transition>
</template>

<style scoped>
.op-panel {
  position: fixed;
  bottom: 36px;
  left: 16px;
  width: 320px;
  z-index: 200;
  box-shadow: 0 -4px 24px rgba(0, 0, 0, 0.12);
}
.op-item {
  padding: 6px 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}
.op-desc {
  font-size: var(--font-size-sm);
}
.op-detail {
  font-size: var(--font-size-xs);
  color: var(--breeze-text-secondary);
}
.op-error {
  color: var(--breeze-danger);
}
.slide-up-enter-active, .slide-up-leave-active {
  transition: all 0.3s ease;
}
.slide-up-enter-from, .slide-up-leave-to {
  transform: translateY(20px);
  opacity: 0;
}
</style>
