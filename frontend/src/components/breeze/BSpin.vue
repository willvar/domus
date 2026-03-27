<script setup>
defineProps({
  show: { type: Boolean, default: true },
  size: { type: String, default: 'medium' },
})

const sizes = { small: 16, medium: 24, large: 40 }
</script>

<template>
  <div v-if="$slots.default" class="breeze-spin-wrap">
    <slot />
    <Transition name="breeze-spin-fade">
      <div v-if="show" class="breeze-spin-overlay">
        <div class="breeze-spinner" :style="{ width: sizes[size] + 'px', height: sizes[size] + 'px' }" />
      </div>
    </Transition>
  </div>
  <div v-else class="breeze-spin-standalone">
    <div class="breeze-spinner" :style="{ width: sizes[size] + 'px', height: sizes[size] + 'px' }" />
  </div>
</template>

<style scoped>
.breeze-spin-wrap {
  position: relative;
  min-height: inherit;
  height: inherit;
}
.breeze-spin-overlay {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(20, 22, 24, 0.45);
  z-index: 5;
}
.breeze-spin-standalone {
  display: flex;
  align-items: center;
  justify-content: center;
}
.breeze-spinner {
  border: 2px solid var(--breeze-border);
  border-top-color: var(--breeze-accent);
  border-radius: 50%;
  animation: breeze-spin 0.7s linear infinite;
}
.breeze-spin-fade-enter-active,
.breeze-spin-fade-leave-active {
  transition: opacity 0.2s ease;
}
.breeze-spin-fade-enter-from,
.breeze-spin-fade-leave-to {
  opacity: 0;
}
@keyframes breeze-spin {
  to { transform: rotate(360deg); }
}
</style>
