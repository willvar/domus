<script setup>
import { watch, nextTick } from 'vue'
import BButton from './BButton.vue'

const props = defineProps({
  show: { type: Boolean, default: false },
  preset: { type: String, default: '' },
  title: { type: String, default: '' },
  positiveText: { type: String, default: '' },
  negativeText: { type: String, default: '' },
})

const emit = defineEmits(['update:show', 'positive-click', 'negative-click', 'close', 'mask-click'])

function onMaskClick() {
  emit('mask-click')
}

function onClose() {
  emit('close')
}

function onPositive() {
  emit('positive-click')
}

function onNegative() {
  emit('negative-click')
}

watch(() => props.show, (v) => {
  if (v) {
    document.body.style.overflow = 'hidden'
  } else {
    document.body.style.overflow = ''
  }
})
</script>

<template>
  <Teleport to="body">
    <Transition name="breeze-modal">
      <div v-if="show" class="breeze-modal-mask" @click.self="onMaskClick">
        <div v-if="preset === 'dialog'" class="breeze-modal-dialog" style="width: 400px; max-width: 95vw">
          <div class="breeze-modal-dialog__header">
            <span class="breeze-modal-dialog__title">{{ title }}</span>
            <button type="button" class="breeze-modal-dialog__close" @click="onClose">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2"><line x1="18" y1="6" x2="6" y2="18" /><line x1="6" y1="6" x2="18" y2="18" /></svg>
            </button>
          </div>
          <div class="breeze-modal-dialog__body">
            <slot />
          </div>
          <div v-if="$slots.action || positiveText || negativeText" class="breeze-modal-dialog__footer">
            <slot name="action">
              <div style="display:flex;justify-content:flex-end;gap:8px">
                <BButton v-if="negativeText" @click="onNegative">{{ negativeText }}</BButton>
                <BButton v-if="positiveText" type="primary" @click="onPositive">{{ positiveText }}</BButton>
              </div>
            </slot>
          </div>
        </div>
        <div v-else class="breeze-modal-bare">
          <slot />
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.breeze-modal-mask {
  position: fixed;
  inset: 0;
  z-index: 5000;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
}
.breeze-modal-dialog {
  background: var(--breeze-surface);
  border: 1px solid var(--breeze-border);
  border-radius: 6px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.5);
  display: flex;
  flex-direction: column;
}
.breeze-modal-dialog__header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 12px 16px;
  border-bottom: 1px solid var(--breeze-border);
}
.breeze-modal-dialog__title {
  font-size: 15px;
  font-weight: 600;
  color: var(--breeze-text);
}
.breeze-modal-dialog__close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border: none;
  background: none;
  color: var(--breeze-text-secondary);
  border-radius: 3px;
  cursor: pointer;
  padding: 0;
}
.breeze-modal-dialog__close:hover {
  background: rgba(255, 255, 255, 0.08);
  color: var(--breeze-text);
}
.breeze-modal-dialog__body {
  padding: 16px;
}
.breeze-modal-dialog__footer {
  padding: 12px 16px;
  border-top: 1px solid var(--breeze-border);
}
.breeze-modal-bare {
  /* bare modal: just centers content, no chrome */
}
/* Transitions */
.breeze-modal-enter-active {
  transition: opacity 0.2s ease;
}
.breeze-modal-enter-active .breeze-modal-dialog,
.breeze-modal-enter-active .breeze-modal-bare {
  transition: transform 0.2s ease, opacity 0.2s ease;
}
.breeze-modal-leave-active {
  transition: opacity 0.15s ease;
}
.breeze-modal-leave-active .breeze-modal-dialog,
.breeze-modal-leave-active .breeze-modal-bare {
  transition: transform 0.15s ease, opacity 0.15s ease;
}
.breeze-modal-enter-from {
  opacity: 0;
}
.breeze-modal-enter-from .breeze-modal-dialog,
.breeze-modal-enter-from .breeze-modal-bare {
  opacity: 0;
  transform: scale(0.96);
}
.breeze-modal-leave-to {
  opacity: 0;
}
.breeze-modal-leave-to .breeze-modal-dialog,
.breeze-modal-leave-to .breeze-modal-bare {
  opacity: 0;
  transform: scale(0.96);
}

@media (max-width: 767px) {
  .breeze-modal-dialog {
    max-height: 90vh;
    overflow-y: auto;
  }
  .breeze-modal-dialog__footer :deep(div) {
    flex-direction: column;
  }
  .breeze-modal-dialog__footer :deep(button) {
    width: 100%;
  }
}
</style>
