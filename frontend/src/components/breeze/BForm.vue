<script setup>
import { provide } from 'vue'

const props = defineProps({
  labelPlacement: { type: String, default: 'top' },
  labelWidth: { type: String, default: 'auto' },
})

provide('bFormCtx', props)
</script>

<template>
  <form class="breeze-form" @submit="$emit('submit', $event)">
    <slot />
  </form>
</template>

<script>
export default { emits: ['submit'] }
</script>

<style scoped>
.breeze-form {
  display: flex;
  flex-direction: column;
  gap: 0;
}
</style>
