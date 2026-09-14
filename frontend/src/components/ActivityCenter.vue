<script setup lang="ts">
import { NButton, NProgress, NScrollbar } from 'naive-ui'
import { useI18n } from '../composables/useI18n'
import { IconCheck } from '../barrels/icons'
import type { Task, UploadSession } from '../types'

withDefaults(defineProps<{
  uploads: UploadSession[]
  serverTasks: Task[]
  pendingCount: number
  activityLabel: string
  showHeading?: boolean
  maxHeight?: string
}>(), {
  showHeading: true,
  maxHeight: 'min(480px, calc(100dvh - 130px))',
})

const emit = defineEmits<{
  cancelUpload: [id: string]
}>()

const { t } = useI18n()

function phaseLabel(phase?: string): string {
  const labels: Record<string, string> = {
    queued: t('files.phase_waiting'),
    generating: t('files.phase_preparing'),
    encrypting: t('files.phase_encrypting'),
    uploading: t('files.phase_uploading'),
    uploading_oss: t('files.phase_uploading'),
    transferring: t('files.phase_uploading'),
    hashing: t('tasks.phase_hashing'),
    thumbnail: t('files.phase_preview'),
    processing: t('files.phase_finishing'),
    finalizing: t('files.phase_finishing'),
    pending: t('tasks.status_pending'),
    running: t('tasks.status_running'),
    paused: t('tasks.status_paused'),
    completed: t('tasks.status_completed'),
    failed: t('tasks.status_failed'),
    aborted: t('tasks.status_aborted'),
    cancelled: t('tasks.status_cancelled'),
  }
  if (!phase) return t('files.phase_waiting')
  return labels[phase] || t('tasks.phase_processing')
}
</script>

<template>
  <section class="activity-center">
    <header v-if="showHeading" class="panel-heading">
      <span>{{ t('files.activity') }}</span>
      <strong>{{ activityLabel }}</strong>
    </header>
    <p v-else class="activity-summary">{{ activityLabel }}</p>

    <div v-if="uploads.length === 0 && serverTasks.length === 0 && pendingCount === 0" class="panel-empty">
      <IconCheck width="30" height="30" />
      <strong>{{ t('files.all_done') }}</strong>
      <span>{{ t('files.all_done_hint') }}</span>
    </div>

    <NScrollbar v-else :style="{ maxHeight }">
      <div class="activity-list">
        <article v-for="item in uploads" :key="item.id">
          <div><strong>{{ item.fileName }}</strong><span :class="{ 'error-text': item.error }">{{ item.error || phaseLabel(item.phase) }}</span></div>
          <span>{{ Math.round(item.progress) }}%</span>
          <NProgress class="progress" type="line" :percentage="item.progress" :show-indicator="false" />
          <NButton
            v-if="item.status === 'uploading' || item.status === 'paused'"
            text
            type="error"
            size="tiny"
            @click="emit('cancelUpload', item.id)"
          >
            {{ t('upload.cancel') }}
          </NButton>
        </article>
        <article v-for="item in serverTasks" :key="item.task_id">
          <div><strong>{{ item.name || item.type }}</strong><span>{{ phaseLabel(item.phase || item.status) }}</span></div>
          <span>{{ Math.round((item.progress || 0) * 100) }}%</span>
          <NProgress class="progress" type="line" :percentage="(item.progress || 0) * 100" :show-indicator="false" />
        </article>
        <article v-if="pendingCount" class="pending-row">
          <div><strong>{{ t('pending.title') }}</strong><span>{{ t('pending.status_queued') }}</span></div>
          <span>{{ pendingCount }}</span>
        </article>
      </div>
    </NScrollbar>
  </section>
</template>

<style scoped>
.activity-center { min-width: 0; }
.panel-heading span,
.panel-heading strong { display: block; }
.panel-heading span { color: #657087; font-size: 11px; font-weight: 800; letter-spacing: .1em; text-transform: uppercase; }
.panel-heading strong { margin-top: 5px; color: #172033; font-size: 16px; }
.activity-summary { margin: 0 0 10px; color: #657087; font-size: 12px; font-weight: 650; }
.panel-empty { display: flex; min-height: 210px; align-items: center; justify-content: center; flex-direction: column; gap: 8px; color: #289471; text-align: center; }
.panel-empty strong { color: #172033; font-size: 15px; }
.panel-empty span { color: #657087; font-size: 12px; }
.activity-list { display: grid; gap: 10px; margin-top: 17px; }
.activity-list article { display: grid; grid-template-columns: minmax(0, 1fr) auto; gap: 8px 12px; padding: 14px; border-radius: 13px; background: #f6f7fa; font-size: 11px; }
.activity-list article strong,
.activity-list article span { display: block; }
.activity-list article strong { overflow: hidden; color: #172033; font-size: 13px; text-overflow: ellipsis; white-space: nowrap; }
.activity-list article div span { margin-top: 3px; color: #657087; }
.activity-list article div span.error-text { color: #d03050; }
.progress { grid-column: 1 / -1; }
.activity-list article > .n-button { grid-column: 1 / -1; justify-self: start; }
</style>
