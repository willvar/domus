<script setup>
import { computed } from 'vue'
import { useFileSystemStore } from '../../stores/fileSystem'
import { useI18n } from '../../composables/useI18n'
import { getFileIcon } from '../../composables/useFileIcon'
import BDescriptions from '../breeze/BDescriptions.vue'
import BDescriptionItem from '../breeze/BDescriptionItem.vue'
import dayjs from 'dayjs'

const fs = useFileSystemStore()
const { t } = useI18n()

const file = computed(() => fs.selectedFile)

function formatSize(bytes) {
  if (!bytes && bytes !== 0) return '—'
  if (bytes < 1024) return `${bytes} B`
  const units = ['KiB', 'MiB', 'GiB', 'TiB']
  let i = 0
  let size = bytes / 1024
  while (size >= 1024 && i < units.length - 1) { size /= 1024; i++ }
  return `${size.toFixed(1)} ${units[i]}`
}

const iconName = computed(() => {
  if (!file.value) return getFileIcon('', true)
  return getFileIcon(file.value.name, file.value.is_dir)
})
</script>

<template>
  <div class="info-panel">
    <template v-if="file">
      <div class="info-preview">
        <div class="info-icon"><component :is="iconName" width="64" height="64" /></div>
        <div class="info-name">{{ file.name }}</div>
      </div>
      <BDescriptions :column="1" size="small" label-placement="left" class="info-details">
        <BDescriptionItem :label="t('info.type')">
          {{ file.is_dir ? t('info.directory') : (file.content_type || t('info.file')) }}
        </BDescriptionItem>
        <BDescriptionItem v-if="!file.is_dir" :label="t('info.size')">
          {{ formatSize(file.size) }}
        </BDescriptionItem>
        <BDescriptionItem v-if="file.last_modified" :label="t('info.modified')">
          {{ dayjs(file.last_modified).format('YYYY-MM-DD HH:mm:ss') }}
        </BDescriptionItem>
      </BDescriptions>
    </template>
    <template v-else-if="fs.selectedFiles.length > 1">
      <div class="info-preview">
        <div class="info-icon"><component :is="iconName" width="64" height="64" /></div>
        <div class="info-name">{{ t('info.items_selected', { n: fs.selectedFiles.length }) }}</div>
      </div>
    </template>
    <template v-else>
      <div class="info-preview">
        <div class="info-icon"><component :is="iconName" width="64" height="64" /></div>
        <div class="info-name">{{ t('info.items_count', { n: fs.sortedFiles.length }) }}</div>
      </div>
    </template>
  </div>
</template>

<style scoped>
.info-panel {
  width: var(--info-panel-width);
  min-width: var(--info-panel-width);
  background: var(--sidebar-bg);
  border-left: 1px solid var(--breeze-border);
  padding: var(--gap-lg);
  overflow-y: auto;
  flex-shrink: 0;
}
.info-preview {
  text-align: center;
  margin-bottom: var(--gap-lg);
}
.info-icon {
  display: flex;
  justify-content: center;
  margin-bottom: var(--gap-sm);
}
.info-name {
  font-size: var(--font-size-md);
  font-weight: 500;
  word-break: break-all;
  color: var(--breeze-text);
}
.info-details {
  font-size: var(--font-size-sm);
}
</style>
