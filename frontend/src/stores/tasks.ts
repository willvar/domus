import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import type { Ref, ComputedRef } from 'vue'
import api from '../composables/useApi'
import { useWebSocket } from '../composables/useWebSocket'
import type { Task, TaskUpdateEvent } from '../types'

export const useTasksStore = defineStore('tasks', () => {
  const tasks: Ref<Task[]> = ref([])
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

  function upsertTask(task: Task): void {
    const idx = tasks.value.findIndex(t => t.task_id === task.task_id)
    if (idx >= 0) {
      tasks.value[idx] = { ...tasks.value[idx], ...task }
      return
    }
    tasks.value.unshift(task)
  }

  async function fetchTasks(): Promise<void> {
    try {
      const { data } = await api.get<Task[]>('/task/')
      tasks.value = Array.isArray(data) ? data : []
    } catch {
      tasks.value = []
    }
  }

  async function cancelTask(taskId: string): Promise<void> {
    await api.delete('/task/' + encodeURIComponent(taskId))
    const idx = tasks.value.findIndex(t => t.task_id === taskId)
    if (idx >= 0) {
      tasks.value[idx].status = 'cancelled'
      tasks.value[idx].updated_at = new Date().toISOString()
    }
  }

  async function clearCompleted(): Promise<void> {
    await api.delete('/task/done')
    await fetchTasks()
  }

  ws.on('task.update', (data: TaskUpdateEvent) => {
    upsertTask(data as Task)
  })

  function cleanup(): void {}

  return {
    tasks,
    activeTasks,
    hasActiveTasks,
    completedTasks,
    hasCompletedTasks,
    error,
    fetchTasks,
    cancelTask,
    clearCompleted,
    upsertTask,
    cleanup,
  }
})
