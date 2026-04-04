import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { Ref, ComputedRef } from 'vue'
import api from '../composables/useApi'
import { addOp, getAllOps, deleteOp, isDBAvailable } from '../composables/useIndexedDB'
import { useFileSystemStore } from './fileSystem'
import { useOperationsStore } from './operations'
import type { PendingOp } from '../types'

interface AxiosLikeError {
  code?: string
  name?: string
  isAxiosError?: boolean
  config?: unknown
  response?: {
    data?: { error?: string }
    status?: number
  }
  message?: string
}

export const usePendingOpsStore = defineStore('pendingOps', () => {
  const ops: Ref<PendingOp[]> = ref([])
  const showPanel: Ref<boolean> = ref(false)
  let _username: string | null = null

  const pendingCount: ComputedRef<number> = computed(() => ops.value.length)
  const hasPending: ComputedRef<boolean> = computed(() => ops.value.length > 0)

  function isQueueableError(error: AxiosLikeError): boolean {
    // Cancelled requests should never be queued
    if (error.code === 'ERR_CANCELED' || error.name === 'CanceledError' || error.name === 'AbortError') {
      return false
    }
    // Axios errors — only queue when no response (network/DNS/timeout)
    if (error.isAxiosError || error.config) {
      return !error.response
    }
    // Fetch errors (SSE operations) — TypeError indicates network failure
    if (error instanceof TypeError) return true
    return false
  }

  async function init(username?: string): Promise<void> {
    _username = username || null
    try {
      const records = await getAllOps()
      ops.value = username
        ? records.filter((r: PendingOp) => r.username === username)
        : records
    } catch (e: any) {
      console.error('Failed to load pending operations:', e)
      ops.value = []
    }
  }

  async function enqueue(record: Partial<PendingOp>): Promise<PendingOp> {
    const op: PendingOp = {
      id: crypto?.randomUUID?.() || (Math.random().toString(36).slice(2) + Date.now().toString(36)),
      createdAt: Date.now(),
      lastAttempt: null,
      lastError: null,
      ...record,
    }

    ops.value.push(op)
    showPanel.value = true

    try {
      await addOp(op)
    } catch (e: any) {
      if (isDBAvailable()) {
        console.error('Failed to persist pending operation:', e)
      }
    }

    return op
  }

  async function retry(id: string): Promise<void> {
    const op = ops.value.find(o => o.id === id)
    if (!op || op._retrying) return

    op._retrying = true
    op.lastAttempt = Date.now()

    try {
      if (op.apiUrl) {
        await retrySimple(op)
      } else if (op.sseUrl) {
        await retrySSE(op)
      }

      // Success — remove from queue
      ops.value = ops.value.filter(o => o.id !== id)
      try { await deleteOp(id) } catch { /* ignored */ }

      // Refresh directory
      try {
        const fs = useFileSystemStore()
        fs.invalidateCache()
        fs.refresh()
      } catch { /* ignored */ }
    } catch (e: unknown) {
      const err = e as AxiosLikeError
      op.lastError = err.response?.data?.error || err.message || String(e)
      op.lastAttempt = Date.now()
      try { await addOp(op) } catch { /* ignored */ }
    } finally {
      op._retrying = false
    }
  }

  async function retrySimple(op: PendingOp): Promise<void> {
    if (op.apiMethod === 'delete') {
      await api.delete(op.apiUrl!)
    } else {
      await (api as unknown as Record<string, Function>)[op.apiMethod!](op.apiUrl!, op.apiData)
    }
  }

  async function retrySSE(op: PendingOp): Promise<void> {
    const opsStore = useOperationsStore()
    // Map old SSE url/body to WS action/data
    const actionMap: Record<string, string> = {
      '/file/copy': 'file.copy',
      '/file/move': 'file.move',
      '/file/delete': 'file.delete',
      '/file/trash': 'trash.clear',
    }
    const action = actionMap[op.sseUrl!] || op.sseUrl!
    await opsStore.runOperation(action, op.sseBody || {}, op.type as 'copy' | 'move' | 'delete', op.description || '')
  }

  async function discard(id: string): Promise<void> {
    ops.value = ops.value.filter(o => o.id !== id)
    try { await deleteOp(id) } catch { /* ignored */ }
    if (ops.value.length === 0) showPanel.value = false
  }

  async function discardAll(): Promise<void> {
    const ids = ops.value.map(o => o.id)
    ops.value = []
    showPanel.value = false
    try {
      for (const id of ids) {
        await deleteOp(id)
      }
    } catch { /* ignored */ }
  }

  return {
    ops,
    showPanel,
    pendingCount,
    hasPending,
    _username,
    isQueueableError,
    init,
    enqueue,
    retry,
    discard,
    discardAll,
  }
})
