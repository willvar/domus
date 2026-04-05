<script setup lang="ts">
defineProps({
  label: { type: String, default: '' },
})
</script>

<template>
  <div class="breeze-desc-item">
    <dt class="breeze-desc-item__label">{{ label }}</dt>
    <dd class="breeze-desc-item__value"><slot /></dd>
  </div>
</template>

<style lang="scss" scoped>
.breeze-desc-item {
  display: flex;
  align-items: baseline;
  padding: 4px 0;
  gap: 8px;

  &__label {
    color: var(--breeze-text-secondary);
    font-size: 13px;
    min-width: 80px;
    flex-shrink: 0;
  }

  &__value {
    color: var(--breeze-text);
    font-size: 13px;
    margin: 0;
    word-break: break-all;
  }
}
</style>
