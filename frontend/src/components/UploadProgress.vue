<script setup>
import { ref } from 'vue'
import { NCard, NProgress, NButton, NList, NListItem } from 'naive-ui'
import { useUploadStore } from '../stores/upload'
import { useI18n } from '../composables/useI18n'

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

function statusColor(status) {
  switch (status) {
    case 'completed': return 'success'
    case 'failed': return 'error'
    case 'paused': return 'warning'
    case 'interrupted': return 'warning'
    default: return 'info'
  }
}

function statusText(status) {
  return t(`upload.status_${status}`)
}
</script>

<template>
  <Transition name="slide-up">
    <NCard
      v-if="upload.showPanel"
      class="upload-panel"
      :bordered="true"
      size="small"
    >
      <template #header>
        <div class="upload-header">
          <span>{{ t('upload.title') }}</span>
          <div class="upload-actions">
            <NButton size="tiny" quaternary @click="upload.removeCompleted">
              {{ t('upload.clear') }}
            </NButton>
            <NButton size="tiny" quaternary @click="upload.showPanel = false">
              ✕
            </NButton>
          </div>
        </div>
      </template>

      <NList :bordered="false" size="small">
        <NListItem v-for="u in upload.uploads" :key="u.id">
          <div class="upload-item">
            <div class="upload-item-header">
              <span class="upload-filename truncate">{{ u.fileName }}</span>
              <span class="upload-size">{{ formatSize(u.fileSize) }}</span>
            </div>
            <NProgress
              :percentage="u.progress"
              :status="statusColor(u.status)"
              :height="4"
              :show-indicator="false"
            />
            <div class="upload-item-footer">
              <span class="upload-status">
                {{ u.status === 'uploading' ? `${u.progress}% · ${formatSpeed(u.speed)}${formatEta(u) ? ' · ' + formatEta(u) : ''}` : statusText(u.status) }}
              </span>
              <div class="upload-item-actions">
                <template v-if="u.status === 'uploading'">
                  <NButton size="tiny" quaternary @click="upload.pauseUpload(u.id)">
                    {{ t('upload.pause') }}
                  </NButton>
                  <NButton size="tiny" quaternary @click="upload.cancelUpload(u.id)">
                    {{ t('upload.cancel') }}
                  </NButton>
                </template>
                <template v-if="u.status === 'paused'">
                  <NButton size="tiny" quaternary type="info" @click="upload.resumeUpload(u.id)">
                    {{ t('upload.resume') }}
                  </NButton>
                  <NButton size="tiny" quaternary @click="upload.cancelUpload(u.id)">
                    {{ t('upload.cancel') }}
                  </NButton>
                </template>
                <template v-if="u.status === 'interrupted'">
                  <NButton size="tiny" quaternary type="info" @click="triggerResume(u.id)">
                    {{ u._file ? t('upload.resume') : t('upload.reselect_resume') }}
                  </NButton>
                  <NButton size="tiny" quaternary @click="upload.dismissInterrupted(u.id)">
                    {{ t('upload.dismiss') }}
                  </NButton>
                </template>
                <template v-if="u.status === 'failed' && u._file">
                  <NButton size="tiny" quaternary type="info" @click="upload.retryUpload(u.id)">
                    {{ t('upload.retry') }}
                  </NButton>
                </template>
              </div>
            </div>
            <div v-if="u.error" class="upload-error">{{ te({ response: { data: { error: u.error } } }) }}</div>
          </div>
        </NListItem>
      </NList>
    </NCard>
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
  overflow-y: auto;
  z-index: 200;
  box-shadow: 0 -4px 24px rgba(0, 0, 0, 0.12);
}
.upload-header {
  display: flex;
  justify-content: space-between;
  align-items: center;
  font-weight: 600;
}
.upload-actions {
  display: flex;
  gap: 4px;
}
.upload-item {
  display: flex;
  flex-direction: column;
  gap: 4px;
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
