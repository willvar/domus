<script setup>
import { ref } from 'vue'
import { useUploadStore } from '../../../stores/upload'
import { useI18n } from '../../../composables/useI18n'
import IconClose from '~icons/mdi/close'

const upload = useUploadStore()
const { t, te } = useI18n()
const fileInputRef = ref(null)
let resumeTargetId = null

function triggerResume(id) {
  const entry = upload.uploads.find(u => u.id === id)
  if (entry?._file) {
    // Same session: File object still in memory, no need to re-select
    upload.resumeInterrupted(id)
    return
  }
  resumeTargetId = id
  fileInputRef.value?.click()
}

function onFileSelected(e) {
  const file = e.target.files?.[0]
  if (file && resumeTargetId) {
    upload.resumeInterrupted(resumeTargetId, file)
  }
  resumeTargetId = null
  e.target.value = ''
}

function formatSize(bytes) {
  if (!bytes) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB']
  let i = 0
  let size = bytes
  while (size >= 1024 && i < units.length - 1) { size /= 1024; i++ }
  return `${size.toFixed(1)} ${units[i]}`
}

function formatSpeed(bytesPerSec) {
  if (!bytesPerSec) return ''
  return formatSize(bytesPerSec) + '/s'
}

function formatEta(u) {
  if (!u.progress || u.progress <= 0 || u.progress >= 100) return ''
  const elapsed = (Date.now() - u.startTime) / 1000
  if (elapsed < 1) return ''
  // Use only progress gained in this session to calculate rate
  const gained = u.progress - (u.startProgress || 0)
  if (gained <= 0) return ''
  const remaining = 100 - u.progress
  const secs = Math.ceil((remaining / gained) * elapsed)
  if (secs <= 0) return ''
  if (secs < 60) return `${secs}s`
  if (secs < 3600) return `${Math.floor(secs / 60)}m ${secs % 60}s`
  return `${Math.floor(secs / 3600)}h ${Math.floor((secs % 3600) / 60)}m`
}

function progressClass(status) {
  switch (status) {
    case 'completed': return 'progress-success'
    case 'failed': return 'progress-error'
    case 'paused':
    case 'interrupted': return 'progress-warning'
    default: return 'progress-info'
  }
}

function statusText(status) {
  return t(`upload.status_${status}`)
}

function processingText(u) {
  const phase = u.serverPhase ? (t(`jobs.phase_${u.serverPhase}`) || u.serverPhase) : t('upload.status_processing')
  const pct = u.serverProgress ? Math.round(u.serverProgress * 100) + '%' : ''
  return pct ? `${phase} ${pct}` : phase
}
</script>

<template>
  <Transition name="slide-up">
    <div v-if="upload.showPanel" class="upload-panel">
      <div class="upload-header">
        <span class="upload-title">{{ t('upload.title') }}</span>
        <div class="upload-actions">
          <button class="tray-btn" @click="upload.removeCompleted">
            {{ t('upload.clear') }}
          </button>
          <button class="tray-btn tray-btn--icon" @click="upload.showPanel = false">
            <IconClose width="14" height="14" />
          </button>
        </div>
      </div>

      <div class="upload-scroll">
        <div v-for="u in upload.uploads" :key="u.id" class="upload-item">
          <div class="upload-item-header">
            <span class="upload-filename truncate">{{ u.fileName }}</span>
            <span class="upload-size">{{ formatSize(u.fileSize) }}</span>
          </div>
          <div class="progress-bar">
            <div class="progress-fill" :class="progressClass(u.status)" :style="{ width: u.progress + '%' }" />
          </div>
          <div class="upload-item-footer">
            <span class="upload-status">
              <template v-if="u.status === 'uploading'">{{ u.progress }}% · {{ formatSpeed(u.speed) }}{{ formatEta(u) ? ' · ' + formatEta(u) : '' }}</template>
              <template v-else-if="u.status === 'processing'">{{ processingText(u) }}</template>
              <template v-else>{{ statusText(u.status) }}</template>
            </span>
            <div class="upload-item-actions">
              <template v-if="u.status === 'uploading'">
                <button class="tray-btn" @click="upload.pauseUpload(u.id)">
                  {{ t('upload.pause') }}
                </button>
                <button class="tray-btn" @click="upload.cancelUpload(u.id)">
                  {{ t('upload.cancel') }}
                </button>
              </template>
              <template v-if="u.status === 'paused'">
                <button class="tray-btn tray-btn--accent" @click="upload.resumeUpload(u.id)">
                  {{ t('upload.resume') }}
                </button>
                <button class="tray-btn" @click="upload.cancelUpload(u.id)">
                  {{ t('upload.cancel') }}
                </button>
              </template>
              <template v-if="u.status === 'interrupted'">
                <button class="tray-btn tray-btn--accent" @click="triggerResume(u.id)">
                  {{ u._file ? t('upload.resume') : t('upload.reselect_resume') }}
                </button>
                <button class="tray-btn" @click="upload.dismissInterrupted(u.id)">
                  {{ t('upload.dismiss') }}
                </button>
              </template>
              <template v-if="u.status === 'failed' && u._file">
                <button class="tray-btn tray-btn--accent" @click="upload.retryUpload(u.id)">
                  {{ t('upload.retry') }}
                </button>
              </template>
            </div>
          </div>
          <div v-if="u.error" class="upload-error">{{ te({ response: { data: { error: u.error } } }) }}</div>
        </div>
      </div>
    </div>
  </Transition>
  <input ref="fileInputRef" type="file" style="display:none" @change="onFileSelected">
