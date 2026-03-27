<script setup>
defineProps({
  checked: { type: Boolean, default: false },
})

const emit = defineEmits(['update:checked'])
</script>

<template>
  <label class="breeze-checkbox" @click.prevent="emit('update:checked', !checked)">
    <span class="breeze-checkbox__box" :class="{ 'breeze-checkbox__box--checked': checked }">
      <svg v-if="checked" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="#fff" stroke-width="3"><polyline points="20 6 9 17 4 12" /></svg>
    </span>
    <span v-if="$slots.default" class="breeze-checkbox__label"><slot /></span>
  </label>
</template>

<style scoped>
.breeze-checkbox {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  cursor: pointer;
  user-select: none;
}
.breeze-checkbox__box {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 16px;
  height: 16px;
  border: 1px solid var(--breeze-border);
  border-radius: 2px;
  background: var(--breeze-bg-alt);
  transition: all var(--transition-fast);
  flex-shrink: 0;
}
.breeze-checkbox__box--checked {
  background: var(--breeze-accent);
  border-color: var(--breeze-accent);
}
.breeze-checkbox__label {
  font-size: 14px;
  color: var(--breeze-text);
}
</style>
