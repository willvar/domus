import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import { useWebSocket } from '../composables/useWebSocket'

export const useJobsStore = defineStore('jobs', () => {
  const tasks = ref([])
  const panelOpen = ref(false)
  const error = ref(null)
  const ws = useWebSocket()

  const activeTasks = computed(() =>
    tasks.value.filter(t => t.status === 'running' || t.status === 'pending')
  )

  const hasActiveTasks = computed(() => activeTasks.value.length > 0)

  const completedTasks = computed(() =>
    tasks.value.filter(t => t.status === 'completed' || t.status === 'failed' || t.status === 'cancelled')
  )

  const hasCompletedTasks = computed(() => completedTasks.value.length > 0)

  function togglePanel() {
    panelOpen.value = !panelOpen.value
  }

  function closePanel() {
    panelOpen.value = false
  }

  async function fetchTasks() {
    try {
      const data = await ws.request('task.list')
      tasks.value = Array.isArray(data) ? data : []
    } catch {
      tasks.value = []
    }
  }

  function addTask(task) {
    tasks.value.unshift(task)
  }

  async function cancelTask(taskId) {
    await ws.request('task.cancel', { task_id: taskId })
    const idx = tasks.value.findIndex(t => t.task_id === taskId)
    if (idx >= 0) {
      tasks.value[idx].status = 'cancelled'
    }
  }

  async function clearCompleted() {
    await ws.request('task.clearDone')
    await fetchTasks()
  }

  // Listen for task push events
  ws.on('task.update', (data) => {
    const idx = tasks.value.findIndex(t => t.task_id === data.task_id)
    if (idx >= 0) {
      tasks.value[idx] = { ...tasks.value[idx], ...data }
    } else {
      tasks.value.push(data)
    }
  })

  function cleanup() {}

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
