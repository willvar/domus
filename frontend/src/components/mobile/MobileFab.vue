<script setup lang="ts">
import { IconPlus } from '../../barrels/icons'

const emit = defineEmits(['click'])
</script>

<template>
  <button class="mobile-fab" @click="emit('click')">
    <IconPlus width="24" height="24" />
  </button>
</template>

<style lang="scss" scoped>
.mobile-fab {
  position: fixed;
  right: 20px;
  bottom: calc(92px + env(safe-area-inset-bottom));
  z-index: 61;
  width: 60px;
  height: 60px;
  border: none;
  border-radius: 20px;
  background: linear-gradient(135deg, #1576b8, #3daee9);
  color: #fff;
  box-shadow: 0 18px 30px rgba(21, 118, 184, 0.32);
  transition: transform 0.18s ease, box-shadow 0.18s ease;

  &:active {
    transform: translateY(1px) scale(0.97);
    box-shadow: 0 12px 20px rgba(21, 118, 184, 0.24);
  }
}
</style>
