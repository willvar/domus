<script setup>
defineProps({
  value: { type: Boolean, default: false },
})

const emit = defineEmits(['update:value'])
</script>

<template>
  <button
    type="button"
    class="breeze-switch"
    :class="{ 'breeze-switch--on': value }"
    role="switch"
    :aria-checked="value"
    @click="emit('update:value', !value)"
  >
    <div class="breeze-switch__thumb" />
  </button>
</template>

<style scoped>
.breeze-switch {
  position: relative;
  display: inline-flex;
  align-items: center;
  width: 36px;
  height: 20px;
  border: none;
  border-radius: 10px;
  background: var(--breeze-border);
  cursor: pointer;
  padding: 2px;
  transition: background var(--transition-normal);
  flex-shrink: 0;
}
.breeze-switch--on {
  background: var(--breeze-accent);
}
.breeze-switch__thumb {
  width: 16px;
  height: 16px;
  border-radius: 50%;
  background: #fff;
  transition: transform var(--transition-normal);
  box-shadow: 0 1px 3px rgba(0, 0, 0, 0.3);
}
.breeze-switch--on .breeze-switch__thumb {
  transform: translateX(16px);
}
</style>
