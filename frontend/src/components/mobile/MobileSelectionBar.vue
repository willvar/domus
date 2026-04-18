<script setup lang="ts">
import { useI18n } from '../../composables/useI18n'

defineProps({
  count: { type: Number, default: 0 },
})

const emit = defineEmits(['copy', 'cut', 'delete', 'cancel'])
const { t } = useI18n()
</script>

<template>
  <div class="mobile-selection-bar">
    <div class="mobile-selection-bar-count">{{ t('mobile.selection.selected', { n: count }) }}</div>
    <div class="mobile-selection-bar-actions">
      <button @click="emit('copy')">{{ t('mobile.selection.copy') }}</button>
      <button @click="emit('cut')">{{ t('mobile.selection.cut') }}</button>
      <button class="danger" @click="emit('delete')">{{ t('mobile.selection.delete') }}</button>
      <button @click="emit('cancel')">{{ t('mobile.selection.done') }}</button>
    </div>
  </div>
</template>

<style lang="scss" scoped>
.mobile-selection-bar {
  position: sticky;
  bottom: 92px;
  z-index: 12;
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 14px 16px;
  border-top: 1px solid rgba(16, 32, 48, 0.08);
  background: rgba(255, 255, 255, 0.98);
  backdrop-filter: blur(12px);

  &-count {
    font-size: 13px;
    font-weight: 700;
    color: #102030;
  }

  &-actions {
    display: grid;
    grid-template-columns: repeat(4, 1fr);
    gap: 8px;

    button {
      min-height: 42px;
      border: none;
      border-radius: 14px;
      background: rgba(16, 32, 48, 0.06);
      color: #102030;
      font-size: 13px;
      font-weight: 600;
    }

    .danger {
      background: rgba(218, 68, 83, 0.12);
      color: #c23242;
    }
  }
}
</style>
