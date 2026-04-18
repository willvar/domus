<script setup lang="ts">
import { useI18n } from '../../composables/useI18n'

defineProps({
  title: { type: String, default: '' },
  subtitle: { type: String, default: '' },
  showBack: { type: Boolean, default: false },
})

const emit = defineEmits(['back'])
const { t } = useI18n()
</script>

<template>
  <header class="mobile-topbar">
    <button v-if="showBack" class="mobile-topbar-back" @click="emit('back')">
      <slot name="back">{{ t('common.back') }}</slot>
    </button>
    <div class="mobile-topbar-copy">
      <div class="mobile-topbar-title">{{ title }}</div>
      <div v-if="subtitle" class="mobile-topbar-subtitle">{{ subtitle }}</div>
    </div>
    <div class="mobile-topbar-actions">
      <slot name="actions" />
    </div>
  </header>
</template>

<style lang="scss" scoped>
.mobile-topbar {
  display: flex;
  align-items: center;
  gap: 12px;
  min-height: 64px;
  padding: calc(12px + env(safe-area-inset-top)) 16px 12px;
  background: linear-gradient(180deg, rgba(255, 255, 255, 0.97), rgba(255, 255, 255, 0.92));
  border-bottom: 1px solid rgba(24, 36, 48, 0.08);
  backdrop-filter: blur(12px);

  &-back {
    border: none;
    background: rgba(24, 36, 48, 0.06);
    color: #102030;
    min-width: 40px;
    height: 40px;
    border-radius: 12px;
    padding: 0 12px;
    font-size: 14px;
  }

  &-copy {
    min-width: 0;
    flex: 1;
  }

  &-title {
    font-size: 18px;
    font-weight: 700;
    color: #102030;
    @include truncate;
  }

  &-subtitle {
    margin-top: 2px;
    font-size: 12px;
    color: rgba(16, 32, 48, 0.65);
    @include truncate;
  }

  &-actions {
    display: flex;
    align-items: center;
    gap: 8px;
  }
}
</style>
