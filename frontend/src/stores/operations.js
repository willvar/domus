import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { useWebSocket } from '../composables/useWebSocket'

export const useOperationsStore = defineStore('operations', () => {
  const operations = ref([])
  const showPanel = ref(false)
  const ws = useWebSocket()

  const activeOps = computed(() => operations.value.filter(o => o.status === 'running'))

  function addOperation(type, description) {
    const op = {
      id: Date.now() + Math.random(),
      opId: null, // will be set from server response
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

  function findByOpId(opId) {
    return operations.value.find(o => o.opId === opId)
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

  /**
   * Run a file operation via WebSocket.
   * @param {string} action - WS action (e.g. 'file.copy', 'file.move', 'file.delete', 'trash.clear')
   * @param {object} data - Request data
   * @param {string} type - Operation type for UI ('copy', 'move', 'delete')
   * @param {string} description - Human readable description
   */
  async function runOperation(action, data, type, description) {
    const op = addOperation(type, description)
    try {
      const result = await ws.request(action, data)
      if (result.op_id) {
        op.opId = result.op_id
      } else {
        // Immediate completion (no async op)
        completeOperation(op.id)
      }
    } catch (e) {
      failOperation(op.id, e.error || 'operation_failed')
    }
    return op
  }

  // Listen for file operation push events
  ws.on('file.op.progress', (data) => {
    const op = findByOpId(data.op_id)
    if (op) {
      Object.assign(op, {
        done: data.done,
        total: data.total,
        current: data.current,
      })
    }
  })

  ws.on('file.op.completed', (data) => {
    const op = findByOpId(data.op_id)
    if (op) {
      completeOperation(op.id)
    }
  })

  ws.on('file.op.error', (data) => {
    const op = findByOpId(data.op_id)
    if (op) {
      failOperation(op.id, data.error)
    }
  })

  return {
    operations,
    showPanel,
    activeOps,
    addOperation,
    updateOperation,
    completeOperation,
    failOperation,
    runOperation,
  }
})
