import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import api from '../composables/useApi'
import { addOp, getAllOps, deleteOp, isDBAvailable } from '../composables/useIndexedDB'

export const usePendingOpsStore = defineStore('pendingOps', () => {
  const ops = ref([])
  const showPanel = ref(false)
  let _username = null

  const pendingCount = computed(() => ops.value.length)
  const hasPending = computed(() => ops.value.length > 0)

  function isQueueableError(error) {
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

  async function init(username) {
    _username = username || null
    try {
      const records = await getAllOps()
      ops.value = username
        ? records.filter(r => r.username === username)
        : records
    } catch (e) {
      console.error('Failed to load pending operations:', e)
      ops.value = []
    }
  }

  async function enqueue(record) {
    const op = {
      id: crypto.randomUUID(),
      createdAt: Date.now(),
      lastAttempt: null,
      lastError: null,
      ...record,
    }

    ops.value.push(op)
    showPanel.value = true

    try {
      await addOp(op)
    } catch (e) {
      if (isDBAvailable()) {
        console.error('Failed to persist pending operation:', e)
      }
    }

    return op
  }

  async function retry(id) {
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
        const { useFileSystemStore } = await import('./fileSystem')
        const fs = useFileSystemStore()
        fs.invalidateCache()
        fs.refresh()
      } catch { /* ignored */ }
    } catch (e) {
      op.lastError = e.response?.data?.error || e.message || String(e)
      op.lastAttempt = Date.now()
      try { await addOp(op) } catch { /* ignored */ }
    } finally {
      op._retrying = false
    }
  }

  async function retrySimple(op) {
    if (op.apiMethod === 'delete') {
      await api.delete(op.apiUrl)
    } else {
      await api[op.apiMethod](op.apiUrl, op.apiData)
    }
  }

  async function retrySSE(op) {
    const { useOperationsStore } = await import('./operations')
    const opsStore = useOperationsStore()
    await opsStore.runSSEOperation(
      op.sseUrl,
      op.sseBody,
      op.type,
      op.description,
      { ...op.sseOptions, _isRetry: true },
    )
  }

  async function discard(id) {
    ops.value = ops.value.filter(o => o.id !== id)
    try { await deleteOp(id) } catch { /* ignored */ }
    if (ops.value.length === 0) showPanel.value = false
  }

  async function discardAll() {
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
