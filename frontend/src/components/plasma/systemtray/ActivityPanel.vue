<script setup lang="ts">
import { computed, onMounted } from 'vue'
import dayjs from 'dayjs'
import { useMessage } from '../../../composables/useMessage'
import { useI18n } from '../../../composables/useI18n'
import { showConfirm } from '../../../composables/useNativeDialog'
import { useActivityStore } from '../../../stores/activity'
import { useTasksStore } from '../../../stores/tasks'
import { useUploadStore } from '../../../stores/upload'
import { usePendingOpsStore } from '../../../stores/pendingOps'
import TrayPopup from './TrayPopup.vue'
import {
  IconSync,
  IconUpload,
  IconContentCopy,
  IconFolderMoveOutline as IconFolder,
  IconDeleteOutline as IconDelete,
  IconCogOutline as IconCog,
  IconPencilOutline as IconPencil,
  IconFolderOutline as IconFolderCreate,
  IconRestore,
  IconContentSaveOutline as IconContentSave,
  IconTimerSand as IconTimer,
} from '../../../barrels/icons'

const { t, te } = useI18n()
const message = useMessage()
const activityStore = useActivityStore()
const tasksStore = useTasksStore()
const uploadStore = useUploadStore()
const pendingOps = usePendingOpsStore()

const localTaskIds = computed(() => new Set(uploadStore.uploads.map(u => u.taskId).filter(Boolean)))
const foreignUploadTasks = computed(() =>
  tasksStore.activeTasks.filter(task =>
    task.type === 'upload' &&
    !localTaskIds.value.has(task.task_id) &&
    !!task.client_instance_id &&
    task.client_instance_id !== uploadStore.clientInstanceId
  )
)
const orphanUploadTasks = computed(() =>
  tasksStore.activeTasks
    .filter(task =>
      task.type === 'upload' &&
      !localTaskIds.value.has(task.task_id) &&
      (!task.client_instance_id || task.client_instance_id === uploadStore.clientInstanceId)
    )
    .map(task => ({ ...task, status: 'interrupted' }))
)
const activeTasks = computed(() =>
  tasksStore.activeTasks.filter(task => task.type !== 'upload' && !localTaskIds.value.has(task.task_id))
)
const completedTasks = computed(() => tasksStore.completedTasks.filter(task => !localTaskIds.value.has(task.task_id)))
const pendingItems = computed(() => [...pendingOps.ops].sort((a, b) => {
  if (!!a._retrying !== !!b._retrying) return a._retrying ? -1 : 1
  if (!!a._completed !== !!b._completed) return a._completed ? 1 : -1
  return b.createdAt - a.createdAt
}))

const hasUploads = computed(() => uploadStore.uploads.length > 0 || foreignUploadTasks.value.length > 0 || orphanUploadTasks.value.length > 0)
const hasProcessing = computed(() => activeTasks.value.length > 0)
const hasPending = computed(() => pendingItems.value.length > 0)
const hasHistory = computed(() => completedTasks.value.length > 0)
const hasActivity = computed(() =>
  hasUploads.value || hasProcessing.value || hasPending.value || hasHistory.value
)

const typeIcons: Record<string, any> = {
  upload: IconUpload,
  copy: IconContentCopy,
  move: IconFolder,
  delete: IconDelete,
  rename: IconPencil,
  mkdir: IconFolderCreate,
  restore: IconRestore,
  saveViewer: IconContentSave,
  deleteTrash: IconDelete,
  emptyTrash: IconDelete,
}

function typeIcon(type_: string | undefined) {
  return (type_ && typeIcons[type_]) || IconTimer
}

function typeLabel(type_: string) {
  const taskKey = `tasks.type_${type_}`
  const pendingKey = `pending.type_${type_}`
  const taskText = t(taskKey)
  if (taskText !== taskKey) return taskText
  const pendingText = t(pendingKey)
  if (pendingText !== pendingKey) return pendingText
  return type_
}

function statusLabel(status: string) {
  if (status === 'interrupted') return t('upload.status_interrupted')
  return t(`tasks.status_${status}`) || status
}

