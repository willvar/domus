import { reactive, createApp, h as vueH } from 'vue'

const notifications = reactive([])
let idCounter = 0

const typeColors = {
  success: 'var(--breeze-success)',
  error: 'var(--breeze-danger)',
  warning: 'var(--breeze-warning)',
  info: 'var(--breeze-accent)',
}

function showNotification({ title, content, type = 'info', duration = 5000, keepAliveOnHover = false, action }) {
  const id = ++idCounter
  const notification = {
    id,
    title,
    content,
    type,
    duration,
    keepAliveOnHover,
    action,
    visible: false,
    paused: false,
  }
  notifications.push(notification)

  requestAnimationFrame(() => {
    const n = notifications.find(n => n.id === id)
    if (n) n.visible = true
  })

  if (duration > 0) {
    let timer = setTimeout(() => remove(id), duration)

    if (keepAliveOnHover) {
      notification._startTimer = () => {
        timer = setTimeout(() => remove(id), duration)
      }
      notification._stopTimer = () => {
        clearTimeout(timer)
      }
    }
  }

  return {
    destroy: () => remove(id),
  }
}

function remove(id) {
  const idx = notifications.findIndex(n => n.id === id)
  if (idx === -1) return
  notifications[idx].visible = false
  setTimeout(() => {
    const i = notifications.findIndex(n => n.id === id)
    if (i !== -1) notifications.splice(i, 1)
  }, 300)
}

export function useNotification() {
  return {
    success: (opts) => showNotification({ ...opts, type: 'success' }),
    warning: (opts) => showNotification({ ...opts, type: 'warning' }),
    error: (opts) => showNotification({ ...opts, type: 'error' }),
    info: (opts) => showNotification({ ...opts, type: 'info' }),
  }
}

export function useNotificationState() {
  return { notifications, typeColors }
}
