import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { Ref, ComputedRef } from 'vue'
import { useWebSocket } from '../composables/useWebSocket'
import type { FileOperation, OperationType, TaskUpdateEvent } from '../types'

export const useOperationsStore = defineStore('operations', () => {
  const operations: Ref<FileOperation[]> = ref([])
  const showPanel: Ref<boolean> = ref(false)
  const ws = useWebSocket()

  const activeOps: ComputedRef<FileOperation[]> = computed(() => operations.value.filter(o => o.status === 'running'))

  function addOperation(type: OperationType, description: string): FileOperation {
    const op: FileOperation = {
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

  function updateOperation(id: number, data: Partial<FileOperation>): void {
    const op = operations.value.find(o => o.id === id)
    if (op) Object.assign(op, data)
  }

  function findByOpId(opId: string): FileOperation | undefined {
    return operations.value.find(o => o.opId === opId)
  }

  function completeOperation(id: number): void {
    updateOperation(id, { status: 'completed' })
    setTimeout(() => {
      if (!activeOps.value.length) {
        showPanel.value = false
        operations.value = operations.value.filter(o => o.status !== 'completed')
      }
    }, 3000)
  }

  function failOperation(id: number, error: string): void {
    updateOperation(id, { status: 'failed', error })
  }

  /**
   * Run a file operation via WebSocket.
   * @param action - WS action (e.g. 'file.copy', 'file.move', 'file.delete', 'trash.clear')
   * @param data - Request data
   * @param type - Operation type for UI ('copy', 'move', 'delete')
   * @param description - Human readable description
   */
  async function runOperation(action: string, data: Record<string, unknown>, type: OperationType, description: string): Promise<FileOperation> {
    const op = addOperation(type, description)
    try {
      const result = await ws.request<{ op_id?: string; task_id?: string }>(action, data)
      if (result.op_id || result.task_id) {
        op.opId = result.op_id || result.task_id || null
      } else {
        // Immediate completion (no async op)
        completeOperation(op.id)
      }
    } catch (e: unknown) {
      failOperation(op.id, (e as { error?: string }).error || 'operation_failed')
    }
    return op
  }

  // Listen for task.update push events to track file operation progress
  ws.on('task.update', (data: TaskUpdateEvent & { error?: string }) => {
    const op = findByOpId(data.task_id)
    if (!op) return
    if (data.status === 'completed') {
      completeOperation(op.id)
    } else if (data.status === 'failed') {
      failOperation(op.id, data.error || 'operation_failed')
    } else {
      Object.assign(op, {
        done: Math.round((data.progress || 0) * 100),
        total: 100,
        current: data.phase || '',
      })
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
