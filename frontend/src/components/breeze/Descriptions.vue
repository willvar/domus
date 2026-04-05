<script setup lang="ts">
defineProps({
  column: { type: Number, default: 1 },
  size: { type: String, default: 'medium' },
  labelPlacement: { type: String, default: 'left' },
})
</script>

<template>
  <dl class="breeze-descriptions" :class="`breeze-descriptions--${size}`">
    <slot />
  </dl>
</template>

<style lang="scss" scoped>
.breeze-descriptions {
  margin: 0;
  padding: 0;
}
</style>
