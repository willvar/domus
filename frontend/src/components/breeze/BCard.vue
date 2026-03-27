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

<style scoped>
.breeze-card {
  background: var(--breeze-surface);
  border-radius: 6px;
}
.breeze-card--bordered {
  border: 1px solid var(--breeze-border);
}
.breeze-card__header {
  padding: 12px 16px;
  font-weight: 600;
  font-size: 15px;
  color: var(--breeze-text);
  border-bottom: 1px solid var(--breeze-border);
}
.breeze-card--small .breeze-card__header {
  padding: 8px 12px;
  font-size: 14px;
}
.breeze-card__body {
  padding: 16px;
}
.breeze-card--small .breeze-card__body {
  padding: 12px;
}
.breeze-card__footer {
  padding: 12px 16px;
  border-top: 1px solid var(--breeze-border);
}
.breeze-card--small .breeze-card__footer {
  padding: 8px 12px;
}
</style>
