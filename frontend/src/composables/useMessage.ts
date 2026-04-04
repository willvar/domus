import { reactive } from 'vue'
import type { NotificationType, MessageItem, MessageHandle } from '../types'

const messages: MessageItem[] = reactive([])
let idCounter: number = 0

const typeColors: Record<NotificationType, string> = {
  success: 'var(--breeze-success)',
  error: 'var(--breeze-danger)',
  warning: 'var(--breeze-warning)',
  info: 'var(--breeze-accent)',
}

function show(content: string, type: NotificationType = 'info', duration: number = 3000): MessageHandle {
  const id: number = ++idCounter
  const msg: MessageItem = { id, content, type, visible: false }
  messages.push(msg)
  requestAnimationFrame(() => {
    const m: MessageItem | undefined = messages.find(m => m.id === id)
    if (m) m.visible = true
  })
  setTimeout(() => remove(id), duration)
  return { destroy: () => remove(id) }
}

function remove(id: number): void {
  const idx: number = messages.findIndex(m => m.id === id)
  if (idx === -1) return
  messages[idx].visible = false
  setTimeout(() => {
    const i: number = messages.findIndex(m => m.id === id)
    if (i !== -1) messages.splice(i, 1)
  }, 200)
}

export function useMessage(): Record<NotificationType, (content: string) => MessageHandle> {
  return {
    success: (content: string): MessageHandle => show(content, 'success'),
    error: (content: string): MessageHandle => show(content, 'error'),
    warning: (content: string): MessageHandle => show(content, 'warning'),
    info: (content: string): MessageHandle => show(content, 'info'),
  }
}

// Exposed for the MessageList component
export function useMessageState(): { messages: MessageItem[]; typeColors: Record<NotificationType, string> } {
  return { messages, typeColors }
}
