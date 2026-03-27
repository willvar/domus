<script setup>
import { defineComponent, h } from 'vue'
import { useNotificationState } from '../../composables/useNotification'

const { notifications, typeColors } = useNotificationState()

const ActionSlot = defineComponent({
  props: { render: { type: Function, required: true } },
  render() { return this.render() },
})

function onMouseEnter(n) {
  if (n.keepAliveOnHover && n._stopTimer) n._stopTimer()
}
function onMouseLeave(n) {
  if (n.keepAliveOnHover && n._startTimer) n._startTimer()
}
</script>

<template>
  <Teleport to="body">
    <div class="breeze-notification-container">
      <TransitionGroup name="breeze-notif">
        <div
          v-for="n in notifications"
          :key="n.id"
          class="breeze-notification"
          :style="{ '--notif-accent': typeColors[n.type] }"
          @mouseenter="onMouseEnter(n)"
          @mouseleave="onMouseLeave(n)"
        >
          <div v-if="n.title" class="breeze-notification__title">{{ n.title }}</div>
          <div v-if="n.content" class="breeze-notification__content">{{ n.content }}</div>
          <div v-if="n.action" class="breeze-notification__action">
            <ActionSlot :render="n.action" />
          </div>
        </div>
      </TransitionGroup>
    </div>
  </Teleport>
</template>

<style scoped>
.breeze-notification-container {
  position: fixed;
  top: 16px;
  right: 16px;
  z-index: 99998;
  display: flex;
  flex-direction: column;
  gap: 8px;
  max-width: 380px;
}
.breeze-notification {
  background: var(--breeze-surface-raised);
  border: 1px solid var(--breeze-border);
  border-left: 3px solid var(--notif-accent);
  border-radius: 6px;
  padding: 12px 16px;
  box-shadow: 0 8px 24px rgba(0, 0, 0, 0.4);
  color: var(--breeze-text);
}
.breeze-notification__title {
  font-weight: 600;
  font-size: 14px;
  margin-bottom: 4px;
}
.breeze-notification__content {
  font-size: 13px;
  color: var(--breeze-text-secondary);
  line-height: 1.5;
}
.breeze-notification__action {
  margin-top: 8px;
  display: flex;
  justify-content: flex-end;
}
.breeze-notif-enter-active {
  transition: all 0.3s ease;
}
.breeze-notif-leave-active {
  transition: all 0.2s ease;
}
.breeze-notif-enter-from {
  opacity: 0;
  transform: translateX(40px);
}
.breeze-notif-leave-to {
  opacity: 0;
  transform: translateX(40px);
}
</style>
