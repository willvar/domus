<script setup>
import { computed, onMounted, onUnmounted } from 'vue'
import { NButton, NProgress, NEmpty, useMessage } from 'naive-ui'
import dayjs from 'dayjs'
import { useJobsStore } from '../stores/jobs'
import { useI18n } from '../composables/useI18n'

const { t, te } = useI18n()
const message = useMessage()
const jobsStore = useJobsStore()

const activeJobs = computed(() => jobsStore.activeJobs)
const completedJobs = computed(() => jobsStore.completedJobs)
const hasJobs = computed(() => jobsStore.jobs.length > 0)

function typeIcon(type_) {
  switch (type_) {
    case 'transcode': return '🔄'
    case 'upload': return '📤'
    default: return '⚙️'
  }
}

function typeLabel(type_) {
  switch (type_) {
    case 'transcode': return t('jobs.type_transcode')
    default: return type_
  }
}

function statusLabel(status) {
  return t(`jobs.status_${status}`) || status
}

function statusType(status) {
  switch (status) {
    case 'completed': return 'success'
    case 'failed': return 'error'
    case 'aborted': return 'warning'
    case 'running': return 'info'
    default: return 'default'
  }
}

function phaseLabel(job) {
  if (!job.phase) return ''
  return t(`jobs.phase_${job.phase}`) || job.phase
}

function jobName(job) {
  try {
    const params = JSON.parse(job.params || '{}')
    return params.original_name || params.output_name || ''
  } catch {
    return ''
  }
}

function timeAgo(ts) {
  if (!ts) return ''
  const d = dayjs(ts)
  const now = dayjs()
  const diffMin = now.diff(d, 'minute')
  if (diffMin < 1) return t('jobs.time_just_now')
  if (diffMin < 60) return t('jobs.time_minutes_ago', { n: diffMin })
  const diffHour = now.diff(d, 'hour')
  if (diffHour < 24) return t('jobs.time_hours_ago', { n: diffHour })
  return d.format('MM-DD HH:mm')
}

async function loadJobs() {
  try {
    await jobsStore.fetchJobs()
  } catch (e) {
    message.error(te(e, 'jobs.load_failed'))
  }
}

async function cancelJob(jobId) {
  try {
    await jobsStore.cancelJob(jobId)
  } catch (e) {
    message.error(te(e, 'jobs.cancel_failed'))
  }
}

async function clearCompleted() {
  try {
    await jobsStore.clearCompleted()
  } catch (e) {
    message.error(te(e, 'jobs.clear_failed'))
  }
}

onMounted(() => {
  loadJobs()
})
</script>

<template>
  <Transition name="slide-up">
    <div v-if="jobsStore.panelOpen" class="jobs-panel">
      <div class="jobs-header">
        <span class="jobs-title">{{ t('jobs.title') }}</span>
        <NButton
          v-if="jobsStore.hasCompletedJobs"
          quaternary
          size="tiny"
          @click="clearCompleted"
        >
          {{ t('jobs.clear') }}
        </NButton>
      </div>

      <div v-if="!hasJobs" class="jobs-empty">
        <NEmpty :description="t('jobs.no_jobs')" size="small" />
      </div>

      <div v-else class="jobs-scroll">
        <!-- Active jobs -->
        <div v-for="job in activeJobs" :key="job.job_id" class="job-item job-item--active">
          <div class="job-header">
            <span class="job-icon">{{ typeIcon(job.type) }}</span>
            <span class="job-name">{{ jobName(job) || typeLabel(job.type) }}</span>
            <span :class="['job-status', `status-${statusType(job.status)}`]">
              {{ statusLabel(job.status) }}
            </span>
          </div>

          <div v-if="job.status === 'running' || job.status === 'pending'" class="job-progress">
            <NProgress
              :percentage="Math.round((job.progress || 0) * 100)"
              :status="job.status === 'running' ? 'info' : 'default'"
              :show-indicator="true"
              :height="4"
            />
            <span v-if="phaseLabel(job)" class="job-phase">{{ phaseLabel(job) }}</span>
          </div>

          <div class="job-footer">
            <span class="job-time">{{ timeAgo(job.created_at) }}</span>
            <NButton
              v-if="job.status === 'pending' || job.status === 'running'"
              quaternary
              size="tiny"
              type="error"
              @click="cancelJob(job.job_id)"
            >
              {{ t('jobs.cancel') }}
            </NButton>
          </div>
        </div>

        <!-- Completed/failed/aborted jobs -->
        <div v-for="job in completedJobs" :key="job.job_id" class="job-item">
          <div class="job-header">
            <span class="job-icon">{{ typeIcon(job.type) }}</span>
            <span class="job-name">{{ jobName(job) || typeLabel(job.type) }}</span>
            <span :class="['job-status', `status-${statusType(job.status)}`]">
              {{ statusLabel(job.status) }}
            </span>
          </div>

          <div v-if="job.error_msg" class="job-error">{{ job.error_msg }}</div>

          <div class="job-footer">
            <span class="job-time">{{ timeAgo(job.updated_at || job.created_at) }}</span>
          </div>
        </div>
      </div>
    </div>
  </Transition>
</template>

<style scoped>
.jobs-panel {
  position: fixed;
  bottom: 68px;
  right: 8px;
  width: 360px;
  max-height: 420px;
  display: flex;
  flex-direction: column;
  z-index: 1000;
  background: #2a2e32;
  border: 1px solid #3b4045;
  border-radius: 8px;
  box-shadow: 0 4px 24px rgba(0, 0, 0, 0.4);
}

.jobs-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  padding: 10px 12px;
  border-bottom: 1px solid #3b4045;
  flex-shrink: 0;
}

.jobs-title {
  font-size: 13px;
  font-weight: 600;
  color: #bfc5ca;
}

.jobs-empty {
  padding: 24px 12px;
}

.jobs-scroll {
  overflow-y: auto;
  padding: 6px;
  flex: 1;
  min-height: 0;
}

.job-item {
  padding: 8px;
  border-radius: 4px;
  background: rgba(255, 255, 255, 0.02);
  margin-bottom: 4px;
}
.job-item:last-child {
  margin-bottom: 0;
}

.job-item--active {
  background: rgba(61, 174, 233, 0.06);
}

.job-header {
  display: flex;
  align-items: center;
  gap: 6px;
}

.job-icon {
  font-size: 13px;
  flex-shrink: 0;
}

.job-name {
  flex: 1;
  font-size: 12px;
  color: #bfc5ca;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.job-status {
  font-size: 11px;
  padding: 1px 6px;
  border-radius: 3px;
  flex-shrink: 0;
}

.status-success { color: #63e2b7; }
.status-error { color: #e88080; }
.status-warning { color: #f2c97d; }
.status-info { color: #70c0e8; }
.status-default { color: #9aa0a6; }

.job-progress {
  margin-top: 6px;
}

.job-phase {
  font-size: 11px;
  color: #6e7a86;
  margin-top: 2px;
  display: block;
}

.job-error {
  font-size: 11px;
  color: #e88080;
  margin-top: 4px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.job-footer {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: 4px;
}

.job-time {
  font-size: 11px;
  color: #505962;
}

.slide-up-enter-active,
.slide-up-leave-active {
  transition: all 0.2s ease;
}
.slide-up-enter-from,
.slide-up-leave-to {
  opacity: 0;
  transform: translateY(12px);
}
</style>
