<script setup>
import { ref, computed, onMounted, onUnmounted, nextTick } from 'vue'

const props = defineProps({
  value: { type: [String, Number], default: null },
  options: { type: Array, default: () => [] },
  placeholder: { type: String, default: '' },
})

const emit = defineEmits(['update:value'])

const open = ref(false)
const triggerRef = ref(null)
const dropdownRef = ref(null)
const dropdownStyle = ref({})

const selectedLabel = computed(() => {
  const opt = props.options.find(o => o.value === props.value)
  return opt ? opt.label : ''
})

function toggle() {
  if (open.value) {
    open.value = false
  } else {
    open.value = true
    nextTick(positionDropdown)
  }
}

function select(val) {
  emit('update:value', val)
  open.value = false
}

function positionDropdown() {
  if (!triggerRef.value) return
  const rect = triggerRef.value.getBoundingClientRect()
  dropdownStyle.value = {
    position: 'fixed',
    top: rect.bottom + 2 + 'px',
    left: rect.left + 'px',
    width: rect.width + 'px',
    zIndex: 9999,
  }
}

function onClickOutside(e) {
  if (!triggerRef.value?.contains(e.target) && !dropdownRef.value?.contains(e.target)) {
    open.value = false
  }
}

onMounted(() => document.addEventListener('mousedown', onClickOutside))
onUnmounted(() => document.removeEventListener('mousedown', onClickOutside))
</script>

<template>
  <div class="breeze-select">
    <button ref="triggerRef" type="button" class="breeze-select__trigger" @click="toggle">
      <span v-if="selectedLabel" class="breeze-select__text">{{ selectedLabel }}</span>
      <span v-else class="breeze-select__placeholder">{{ placeholder }}</span>
      <svg class="breeze-select__arrow" :class="{ open }" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><polyline points="6 9 12 15 18 9" /></svg>
    </button>
    <Teleport to="body">
      <Transition name="breeze-dropdown">
        <div v-if="open" ref="dropdownRef" class="breeze-select__dropdown" :style="dropdownStyle">
          <div
            v-for="opt in options"
            :key="opt.value"
            class="breeze-select__option"
            :class="{ active: opt.value === value }"
            @click="select(opt.value)"
          >
            {{ opt.label }}
          </div>
        </div>
      </Transition>
    </Teleport>
  </div>
</template>

<style scoped>
.breeze-select {
  width: 100%;
}
.breeze-select__trigger {
  display: flex;
  align-items: center;
  width: 100%;
  height: 32px;
  padding: 0 8px;
  background: var(--breeze-bg-alt);
  border: 1px solid var(--breeze-border);
  border-radius: 3px;
  color: var(--breeze-text);
  font-family: inherit;
  font-size: 14px;
  cursor: pointer;
  transition: border-color var(--transition-fast);
  text-align: left;
}
.breeze-select__trigger:focus {
  border-color: var(--breeze-accent);
  outline: none;
}
.breeze-select__text {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.breeze-select__placeholder {
  flex: 1;
  color: var(--breeze-text-disabled);
}
.breeze-select__arrow {
  flex-shrink: 0;
  color: var(--breeze-text-secondary);
  transition: transform var(--transition-fast);
}
.breeze-select__arrow.open {
  transform: rotate(180deg);
}
.breeze-select__dropdown {
  background: var(--breeze-surface-raised);
  border: 1px solid var(--breeze-border);
  border-radius: 4px;
  box-shadow: 0 4px 16px rgba(0, 0, 0, 0.4);
  max-height: 240px;
  overflow-y: auto;
  padding: 4px 0;
}
.breeze-select__option {
  padding: 6px 10px;
  font-size: 14px;
  color: var(--breeze-text);
  cursor: pointer;
  transition: background var(--transition-fast);
}
.breeze-select__option:hover {
  background: rgba(255, 255, 255, 0.06);
}
.breeze-select__option.active {
  color: var(--breeze-accent);
}
.breeze-dropdown-enter-active {
  transition: all 0.12s ease;
}
.breeze-dropdown-leave-active {
  transition: all 0.08s ease;
}
.breeze-dropdown-enter-from,
.breeze-dropdown-leave-to {
  opacity: 0;
  transform: scaleY(0.9);
  transform-origin: top;
}
</style>
