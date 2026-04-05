<script setup lang="ts">
defineProps({
  type: { type: String, default: 'default' },
})
</script>

<template>
  <span class="breeze-tag" :class="`breeze-tag--${type}`"><slot /></span>
</template>

<style lang="scss" scoped>
.breeze-tag {
  display: inline-flex;
  align-items: center;
  padding: 2px 8px;
  border-radius: 3px;
  font-size: 12px;
  line-height: 1.5;

  &--default {
    background: var(--breeze-surface-raised);
    color: var(--breeze-text-secondary);
  }

  &--success {
    background: rgba(39, 174, 96, 0.15);
    color: var(--breeze-success);
  }

  &--warning {
    background: rgba(246, 116, 0, 0.15);
    color: var(--breeze-warning);
  }

  &--error {
    background: rgba(218, 68, 83, 0.15);
    color: var(--breeze-danger);
  }
}
</style>