function statusType(status: string) {
  switch (status) {
    case 'completed': return 'success'
    case 'failed': return 'error'
    case 'aborted':
    case 'cancelled':
    case 'interrupted': return 'warning'
    case 'running':
    case 'uploading': return 'info'
    default: return 'default'
  }
}

function phaseLabel(job: any) {
  if (!job.phase) return ''
  return t(`tasks.phase_${job.phase}`) || job.phase
}

function remoteUploadDetail(task: any) {
  const phase = phaseLabel(task)
  if (phase && phase !== t('upload.status_uploading') && phase !== t('tasks.phase_uploading')) {
    return phase
  }
  return t('upload.remote_waiting')
}

function taskName(task: any) {
  return task.name || ''
}

function formatSize(bytes: number) {
  if (!bytes) return ''
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0
  let size = bytes
  while (size >= 1024 && i < units.length - 1) {
    size /= 1024
    i++
  }
  return `${size.toFixed(1)} ${units[i]}`
}

function formatSpeed(bytesPerSec: number) {
  if (!bytesPerSec) return ''
  return formatSize(bytesPerSec) + '/s'
}

function timeAgo(ts: string | number | Date | undefined) {
  if (!ts) return ''
  const d = dayjs(ts)
  const now = dayjs()
  const diffMin = now.diff(d, 'minute')
  if (diffMin < 1) return t('tasks.time_just_now')
  if (diffMin < 60) return t('tasks.time_minutes_ago', { n: diffMin })
  const diffHour = now.diff(d, 'hour')
  if (diffHour < 24) return t('tasks.time_hours_ago', { n: diffHour })
  return d.format('MM-DD HH:mm')
}

function formatPendingError(error: string | null | undefined) {
  if (!error) return ''
  if (error === 'Network Error' || error === 'Failed to fetch' || error === 'Load failed') {
    return t('pending.error_network')
  }
  if (/^[a-z0-9_]+$/i.test(error)) {
    return te({ response: { data: { error } } } as any)
  }
  return error
}

function pendingStatusText(op: { _retrying?: boolean; _completed?: boolean; lastError?: string | null }) {
  if (op._retrying) return t('pending.retrying')
  if (op._completed) return t('pending.completed')
  return formatPendingError(op.lastError)
}

function pendingStatusKind(op: { _retrying?: boolean; _completed?: boolean; lastError?: string | null }) {
  if (op._retrying) return 'retrying'
  if (op._completed) return 'completed'
  if (op.lastError) return 'failed'
  return 'queued'
}

function pendingStatusBadge(op: { _retrying?: boolean; _completed?: boolean; lastError?: string | null }) {
  return t(`pending.status_${pendingStatusKind(op)}`)
}

async function loadTasks() {
  try {
    await tasksStore.fetchTasks()
  } catch (e: any) {
    message.error(te(e, 'activity.load_failed'))
  }
}

async function cancelTask(taskId: string) {
  try {
    await tasksStore.cancelTask(taskId)
  } catch (e: any) {
    message.error(te(e, 'activity.cancel_failed'))
  }
}

async function clearCompleted() {
  try {
    await tasksStore.clearCompleted()
  } catch (e: any) {
    message.error(te(e, 'activity.clear_failed'))
  }
}

async function handleDiscardAll() {
  if (!await showConfirm(t('pending.confirm_discard_all'))) return
  pendingOps.discardAll()
}

onMounted(() => {
  loadTasks()
})
</script>

