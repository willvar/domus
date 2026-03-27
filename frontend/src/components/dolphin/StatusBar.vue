<script setup>
import { computed } from 'vue'
import { useFileSystemStore } from '../../stores/fileSystem'
import { useUploadStore } from '../../stores/upload'
import { useI18n } from '../../composables/useI18n'

const fs = useFileSystemStore()
const upload = useUploadStore()
const { t } = useI18n()

const itemCount = computed(() => {
  const dirs = fs.sortedFiles.filter(f => f.is_dir).length
  const files = fs.sortedFiles.length - dirs
  const parts = []
  if (dirs) parts.push(t('status.folders', { n: dirs }))
  if (files) parts.push(t('status.files', { n: files }))
  return parts.join(', ') || t('fileview.empty')
})

const selectionInfo = computed(() => {
  const count = fs.selectedFiles.length
  if (count === 0) return ''
  return t('status.selected', { n: count })
})
</script>

<template>
  <div class="status-bar">
    <div class="status-left">
      <span>{{ itemCount }}</span>
      <span v-if="selectionInfo" class="status-sep">|</span>
      <span v-if="selectionInfo">{{ selectionInfo }}</span>
      <span v-if="upload.hasActive" class="status-sep">|</span>
      <span
        v-if="upload.hasActive"
        class="upload-indicator"
        @click="upload.showPanel = true"
      >
        {{ t('status.uploading', { n: upload.activeUploads.length }) }}
      </span>
    </div>
    <div class="status-right">
      <span class="zoom-label">{{ t('status.zoom') }}:</span>
      <input
        type="range"
        class="zoom-slider"
        :value="fs.iconSize"
        :min="32"
        :max="96"
        :step="8"
        @input="fs.iconSize = +$event.target.value"
      />
    </div>
  </div>
</template>

<style scoped>
.status-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  height: 22px;
  padding: 0 var(--gap-sm);
  background: var(--breeze-bg-alt);
  border-top: 1px solid var(--breeze-border);
  font-size: var(--font-size-xs);
  color: var(--breeze-text-secondary);
  flex-shrink: 0;
}
.status-left {
  display: flex;
  align-items: center;
  gap: var(--gap-sm);
}
.status-sep { color: var(--breeze-border); }
.upload-indicator {
  color: var(--breeze-accent);
}
.status-right {
  display: flex;
  align-items: center;
  gap: var(--gap-sm);
}
.zoom-label { white-space: nowrap; }

/* ─── Breeze-style range slider ─── */
.zoom-slider {
  width: 100px;
  height: 4px;
  -webkit-appearance: none;
  appearance: none;
  background: var(--breeze-border, #3b4045);
  border-radius: 2px;
  outline: none;
  cursor: default;
}
.zoom-slider::-webkit-slider-thumb {
  -webkit-appearance: none;
  width: 12px;
  height: 12px;
  border-radius: 50%;
  background: var(--breeze-accent, #3daee9);
  border: none;
  cursor: default;
}
.zoom-slider::-moz-range-thumb {
  width: 12px;
  height: 12px;
  border-radius: 50%;
  background: var(--breeze-accent, #3daee9);
  border: none;
  cursor: default;
}

@media (max-width: 767px) {
  .status-right { display: none; }
}
</style>
