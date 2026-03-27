<script setup>
import { computed, onMounted, h } from 'vue'
import { NButton, NProgress, NEmpty, NIcon, useMessage } from 'naive-ui'
import { usePanelResize } from '../../../composables/usePanelResize'
import dayjs from 'dayjs'
import { useJobsStore } from '../../../stores/jobs'
import { useUploadStore } from '../../../stores/upload'
import { useI18n } from '../../../composables/useI18n'
import IconSync from '~icons/mdi/sync'
import IconUpload from '~icons/mdi/upload'
import IconContentCopy from '~icons/mdi/content-copy'
import IconFolder from '~icons/mdi/folder-move-outline'
import IconDelete from '~icons/mdi/delete-outline'
import IconCog from '~icons/mdi/cog-outline'

const { t, te } = useI18n()
const message = useMessage()
const jobsStore = useJobsStore()
const uploadStore = useUploadStore()
const { panelSize, onMouseDown } = usePanelResize()

// Filter out server tasks that have a matching local upload (avoid duplicates)
const localTaskIds = computed(() => new Set(uploadStore.uploads.map(u => u.taskId).filter(Boolean)))
const activeTasks = computed(() => jobsStore.activeTasks.filter(t => !localTaskIds.value.has(t.task_id)))
const completedTasks = computed(() => jobsStore.completedTasks)
const hasTasks = computed(() => jobsStore.tasks.length > 0 || uploadStore.uploads.length > 0)

const typeIcons = {
  transcode: IconSync,
  upload: IconUpload,
  copy: IconContentCopy,
  move: IconFolder,
  delete: IconDelete,
  clear_trash: IconDelete,
}
function typeIcon(type_) {
  return typeIcons[type_] || IconCog
}

function typeLabel(type_) {
  return t(`jobs.type_${type_}`) || type_
}

function statusLabel(status) {
  return t(`jobs.status_${status}`) || status
}

function statusType(status) {
  switch (status) {
    case 'completed': return 'success'
    case 'failed': return 'error'
    case 'aborted':
    case 'cancelled': return 'warning'
    case 'running':
    case 'uploading': return 'info'
    default: return 'default'
  }
}

function phaseLabel(job) {
  if (!job.phase) return ''
  return t(`jobs.phase_${job.phase}`) || job.phase
}

function taskName(task) {
  return task.name || ''
}

function formatSize(bytes) {
  if (!bytes) return ''
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0; let size = bytes
  while (size >= 1024 && i < units.length - 1) { size /= 1024; i++ }
  return `${size.toFixed(1)} ${units[i]}`
}

function formatSpeed(bytesPerSec) {
  if (!bytesPerSec) return ''
  return formatSize(bytesPerSec) + '/s'
}

