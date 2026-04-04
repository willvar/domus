import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { Ref, ComputedRef } from 'vue'
import { useWebSocket } from '../composables/useWebSocket'
import type { Task, TaskUpdateEvent } from '../types'

export const useJobsStore = defineStore('jobs', () => {
  const tasks: Ref<Task[]> = ref([])
  const panelOpen: Ref<boolean> = ref(false)
  const error: Ref<string | null> = ref(null)
  const ws = useWebSocket()

  const activeTasks: ComputedRef<Task[]> = computed(() =>
    tasks.value.filter(t => t.status === 'running' || t.status === 'pending')
  )

  const hasActiveTasks: ComputedRef<boolean> = computed(() => activeTasks.value.length > 0)

  const completedTasks: ComputedRef<Task[]> = computed(() =>
    tasks.value.filter(t => t.status === 'completed' || t.status === 'failed' || t.status === 'cancelled')
  )

  const hasCompletedTasks: ComputedRef<boolean> = computed(() => completedTasks.value.length > 0)

  function togglePanel(): void {
    panelOpen.value = !panelOpen.value
  }

  function closePanel(): void {
    panelOpen.value = false
  }

  async function fetchTasks(): Promise<void> {
    try {
      const data = await ws.request<Task[]>('task.list')
      tasks.value = Array.isArray(data) ? data : []
    } catch {
      tasks.value = []
    }
  }

  function addTask(task: Task): void {
    tasks.value.unshift(task)
  }

  async function cancelTask(taskId: string): Promise<void> {
    await ws.request('task.cancel', { task_id: taskId })
    const idx = tasks.value.findIndex(t => t.task_id === taskId)
    if (idx >= 0) {
      tasks.value[idx].status = 'cancelled'
    }
  }

  async function clearCompleted(): Promise<void> {
    await ws.request('task.clearDone')
    await fetchTasks()
  }

  // Listen for task push events
  ws.on('task.update', (data: TaskUpdateEvent) => {
    const idx = tasks.value.findIndex(t => t.task_id === data.task_id)
    if (idx >= 0) {
      tasks.value[idx] = { ...tasks.value[idx], ...data } as Task
    } else {
      tasks.value.push(data as unknown as Task)
    }
  })

  function cleanup(): void {}

  return {
    tasks,
    activeTasks,
    hasActiveTasks,
    completedTasks,
    hasCompletedTasks,
    panelOpen,
    error,
    togglePanel,
    closePanel,
    fetchTasks,
    cancelTask,
    addTask,
    clearCompleted,
    cleanup,
  }
})
