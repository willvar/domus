import { defineStore } from 'pinia'
import { ref, computed, watch } from 'vue'
import type { Ref, ComputedRef } from 'vue'
import api from '../composables/useApi'
import { useWebSocket } from '../composables/useWebSocket'
import type { Task, TaskUpdateEvent } from '../types'
import { useAuthStore } from './auth'

export const useTasksStore = defineStore('tasks', () => {
  const tasks: Ref<Task[]> = ref([])
  const error: Ref<string | null> = ref(null)
  const ws = useWebSocket()
  const auth = useAuthStore()
  let users = 0
  let fetching: Promise<void> | null = null
  let refreshAgain = false
  watch(() => auth.username, () => { tasks.value = []; error.value = null })

  const activeTasks: ComputedRef<Task[]> = computed(() =>
    tasks.value.filter(t => ['queued', 'running', 'pending', 'paused', 'cancelling'].includes(t.status))
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
    if (!auth.isLoggedIn) { tasks.value = []; return }
    if (fetching) { refreshAgain = true; return fetching }
    const username = auth.username
    fetching = (async () => {
      do {
        refreshAgain = false
        try {
          const { data } = await api.get<Task[]>('/task/')
          if (auth.isLoggedIn && auth.username === username) tasks.value = Array.isArray(data) ? data : []
          error.value = null
        } catch {
          error.value = 'task_status_unavailable'
        }
      } while (refreshAgain && auth.isLoggedIn && auth.username === username)
    })().finally(() => { fetching = null })
    return fetching
  }

  function resync(): void {
    if (users && document.visibilityState !== 'hidden') void fetchTasks()
  }
  function startSync(): void {
    if (users++ === 0) {
      window.addEventListener('focus', resync)
      document.addEventListener('visibilitychange', resync)
      resync()
    }
  }
  function stopSync(): void {
    if (users > 0 && --users === 0) {
      window.removeEventListener('focus', resync)
      document.removeEventListener('visibilitychange', resync)
    }
  }

  async function cancelTask(taskId: string): Promise<void> {
    await api.delete('/task/' + encodeURIComponent(taskId))
    await fetchTasks()
  }
  async function retryTask(taskId: string): Promise<void> {
    await api.post('/task/' + encodeURIComponent(taskId) + '/retry', {})
    await fetchTasks()
  }

  async function clearCompleted(): Promise<void> {
    await api.delete('/task/done')
    await fetchTasks()
  }

  ws.on('task.update', (data: TaskUpdateEvent) => {
    if (!auth.isLoggedIn) return
    upsertTask(data as Task)
    if (fetching) refreshAgain = true
  })
  ws.onReconnect(resync)

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
    retryTask,
    startSync,
    stopSync,
    clearCompleted,
    upsertTask,
    cleanup,
  }
})
