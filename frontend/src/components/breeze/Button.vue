<script setup lang="ts">
const props = defineProps({
  type: { type: String, default: 'default' },
  size: { type: String, default: 'medium' },
  loading: { type: Boolean, default: false },
  disabled: { type: Boolean, default: false },
  block: { type: Boolean, default: false },
  quaternary: { type: Boolean, default: false },
  attrType: { type: String as () => 'button' | 'submit' | 'reset', default: 'button' },
})

const emit = defineEmits(['click'])

function handleClick(e: MouseEvent) {
  if (props.loading || props.disabled) return
  emit('click', e)
}
</script>

<template>
  <button
    class="breeze-btn"
    :class="[
      `breeze-btn--${type}`,
      `breeze-btn--${size}`,
      {
        'breeze-btn--block': block,
        'breeze-btn--quaternary': quaternary,
        'breeze-btn--loading': loading,
        'breeze-btn--disabled': disabled,
      }
    ]"
    :type="attrType"
    :disabled="disabled || loading"
    @click="handleClick"
  >
    <span v-if="loading" class="breeze-btn__spinner" />
    <span v-if="$slots.icon" class="breeze-btn__icon"><slot name="icon" /></span>
    <span v-if="$slots.default" class="breeze-btn__content"><slot /></span>
  </button>
</template>

<style lang="scss" scoped>
.breeze-btn {
  @include inline-flex-center;
  gap: 6px;
  border-radius: 3px;
  font-family: inherit;
  font-size: 14px;
  cursor: pointer;
  white-space: nowrap;
  transition: background var(--transition-fast), border-color var(--transition-fast), opacity var(--transition-fast);
  border: 1px solid var(--breeze-border);
  background: var(--breeze-surface-raised);
  color: var(--breeze-text);
  padding: 0 12px;

  // Sizes
  &--medium { height: 32px; font-size: 14px; }
  &--small { height: 28px; font-size: 13px; padding: 0 10px; }
  &--tiny { height: 22px; font-size: 12px; padding: 0 8px; }

  // Block
  &--block { width: 100%; }

  // Types
  &--primary {
    background: var(--breeze-accent);
    border-color: var(--breeze-accent);
    color: #fff;

    &:active:not(:disabled) {
      background: var(--breeze-accent-hover);
      border-color: var(--breeze-accent-hover);
    }

    @include hover {
      background: var(--breeze-accent-hover);
      border-color: var(--breeze-accent-hover);
    }
  }

  &--error {
    background: transparent;
    border-color: var(--breeze-danger);
    color: var(--breeze-danger);

    &:active:not(:disabled) {
      background: rgba(218, 68, 83, 0.1);
    }

    @include hover {
      background: rgba(218, 68, 83, 0.1);
    }
  }

  &--warning {
    background: transparent;
    border-color: var(--breeze-warning);
    color: var(--breeze-warning);

    &:active:not(:disabled) {
      background: rgba(246, 116, 0, 0.1);
    }

    @include hover {
      background: rgba(246, 116, 0, 0.1);
    }
  }

  &--default {
    &:active:not(:disabled) {
      background: $hover-white-medium;
    }

    @include hover {
      background: $hover-white-medium;
    }
  }

  // Quaternary
  &--quaternary {
    background: transparent;
    border: none;

    &:active:not(:disabled) {
      background: $hover-white-medium;
    }

    @include hover {
      background: $hover-white-medium;
    }
  }

  // States
  &--loading,
  &--disabled {
    opacity: 0.55;
    cursor: not-allowed;
  }

  // Icon
  &__icon {
    @include inline-flex-center;
    line-height: 0;
  }

  // Spinner
  &__spinner {
    width: 14px;
    height: 14px;
    border: 2px solid currentColor;
    border-top-color: transparent;
    border-radius: 50%;
    animation: breeze-btn-spin 0.6s linear infinite;
    flex-shrink: 0;
  }
}

@keyframes breeze-btn-spin {
  to { transform: rotate(360deg); }
}
</style>
