import { ref } from 'vue'

const WS_TIMEOUT = 30000
const MAX_RECONNECT_DELAY = 30000

// Singleton state
const ws = ref(null)
const connected = ref(false)
const reconnecting = ref(false)
const pendingRequests = new Map()
let reqCounter = 0
let reconnectAttempts = 0
let reconnectTimer = null
let intentionalClose = false

// Push event handlers: eventType -> Set<callback>
const pushHandlers = new Map()

// Reconnect callbacks
const reconnectCallbacks = new Set()

function getWsUrl() {
  const loc = window.location
  const proto = loc.protocol === 'https:' ? 'wss:' : 'ws:'
  const base = import.meta.env.VITE_API_BASE || ''
  if (base && (base.startsWith('http://') || base.startsWith('https://'))) {
    const url = new URL(base)
    return `${url.protocol === 'https:' ? 'wss:' : 'ws:'}//${url.host}/ws`
  }
  return `${proto}//${loc.host}${base}/ws`
}

function connect() {
  if (ws.value && ws.value.readyState <= 1) return

  intentionalClose = false
  const socket = new WebSocket(getWsUrl())

  socket.onopen = () => {
    ws.value = socket
    connected.value = true
    reconnecting.value = false
    reconnectAttempts = 0

    // Notify reconnect listeners
    for (const cb of reconnectCallbacks) {
      try { cb() } catch { /* ignore */ }
    }
  }

  socket.onmessage = (event) => {
    let msg
    try {
      msg = JSON.parse(event.data)
    } catch {
      return
    }

    if (msg.id) {
      // Response to a pending request
      const pending = pendingRequests.get(msg.id)
      if (pending) {
        clearTimeout(pending.timeout)
        pendingRequests.delete(msg.id)
        if (msg.ok) {
          pending.resolve(msg.data)
        } else {
          pending.reject({ error: msg.error, action: msg.action })
        }
      }
    } else if (msg.event) {
      // Push event
      const handlers = pushHandlers.get(msg.event)
      if (handlers) {
        for (const h of handlers) {
          try { h(msg.data) } catch { /* ignore */ }
        }
      }
    }
  }

  socket.onclose = () => {
    ws.value = null
    connected.value = false

    // Reject all pending requests
    for (const [id, pending] of pendingRequests) {
      clearTimeout(pending.timeout)
      pending.reject({ error: 'ws_disconnected' })
    }
    pendingRequests.clear()

    if (!intentionalClose) {
      scheduleReconnect()
    }
  }

  socket.onerror = () => {
    // onclose will fire after onerror
  }

  ws.value = socket
}

function disconnect() {
  intentionalClose = true
  if (reconnectTimer) {
    clearTimeout(reconnectTimer)
    reconnectTimer = null
  }
  reconnecting.value = false
  reconnectAttempts = 0

  if (ws.value) {
    ws.value.close()
    ws.value = null
  }
  connected.value = false

  // Reject all pending
  for (const [, pending] of pendingRequests) {
    clearTimeout(pending.timeout)
    pending.reject({ error: 'ws_disconnected' })
  }
  pendingRequests.clear()
}

function scheduleReconnect() {
  reconnecting.value = true
  const delay = Math.min(1000 * Math.pow(2, reconnectAttempts), MAX_RECONNECT_DELAY)
  const jitter = delay * 0.2 * Math.random()

  reconnectTimer = setTimeout(() => {
    reconnectTimer = null
    reconnectAttempts++
    connect()
  }, delay + jitter)
}

/**
 * Send a request and wait for the response.
 * @param {string} action - The action name (e.g. 'file.list')
 * @param {object} data - The request data
 * @returns {Promise<any>} The response data
 */
function request(action, data = {}) {
  return new Promise((resolve, reject) => {
    if (!ws.value || ws.value.readyState !== 1) {
      reject({ error: 'ws_not_connected' })
      return
    }

    const id = `req-${++reqCounter}-${Date.now()}`
    const timeout = setTimeout(() => {
      pendingRequests.delete(id)
      reject({ error: 'ws_timeout' })
    }, WS_TIMEOUT)

    pendingRequests.set(id, { resolve, reject, timeout })

    try {
      ws.value.send(JSON.stringify({ id, action, data }))
    } catch (e) {
      clearTimeout(timeout)
      pendingRequests.delete(id)
      reject({ error: 'ws_send_failed' })
    }
  })
}

/**
 * Subscribe to a push event type.
 */
function on(event, callback) {
  if (!pushHandlers.has(event)) {
    pushHandlers.set(event, new Set())
  }
  pushHandlers.get(event).add(callback)
}

/**
 * Unsubscribe from a push event type.
 */
function off(event, callback) {
  const handlers = pushHandlers.get(event)
  if (handlers) {
    handlers.delete(callback)
  }
}

/**
 * Register a callback that fires after successful reconnection.
 */
function onReconnect(callback) {
  reconnectCallbacks.add(callback)
}

/**
 * Remove a reconnect callback.
 */
function offReconnect(callback) {
  reconnectCallbacks.delete(callback)
}

export function useWebSocket() {
  return {
    connected,
    reconnecting,
    connect,
    disconnect,
    request,
    on,
    off,
    onReconnect,
    offReconnect,
  }
}

export default { connect, disconnect, request, on, off, onReconnect, offReconnect, connected, reconnecting }
