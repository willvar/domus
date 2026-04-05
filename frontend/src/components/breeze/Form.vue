<script setup lang="ts">
import { provide } from 'vue'

const props = defineProps({
  labelPlacement: { type: String, default: 'top' },
  labelWidth: { type: String, default: 'auto' },
})
const emit = defineEmits(['submit'])

provide('bFormCtx', props)

function onSubmit(event: Event) {
  emit('submit', event)
}
</script>

<template>
  <form class="breeze-form" @submit="onSubmit">
    <slot />
  </form>
</template>

<style lang="scss" scoped>
.breeze-form {
  display: flex;
  flex-direction: column;
  gap: 0;
}
</style>
