import { ref } from 'vue'
import type { Ref } from 'vue'
import type { PendingRequest, WSResponse } from '../types'

const WS_TIMEOUT: number = 30000
const MAX_RECONNECT_DELAY: number = 30000

// Singleton state
const ws: Ref<WebSocket | null> = ref(null)
const connected: Ref<boolean> = ref(false)
const reconnecting: Ref<boolean> = ref(false)
const pendingRequests: Map<string, PendingRequest> = new Map()
let reqCounter: number = 0
let reconnectAttempts: number = 0
let reconnectTimer: ReturnType<typeof setTimeout> | null = null
let intentionalClose: boolean = false

// Push event handlers: eventType -> Set<callback>
type PushCallback = (data: any) => void
const pushHandlers: Map<string, Set<PushCallback>> = new Map()

// Reconnect callbacks
const reconnectCallbacks: Set<() => void> = new Set()

function getWsUrl(): string {
  const loc: Location = window.location
  const proto: string = loc.protocol === 'https:' ? 'wss:' : 'ws:'
  const base: string = import.meta.env.VITE_API_BASE || ''
  if (base && (base.startsWith('http://') || base.startsWith('https://'))) {
    const url: URL = new URL(base)
    return `${url.protocol === 'https:' ? 'wss:' : 'ws:'}//${url.host}/ws`
  }
  return `${proto}//${loc.host}${base}/ws`
}

function connect(): void {
  if (ws.value && ws.value.readyState <= 1) return

  intentionalClose = false
  const socket: WebSocket = new window.WebSocket(getWsUrl())

  socket.onopen = (): void => {
    ws.value = socket
    connected.value = true
    reconnecting.value = false
    reconnectAttempts = 0

    // Notify reconnect listeners
    for (const cb of reconnectCallbacks) {
      try { cb() } catch { /* ignore */ }
    }
  }

  socket.onmessage = (event: MessageEvent): void => {
    let msg: WSResponse & { event?: string }
    try {
      msg = JSON.parse(event.data as string)
    } catch {
      return
    }

    if (msg.id) {
      // Response to a pending request
      const pending: PendingRequest | undefined = pendingRequests.get(msg.id)
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
      const handlers: Set<PushCallback> | undefined = pushHandlers.get(msg.event)
      if (handlers) {
        for (const h of handlers) {
          try { h(msg.data) } catch { /* ignore */ }
        }
      }
    }
  }

  socket.onclose = (): void => {
    ws.value = null
    connected.value = false

    // Reject all pending requests
    for (const [, pending] of pendingRequests) {
      clearTimeout(pending.timeout)
      pending.reject({ error: 'ws_disconnected' })
    }
    pendingRequests.clear()

    if (!intentionalClose) {
      scheduleReconnect()
    }
  }

  socket.onerror = (): void => {
    // onclose will fire after onerror
  }

  ws.value = socket
}

function disconnect(): void {
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

function scheduleReconnect(): void {
  reconnecting.value = true
  const delay: number = Math.min(1000 * Math.pow(2, reconnectAttempts), MAX_RECONNECT_DELAY)
  const jitter: number = delay * 0.2 * Math.random()

  reconnectTimer = setTimeout(() => {
    reconnectTimer = null
    reconnectAttempts++
    connect()
  }, delay + jitter)
}

/**
 * Send a request and wait for the response.
 * @param action - The action name (e.g. 'file.list')
 * @param data - The request data
 * @returns The response data
 */
function request<T = any>(action: string, data: Record<string, unknown> = {}): Promise<T> {
  return new Promise((resolve, reject) => {
    if (!ws.value || ws.value.readyState !== 1) {
      reject({ error: 'ws_not_connected' })
      return
    }

    const id: string = `req-${++reqCounter}-${Date.now()}`
    const timeout: ReturnType<typeof setTimeout> = setTimeout(() => {
      pendingRequests.delete(id)
      reject({ error: 'ws_timeout' })
    }, WS_TIMEOUT)

    pendingRequests.set(id, { resolve: resolve as (value: unknown) => void, reject, timeout })

    try {
      ws.value.send(JSON.stringify({ id, action, data }))
    } catch {
      clearTimeout(timeout)
      pendingRequests.delete(id)
      reject({ error: 'ws_send_failed' })
    }
  })
}

/**
 * Subscribe to a push event type.
 */
function on(event: string, callback: PushCallback): void {
  if (!pushHandlers.has(event)) {
    pushHandlers.set(event, new Set())
  }
  pushHandlers.get(event)!.add(callback)
}

/**
 * Unsubscribe from a push event type.
 */
function off(event: string, callback: PushCallback): void {
  const handlers: Set<PushCallback> | undefined = pushHandlers.get(event)
  if (handlers) {
    handlers.delete(callback)
  }
}

/**
 * Register a callback that fires after successful reconnection.
 */
function onReconnect(callback: () => void): void {
  reconnectCallbacks.add(callback)
}

/**
 * Remove a reconnect callback.
 */
function offReconnect(callback: () => void): void {
  reconnectCallbacks.delete(callback)
}

export interface WebSocketAPI {
  connected: Ref<boolean>
  reconnecting: Ref<boolean>
  connect: () => void
  disconnect: () => void
  request: <T = any>(action: string, data?: Record<string, unknown>) => Promise<T>
  on: (event: string, callback: PushCallback) => void
  off: (event: string, callback: PushCallback) => void
  onReconnect: (callback: () => void) => void
  offReconnect: (callback: () => void) => void
}

export function useWebSocket(): WebSocketAPI {
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
