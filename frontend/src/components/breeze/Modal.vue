<script setup lang="ts">
import { watch } from 'vue'
import Button from './Button.vue'

const props = defineProps({
  show: { type: Boolean, default: false },
  preset: { type: String, default: '' },
  title: { type: String, default: '' },
  positiveText: { type: String, default: '' },
  positiveType: { type: String, default: 'primary' },
  negativeText: { type: String, default: '' },
  zIndex: { type: Number, default: null },
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
      <div v-if="show" class="breeze-modal-mask" :style="props.zIndex != null ? { zIndex: String(props.zIndex) } : undefined" @click.self="onMaskClick">
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
                <Button v-if="negativeText" @click="onNegative">{{ negativeText }}</Button>
                <Button v-if="positiveText" :type="positiveType" @click="onPositive">{{ positiveText }}</Button>
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

<style lang="scss" scoped>
.breeze-modal-mask {
  position: fixed;
  inset: 0;
  z-index: $z-modal;
  background: rgba(0, 0, 0, 0.5);
  @include flex-center;
}

.breeze-modal-dialog {
  background: var(--breeze-surface);
  border: 1px solid var(--breeze-border);
  border-radius: 6px;
  box-shadow: 0 8px 32px rgba(0, 0, 0, 0.5);
  display: flex;
  flex-direction: column;

  @include mobile {
    max-height: 90vh;
    overflow-y: auto;
  }

  &__header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 12px 16px;
    border-bottom: 1px solid var(--breeze-border);
  }

  &__title {
    font-size: 15px;
    font-weight: 600;
    color: var(--breeze-text);
  }

  &__close {
    @include inline-flex-center;
    width: 24px;
    height: 24px;
    border: none;
    background: none;
    color: var(--breeze-text-secondary);
    border-radius: 3px;
    cursor: pointer;
    padding: 0;

    &:active {
      background: $hover-white-medium;
      color: var(--breeze-text);
    }

    @include hover {
      background: $hover-white-medium;
      color: var(--breeze-text);
    }
  }

  &__body {
    padding: 16px;

    &:not(:has(*)) {
      display: none;
    }
  }

  &__footer {
    padding: 12px 16px;
    border-top: 1px solid var(--breeze-border);

    @include mobile {
      :deep(div) {
        flex-direction: column;
      }

      :deep(button) {
        width: 100%;
      }
    }
  }
}

.breeze-modal-bare {
  /* bare modal: just centers content, no chrome */
}

/* Transitions */
.breeze-modal-enter-active {
  transition: opacity 0.2s ease;

  .breeze-modal-dialog,
  .breeze-modal-bare {
    transition: transform 0.2s ease, opacity 0.2s ease;
  }
}

.breeze-modal-leave-active {
  transition: opacity 0.15s ease;

  .breeze-modal-dialog,
  .breeze-modal-bare {
    transition: transform 0.15s ease, opacity 0.15s ease;
  }
}

.breeze-modal-enter-from {
  opacity: 0;

  .breeze-modal-dialog,
  .breeze-modal-bare {
    opacity: 0;
    transform: scale(0.96);
  }
}

.breeze-modal-leave-to {
  opacity: 0;

  .breeze-modal-dialog,
  .breeze-modal-bare {
    opacity: 0;
    transform: scale(0.96);
  }
}
</style>
