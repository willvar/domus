<script setup>
import { toRef } from 'vue'
import { usePanelResize } from '../../../composables/usePanelResize'
import { useTrayPanel } from '../../../composables/useTrayPanel'
import IconPin from '~icons/mdi/pin-outline'
import IconPinOff from '~icons/mdi/pin-off-outline'

const props = defineProps({
  show: { type: Boolean, required: true },
  title: { type: String, default: '' },
})

const emit = defineEmits(['update:show'])

const { panelSize, onMouseDown, onLeftMouseDown } = usePanelResize()
const { panelRef, pinned, togglePin } = useTrayPanel(
  toRef(props, 'show'),
  () => emit('update:show', false),
)
</script>

<template>
  <Transition name="tray-popup">
    <div
      ref="panelRef"
      v-if="show"
      class="tray-popup"
      :style="{ width: panelSize.width + 'px', height: panelSize.height + 'px' }"
    >
      <div class="tray-popup__resize-top" @mousedown="onMouseDown" />
      <div class="tray-popup__resize-left" @mousedown="onLeftMouseDown" />
      <div class="tray-popup__header">
        <span class="tray-popup__title">{{ title }}</span>
        <div class="tray-popup__actions">
          <slot name="actions" />
          <button
            class="tray-popup__pin"
            :class="{ 'tray-popup__pin--active': pinned }"
            :title="pinned ? 'Unpin' : 'Keep Open'"
            @click="togglePin"
          >
            <component :is="pinned ? IconPinOff : IconPin" width="14" height="14" />
          </button>
        </div>
      </div>
      <div class="tray-popup__body">
        <slot />
      </div>
    </div>
  </Transition>
</template>

<style lang="scss" scoped>
.tray-popup {
  position: fixed;
  bottom: 68px;
  right: 8px;
  z-index: $z-tray;
  background: #292c30;
  border: 1px solid #3b4045;
  border-radius: 8px;
  box-shadow: 0 4px 24px rgba(0, 0, 0, 0.4);
  display: flex;
  flex-direction: column;
  overflow: hidden;

  &__resize-top {
    height: 4px;
    cursor: ns-resize;
    flex-shrink: 0;

    &:hover { background: rgba(61, 174, 233, 0.3); }
  }

  &__resize-left {
    position: absolute;
    left: 0;
    top: 0;
    bottom: 0;
    width: 4px;
    cursor: ew-resize;
    z-index: 1;

    &:hover { background: rgba(61, 174, 233, 0.3); }
  }

  &__header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 10px 12px;
    border-bottom: 1px solid #3b4045;
    flex-shrink: 0;
  }

  &__title {
    font-size: 13px;
    font-weight: 600;
    color: #fcfcfc;
  }

  &__actions {
    display: flex;
    align-items: center;
    gap: 2px;
  }

  &__pin {
    @include inline-flex-center;
    padding: 4px;
    border: none;
    border-radius: 3px;
    background: none;
    color: var(--breeze-text-secondary, #a1a9b1);
    cursor: default;

    &:hover {
      background: $hover-white-medium;
      color: var(--breeze-text, #fcfcfc);
    }

    &--active {
      color: var(--breeze-accent, #3daee9);
    }
  }

  &__body {
    flex: 1;
    min-height: 0;
    overflow-y: auto;
  }
}

@include mobile {
  .tray-popup {
    left: 8px;
    right: 8px;
    width: auto !important;
    bottom: 72px;

    &__resize-top,
    &__resize-left {
      display: none;
    }
  }
}

.tray-popup-enter-active,
.tray-popup-leave-active {
  transition: all 0.2s ease;
}

.tray-popup-enter-from,
.tray-popup-leave-to {
  opacity: 0;
  transform: translateY(12px);
}
</style>
