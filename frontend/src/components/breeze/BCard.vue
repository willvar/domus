<script setup>
defineProps({
  title: { type: String, default: '' },
  bordered: { type: Boolean, default: true },
  size: { type: String, default: 'medium' },
})
</script>

<template>
  <div
    class="breeze-card"
    :class="[`breeze-card--${size}`, { 'breeze-card--bordered': bordered }]"
  >
    <div v-if="title" class="breeze-card__header">{{ title }}</div>
    <div class="breeze-card__body"><slot /></div>
    <div v-if="$slots.footer" class="breeze-card__footer"><slot name="footer" /></div>
  </div>
</template>

<style lang="scss" scoped>
.breeze-card {
  background: var(--breeze-surface);
  border-radius: 6px;

  &--bordered {
    border: 1px solid var(--breeze-border);
  }

  &__header {
    padding: 12px 16px;
    font-weight: 600;
    font-size: 15px;
    color: var(--breeze-text);
    border-bottom: 1px solid var(--breeze-border);
  }

  &--small &__header {
    padding: 8px 12px;
    font-size: 14px;
  }

  &__body {
    padding: 16px;
  }

  &--small &__body {
    padding: 12px;
  }

  &__footer {
    padding: 12px 16px;
    border-top: 1px solid var(--breeze-border);
  }

  &--small &__footer {
    padding: 8px 12px;
  }
}
</style>
