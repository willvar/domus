import { ref, reactive, markRaw } from 'vue'

const messages = reactive([])
let idCounter = 0

const typeColors = {
  success: 'var(--breeze-success)',
  error: 'var(--breeze-danger)',
  warning: 'var(--breeze-warning)',
  info: 'var(--breeze-accent)',
}

function show(content, type = 'info', duration = 3000) {
  const id = ++idCounter
  const msg = { id, content, type, visible: false }
  messages.push(msg)
  requestAnimationFrame(() => {
    const m = messages.find(m => m.id === id)
    if (m) m.visible = true
  })
  setTimeout(() => remove(id), duration)
  return { destroy: () => remove(id) }
}

function remove(id) {
  const idx = messages.findIndex(m => m.id === id)
  if (idx === -1) return
  messages[idx].visible = false
  setTimeout(() => {
    const i = messages.findIndex(m => m.id === id)
    if (i !== -1) messages.splice(i, 1)
  }, 200)
}

export function useMessage() {
  return {
    success: (content) => show(content, 'success'),
    error: (content) => show(content, 'error'),
    warning: (content) => show(content, 'warning'),
    info: (content) => show(content, 'info'),
  }
}

// Exposed for the MessageContainer component
export function useMessageState() {
  return { messages, typeColors }
}
