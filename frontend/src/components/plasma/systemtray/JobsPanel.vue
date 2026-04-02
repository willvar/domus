<script setup>
import { computed, onMounted, toRef } from 'vue'
import { useMessage } from '../../../composables/useMessage'
import dayjs from 'dayjs'
import { useJobsStore } from '../../../stores/jobs'
import { useUploadStore } from '../../../stores/upload'
import { useI18n } from '../../../composables/useI18n'
import TrayPopup from './TrayPopup.vue'
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
  <TrayPopup :show="jobsStore.panelOpen" :title="t('jobs.title')" @update:show="v => jobsStore.panelOpen = v">
    <template #actions>
      <button v-if="jobsStore.hasCompletedTasks" class="tray-btn" @click="clearCompleted">
        {{ t('jobs.clear') }}
      </button>
    </template>

    <div v-if="!hasTasks" class="jobs-empty">
      <div class="empty-state">{{ t('jobs.no_jobs') }}</div>
    </div>

    <div v-else class="jobs-scroll">
      <!-- Local uploads (client-side uploading) -->
      <div v-for="u in uploadStore.uploads" :key="'upload-' + u.id" class="job-item job-item--active">
        <div class="job-header">
          <IconUpload class="job-icon" width="14" height="14" />
          <span class="job-name">{{ u.fileName }}</span>
          <span :class="['job-status', 'status-info']">
            {{ u.status === 'paused' ? statusLabel('paused') : statusLabel('uploading') }}
          </span>
        </div>

        <div class="job-progress">
          <div class="progress-bar">
            <div class="progress-fill progress-info" :style="{ width: u.progress + '%' }" />
          </div>
          <span class="job-phase">{{ u.progress }}% · {{ formatSpeed(u.speed) }}{{ u.fileSize ? ' · ' + formatSize(u.fileSize) : '' }}</span>
        </div>

        <div class="job-footer">
          <span class="job-time">{{ formatSize(u.bytesUploaded) }}</span>
          <div class="job-actions">
            <template v-if="u.status === 'uploading'">
              <button class="tray-btn" @click="uploadStore.pauseUpload(u.id)">{{ t('upload.pause') }}</button>
              <button class="tray-btn tray-btn--danger" @click="uploadStore.cancelUpload(u.id)">{{ t('jobs.cancel') }}</button>
            </template>
            <template v-if="u.status === 'paused'">
              <button class="tray-btn tray-btn--accent" @click="uploadStore.resumeUpload(u.id)">{{ t('upload.resume') }}</button>
              <button class="tray-btn tray-btn--danger" @click="uploadStore.cancelUpload(u.id)">{{ t('jobs.cancel') }}</button>
            </template>
          </div>
        </div>
        <div v-if="u.error" class="job-error">{{ u.error }}</div>
      </div>

      <!-- Active server tasks -->
      <div v-for="task in activeTasks" :key="task.task_id" class="job-item job-item--active">
        <div class="job-header">
          <component :is="typeIcon(task.type)" class="job-icon" width="14" height="14" />
          <span class="job-name">{{ taskName(task) || typeLabel(task.type) }}</span>
          <span :class="['job-status', `status-${statusType(task.status)}`]">
            <template v-if="task.type === 'upload'">{{ phaseLabel(task) || statusLabel(task.status) }}</template>
            <template v-else>{{ statusLabel(task.status) }}</template>
          </span>
        </div>

        <div v-if="task.type !== 'upload'" class="job-progress">
          <div class="progress-bar">
            <div class="progress-fill" :class="task.status === 'running' ? 'progress-info' : ''" :style="{ width: Math.round((task.progress || 0) * 100) + '%' }" />
          </div>
          <span v-if="phaseLabel(task)" class="job-phase">{{ phaseLabel(task) }}</span>
        </div>
        <div v-else class="job-progress">
          <span class="job-phase">{{ phaseLabel(task) }} {{ Math.round((task.progress || 0) * 100) }}%</span>
        </div>

        <div class="job-footer">
          <span class="job-time">{{ timeAgo(task.created_at) }}</span>
          <button v-if="task.status === 'pending' || task.status === 'running'" class="tray-btn tray-btn--danger" @click="cancelTask(task.task_id)">
            {{ t('jobs.cancel') }}
          </button>
        </div>
      </div>

      <!-- Completed/failed/cancelled tasks -->
      <div v-for="task in completedTasks" :key="task.task_id" class="job-item">
        <div class="job-header">
          <component :is="typeIcon(task.type)" class="job-icon" width="14" height="14" />
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
  </TrayPopup>
</template>

<style lang="scss" scoped>
.jobs-empty { padding: 24px 12px; }

.empty-state {
  text-align: center;
  color: var(--breeze-text-disabled, #505962);
  font-size: 13px;
}

.jobs-scroll { padding: 6px; }

.job-item {
  padding: 8px;
  border-radius: 4px;
  background: rgba(255, 255, 255, 0.02);
  margin-bottom: 4px;

  &:last-child { margin-bottom: 0; }
  &--active { background: rgba(61, 174, 233, 0.06); }
}

.job-header { display: flex; align-items: center; gap: 6px; }
.job-icon { font-size: 13px; flex-shrink: 0; }
.job-name { flex: 1; font-size: 12px; color: #fcfcfc; @include truncate; }
.job-status { font-size: 11px; padding: 1px 6px; border-radius: 3px; flex-shrink: 0; }

.status-success { color: #63e2b7; }
.status-error { color: #e88080; }
.status-warning { color: #f2c97d; }
.status-info { color: #70c0e8; }
.status-default { color: #9aa0a6; }

.job-progress { margin-top: 6px; }

.progress-bar { height: 4px; background: var(--breeze-border, #3b4045); border-radius: 2px; overflow: hidden; }

.progress-fill {
  height: 100%;
  border-radius: 2px;
  background: var(--breeze-text-secondary, #a1a9b1);
  transition: width 0.3s ease;

  &.progress-info { background: var(--breeze-accent, #3daee9); }
  &.progress-success { background: var(--breeze-success, #27ae60); }
  &.progress-error { background: var(--breeze-danger, #da4453); }
}

.job-phase { font-size: 11px; color: #6e7a86; margin-top: 2px; display: block; }
.job-error { font-size: 11px; color: #e88080; margin-top: 4px; @include truncate; }
.job-footer { display: flex; align-items: center; justify-content: space-between; margin-top: 4px; }
.job-time { font-size: 11px; color: #505962; }
.job-actions { display: flex; gap: 2px; }

.tray-btn {
  padding: 2px 8px;
  border: none;
  border-radius: 3px;
  background: none;
  color: var(--breeze-text-secondary, #a1a9b1);
  font-size: 12px;
  cursor: default;

  &:hover { background: $hover-white-medium; color: var(--breeze-text, #fcfcfc); }
  &--accent { color: var(--breeze-accent, #3daee9); }

  &--danger {
    color: var(--breeze-danger, #da4453);

    &:hover { background: rgba(218, 68, 83, 0.15); }
  }
}
</style>
