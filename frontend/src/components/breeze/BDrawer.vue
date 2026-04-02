<script setup>
const props = defineProps({
  show: { type: Boolean, default: false },
  placement: { type: String, default: 'bottom' },
  height: { type: String, default: 'auto' },
})

const emit = defineEmits(['update:show'])

function onMaskClick() {
  emit('update:show', false)
}
</script>

<template>
  <Teleport to="body">
    <Transition name="breeze-drawer">
      <div v-if="show" class="breeze-drawer-mask" @click.self="onMaskClick">
        <div
          class="breeze-drawer-panel"
          :class="`breeze-drawer-panel--${placement}`"
          :style="{ height: height === 'auto' ? 'auto' : height }"
        >
          <slot />
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style lang="scss" scoped>
.breeze-drawer-mask {
  position: fixed;
  inset: 0;
  z-index: $z-modal;
  background: rgba(0, 0, 0, 0.4);
}

.breeze-drawer-panel {
  position: absolute;
  background: var(--breeze-surface-raised);
  overflow-y: auto;

  &--bottom {
    bottom: 0;
    left: 0;
    right: 0;
    border-radius: 12px 12px 0 0;
    max-height: 80vh;
    padding-bottom: env(safe-area-inset-bottom);
  }

  &--right {
    top: 0;
    right: 0;
    bottom: 0;
    width: 320px;
    max-width: 80vw;
  }

  &--left {
    top: 0;
    left: 0;
    bottom: 0;
    width: 320px;
    max-width: 80vw;
  }
}

/* Transitions */
.breeze-drawer-enter-active {
  transition: opacity 0.25s ease;

  .breeze-drawer-panel {
    transition: transform 0.25s ease;
  }
}

.breeze-drawer-leave-active {
  transition: opacity 0.2s ease;

  .breeze-drawer-panel {
    transition: transform 0.2s ease;
  }
}

.breeze-drawer-enter-from {
  opacity: 0;

  .breeze-drawer-panel--bottom {
    transform: translateY(100%);
  }

  .breeze-drawer-panel--right {
    transform: translateX(100%);
  }

  .breeze-drawer-panel--left {
    transform: translateX(-100%);
  }
}

.breeze-drawer-leave-to {
  opacity: 0;

  .breeze-drawer-panel--bottom {
    transform: translateY(100%);
  }

  .breeze-drawer-panel--right {
    transform: translateX(100%);
  }

  .breeze-drawer-panel--left {
    transform: translateX(-100%);
  }
}
</style>