<template>
  <TrayPopup :show="activityStore.open" :title="t('activity.title')" @update:show="v => v ? activityStore.show() : activityStore.close()">
    <template #actions>
      <button v-if="hasHistory" class="tray-btn" @click="clearCompleted">
        {{ t('tasks.clear') }}
      </button>
      <button v-if="hasPending" class="tray-btn" @click="handleDiscardAll">
        {{ t('pending.discard_all') }}
      </button>
    </template>

    <div v-if="!hasActivity" class="tasks-empty">
      <div class="empty-state">{{ t('activity.empty') }}</div>
    </div>

    <div v-else class="tasks-scroll">
      <section v-if="hasUploads" class="activity-section">
        <div class="section-title">{{ t('activity.section_uploads') }}</div>
        <div v-for="u in uploadStore.uploads" :key="'upload-' + u.id" class="task-item task-item--active">
          <div class="task-header">
            <IconUpload class="task-icon" width="14" height="14" />
            <span class="task-name">{{ u.fileName }}</span>
            <span :class="['task-status', `status-${statusType(u.status)}`]">
              {{ phaseLabel(u) || (u.status === 'paused' ? statusLabel('paused') : statusLabel('uploading')) }}
            </span>
          </div>

          <div class="task-progress">
            <div class="progress-bar">
              <div class="progress-fill progress-info" :style="{ width: u.progress + '%' }" />
            </div>
            <span class="task-phase">
              {{ u.progress }}%<template v-if="phaseLabel(u)"> · {{ phaseLabel(u) }}</template>
              <template v-if="u.speed"> · {{ formatSpeed(u.speed) }}</template>
              <template v-if="u.fileSize"> · {{ formatSize(u.fileSize) }}</template>
            </span>
          </div>

          <div class="task-footer">
            <span class="task-time">{{ formatSize(u.bytesUploaded) }}</span>
            <div class="task-actions">
              <template v-if="u.status === 'uploading'">
                <button class="tray-btn" @click="uploadStore.pauseUpload(u.id)">{{ t('upload.pause') }}</button>
                <button class="tray-btn tray-btn--danger" @click="uploadStore.cancelUpload(u.id)">{{ t('tasks.cancel') }}</button>
              </template>
              <template v-if="u.status === 'paused'">
                <button class="tray-btn tray-btn--accent" @click="uploadStore.resumeUpload(u.id)">{{ t('upload.resume') }}</button>
                <button class="tray-btn tray-btn--danger" @click="uploadStore.cancelUpload(u.id)">{{ t('tasks.cancel') }}</button>
              </template>
            </div>
          </div>
          <div v-if="u.error" class="task-error">{{ /^[a-z0-9_.-]+$/i.test(u.error) ? t(u.error, { name: u.fileName }) : u.error }}</div>
        </div>
        <div v-for="task in foreignUploadTasks" :key="'foreign-upload-' + task.task_id" class="task-item task-item--active">
          <div class="task-header">
            <IconUpload class="task-icon" width="14" height="14" />
            <span class="task-name">{{ taskName(task) || typeLabel(task.type) }}</span>
            <span :class="['task-status', `status-${statusType('uploading')}`]">
              {{ t('upload.status_uploading') }}
            </span>
          </div>
          <div class="task-progress">
            <span class="task-phase">{{ remoteUploadDetail(task) }}</span>
          </div>
          <div class="task-footer">
            <span class="task-time">{{ timeAgo(task.created_at) }}</span>
            <button class="tray-btn tray-btn--danger" @click="cancelTask(task.task_id)">
              {{ t('tasks.cancel') }}
            </button>
          </div>
        </div>
        <div v-for="task in orphanUploadTasks" :key="'orphan-upload-' + task.task_id" class="task-item task-item--active">
          <div class="task-header">
            <IconUpload class="task-icon" width="14" height="14" />
            <span class="task-name">{{ taskName(task) || typeLabel(task.type) }}</span>
            <span :class="['task-status', `status-${statusType('interrupted')}`]">
              {{ statusLabel('interrupted') }}
            </span>
          </div>
          <div class="task-progress">
            <span class="task-phase">
              {{ t('upload.resume_unavailable') }}
            </span>
          </div>
          <div class="task-footer">
            <span class="task-time">{{ timeAgo(task.created_at) }}</span>
            <button class="tray-btn tray-btn--danger" @click="cancelTask(task.task_id)">
              {{ t('tasks.cancel') }}
            </button>
          </div>
        </div>
      </section>

      <section v-if="hasProcessing" class="activity-section">
        <div class="section-title">{{ t('activity.section_processing') }}</div>
        <div v-for="task in activeTasks" :key="task.task_id" class="task-item task-item--active">
          <div class="task-header">
            <component :is="typeIcon(task.type)" class="task-icon" width="14" height="14" />
            <span class="task-name">{{ taskName(task) || typeLabel(task.type) }}</span>
            <span :class="['task-status', `status-${statusType(task.status)}`]">
              <template v-if="task.type === 'upload'">{{ phaseLabel(task) || statusLabel(task.status) }}</template>
              <template v-else>{{ statusLabel(task.status) }}</template>
            </span>
          </div>

          <div v-if="task.type !== 'upload'" class="task-progress">
            <div class="progress-bar">
              <div class="progress-fill" :class="task.status === 'running' ? 'progress-info' : ''" :style="{ width: Math.round((task.progress || 0) * 100) + '%' }" />
            </div>
            <span v-if="phaseLabel(task)" class="task-phase">{{ phaseLabel(task) }}</span>
          </div>
          <div v-else class="task-progress">
            <span class="task-phase">{{ phaseLabel(task) }} {{ Math.round((task.progress || 0) * 100) }}%</span>
          </div>

          <div class="task-footer">
            <span class="task-time">{{ timeAgo(task.created_at) }}</span>
            <button v-if="task.status === 'pending' || task.status === 'running'" class="tray-btn tray-btn--danger" @click="cancelTask(task.task_id)">
              {{ t('tasks.cancel') }}
            </button>
          </div>
        </div>
      </section>

      <section v-if="hasPending" class="activity-section">
        <div class="section-title">{{ t('activity.section_pending') }}</div>
        <TransitionGroup name="pending-list" tag="div">
          <div
            v-for="op in pendingItems"
            :key="op.id"
            class="task-item task-item--pending"
            :class="{ 'task-item--completed': !!op._completed }"
          >
            <div class="task-header">
              <component :is="typeIcon(op.type)" class="task-icon" width="14" height="14" />
              <span class="task-name">{{ op.description || typeLabel(op.type || '') }}</span>
              <span class="task-status" :class="`status-${pendingStatusKind(op)}`">
                {{ pendingStatusBadge(op) }}
              </span>
            </div>

            <div class="task-footer">
              <span class="task-time">{{ timeAgo(op.createdAt) }}</span>
              <span
                v-if="op._retrying || op._completed || op.lastError"
                class="task-inline-status"
                :class="{ 'task-inline-status--error': !!op.lastError && !op._retrying && !op._completed }"
              >
                {{ pendingStatusText(op) }}
              </span>
            </div>

            <div v-if="!op._completed" class="task-actions task-actions--pending">
              <button class="tray-btn tray-btn--accent" :disabled="!!op._retrying" @click="pendingOps.retry(op.id)">
                {{ op._retrying ? t('pending.retrying') : t('pending.retry') }}
              </button>
              <button class="tray-btn" @click="pendingOps.discard(op.id)">
                {{ t('pending.discard') }}
              </button>
            </div>
          </div>
        </TransitionGroup>
      </section>

      <section v-if="hasHistory" class="activity-section">
        <div class="section-title">{{ t('activity.section_history') }}</div>
        <div v-for="task in completedTasks" :key="task.task_id" class="task-item">
          <div class="task-header">
            <component :is="typeIcon(task.type)" class="task-icon" width="14" height="14" />
            <span class="task-name">{{ taskName(task) || typeLabel(task.type) }}</span>
            <span :class="['task-status', `status-${statusType(task.status)}`]">
              {{ statusLabel(task.status) }}
            </span>
          </div>
          <div class="task-footer">
            <span class="task-time">{{ timeAgo(task.updated_at || task.created_at) }}</span>
          </div>
        </div>
      </section>
    </div>
  </TrayPopup>
