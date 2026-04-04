import { reactive } from 'vue'
import type { NotificationType, NotificationItem, NotificationOptions, MessageHandle } from '../types'

const notifications: NotificationItem[] = reactive([])
let idCounter: number = 0

const typeColors: Record<NotificationType, string> = {
  success: 'var(--breeze-success)',
  error: 'var(--breeze-danger)',
  warning: 'var(--breeze-warning)',
  info: 'var(--breeze-accent)',
}

function showNotification({ title, content, type = 'info', duration = 5000, keepAliveOnHover = false, action }: NotificationOptions & { type: NotificationType }): MessageHandle {
  const id: number = ++idCounter
  const notification: NotificationItem = {
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
    const n: NotificationItem | undefined = notifications.find(n => n.id === id)
    if (n) n.visible = true
  })

  if (duration > 0) {
    let timer: ReturnType<typeof setTimeout> = setTimeout(() => remove(id), duration)

    if (keepAliveOnHover) {
      notification._startTimer = (): void => {
        timer = setTimeout(() => remove(id), duration)
      }
      notification._stopTimer = (): void => {
        clearTimeout(timer)
      }
    }
  }

  return {
    destroy: () => remove(id),
  }
}

function remove(id: number): void {
  const idx: number = notifications.findIndex(n => n.id === id)
  if (idx === -1) return
  notifications[idx].visible = false
  setTimeout(() => {
    const i: number = notifications.findIndex(n => n.id === id)
    if (i !== -1) notifications.splice(i, 1)
  }, 300)
}

export function useNotification(): Record<NotificationType, (opts: NotificationOptions) => MessageHandle> {
  return {
    success: (opts: NotificationOptions): MessageHandle => showNotification({ ...opts, type: 'success' }),
    warning: (opts: NotificationOptions): MessageHandle => showNotification({ ...opts, type: 'warning' }),
    error: (opts: NotificationOptions): MessageHandle => showNotification({ ...opts, type: 'error' }),
    info: (opts: NotificationOptions): MessageHandle => showNotification({ ...opts, type: 'info' }),
  }
}

export function useNotificationState(): { notifications: NotificationItem[]; typeColors: Record<NotificationType, string> } {
  return { notifications, typeColors }
}
