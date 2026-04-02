<script setup>
import { inject } from 'vue'

defineProps({
  label: { type: String, default: '' },
})

const ctx = inject('bFormCtx', { labelPlacement: 'top', labelWidth: 'auto' })
</script>

<template>
  <div class="breeze-form-item" :class="{ 'breeze-form-item--left': ctx.labelPlacement === 'left' }">
    <label
      v-if="label"
      class="breeze-form-item__label"
      :style="ctx.labelPlacement === 'left' ? { minWidth: ctx.labelWidth } : {}"
    >{{ label }}</label>
    <div class="breeze-form-item__content"><slot /></div>
  </div>
</template>

<style lang="scss" scoped>
.breeze-form-item {
  margin-bottom: 12px;

  &__label {
    display: block;
    font-size: 13px;
    color: var(--breeze-text-secondary);
    margin-bottom: 4px;
  }

  &--left {
    display: flex;
    align-items: center;
    gap: 8px;

    .breeze-form-item__label {
      margin-bottom: 0;
      flex-shrink: 0;
    }
  }

  &__content {
    flex: 1;
    min-width: 0;
  }
}
</style>
