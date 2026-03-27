<script setup>
import { ref, onMounted } from 'vue'

const props = defineProps({
  value: { type: String, default: '' },
  type: { type: String, default: 'text' },
  placeholder: { type: String, default: '' },
  size: { type: String, default: 'medium' },
  disabled: { type: Boolean, default: false },
  readonly: { type: Boolean, default: false },
  maxlength: { type: [String, Number], default: undefined },
  showPasswordOn: { type: String, default: '' },
  autofocus: { type: Boolean, default: false },
})

const emit = defineEmits(['update:value', 'keydown', 'keyup', 'blur', 'focus'])

const inputEl = ref(null)
const showPwd = ref(false)

const inputType = computed(() => {
  if (props.type === 'password' && showPwd.value) return 'text'
  return props.type
})

import { computed } from 'vue'

function onInput(e) {
  emit('update:value', e.target.value)
}

onMounted(() => {
  if (props.autofocus) inputEl.value?.focus()
})

defineExpose({
  focus: () => inputEl.value?.focus(),
  select: (start, end) => {
    inputEl.value?.focus()
    if (start !== undefined && end !== undefined) {
      inputEl.value?.setSelectionRange(start, end)
    } else {
      inputEl.value?.select()
    }
  },
  blur: () => inputEl.value?.blur(),
})
</script>

<template>
  <div class="breeze-input" :class="[`breeze-input--${size}`, { 'breeze-input--disabled': disabled }]">
    <input
      ref="inputEl"
      class="breeze-input__inner"
      :type="inputType"
      :value="value"
      :placeholder="placeholder"
      :disabled="disabled"
      :readonly="readonly"
      :maxlength="maxlength"
      @input="onInput"
      @keydown="emit('keydown', $event)"
      @keyup="emit('keyup', $event)"
      @blur="emit('blur', $event)"
      @focus="emit('focus', $event)"
    />
    <button
      v-if="type === 'password' && showPasswordOn === 'click'"
      type="button"
      class="breeze-input__eye"
      tabindex="-1"
      @click="showPwd = !showPwd"
    >
      <svg v-if="showPwd" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <path d="M1 12s4-8 11-8 11 8 11 8-4 8-11 8-11-8-11-8z" /><circle cx="12" cy="12" r="3" />
      </svg>
      <svg v-else width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
        <path d="M17.94 17.94A10.07 10.07 0 0 1 12 20c-7 0-11-8-11-8a18.45 18.45 0 0 1 5.06-5.94M9.9 4.24A9.12 9.12 0 0 1 12 4c7 0 11 8 11 8a18.5 18.5 0 0 1-2.16 3.19m-6.72-1.07a3 3 0 1 1-4.24-4.24" />
        <line x1="1" y1="1" x2="23" y2="23" />
      </svg>
    </button>
  </div>
</template>

<style scoped>
.breeze-input {
  display: flex;
  align-items: center;
  background: var(--breeze-bg-alt);
  border: 1px solid var(--breeze-border);
  border-radius: 3px;
  transition: border-color var(--transition-fast);
  width: 100%;
}
.breeze-input:focus-within {
  border-color: var(--breeze-accent);
}
.breeze-input--disabled {
  opacity: 0.55;
}
.breeze-input__inner {
  flex: 1;
  min-width: 0;
  border: none;
  background: transparent;
  color: var(--breeze-text);
  font-family: inherit;
  outline: none;
  caret-color: var(--breeze-accent);
  padding: 0 8px;
  width: 100%;
}
.breeze-input__inner::placeholder {
  color: var(--breeze-text-disabled);
}
/* Sizes */
.breeze-input--medium { height: 32px; }
.breeze-input--medium .breeze-input__inner { font-size: 14px; }
.breeze-input--small { height: 28px; }
.breeze-input--small .breeze-input__inner { font-size: 13px; }
.breeze-input--tiny { height: 22px; }
.breeze-input--tiny .breeze-input__inner { font-size: 12px; }
/* Password eye */
.breeze-input__eye {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 100%;
  border: none;
  background: none;
  color: var(--breeze-text-secondary);
  cursor: pointer;
  flex-shrink: 0;
  padding: 0;
}
.breeze-input__eye:hover {
  color: var(--breeze-text);
}
</style>
