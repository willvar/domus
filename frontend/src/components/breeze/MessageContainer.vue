<script setup>
import { useMessageState } from '../../composables/useMessage'

const { messages, typeColors } = useMessageState()
</script>

<template>
  <Teleport to="body">
    <div class="breeze-message-container">
      <TransitionGroup name="breeze-msg">
        <div
          v-for="msg in messages"
          :key="msg.id"
          class="breeze-message"
          :style="{ '--msg-accent': typeColors[msg.type] }"
        >
          {{ msg.content }}
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<style lang="scss" scoped>
.breeze-message-container {
  position: fixed;
  top: 16px;
  left: 50%;
  transform: translateX(-50%);
  z-index: $z-message;
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: 8px;
  pointer-events: none;
}

.breeze-message {
  background: var(--breeze-surface-raised);
  border: 1px solid var(--breeze-border);
  border-left: 3px solid var(--msg-accent);
  border-radius: 4px;
  padding: 8px 16px;
  font-size: 14px;
  color: var(--breeze-text);
  box-shadow: 0 4px 12px rgba(0, 0, 0, 0.3);
  pointer-events: auto;
  max-width: 480px;
  word-break: break-word;
}

.breeze-msg-enter-active {
  transition: all 0.2s ease;
}

.breeze-msg-leave-active {
  transition: all 0.15s ease;
}

.breeze-msg-enter-from {
  opacity: 0;
  transform: translateY(-8px);
}

.breeze-msg-leave-to {
  opacity: 0;
  transform: translateY(-8px);
}
</style>