</template>

<style scoped>
.upload-panel {
  position: fixed;
  bottom: 36px;
  right: 16px;
  width: 360px;
  max-height: 400px;
  z-index: 200;
  background: var(--breeze-surface, #292c30);
  border: 1px solid var(--breeze-border, #3b4045);
  border-radius: 8px;
  box-shadow: 0 -4px 24px rgba(0, 0, 0, 0.12);
  display: flex;
  flex-direction: column;
  overflow: hidden;
}
.upload-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  padding: 10px 12px;
  border-bottom: 1px solid var(--breeze-border, #3b4045);
  flex-shrink: 0;
}
.upload-title {
  font-weight: 600;
  font-size: 13px;
}
.upload-actions {
  display: flex;
  gap: 4px;
}
.upload-scroll {
  overflow-y: auto;
  flex: 1;
  min-height: 0;
}
.upload-item {
  display: flex;
  flex-direction: column;
  gap: 4px;
  padding: 8px 12px;
  border-bottom: 1px solid rgba(255, 255, 255, 0.04);
}
.upload-item:last-child {
  border-bottom: none;
}
.upload-item-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.upload-filename {
  font-size: var(--font-size-sm);
  max-width: 200px;
}
.upload-size {
  font-size: var(--font-size-xs);
  color: var(--breeze-text-secondary);
}
.upload-item-footer {
  display: flex;
  justify-content: space-between;
  align-items: center;
}
.upload-status {
  font-size: var(--font-size-xs);
  color: var(--breeze-text-secondary);
}
.upload-item-actions {
  display: flex;
  gap: 4px;
}
.upload-error {
  font-size: var(--font-size-xs);
  color: var(--breeze-danger);
}

/* ─── Progress bar ─── */
.progress-bar {
  height: 4px;
  background: var(--breeze-border, #3b4045);
  border-radius: 2px;
  overflow: hidden;
}
.progress-fill {
  height: 100%;
  border-radius: 2px;
  transition: width 0.3s ease;
}
.progress-info { background: var(--breeze-accent, #3daee9); }
.progress-success { background: var(--breeze-success, #27ae60); }
.progress-error { background: var(--breeze-danger, #da4453); }
.progress-warning { background: var(--breeze-warning, #f67400); }

/* ─── Flat tray button ─── */
.tray-btn {
  padding: 2px 8px;
  border: none;
  border-radius: 3px;
  background: none;
  color: var(--breeze-text-secondary, #a1a9b1);
  font-size: 12px;
  cursor: default;
}
.tray-btn:hover {
  background: rgba(255, 255, 255, 0.08);
  color: var(--breeze-text, #fcfcfc);
}
.tray-btn--accent {
  color: var(--breeze-accent, #3daee9);
}
.tray-btn--icon {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 4px;
}

.slide-up-enter-active, .slide-up-leave-active {
  transition: all 0.3s ease;
}
.slide-up-enter-from, .slide-up-leave-to {
  transform: translateY(20px);
  opacity: 0;
}

@media (max-width: 767px) {
  .upload-panel { left: 8px; right: 8px; width: auto; bottom: 72px; }
}
</style>