// Find local upload entry for a server-side upload job (for pause/resume/cancel)
function findUploadEntry(taskId) {
  return uploadStore.uploads.find(u => u.taskId === taskId)
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

async function loadTasks() {
  try {
    await jobsStore.fetchTasks()
  } catch (e) {
    message.error(te(e, 'jobs.load_failed'))
  }
}

async function cancelTask(taskId) {
  try {
    await jobsStore.cancelTask(taskId)
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
  loadTasks()
})
</script>

<template>
  <Transition name="slide-up">
    <div v-if="jobsStore.panelOpen" class="jobs-panel" :style="{ width: panelSize.width + 'px', height: panelSize.height + 'px' }">
      <div class="panel-resize-handle" @mousedown="onMouseDown" />
      <div class="jobs-header">
        <span class="jobs-title">{{ t('jobs.title') }}</span>
        <NButton
          v-if="jobsStore.hasCompletedTasks"
          quaternary
          size="tiny"
          @click="clearCompleted"
        >
          {{ t('jobs.clear') }}
        </NButton>
      </div>

      <div v-if="!hasTasks" class="jobs-empty">
        <NEmpty :description="t('jobs.no_jobs')" size="small" />
      </div>

      <div v-else class="jobs-scroll">
        <!-- Local uploads (client-side uploading) -->
        <div v-for="u in uploadStore.uploads" :key="'upload-' + u.id" class="job-item job-item--active">
          <div class="job-header">
            <NIcon class="job-icon" :size="14"><IconUpload /></NIcon>
            <span class="job-name">{{ u.fileName }}</span>
            <span :class="['job-status', 'status-info']">
              {{ u.status === 'paused' ? statusLabel('paused') : statusLabel('uploading') }}
            </span>
          </div>

          <div class="job-progress">
            <NProgress :percentage="u.progress" status="info" :show-indicator="true" :height="4" />
            <span class="job-phase">{{ u.progress }}% · {{ formatSpeed(u.speed) }}{{ u.fileSize ? ' · ' + formatSize(u.fileSize) : '' }}</span>
          </div>

          <div class="job-footer">
            <span class="job-time">{{ formatSize(u.bytesUploaded) }}</span>
            <div class="job-actions">
              <template v-if="u.status === 'uploading'">
                <NButton quaternary size="tiny" @click="uploadStore.pauseUpload(u.id)">{{ t('upload.pause') }}</NButton>
                <NButton quaternary size="tiny" type="error" @click="uploadStore.cancelUpload(u.id)">{{ t('jobs.cancel') }}</NButton>
              </template>
              <template v-if="u.status === 'paused'">
                <NButton quaternary size="tiny" type="info" @click="uploadStore.resumeUpload(u.id)">{{ t('upload.resume') }}</NButton>
                <NButton quaternary size="tiny" type="error" @click="uploadStore.cancelUpload(u.id)">{{ t('jobs.cancel') }}</NButton>
              </template>
            </div>
          </div>
          <div v-if="u.error" class="job-error">{{ u.error }}</div>
        </div>

        <!-- Active server tasks (upload processing, transcode, etc.) -->
        <div v-for="task in activeTasks" :key="task.task_id" class="job-item job-item--active">
          <div class="job-header">
            <NIcon class="job-icon" :size="14"><component :is="typeIcon(task.type)" /></NIcon>
            <span class="job-name">{{ taskName(task) || typeLabel(task.type) }}</span>
            <span :class="['job-status', `status-${statusType(task.status)}`]">
              <template v-if="task.type === 'upload'">{{ phaseLabel(task) || statusLabel(task.status) }}</template>
              <template v-else>{{ statusLabel(task.status) }}</template>
            </span>
          </div>

          <div v-if="task.type !== 'upload'" class="job-progress">
            <NProgress
              :percentage="Math.round((task.progress || 0) * 100)"
              :status="task.status === 'running' ? 'info' : 'default'"
              :show-indicator="true"
              :height="4"
            />
            <span v-if="phaseLabel(task)" class="job-phase">{{ phaseLabel(task) }}</span>
          </div>
          <div v-else class="job-progress">
            <span class="job-phase">{{ phaseLabel(task) }} {{ Math.round((task.progress || 0) * 100) }}%</span>
          </div>

          <div class="job-footer">
            <span class="job-time">{{ timeAgo(task.created_at) }}</span>
            <NButton
              v-if="task.status === 'pending' || task.status === 'running'"
              quaternary
              size="tiny"
              type="error"
              @click="cancelTask(task.task_id)"
            >
              {{ t('jobs.cancel') }}
            </NButton>
          </div>
        </div>

        <!-- Completed/failed/cancelled tasks -->
        <div v-for="task in completedTasks" :key="task.task_id" class="job-item">
          <div class="job-header">
            <NIcon class="job-icon" :size="14"><component :is="typeIcon(task.type)" /></NIcon>
            <span class="job-name">{{ taskName(task) || typeLabel(task.type) }}</span>
            <span :class="['job-status', `status-${statusType(task.status)}`]">
              {{ statusLabel(task.status) }}
            </span>
          </div>

          <div class="job-footer">
            <span class="job-time">{{ timeAgo(task.updated_at || task.created_at) }}</span>
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
  display: flex;
  flex-direction: column;
  z-index: 1000;
  background: #2a2e32;
  border: 1px solid #3b4045;
  border-radius: 8px;
  box-shadow: 0 4px 24px rgba(0, 0, 0, 0.4);
  overflow: hidden;
}

.panel-resize-handle {
  height: 4px;
  cursor: ns-resize;
  flex-shrink: 0;
  background: transparent;
}
.panel-resize-handle:hover {
  background: rgba(61, 174, 233, 0.3);
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

.job-actions {
  display: flex;
  gap: 2px;
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
