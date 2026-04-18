<script setup lang="ts">
import type { PropType } from 'vue'
import type { MobileTabKey } from '../../stores/mobileUi'
import { useI18n } from '../../composables/useI18n'
import { IconFolderHome, IconTimerSand, IconAccountArrowLeftOutline, IconAccountCircle } from '../../barrels/icons'

defineProps({
  active: { type: String as PropType<MobileTabKey>, default: 'files' },
  activityCount: { type: Number, default: 0 },
})

const emit = defineEmits(['select'])
const { t } = useI18n()

const items = [
  { key: 'files', label: 'mobile.nav.files', icon: IconFolderHome },
  { key: 'recent', label: 'mobile.nav.recent', icon: IconTimerSand },
  { key: 'shared', label: 'mobile.nav.shared', icon: IconAccountArrowLeftOutline },
  { key: 'me', label: 'mobile.nav.me', icon: IconAccountCircle },
]
</script>

<template>
  <nav class="mobile-bottom-nav">
    <button
      v-for="item in items"
      :key="item.key"
      class="mobile-bottom-nav-item"
      :class="{ 'mobile-bottom-nav-item--active': active === item.key }"
      @click="emit('select', item.key)"
    >
      <component :is="item.icon" width="22" height="22" />
      <span>{{ t(item.label) }}</span>
      <span v-if="item.key === 'recent' && activityCount > 0" class="mobile-bottom-nav-badge">{{ activityCount }}</span>
    </button>
  </nav>
</template>

<style lang="scss" scoped>
.mobile-bottom-nav {
  position: fixed;
  left: 12px;
  right: 12px;
  bottom: calc(12px + env(safe-area-inset-bottom));
  z-index: 60;
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 6px;
  padding: 8px;
  border-radius: 22px;
  background: rgba(15, 21, 28, 0.92);
  box-shadow: 0 20px 60px rgba(4, 12, 20, 0.24);
  backdrop-filter: blur(18px);

  &-item {
    position: relative;
    display: flex;
    flex-direction: column;
    align-items: center;
    justify-content: center;
    gap: 4px;
    min-height: 56px;
    border: none;
    border-radius: 16px;
    background: transparent;
    color: rgba(231, 238, 244, 0.62);
    font-size: 11px;
    font-weight: 600;
    transition: transform 0.16s ease, background 0.16s ease, color 0.16s ease;

    &:active {
      transform: scale(0.96);
    }

    &--active {
      background: rgba(61, 174, 233, 0.18);
      color: #f7fbff;
    }
  }

  &-badge {
    position: absolute;
    top: 4px;
    right: 18px;
    min-width: 18px;
    height: 18px;
    padding: 0 5px;
    border-radius: 999px;
    background: #da4453;
    color: #fff;
    font-size: 10px;
    line-height: 18px;
  }
}
</style>
