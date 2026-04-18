<script setup lang="ts">
defineProps({
  title: { type: String, default: '' },
  body: { type: String, default: '' },
})
</script>

<template>
  <div class="mobile-empty-state">
    <div class="mobile-empty-state-title">{{ title }}</div>
    <div v-if="body" class="mobile-empty-state-body">{{ body }}</div>
    <slot />
  </div>
</template>

<style lang="scss" scoped>
.mobile-empty-state {
  padding: 36px 20px;
  border-radius: 24px;
  background: rgba(255, 255, 255, 0.82);
  text-align: center;
  box-shadow: 0 12px 28px rgba(11, 24, 36, 0.05);

  &-title {
    font-size: 16px;
    font-weight: 700;
    color: #102030;
  }

  &-body {
    margin-top: 8px;
    font-size: 13px;
    color: rgba(16, 32, 48, 0.58);
    line-height: 1.5;
  }
}
</style>
