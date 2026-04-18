<script setup lang="ts">
import { Drawer } from '../../barrels/breeze'

defineProps({
  show: { type: Boolean, default: false },
  title: { type: String, default: '' },
})

const emit = defineEmits(['update:show'])
</script>

<template>
  <Drawer :show="show" placement="bottom" height="auto" @update:show="emit('update:show', $event)">
    <div class="mobile-sheet">
      <div class="mobile-sheet-handle" />
      <div v-if="title" class="mobile-sheet-title">{{ title }}</div>
      <slot />
    </div>
  </Drawer>
</template>

<style lang="scss" scoped>
.mobile-sheet {
  padding: 8px 16px 20px;

  &-handle {
    width: 42px;
    height: 5px;
    margin: 4px auto 14px;
    border-radius: 999px;
    background: rgba(16, 32, 48, 0.14);
  }

  &-title {
    margin-bottom: 14px;
    font-size: 13px;
    font-weight: 700;
    letter-spacing: 0.04em;
    text-transform: uppercase;
    color: rgba(16, 32, 48, 0.5);
  }
}
</style>
