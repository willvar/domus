<script setup lang="ts">
import { IconClose } from '../../barrels/icons'

defineProps({
  modelValue: { type: String, default: '' },
  placeholder: { type: String, default: '' },
})

const emit = defineEmits(['update:modelValue', 'clear'])
</script>

<template>
  <div class="mobile-search-box">
    <input
      :value="modelValue"
      type="text"
      :placeholder="placeholder"
      class="mobile-search-input"
      autofocus
      @input="emit('update:modelValue', ($event.target as HTMLInputElement).value)"
    />
    <button v-if="modelValue" class="mobile-search-clear" @click="emit('clear')">
      <IconClose width="14" height="14" />
    </button>
  </div>
</template>

<style lang="scss" scoped>
.mobile-search-box {
  display: flex;
  align-items: center;
  gap: 8px;
  min-height: 56px;
  border-radius: 20px;
  background: rgba(255, 255, 255, 0.96);
  box-shadow: 0 12px 28px rgba(11, 24, 36, 0.06);
  padding: 0 12px 0 16px;
}

.mobile-search-input {
  flex: 1;
  border: none;
  background: transparent;
  color: #102030;
  font-size: 15px;
  outline: none;
}

.mobile-search-clear {
  width: 36px;
  height: 36px;
  border: none;
  border-radius: 12px;
  background: rgba(16, 32, 48, 0.06);
}
</style>
