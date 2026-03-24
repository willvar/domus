import { defineStore } from 'pinia'
import { ref, computed } from 'vue'
import api, { API_BASE } from '../composables/useApi'

export const useJobsStore = defineStore('jobs', () => {
  const jobs = ref([])
  const sseConnections = new Map()
  const panelOpen = ref(false)
  const error = ref(null)

  const activeJobs = computed(() =>
    jobs.value.filter(j => j.status === 'pending' || j.status === 'running' || j.status === 'paused')
  )

  const hasActiveJobs = computed(() => activeJobs.value.length > 0)

  const completedJobs = computed(() =>
    jobs.value.filter(j => j.status === 'completed' || j.status === 'failed' || j.status === 'aborted')
  )

  const hasCompletedJobs = computed(() => completedJobs.value.length > 0)

  function togglePanel() {
    panelOpen.value = !panelOpen.value
  }

  function closePanel() {
    panelOpen.value = false
  }

  async function fetchJobs() {
    const res = await api.get('/job')
    jobs.value = Array.isArray(res.data) ? res.data : []
    for (const job of activeJobs.value) {
      if (!sseConnections.has(job.job_id)) {
        watchJob(job.job_id)
      }
    }
  }

  function watchJob(jobId) {
    if (sseConnections.has(jobId)) return

    const evtSource = new EventSource(`${API_BASE}/job/${jobId}/status`, { withCredentials: true })
    sseConnections.set(jobId, evtSource)

    evtSource.onmessage = (event) => {
      let data
      try {
        data = JSON.parse(event.data)
      } catch {
        return
      }
      const idx = jobs.value.findIndex(j => j.job_id === jobId)
      if (idx >= 0) {
        jobs.value[idx] = { ...jobs.value[idx], ...data }
      } else {
        jobs.value.push(data)
      }

      if (data.status === 'completed' || data.status === 'failed' || data.status === 'aborted') {
        evtSource.close()
        sseConnections.delete(jobId)
      }
    }

    evtSource.onerror = () => {
      evtSource.close()
      sseConnections.delete(jobId)
    }
  }

  async function cancelJob(jobId) {
    await api.delete(`/job/${jobId}`)
    const idx = jobs.value.findIndex(j => j.job_id === jobId)
    if (idx >= 0) {
      jobs.value[idx].status = 'aborted'
    }
    const evtSource = sseConnections.get(jobId)
    if (evtSource) {
      evtSource.close()
      sseConnections.delete(jobId)
    }
  }

  function addJob(job) {
    jobs.value.unshift(job)
    watchJob(job.job_id)
  }

  async function clearCompleted() {
    await api.delete('/job/done')
    await fetchJobs()
  }

  function cleanup() {
    for (const [, evtSource] of sseConnections) {
      evtSource.close()
    }
    sseConnections.clear()
  }

  return {
    jobs,
    activeJobs,
    hasActiveJobs,
    completedJobs,
    hasCompletedJobs,
    panelOpen,
    error,
    togglePanel,
    closePanel,
    fetchJobs,
    watchJob,
    cancelJob,
    addJob,
    clearCompleted,
    cleanup,
  }
})