</template>

<style lang="scss" scoped>
.tasks-empty { padding: 24px 12px; }

.empty-state {
  text-align: center;
  color: var(--breeze-text-disabled, #505962);
  font-size: 13px;
}

.tasks-scroll {
  padding: 6px;
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.activity-section {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.section-title {
  padding: 4px 2px;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.04em;
  text-transform: uppercase;
  color: var(--breeze-text-secondary, #8b949e);
}

.task-item {
  padding: 8px;
  border-radius: 6px;
  background: rgba(255, 255, 255, 0.02);
  border: 1px solid rgba(255, 255, 255, 0.03);

  &--active { background: rgba(61, 174, 233, 0.06); }
  &--pending { background: rgba(242, 201, 125, 0.06); }
  &--completed { background: rgba(39, 174, 96, 0.08); }
}

.task-header { display: flex; align-items: center; gap: 6px; }
.task-icon { font-size: 13px; flex-shrink: 0; }
.task-name { flex: 1; font-size: 12px; color: #fcfcfc; @include truncate; }
.task-status {
  font-size: 11px;
  padding: 1px 6px;
  border-radius: 999px;
  flex-shrink: 0;
  border: 1px solid transparent;
}

.status-success { color: #63e2b7; }
.status-error { color: #e88080; }
.status-warning { color: #f2c97d; }
.status-info { color: #70c0e8; }
.status-default { color: #9aa0a6; }
.status-queued {
  color: var(--breeze-text-secondary);
  background: rgba(127, 140, 141, 0.14);
  border-color: rgba(127, 140, 141, 0.22);
}
.status-retrying {
  color: var(--breeze-accent, #3daee9);
  background: rgba(61, 174, 233, 0.12);
  border-color: rgba(61, 174, 233, 0.24);
}
.status-failed {
  color: var(--breeze-danger, #da4453);
  background: rgba(218, 68, 83, 0.12);
  border-color: rgba(218, 68, 83, 0.24);
}
.status-completed {
  color: var(--breeze-success, #27ae60);
  background: rgba(39, 174, 96, 0.12);
  border-color: rgba(39, 174, 96, 0.24);
}

.task-progress { margin-top: 6px; }

.progress-bar { height: 4px; background: var(--breeze-border, #3b4045); border-radius: 2px; overflow: hidden; }

.progress-fill {
  height: 100%;
  border-radius: 2px;
  background: var(--breeze-text-secondary, #a1a9b1);
  transition: width 0.3s ease;

  &.progress-info { background: var(--breeze-accent, #3daee9); }
}

.task-phase { font-size: 11px; color: #6e7a86; margin-top: 2px; display: block; }
.task-error { font-size: 11px; color: #e88080; margin-top: 4px; @include truncate; }
.task-footer { display: flex; align-items: center; justify-content: space-between; gap: 8px; margin-top: 4px; }
.task-time { font-size: 11px; color: #505962; flex-shrink: 0; }
.task-inline-status {
  font-size: 11px;
  color: var(--breeze-text-secondary, #8b949e);
  @include truncate;
}
.task-inline-status--error { color: var(--breeze-danger, #da4453); }
.task-actions { display: flex; gap: 4px; justify-content: flex-end; margin-top: 6px; }
.task-actions--pending { margin-top: 8px; }

.tray-btn {
  padding: 2px 8px;
  border: none;
  border-radius: 3px;
  background: none;
  color: var(--breeze-text-secondary, #a1a9b1);
  font-size: 12px;
  cursor: default;

  &:active { background: $hover-white-medium; color: var(--breeze-text, #fcfcfc); }
  @include hover { background: $hover-white-medium; color: var(--breeze-text, #fcfcfc); }
  &:disabled { opacity: 0.5; }
  &--accent { color: var(--breeze-accent, #3daee9); }
  &--danger {
    color: var(--breeze-danger, #da4453);
    &:active { background: rgba(218, 68, 83, 0.15); }
    @include hover { background: rgba(218, 68, 83, 0.15); }
  }
}

.pending-list-enter-active,
.pending-list-leave-active {
  transition: opacity 0.2s ease, transform 0.2s ease;
}

.pending-list-enter-from,
.pending-list-leave-to {
  opacity: 0;
  transform: translateY(6px);
}

.pending-list-move {
  transition: transform 0.2s ease;
}
</style>
