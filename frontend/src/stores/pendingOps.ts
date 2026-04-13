import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { Ref, ComputedRef } from 'vue'
import api from '../composables/useApi'
import { addOp, getAllOps, deleteOp, isDBAvailable } from '../composables/useIndexedDB'
import { useActivityStore } from './activity'
import { useFileSystemStore } from './fileSystem'
import type { PendingOp, PendingOpInput } from '../types'

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

const COMPLETED_VISUAL_DELAY_MS = 900

export const usePendingOpsStore = defineStore('pendingOps', () => {
  const ops: Ref<PendingOp[]> = ref([])
  const activityStore = useActivityStore()
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
    // Fetch/transport errors — TypeError indicates network failure
    if (error instanceof TypeError) return true
    return false
  }

  function isPendingOpRecord(value: unknown): value is PendingOp {
    if (!value || typeof value !== 'object') return false
    const op = value as Record<string, unknown>
    return typeof op.id === 'string'
      && typeof op.createdAt === 'number'
      && (op.lastAttempt === null || typeof op.lastAttempt === 'number')
      && (op.lastError === null || typeof op.lastError === 'string')
      && typeof op.apiUrl === 'string'
      && typeof op.apiMethod === 'string'
  }

  async function discardInvalidRecords(records: unknown[]): Promise<PendingOp[]> {
    const valid: PendingOp[] = []

    for (const record of records) {
      if (isPendingOpRecord(record)) {
        valid.push(record)
        continue
      }
      const invalidId = typeof (record as { id?: unknown })?.id === 'string'
        ? (record as { id: string }).id
        : null
      console.error('Discarding invalid pending operation record:', record)
      if (invalidId) {
        try { await deleteOp(invalidId) } catch { /* ignored */ }
      }
    }

    return valid
  }

  async function init(username?: string): Promise<void> {
    _username = username || null
    try {
      const records = await getAllOps()
      const validRecords = await discardInvalidRecords(records as unknown[])
      ops.value = username
        ? validRecords.filter((r: PendingOp) => r.username === username)
        : validRecords
    } catch (e: any) {
      console.error('Failed to load pending operations:', e)
      ops.value = []
    }
  }

  async function enqueue(record: PendingOpInput): Promise<PendingOp> {
    const op = {
      ...record,
      id: crypto?.randomUUID?.() || (Math.random().toString(36).slice(2) + Date.now().toString(36)),
      createdAt: Date.now(),
      lastAttempt: null,
      lastError: null,
    } as PendingOp

    ops.value.push(op)
    try { activityStore.show() } catch { /* ignore */ }

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

    if (!isPendingOpRecord(op)) {
      console.error('Discarding invalid pending operation record:', op)
      await discard(id)
      return
    }

    op._retrying = true
    op._completed = false
    op.lastAttempt = Date.now()

    try {
      await retryHttp(op)

      try { await deleteOp(id) } catch { /* ignored */ }
      op.lastError = null
      op._retrying = false
      op._completed = true

      setTimeout(() => {
        const current = ops.value.find(o => o.id === id)
        if (!current || !current._completed) return
        ops.value = ops.value.filter(o => o.id !== id)
      }, COMPLETED_VISUAL_DELAY_MS)

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
      op._completed = false
      try { await addOp(op) } catch { /* ignored */ }
    } finally {
      op._retrying = false
    }
  }

  async function retryHttp(op: PendingOp): Promise<void> {
    if (op.apiMethod === 'delete') {
      await api.delete(op.apiUrl, op.apiData ? { params: op.apiData as Record<string, unknown> } : undefined)
    } else {
      await (api as unknown as Record<string, Function>)[op.apiMethod](op.apiUrl, op.apiData)
    }
  }

  async function discard(id: string): Promise<void> {
    ops.value = ops.value.filter(o => o.id !== id)
    try { await deleteOp(id) } catch { /* ignored */ }
  }

  async function discardAll(): Promise<void> {
    const ids = ops.value.map(o => o.id)
    ops.value = []
    try {
      for (const id of ids) {
        await deleteOp(id)
      }
    } catch { /* ignored */ }
  }

  return {
    ops,
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
