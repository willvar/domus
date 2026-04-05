<script setup lang="ts">
defineProps({
  name: { type: String, required: true },
  tab: { type: String, default: '' },
})
</script>

<template>
  <slot />
</template>
