import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { API_BASE } from '../composables/useApi'

export const useOperationsStore = defineStore('operations', () => {
  const operations = ref([])
  const showPanel = ref(false)

  const activeOps = computed(() => operations.value.filter(o => o.status === 'running'))

  function addOperation(type, description) {
    const op = {
      id: Date.now() + Math.random(),
      type, // 'copy' | 'move' | 'delete'
      description,
      status: 'running',
      done: 0,
      total: 0,
      current: '',
      error: null,
    }
    operations.value.push(op)
    showPanel.value = true
    return op
  }

  function updateOperation(id, data) {
    const op = operations.value.find(o => o.id === id)
    if (op) Object.assign(op, data)
  }

  function completeOperation(id) {
    updateOperation(id, { status: 'completed' })
    setTimeout(() => {
      if (!activeOps.value.length) {
        showPanel.value = false
        operations.value = operations.value.filter(o => o.status !== 'completed')
      }
    }, 3000)
  }

  function failOperation(id, error) {
    updateOperation(id, { status: 'failed', error })
  }

  // Run an SSE-based operation
  // options: { method, params }
  async function runSSEOperation(url, body, type, description, options = {}) {
    const op = addOperation(type, description)

    try {
      const method = options.method || (body ? 'POST' : 'DELETE')
      let fetchUrl = API_BASE + url
      if (options.params) {
        const qs = new URLSearchParams(options.params).toString()
        fetchUrl += '?' + qs
      }

      const response = await fetch(fetchUrl, {
        method,
        headers: {
          'Content-Type': 'application/json',
          'Accept': 'text/event-stream',
        },
        body: body ? JSON.stringify(body) : undefined,
        credentials: 'include',
      })

      if (response.status >= 500 && !options._isRetry) {
        failOperation(op.id, `Server error ${response.status}`)
        const { usePendingOpsStore } = await import('./pendingOps')
        const pendingOps = usePendingOpsStore()
        pendingOps.enqueue({
          type,
          description,
          username: pendingOps._username,
          sseUrl: url,
          sseBody: body,
          sseOptions: { method: options.method, params: options.params },
        })
        return op
      }

      const reader = response.body.getReader()
      const decoder = new TextDecoder()
      let buffer = ''

      while (true) {
        const { done, value } = await reader.read()
        if (done) break

        buffer += decoder.decode(value, { stream: true })
        const lines = buffer.split('\n')
        buffer = lines.pop() || ''

        for (const line of lines) {
          if (line.startsWith('data: ')) {
            try {
              const data = JSON.parse(line.slice(6))
              if (data.error) {
                failOperation(op.id, data.error)
                return
              }
              if (data.done === true) {
                completeOperation(op.id)
                return
              }
              updateOperation(op.id, {
                done: data.done,
                total: data.total,
                current: data.current,
              })
            } catch { /* skip malformed SSE line, continue reading stream */ }
          }
        }
      }

      completeOperation(op.id)
    } catch (e) {
      failOperation(op.id, e.message)
      if (!options._isRetry && (e instanceof TypeError || !navigator.onLine)) {
        try {
          const { usePendingOpsStore } = await import('./pendingOps')
          const pendingOps = usePendingOpsStore()
          pendingOps.enqueue({
            type,
            description,
            username: pendingOps._username,
            sseUrl: url,
            sseBody: body,
            sseOptions: { method: options.method, params: options.params },
          })
        } catch { /* ignored */ }
      }
    }

    return op
  }

  return {
    operations,
    showPanel,
    activeOps,
    addOperation,
    updateOperation,
    completeOperation,
    failOperation,
    runSSEOperation,
  }
})
