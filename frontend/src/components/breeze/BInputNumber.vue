<script setup>
import { ref, computed } from 'vue'

const props = defineProps({
  value: { type: Number, default: 0 },
  min: { type: Number, default: -Infinity },
  max: { type: Number, default: Infinity },
  step: { type: Number, default: 1 },
})

const emit = defineEmits(['update:value'])

function clamp(v) {
  return Math.min(props.max, Math.max(props.min, v))
}

function onInput(e) {
  const v = parseFloat(e.target.value)
  if (!isNaN(v)) emit('update:value', clamp(v))
}

function increment() {
  emit('update:value', clamp((props.value || 0) + props.step))
}

function decrement() {
  emit('update:value', clamp((props.value || 0) - props.step))
}
</script>

<template>
  <div class="breeze-input-number">
    <button type="button" class="breeze-input-number__btn" tabindex="-1" @click="decrement">−</button>
    <input
      class="breeze-input-number__inner"
      type="number"
      :value="value"
      :min="min"
      :max="max"
      :step="step"
      @input="onInput"
      @blur="onInput"
    />
    <span v-if="$slots.suffix" class="breeze-input-number__suffix"><slot name="suffix" /></span>
    <button type="button" class="breeze-input-number__btn" tabindex="-1" @click="increment">+</button>
  </div>
</template>

<style lang="scss" scoped>
.breeze-input-number {
  display: flex;
  align-items: center;
  background: var(--breeze-bg-alt);
  border: 1px solid var(--breeze-border);
  border-radius: 3px;
  height: 32px;
  width: 100%;
  transition: border-color var(--transition-fast);

  &:focus-within {
    border-color: var(--breeze-accent);
  }

  &__btn {
    @include inline-flex-center;
    width: 28px;
    height: 100%;
    border: none;
    background: none;
    color: var(--breeze-text-secondary);
    font-size: 16px;
    cursor: pointer;
    flex-shrink: 0;
    padding: 0;

    &:hover {
      color: var(--breeze-text);
      background: $hover-white-light;
    }
  }

  &__inner {
    flex: 1;
    min-width: 0;
    border: none;
    background: transparent;
    color: var(--breeze-text);
    font-family: inherit;
    font-size: 14px;
    outline: none;
    text-align: center;
    caret-color: var(--breeze-accent);
    -moz-appearance: textfield;
    padding: 0;

    &::-webkit-inner-spin-button,
    &::-webkit-outer-spin-button {
      -webkit-appearance: none;
      margin: 0;
    }
  }

  &__suffix {
    font-size: 12px;
    color: var(--breeze-text-secondary);
    padding-right: 4px;
    flex-shrink: 0;
  }
}
</style>
