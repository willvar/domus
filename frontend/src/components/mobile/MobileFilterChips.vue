<script setup lang="ts">
import type { PropType } from 'vue'

defineProps({
  options: {
    type: Array as PropType<Array<{ key: string; label: string }>>,
    default: () => [],
  },
  active: { type: String, default: '' },
})

const emit = defineEmits(['select'])
</script>

<template>
  <div class="mobile-filter-chips">
    <button
      v-for="item in options"
      :key="item.key"
      class="mobile-filter-chip"
      :class="{ 'mobile-filter-chip--active': active === item.key }"
      @click="emit('select', item.key)"
    >
      {{ item.label }}
    </button>
  </div>
</template>

<style lang="scss" scoped>
.mobile-filter-chips {
  display: flex;
  gap: 10px;
  overflow-x: auto;
  padding-bottom: 2px;
  scrollbar-width: none;

  &::-webkit-scrollbar {
    display: none;
  }
}

.mobile-filter-chip {
  min-height: 36px;
  border: none;
  border-radius: 999px;
  padding: 0 14px;
  background: rgba(255, 255, 255, 0.82);
  color: rgba(16, 32, 48, 0.68);
  font-size: 13px;
  font-weight: 700;
  white-space: nowrap;

  &--active {
    background: rgba(61, 174, 233, 0.14);
    color: #1576b8;
  }
}
</style>
